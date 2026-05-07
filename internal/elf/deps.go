// Package elf provides ELF binary analysis for shared library dependency
// resolution and binary metadata extraction.
package elf

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DataDog/tare/internal/rootfs"
)

// DepStatus indicates whether a dependency was resolved.
type DepStatus string

const (
	DepOK      DepStatus = "ok"
	DepMissing DepStatus = "missing"
)

// Dep represents a single shared library dependency.
type Dep struct {
	Name     string    `json:"name"`
	Resolved string    `json:"resolved,omitempty"`
	Status   DepStatus `json:"status"`
}

// Interp represents the ELF interpreter (dynamic linker).
type Interp struct {
	Path   string    `json:"path"`
	Status DepStatus `json:"status"`
}

// DepsResult contains the dependency resolution results for a single binary.
type DepsResult struct {
	Interp *Interp `json:"interpreter,omitempty"`
	Deps   []Dep   `json:"dependencies"`
}

// Deps resolves the shared library dependency tree for a binary.
// It recursively traces transitive dependencies.
func Deps(fsys rootfs.FS, path string) (*DepsResult, error) {
	f, err := openELF(fsys, path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return DepsFromFile(fsys, f, path)
}

// DepsFromFile resolves dependencies from an already-opened ELF file.
// binaryPath is used for $ORIGIN expansion and as the starting point
// for recursive resolution of transitive dependencies on the filesystem.
func DepsFromFile(fsys rootfs.FS, f *elf.File, binaryPath string) (*DepsResult, error) {
	libs, err := f.DynString(elf.DT_NEEDED)
	if err != nil {
		return nil, fmt.Errorf("reading DT_NEEDED: %w", err)
	}
	if len(libs) == 0 {
		return &DepsResult{}, nil
	}

	result := &DepsResult{}

	// Interpreter.
	interp := getInterp(f)
	if interp != "" {
		status := DepOK
		if _, err := fsys.Stat(interp); err != nil {
			status = DepMissing
		}
		result.Interp = &Interp{Path: interp, Status: status}
	}

	// Resolve direct dependencies using the in-memory ELF's search paths.
	searchPaths := collectSearchPaths(f, binaryPath)
	seen := map[string]string{} // lib name -> resolved path (or "")
	for _, lib := range libs {
		if interp != "" && filepath.Base(interp) == lib {
			continue
		}
		resolved := resolveLib(fsys, lib, searchPaths)
		seen[lib] = resolved
	}

	// Recursively resolve transitive dependencies on the filesystem.
	var resolve func(elfPath string)
	resolve = func(elfPath string) {
		ef, err := openELF(fsys, elfPath)
		if err != nil {
			return
		}
		defer ef.Close()

		sp := collectSearchPaths(ef, elfPath)
		needed, err := ef.DynString(elf.DT_NEEDED)
		if err != nil {
			return
		}
		for _, lib := range needed {
			if _, ok := seen[lib]; ok {
				continue
			}
			if interp != "" && filepath.Base(interp) == lib {
				continue
			}
			resolved := resolveLib(fsys, lib, sp)
			seen[lib] = resolved
			if resolved != "" {
				resolve(resolved)
			}
		}
	}

	// Kick off transitive resolution from each resolved direct dep.
	for _, resolved := range seen {
		if resolved != "" {
			resolve(resolved)
		}
	}

	for name, resolved := range seen {
		dep := Dep{Name: name, Status: DepOK, Resolved: resolved}
		if resolved == "" {
			dep.Status = DepMissing
		}
		result.Deps = append(result.Deps, dep)
	}
	sort.Slice(result.Deps, func(i, j int) bool {
		return result.Deps[i].Name < result.Deps[j].Name
	})

	return result, nil
}

// HasMissing returns true if any dependency is unresolved.
func (r *DepsResult) HasMissing() bool {
	if r.Interp != nil && r.Interp.Status == DepMissing {
		return true
	}
	for _, d := range r.Deps {
		if d.Status == DepMissing {
			return true
		}
	}
	return false
}

// IsDynamic returns true if the binary has any dynamic dependencies.
func (r *DepsResult) IsDynamic() bool {
	return len(r.Deps) > 0 || r.Interp != nil
}

// openELF opens a file via the FS and parses it as ELF.
func openELF(fsys rootfs.FS, path string) (*elf.File, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, err
	}
	ef, err := elf.NewFile(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return ef, nil
}

// resolveLib searches for a library in the given paths.
func resolveLib(fsys rootfs.FS, name string, searchPaths []string) string {
	for _, dir := range searchPaths {
		candidate := filepath.Join(dir, name)
		if _, err := fsys.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// Default library search paths (standard Linux).
var defaultLibPaths = []string{
	"/lib",
	"/lib64",
	"/usr/lib",
	"/usr/lib64",
}

// collectSearchPaths returns library search paths from the ELF binary
// and the default system paths. elfPath is the path to the binary,
// used for $ORIGIN expansion.
func collectSearchPaths(f *elf.File, elfPath string) []string {
	if resolved, err := filepath.EvalSymlinks(elfPath); err == nil {
		elfPath = resolved
	}
	origin := filepath.Dir(elfPath)
	var paths []string

	rpath, _ := f.DynString(elf.DT_RPATH)
	runpath, _ := f.DynString(elf.DT_RUNPATH)

	if len(runpath) == 0 {
		for _, p := range rpath {
			for _, dir := range strings.Split(p, ":") {
				paths = append(paths, expandOrigin(dir, origin))
			}
		}
	}

	if ldPath := os.Getenv("LD_LIBRARY_PATH"); ldPath != "" {
		paths = append(paths, strings.Split(ldPath, ":")...)
	}

	for _, p := range runpath {
		for _, dir := range strings.Split(p, ":") {
			paths = append(paths, expandOrigin(dir, origin))
		}
	}

	paths = append(paths, archLibPaths(f)...)
	paths = append(paths, defaultLibPaths...)

	return paths
}

func expandOrigin(path string, origin string) string {
	path = strings.ReplaceAll(path, "$ORIGIN", origin)
	path = strings.ReplaceAll(path, "${ORIGIN}", origin)
	return path
}

func archLibPaths(f *elf.File) []string {
	switch f.Machine {
	case elf.EM_X86_64:
		return []string{"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu"}
	case elf.EM_AARCH64:
		return []string{"/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu"}
	case elf.EM_386:
		return []string{"/lib/i386-linux-gnu", "/usr/lib/i386-linux-gnu"}
	default:
		return nil
	}
}

func getInterp(f *elf.File) string {
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			data := make([]byte, prog.Filesz)
			if _, err := prog.ReadAt(data, 0); err == nil {
				return strings.TrimRight(string(data), "\x00")
			}
		}
	}
	return ""
}
