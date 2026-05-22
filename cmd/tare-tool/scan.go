package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DataDog/tare/internal/scan"
)

// ignoreFlags collects repeated --ignore flags.
type ignoreFlags []string

func (f *ignoreFlags) String() string { return strings.Join(*f, ", ") }
func (f *ignoreFlags) Set(val string) error {
	*f = append(*f, val)
	return nil
}

func runScan(args []string) int {
	fs := flag.NewFlagSet("tare-tool scan", flag.ExitOnError)
	var ignores ignoreFlags
	scanLimit := fs.Int("limit", scan.DefaultLimit, "")
	format := fs.String("format", "", "")
	noRuntime := fs.Bool("no-runtime", false, "")
	targetArch := fs.String("target-arch", "", "")
	fs.Var(&ignores, "ignore", "")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: tare-tool scan [--format json|bats] [--ignore PATTERN]... [--limit N] [--target-arch ARCH] PATH [PATH...]\n")
	}
	fs.Parse(args)

	if *format != "" {
		valid := map[string]bool{"json": true, "bats": true}
		if !valid[*format] {
			fmt.Fprintf(os.Stderr, "error: invalid format %q (use json or bats)\n", *format)
			return 1
		}
	}

	if *scanLimit <= 0 {
		fmt.Fprintf(os.Stderr, "error: --limit must be greater than 0\n")
		return 1
	}

	paths := fs.Args()
	if len(paths) == 0 {
		fs.Usage()
		return 1
	}

	var patterns []scan.IgnorePattern
	for _, ig := range ignores {
		p, err := scan.ParseIgnorePattern(ig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		patterns = append(patterns, p)
	}

	report, err := scan.Run(paths, scan.Options{
		Ignore:     patterns,
		Limit:      *scanLimit,
		NoRuntime:  *noRuntime,
		TargetArch: *targetArch,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan: %v\n", err)
		return 1
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(report)
	case "bats":
		scan.PrintReport(os.Stdout, report, "# ")
	default:
		scan.PrintReport(os.Stdout, report, "")
	}

	if report.Summary.Errors > 0 {
		return 1
	}
	return 0
}
