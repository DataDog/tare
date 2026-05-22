package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/DataDog/tare/internal/container"
	"github.com/DataDog/tare/internal/harness"
	"github.com/DataDog/tare/internal/oci"
	"github.com/DataDog/tare/internal/testplan"
)

const checkUsage = `Usage:
    tare check -i IMAGE [options] [config-files...]

Check a container image for runtime issues and run structure tests.

By default, checks common runtime files and scans the entrypoint directory
for shared library dependencies. Config files add structure tests (file
existence, file content, commands, metadata).

Options:
    -i, --image IMAGE         Container image to check (required)
        --scan PATH           Additional path to scan for ELF dependencies (repeatable)
        --scan-ignore PAT     Ignore a binary path or library name during scan (repeatable)
        --scan-limit N        Max ELF binaries to scan (default: 1024)
        --harness PATH        Path to local harness directory (overrides embedded harness)
        --runtime BIN         Container runtime binary (default: docker)
        --platform PLAT       Target platform (default: linux/GOARCH)
        --pull POLICY         Pull policy: never (default), missing, always
        --no-cleanup          Leave container running after check for debugging
        --verbose             Print tare lifecycle actions to stderr

Examples:
    tare check -i myapp:latest
    tare check -i myapp:latest --scan /app --scan /opt/venv
    tare check -i myapp:latest tests.yaml
    tare check -i myapp:latest --scan /app tests.yaml`

func runCheck(args []string) int {
	fs := flag.NewFlagSet("tare check", flag.ExitOnError)

	var sf sessionFlags
	sf.register(fs)

	var (
		scanPaths  repeatedFlag
		scanIgnore repeatedFlag
		scanLimit  int
	)

	fs.Var(&scanPaths, "scan", "")
	fs.Var(&scanIgnore, "scan-ignore", "")
	fs.IntVar(&scanLimit, "scan-limit", 0, "")

	fs.Usage = func() { fmt.Fprintln(os.Stderr, checkUsage) }

	fs.Parse(args)

	if err := sf.validate(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fs.Usage()
		return 1
	}

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
		fmt.Fprintf(os.Stderr, "error: --scan-limit cannot be negative\n")
		return 1
	}

	if isOCILayout(sf.image) {
		return runCheckOCILayout(&sf, cfg, scanPaths, scanIgnore, scanLimit)
	}

	// Build harness and start session.
	rt := &container.Runtime{Bin: sf.runtimeBin}
	harnessReader, err := harness.Select(sf.harnessPath, sf.platform)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	sessOpts := container.SessionOpts{
		Image:    sf.image,
		Platform: sf.platform,
		Pull:     sf.pull,
		Harness:  harnessReader,
		Verbose:  sf.verbose,
	}
	applyRuntimeOpts(&sessOpts, cfg)
	sess, err := rt.NewSession(sessOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			signal.Stop(sigCh)
			if sf.noCleanup {
				fmt.Fprintf(os.Stderr, "container left running: %s\n", sess.ID())
				return
			}
			if sf.verbose {
				fmt.Fprintf(os.Stderr, "removing container %s...\n", sess.ID()[:12])
			}
			_ = sess.Close()
		})
	}
	defer cleanup()

	go func() {
		<-sigCh
		cleanup()
		os.Exit(130)
	}()

	// Autodetect scan path from image config if no paths specified.
	if len(scanPaths) == 0 {
		ensureDefaultScanPath(cfg, sess.Config)
	}
	mergeScanFlags(cfg, scanPaths, scanIgnore, scanLimit)

	// Verify at least one test is configured.
	hasTests := len(cfg.Metadata) > 0 ||
		len(cfg.Files) > 0 ||
		len(cfg.Commands) > 0 ||
		len(cfg.Scan) > 0
	if !hasTests {
		fmt.Fprintf(os.Stderr, "error: no tests configured\n\nUse config files, --scan, or ensure the image has a scannable entrypoint.\n")
		return 2
	}

	// Build test plan and inject into container.
	plan := testplan.FromConfig(cfg)
	planJSON, err := json.Marshal(plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := sess.WriteFile("/tmp/tare-plan.json", planJSON, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Run tests.
	if sf.verbose {
		fmt.Fprintf(os.Stderr, "running checks...\n")
	}

	exitCode, err := sess.Exec(container.ExecOpts{}, container.HarnessBin("tare-tool"), "run-tests", "/tmp/tare-plan.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	printTareWeight(sess, cfg)

	return exitCode
}

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

// detectScanPath determines a default scan path from the image config's
// entrypoint or cmd. Relative paths are resolved against WorkingDir.
// Returns "" if nothing useful is found.
func detectScanPath(icfg *oci.ImageConfig) string {
	if icfg == nil {
		return ""
	}

	// Try entrypoint first, then cmd.
	var binary string
	var source string
	if len(icfg.Entrypoint) > 0 {
		binary = icfg.Entrypoint[0]
		source = "ENTRYPOINT"
	} else if len(icfg.Cmd) > 0 {
		binary = icfg.Cmd[0]
		source = "CMD"
	}

	if binary == "" {
		return ""
	}

	dir := filepath.Dir(binary)

	// Resolve relative paths against WORKDIR.
	if !filepath.IsAbs(dir) && icfg.WorkingDir != "" {
		dir = filepath.Join(icfg.WorkingDir, dir)
	}

	if systemDirs[dir] || !filepath.IsAbs(dir) {
		return ""
	}

	fmt.Fprintf(os.Stderr, "Autodetected scan path %s from:\n", dir)
	fmt.Fprintf(os.Stderr, "  %s: %s\n", source, binary)
	if icfg.WorkingDir != "" && !filepath.IsAbs(binary) {
		fmt.Fprintf(os.Stderr, "  WORKDIR: %s\n", icfg.WorkingDir)
	}
	fmt.Fprintf(os.Stderr, "\nUse \"tare check --scan\" or \"tare scan --path\" to override, or set tare.scan in a config file.\n\n")
	return dir
}
