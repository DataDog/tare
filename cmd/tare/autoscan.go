package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/tare/internal/oci"
)

// statFS is the subset of FS we need for the exists callback.
type statFS interface {
	Stat(name string) (fs.FileInfo, error)
}

// fsysExists returns an exists callback backed by a filesystem's Stat.
// A path counts as "exists" only if it's a directory — autoscan-derived
// scan paths should never be regular files.
func fsysExists(fsys statFS) func(string) bool {
	return func(p string) bool {
		info, err := fsys.Stat(p)
		return err == nil && info.IsDir()
	}
}

// envVars are the library-path env vars autoscan inspects, in display order.
var envVars = []string{
	"LD_LIBRARY_PATH",
	"PYTHONPATH",
	"NODE_PATH",
	"CLASSPATH",
	"PERL5LIB",
	"GEM_PATH",
}

// envPathSoftCap is the per-env-var path count above which we print a
// warning. The paths are still scanned — the warning just nudges the
// user toward --no-autoscan if the expansion was unintended.
const envPathSoftCap = 10

// systemDirs are directories that aren't useful as default scan targets.
var systemDirs = map[string]bool{
	"/":        true,
	"/usr":     true,
	"/usr/bin": true,
	"/usr/lib": true,
	"/bin":     true,
	"/sbin":    true,
	"/lib":     true,
}

// autoCandidate is one detected scan path.
type autoCandidate struct {
	Path   string // absolute, deduped
	Source string // "ENTRYPOINT", "CMD", or env var name
	From   string // detail shown after source (binary path; or verbose env value)
	Exists bool   // result of exists() check; true if exists callback was nil
}

// detectScanPaths returns the autodetected scan paths from an image
// config: the ENTRYPOINT/CMD binary directory plus library-path env vars.
// Paths are filtered to absolute, non-system, non-duplicate values. The
// exists callback (if non-nil) is invoked once per candidate to mark
// missing paths — they're returned but flagged so the caller can skip
// them.
func detectScanPaths(icfg *oci.ImageConfig, exists func(string) bool, verbose bool) []autoCandidate {
	if icfg == nil {
		return nil
	}

	var candidates []autoCandidate
	seen := map[string]bool{}

	if dir, source, binary := entrypointDir(icfg); dir != "" {
		candidates = append(candidates, autoCandidate{
			Path:   dir,
			Source: source,
			From:   binary,
		})
		seen[dir] = true
	}

	envMap := envToMap(icfg.Env)
	for _, name := range envVars {
		raw, ok := envMap[name]
		if !ok || raw == "" {
			continue
		}
		entries := parseEnvPaths(name, raw)

		if len(entries) > envPathSoftCap {
			fmt.Fprintf(os.Stderr,
				"warning: %s expanded to %d paths (more than %d). Use --no-autoscan and set tare.scan explicitly if this is unintended.\n",
				name, len(entries), envPathSoftCap)
		}

		for _, p := range entries {
			if !filepath.IsAbs(p) {
				continue
			}
			if systemDirs[p] {
				continue
			}
			if seen[p] {
				continue
			}
			seen[p] = true
			from := ""
			if verbose {
				from = name + "=" + raw
			}
			candidates = append(candidates, autoCandidate{
				Path:   p,
				Source: name,
				From:   from,
			})
		}
	}

	for i := range candidates {
		if exists == nil {
			candidates[i].Exists = true
			continue
		}
		candidates[i].Exists = exists(candidates[i].Path)
	}

	return candidates
}

// entrypointDir returns the directory of the image's ENTRYPOINT[0] or
// (if no entrypoint) CMD[0]. Relative paths are resolved against
// WorkingDir. Returns empty if the result lands in a system dir or
// can't be made absolute.
func entrypointDir(icfg *oci.ImageConfig) (dir, source, binary string) {
	if len(icfg.Entrypoint) > 0 {
		binary = icfg.Entrypoint[0]
		source = "ENTRYPOINT"
	} else if len(icfg.Cmd) > 0 {
		binary = icfg.Cmd[0]
		source = "CMD"
	}
	if binary == "" {
		return "", "", ""
	}
	dir = filepath.Dir(binary)
	if !filepath.IsAbs(dir) && icfg.WorkingDir != "" {
		dir = filepath.Join(icfg.WorkingDir, dir)
	}
	if !filepath.IsAbs(dir) || systemDirs[dir] {
		return "", "", ""
	}
	return dir, source, binary
}

// envToMap parses the OCI image config Env list ("KEY=VALUE" strings).
// Later occurrences win, matching docker semantics.
func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		if i := strings.Index(e, "="); i >= 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}

// parseEnvPaths splits a colon-separated path value. CLASSPATH entries
// are massaged: dir/* expands to dir, and a .jar file is reduced to its
// parent directory (where its siblings likely live).
func parseEnvPaths(name, value string) []string {
	var out []string
	for _, raw := range strings.Split(value, ":") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if name == "CLASSPATH" {
			raw = classpathToDir(raw)
			if raw == "" {
				continue
			}
		}
		out = append(out, raw)
	}
	return out
}

// classpathToDir normalizes a CLASSPATH entry to a directory.
func classpathToDir(entry string) string {
	if strings.HasSuffix(entry, "/*") {
		return strings.TrimSuffix(entry, "/*")
	}
	if strings.HasSuffix(entry, ".jar") {
		return filepath.Dir(entry)
	}
	return entry
}

// printAutoscan writes the autodetection block to stderr. Returns true
// if anything was printed (callers may want to add trailing whitespace).
func printAutoscan(candidates []autoCandidate) bool {
	if len(candidates) == 0 {
		return false
	}

	var found, skipped []autoCandidate
	for _, c := range candidates {
		if c.Exists {
			found = append(found, c)
		} else {
			skipped = append(skipped, c)
		}
	}

	fmt.Fprintln(os.Stderr, "Autodetected scan paths:")
	if len(found) == 0 {
		fmt.Fprintln(os.Stderr, "  (none)")
	}
	for _, c := range found {
		writeAutoRow(os.Stderr, c)
	}
	if len(skipped) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Skipped (path not found in image):")
		for _, c := range skipped {
			writeAutoRow(os.Stderr, c)
		}
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, `Use "tare check --scan" or "tare scan --path" to override, set tare.scan in a config file, or pass --no-autoscan to disable autodetection.`)
	fmt.Fprintln(os.Stderr)
	return true
}

// pathColumn is the column width for the path before "from SOURCE".
const pathColumn = 32

// writeAutoRow prints one "  /path   from SOURCE[: detail]" row,
// wrapping to a second line if the path doesn't fit the column.
func writeAutoRow(w *os.File, c autoCandidate) {
	source := "from " + c.Source
	if c.From != "" {
		source += ": " + c.From
	}
	if len(c.Path) < pathColumn {
		fmt.Fprintf(w, "  %-*s%s\n", pathColumn, c.Path, source)
		return
	}
	fmt.Fprintf(w, "  %s\n  %*s%s\n", c.Path, pathColumn, "", source)
}
