package scan

import (
	"debug/elf"
	"errors"
	"io/fs"

	tareelf "github.com/DataDog/tare/internal/elf"
	"github.com/DataDog/tare/internal/rootfs"
)

// skipDirs are directories that should never be scanned.
var skipDirs = map[string]bool{
	"/proc": true,
	"/sys":  true,
	"/dev":  true,
}

// workItem represents a discovered binary to analyze.
type workItem struct {
	path     string    // filesystem ELF path
	jarEntry *jarEntry // in-jar ELF (mutually exclusive with path)
}

// discoverResult holds the results of a discover walk.
type discoverResult struct {
	truncated bool
	warnings  []Warning
}

// discover walks the given paths, finds ELF binaries and jar entries,
// and sends them to the found channel. The caller is responsible for
// closing the channel. Stops after limit items have been sent.
func discover(fsys rootfs.FS, paths []string, limit int, found chan<- workItem) discoverResult {
	seen := map[uint64]bool{} // inode -> seen
	count := 0
	var result discoverResult

	// fsys.FS() returns an fs.FS that accepts absolute paths.
	walkFS := fsys.FS()

	for _, root := range paths {
		if result.truncated {
			break
		}
		fs.WalkDir(walkFS, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					result.warnings = append(result.warnings, Warning{
						Path:    path,
						Message: "permission denied — contents not scanned",
					})
				}
				return nil
			}
			if result.truncated {
				return fs.SkipAll
			}
			if d.IsDir() {
				if skipDirs[path] {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() && d.Type()&fs.ModeSymlink == 0 {
				return nil
			}

			// Deduplicate by inode (handles hardlinks and symlinks to same target).
			if info, err := fsys.Stat(path); err == nil {
				if stat, ok := inodeOf(info); ok {
					if seen[stat] {
						return nil
					}
					seen[stat] = true
				}
			}

			// Check for jar files — scan for embedded .so files.
			if isJar(path) {
				for _, entry := range scanJar(fsys, path) {
					if count >= limit {
						result.truncated = true
						return fs.SkipAll
					}
					found <- workItem{jarEntry: &entry}
					count++
				}
				return nil
			}

			// Check for ELF binaries.
			isElf, elfErr := tareelf.CheckELF(fsys, path)
			if isElf || elfErr != nil {
				if count >= limit {
					result.truncated = true
					return fs.SkipAll
				}
				found <- workItem{path: path}
				count++
			}

			return nil
		})
	}

	return result
}

// analyzeBinary analyzes a single ELF binary on the filesystem.
func analyzeBinary(fsys rootfs.FS, path string) *BinaryResult {
	f, err := fsys.Open(path)
	if err != nil {
		return &BinaryResult{
			Info: &tareelf.BinaryInfo{Path: path},
			Err:  err.Error(),
		}
	}

	ef, err := elf.NewFile(f)
	if err != nil {
		f.Close()
		return &BinaryResult{
			Info: &tareelf.BinaryInfo{Path: path},
			Err:  err.Error(),
		}
	}
	defer ef.Close()

	info := tareelf.InfoFromFile(ef, path)
	result := &BinaryResult{Info: info}

	if info.Type == tareelf.TypeDynamic {
		deps, err := tareelf.DepsFromFile(fsys, ef, path)
		if err != nil {
			result.Err = err.Error()
		} else {
			result.Deps = deps
		}
	}

	return result
}
