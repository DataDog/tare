package scan

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	tareelf "github.com/DataDog/tare/internal/elf"
	"github.com/DataDog/tare/internal/rootfs"
)

// BinaryResult contains the analysis results for a single ELF binary.
type BinaryResult struct {
	Info *tareelf.BinaryInfo `json:"info"`
	Deps *tareelf.DepsResult `json:"deps,omitempty"`
	Err  string              `json:"error,omitempty"`
}

// DefaultLimit is the maximum number of ELF binaries to scan.
const DefaultLimit = 1024

// Report is the combined analysis output.
type Report struct {
	Summary   Summary         `json:"summary"`
	Warnings  []Warning       `json:"warnings,omitempty"`
	Binaries  []*BinaryResult `json:"binaries"`
	Truncated bool            `json:"truncated,omitempty"`
}

// Summary provides counts for the report.
type Summary struct {
	Total     int `json:"total"`
	OK        int `json:"ok"`
	Static    int `json:"static"`
	DynamicOK int `json:"dynamic_ok"`
	Errors    int `json:"errors"`
	Warnings  int `json:"warnings"`

	// SkippedCrossArch counts JAR-internal ELF entries dropped because
	// their architecture didn't match Options.TargetArch. Filesystem
	// entries are never auto-skipped.
	SkippedCrossArch int `json:"skipped_cross_arch,omitempty"`
}

// Options configures a scan run.
type Options struct {
	Ignore    []IgnorePattern
	Limit     int        // max ELF binaries to scan; 0 means DefaultLimit
	NoRuntime bool       // skip runtime file checks (CA certs, passwd, etc.)
	FS        rootfs.FS  // filesystem to scan; nil uses os.OpenRoot("/")

	// TargetArch is the image's target architecture (amd64, arm64, etc.,
	// in OCI/Docker convention). When set, JAR-internal ELF entries whose
	// arch differs are auto-skipped — common with fat JARs that bundle
	// native libraries for multiple architectures. Filesystem-walked
	// ELFs are never auto-skipped, since cross-arch on disk usually
	// indicates a real bug. Empty disables the check.
	TargetArch string
}

// Run performs a full scan: walks paths for ELF binaries (including
// inside jar files), resolves their dependencies, and checks for common
// runtime files.
func Run(paths []string, opts ...Options) (*Report, error) {
	var ignore []IgnorePattern
	limit := DefaultLimit
	noRuntime := false
	var fsys rootfs.FS
	var targetArch string
	if len(opts) > 0 {
		ignore = opts[0].Ignore
		if opts[0].Limit > 0 {
			limit = opts[0].Limit
		}
		noRuntime = opts[0].NoRuntime
		fsys = opts[0].FS
		targetArch = opts[0].TargetArch
	}

	if fsys == nil {
		var err error
		fsys, err = defaultFS()
		if err != nil {
			return nil, fmt.Errorf("opening root filesystem: %w", err)
		}
	}

	// Validate scan paths exist.
	for _, p := range paths {
		if _, err := fsys.Stat(p); err != nil {
			return nil, fmt.Errorf("scan path: %w", err)
		}
	}

	// Set up worker pool.
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 4
	}

	found := make(chan workItem, workers*2)
	var disc discoverResult

	// Producer: walk paths, discover ELFs and jar entries.
	go func() {
		disc = discover(fsys, paths, limit, found)
		close(found)
	}()

	// Consumers: analyze discovered items.
	var (
		mu              sync.Mutex
		results         []*BinaryResult
		skippedCrossArch int
	)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range found {
				var r *BinaryResult
				if item.jarEntry != nil {
					r = analyzeJarEntry(fsys, *item.jarEntry)
					// Skip cross-arch ELFs only when we know both archs.
					// Empty Info.Arch usually means the ELF parse failed —
					// let it fall through to the normal error path.
					if r != nil && r.Info != nil && r.Info.Arch != "" &&
						targetArch != "" && !archMatches(r.Info.Arch, targetArch) {
						mu.Lock()
						skippedCrossArch++
						mu.Unlock()
						continue
					}
				} else {
					r = analyzeBinary(fsys, item.path)
				}
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if results == nil {
		results = []*BinaryResult{}
	}

	// Apply ignore patterns.
	if len(ignore) > 0 {
		var filtered []*BinaryResult
		for _, r := range results {
			if matchesBinaryIgnore(r.Info.Path, ignore) {
				continue
			}
			if r.Deps != nil {
				var kept []tareelf.Dep
				for _, d := range r.Deps.Deps {
					if !matchesDepIgnore(d.Name, ignore) {
						kept = append(kept, d)
					}
				}
				r.Deps.Deps = kept
			}
			filtered = append(filtered, r)
		}
		if filtered == nil {
			filtered = []*BinaryResult{}
		}
		results = filtered
	}

	// Collect warnings: access errors from discovery, then runtime file checks.
	var warnings []Warning
	warnings = append(warnings, disc.warnings...)
	if !noRuntime {
		warnings = checkRuntime(fsys)
		if len(ignore) > 0 {
			var kept []Warning
			for _, w := range warnings {
				if !matchesWarningIgnore(w.Path, ignore) {
					kept = append(kept, w)
				}
			}
			warnings = kept
		}
	}

	// Build report.
	report := &Report{
		Binaries:  results,
		Warnings:  warnings,
		Truncated: disc.truncated,
	}
	for _, r := range results {
		report.Summary.Total++
		if r.Err != "" {
			report.Summary.Errors++
		} else if r.Info.Type == tareelf.TypeStatic {
			report.Summary.Static++
			report.Summary.OK++
		} else if r.Deps != nil && r.Deps.HasMissing() {
			report.Summary.Errors++
		} else {
			report.Summary.DynamicOK++
			report.Summary.OK++
		}
	}
	report.Summary.Warnings = len(warnings)
	report.Summary.SkippedCrossArch = skippedCrossArch

	return report, nil
}

// archMatches reports whether an ELF arch string matches a target arch
// string, normalizing across the OCI/Docker convention (amd64, arm64) and
// the ELF e_machine convention (x86_64, aarch64).
func archMatches(elfArch, targetArch string) bool {
	return normalizeArch(elfArch) == normalizeArch(targetArch)
}

func normalizeArch(a string) string {
	switch a {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "386", "i386":
		return "386"
	case "arm":
		return "arm"
	}
	return a
}

// defaultFS returns a rootfs.FS backed by os.OpenRoot("/").
func defaultFS() (rootfs.FS, error) {
	root, err := os.OpenRoot("/")
	if err != nil {
		return nil, err
	}
	return rootfs.New(root, "/"), nil
}
