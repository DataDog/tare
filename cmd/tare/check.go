package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/DataDog/tare/internal/container"
	"github.com/DataDog/tare/internal/testplan"
)

const checkUsage = `Usage:
    tare check -i IMAGE [options] [config-files...]

Check a container image for runtime issues and run structure tests.

By default, checks common runtime files and autoscans the image for shared
library dependencies. Autoscan looks at ENTRYPOINT/CMD plus library-path
env vars (PYTHONPATH, LD_LIBRARY_PATH, CLASSPATH, NODE_PATH, PERL5LIB, GEM_PATH).
Config files add structure tests (file existence, file content, commands,
metadata).

Options:
    -i, --image IMAGE         Container image to check (required)
        --scan PATH           Additional path to scan for ELF dependencies (repeatable)
        --scan-ignore PAT     Ignore a binary path or library name during scan (repeatable)
        --scan-limit N        Max ELF binaries to scan (default: 1024)
        --no-autoscan         Disable ENTRYPOINT/CMD + env var scan-path detection
                              (paths in tare.scan or --scan are still scanned)
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

	return withSession(&sf, cfg, func(sess *container.Session) int {
		// Autodetect scan paths from image config if no paths specified.
		if len(scanPaths) == 0 && !sf.noAutoscan {
			ensureDefaultScanPath(cfg, sess.Config, sess.PathExists, sf.verbose)
		}
		mergeScanFlags(cfg, scanPaths, scanIgnore, scanLimit)

		// Verify at least one test is configured.
		hasTests := len(cfg.Metadata) > 0 ||
			len(cfg.Files) > 0 ||
			len(cfg.Commands) > 0 ||
			len(cfg.Scan) > 0
		if !hasTests {
			fmt.Fprintf(os.Stderr, "error: no tests configured\n\nUse config files, --scan, or ensure the image has a scannable entrypoint.\n")
			return 1
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
	})
}

