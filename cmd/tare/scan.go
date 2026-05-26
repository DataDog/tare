package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DataDog/tare/internal/config"
	"github.com/DataDog/tare/internal/container"
	"github.com/DataDog/tare/internal/scan"
)

const scanUsage = `Usage:
    tare scan -i IMAGE [options] [config-files...]

Scan a container image for shared library dependency issues.

Walks ELF binaries (including .so files inside JARs) under the given paths,
resolves their shared library dependencies, and reports any that are missing.

By default, autoscans the image: ENTRYPOINT/CMD directory plus library-path
env vars (PYTHONPATH, LD_LIBRARY_PATH, CLASSPATH, NODE_PATH, PERL5LIB, GEM_PATH).
Config files can specify paths via the tare.scan section.

Options:
    -i, --image IMAGE         Container image to scan (required)
        --path PATH           Path to scan for ELF dependencies (repeatable)
        --ignore PAT          Ignore a binary path or library name (repeatable)
        --limit N             Max ELF binaries to scan (default: 1024)
        --json                Output scan report as JSON
        --no-autoscan         Disable ENTRYPOINT/CMD + env var scan-path detection
                              (paths in tare.scan or --path are still scanned)
        --harness PATH        Path to local harness directory (overrides embedded harness)
        --runtime BIN         Container runtime binary (default: docker)
        --platform PLAT       Target platform (default: linux/GOARCH)
        --pull POLICY         Pull policy: never (default), missing, always
        --no-cleanup          Leave container running after scan for debugging
        --verbose             Print tare lifecycle actions to stderr

Examples:
    tare scan -i myapp:latest
    tare scan -i myapp:latest --path /app --path /opt/venv
    tare scan -i myapp:latest --ignore "/opt/venv/lib/python3/site-packages/PIL/*.so"
    tare scan -i myapp:latest config.yaml`

func runScan(args []string) int {
	fs := flag.NewFlagSet("tare scan", flag.ExitOnError)

	var sf sessionFlags
	sf.register(fs)

	var (
		scanPaths  repeatedFlag
		scanIgnore repeatedFlag
		scanLimit  int
		jsonOutput bool
	)

	fs.Var(&scanPaths, "path", "")
	fs.Var(&scanIgnore, "ignore", "")
	fs.IntVar(&scanLimit, "limit", 0, "")
	fs.BoolVar(&jsonOutput, "json", false, "")

	fs.Usage = func() { fmt.Fprintln(os.Stderr, scanUsage) }

	fs.Parse(args)

	if err := sf.validate(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fs.Usage()
		return 1
	}

	// Load configs — only tare.scan is used; other test types are ignored.
	cfg, err := loadConfigs(fs.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if err := validateIgnorePatterns(scanIgnore); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if scanLimit < 0 {
		fmt.Fprintf(os.Stderr, "error: --limit cannot be negative\n")
		return 1
	}

	if isOCILayout(sf.image) {
		return runScanOCILayout(&sf, cfg, scanPaths, scanIgnore, scanLimit, jsonOutput)
	}

	return withSession(&sf, cfg, func(sess *container.Session) int {
		// Autodetect scan paths from image config if no paths specified.
		if len(scanPaths) == 0 && !sf.noAutoscan {
			ensureDefaultScanPath(cfg, sess.Config, sess.PathExists, sf.verbose)
		}
		mergeScanFlags(cfg, scanPaths, scanIgnore, scanLimit)

		if len(cfg.Scan) == 0 {
			fmt.Fprintf(os.Stderr, "error: no scan paths found. Use --path or configure scan in a config file.\n")
			return 1
		}

		entries := cfg.Scan
		printScanPreamble(entries)

		var reports []*scan.Report
		for i, entry := range entries {
			cmd := []string{container.HarnessBin("tare-tool"), "scan", "--format", "json"}
			if i > 0 {
				cmd = append(cmd, "--no-runtime")
			}
			if entry.Limit > 0 {
				cmd = append(cmd, "--limit", fmt.Sprintf("%d", entry.Limit))
			}
			for _, ig := range entry.Ignore {
				cmd = append(cmd, "--ignore", ig)
			}
			if sess.Config != nil && sess.Config.Architecture != "" {
				cmd = append(cmd, "--target-arch", sess.Config.Architecture)
			}
			cmd = append(cmd, entry.Path)

			var stdout bytes.Buffer
			exitCode, err := sess.Exec(container.ExecOpts{Stdout: &stdout}, cmd...)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			if exitCode == 2 {
				// Exit code 2 = tare-tool usage error (bad flags, etc.)
				fmt.Fprintf(os.Stderr, "error: tare-tool scan failed\n%s", stdout.String())
				return 1
			}

			var report scan.Report
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				fmt.Fprintf(os.Stderr, "error: parsing scan output: %v\n", err)
				return 1
			}
			reports = append(reports, &report)
		}

		exitCode := emitScanReports(reports, jsonOutput)
		printTareWeight(sess, cfg)
		return exitCode
	})
}

// printScanPreamble writes "scanning ..." progress to stderr.
func printScanPreamble(entries []config.ScanEntry) {
	if len(entries) == 1 {
		fmt.Fprintf(os.Stderr, "scanning %s...\n", entries[0].Path)
		return
	}
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	fmt.Fprintf(os.Stderr, "scanning %d paths: %s\n", len(entries), strings.Join(paths, ", "))
}

// emitScanReports merges reports, writes them in the chosen format, and
// returns the exit code (1 if any binary has missing deps, 0 otherwise).
func emitScanReports(reports []*scan.Report, jsonOutput bool) int {
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
