package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/DataDog/tare/internal/config"
	"github.com/DataDog/tare/internal/oci"
	"github.com/DataDog/tare/internal/scan"
	"github.com/DataDog/tare/internal/testexec"
	"github.com/DataDog/tare/internal/testplan"
)

// isOCILayout reports whether image refers to an OCI layout directory.
func isOCILayout(image string) bool {
	info, err := os.Stat(image)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(image + "/oci-layout")
	return err == nil
}

// archFromPlatform extracts the architecture from a platform string
// like "linux/amd64" → "amd64".
func archFromPlatform(platform string) string {
	if i := strings.Index(platform, "/"); i >= 0 {
		return platform[i+1:]
	}
	return platform
}

func runCheckOCILayout(sf *sessionFlags, cfg *config.Config, scanPaths, scanIgnore repeatedFlag, scanLimit int) int {
	layout, err := oci.Open(sf.image)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	arch := archFromPlatform(sf.platform)
	icfg, err := layout.ImageConfig("linux", arch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Autodetect scan path from image config.
	if len(scanPaths) == 0 {
		ensureDefaultScanPath(cfg, icfg)
	}
	mergeScanFlags(cfg, scanPaths, scanIgnore, scanLimit)

	// Warn about command tests.
	if len(cfg.Commands) > 0 {
		fmt.Fprintf(os.Stderr, "warning: command tests are not available for OCI layouts and will be skipped\n")
	}
	if hasRuntimeOpts(cfg) {
		fmt.Fprintf(os.Stderr, "warning: tare.runtime options are ignored for OCI layouts (no container is created)\n")
	}

	// Verify at least one non-command test is configured.
	hasTests := len(cfg.Metadata) > 0 ||
		len(cfg.Files) > 0 ||
		len(cfg.Scan) > 0
	if !hasTests {
		fmt.Fprintf(os.Stderr, "error: no tests configured\n\nUse config files, --scan, or ensure the image has a scannable entrypoint.\n")
		return 2
	}

	// Extract rootfs.
	destDir, err := os.MkdirTemp("", "tare-oci-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: creating temp dir: %v\n", err)
		return 1
	}
	defer func() {
		if sf.noCleanup {
			fmt.Fprintf(os.Stderr, "extracted rootfs left at: %s\n", destDir)
			return
		}
		os.RemoveAll(destDir)
	}()

	if sf.verbose {
		fmt.Fprintf(os.Stderr, "extracting rootfs to %s...\n", destDir)
	}

	result, err := layout.Extract(destDir, "linux", arch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: extracting rootfs: %v\n", err)
		return 1
	}
	defer result.Root.Close()

	cwd := icfg.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	fsys := oci.NewFS(result, cwd)

	// Build and run test plan.
	plan := testplan.FromConfig(cfg)

	if sf.verbose {
		fmt.Fprintf(os.Stderr, "running checks...\n")
	}

	failures := testexec.Run(os.Stdout, plan, icfg, testexec.Options{
		FS:         fsys,
		NoCommands: true,
	})

	if failures > 0 {
		return 1
	}
	return 0
}

func runScanOCILayout(sf *sessionFlags, cfg *config.Config, scanPaths, scanIgnore repeatedFlag, scanLimit int, jsonOutput bool) int {
	layout, err := oci.Open(sf.image)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	arch := archFromPlatform(sf.platform)
	icfg, err := layout.ImageConfig("linux", arch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if len(scanPaths) == 0 {
		ensureDefaultScanPath(cfg, icfg)
	}
	mergeScanFlags(cfg, scanPaths, scanIgnore, scanLimit)

	if len(cfg.Scan) == 0 {
		fmt.Fprintf(os.Stderr, "error: no scan paths found. Use --path or configure scan in a config file.\n")
		return 2
	}

	// Extract rootfs.
	destDir, err := os.MkdirTemp("", "tare-oci-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: creating temp dir: %v\n", err)
		return 1
	}
	defer func() {
		if sf.noCleanup {
			fmt.Fprintf(os.Stderr, "extracted rootfs left at: %s\n", destDir)
			return
		}
		os.RemoveAll(destDir)
	}()

	if sf.verbose {
		fmt.Fprintf(os.Stderr, "extracting rootfs to %s...\n", destDir)
	}

	result, err := layout.Extract(destDir, "linux", arch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: extracting rootfs: %v\n", err)
		return 1
	}
	defer result.Root.Close()

	cwd := icfg.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	fsys := oci.NewFS(result, cwd)

	// Run scans directly via scan.Run.
	entries := cfg.Scan
	if len(entries) == 1 {
		fmt.Fprintf(os.Stderr, "scanning %s...\n", entries[0].Path)
	} else {
		paths := make([]string, len(entries))
		for i, e := range entries {
			paths[i] = e.Path
		}
		fmt.Fprintf(os.Stderr, "scanning %d paths: %s\n", len(entries), strings.Join(paths, ", "))
	}

	var reports []*scan.Report
	for i, entry := range entries {
		var patterns []scan.IgnorePattern
		for _, ig := range entry.Ignore {
			p, err := scan.ParseIgnorePattern(ig)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			patterns = append(patterns, p)
		}

		report, err := scan.Run([]string{entry.Path}, scan.Options{
			Ignore:     patterns,
			Limit:      entry.Limit,
			NoRuntime:  i > 0,
			FS:         fsys,
			TargetArch: arch,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		reports = append(reports, report)
	}

	merged := scan.MergeReports(reports...)

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(merged)
	} else {
		scan.PrintReport(os.Stdout, merged, "")
	}

	if merged.Summary.Errors > 0 {
		return 1
	}
	return 0
}
