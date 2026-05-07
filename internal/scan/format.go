package scan

import (
	"fmt"
	"io"

	tareelf "github.com/DataDog/tare/internal/elf"
)

// PrintReport writes a human-readable scan report to w. If prefix is non-empty,
// each line is prefixed (e.g. "# " for TAP/BATS compatibility).
func PrintReport(w io.Writer, report *Report, prefix string) {
	p := func(format string, args ...any) {
		if prefix != "" && format == "\n" {
			fmt.Fprint(w, "\n")
			return
		}
		fmt.Fprintf(w, prefix+format, args...)
	}

	for _, bin := range report.Binaries {
		if bin.Err != "" {
			p("%s: error: %s\n", bin.Info.Path, bin.Err)
			continue
		}

		if bin.Info.Type == tareelf.TypeStatic {
			continue
		}

		hasMissing := bin.Deps != nil && bin.Deps.HasMissing()
		status := "ok"
		if hasMissing {
			status = "FAIL"
		}

		p("%s: %s (%s)\n", bin.Info.Path, status, bin.Info.Arch)

		if bin.Deps == nil {
			continue
		}

		if bin.Deps.Interp != nil && bin.Deps.Interp.Status == tareelf.DepMissing {
			p("  interpreter: %s [MISSING]\n", bin.Deps.Interp.Path)
		}

		for _, dep := range bin.Deps.Deps {
			if dep.Status == tareelf.DepMissing {
				p("  %s => not found\n", dep.Name)
			}
		}
	}

	if len(report.Warnings) > 0 {
		p("\n")
		p("warnings:\n")
		for _, w := range report.Warnings {
			p("  %s not found — %s\n", w.Path, w.Message)
		}
	}

	if report.Truncated {
		p("\n")
		p("warning: scan limit reached (%d binaries). Use --path or --limit to adjust.\n", report.Summary.Total)
	}

	s := report.Summary
	line := fmt.Sprintf("%d binaries scanned: %d ok", s.Total, s.OK)
	if s.Static > 0 {
		line += fmt.Sprintf(" (%d static)", s.Static)
	}
	line += fmt.Sprintf(", %d with errors", s.Errors)
	if s.Warnings > 0 {
		line += fmt.Sprintf(", %d warnings", s.Warnings)
	}
	if s.SkippedCrossArch > 0 {
		line += fmt.Sprintf(", %d cross-architecture entries skipped (in JARs)", s.SkippedCrossArch)
	}
	p("\n")
	p("%s\n", line)
}

// MergeReports combines multiple reports into one with aggregated summaries.
func MergeReports(reports ...*Report) *Report {
	merged := &Report{}
	for _, r := range reports {
		merged.Binaries = append(merged.Binaries, r.Binaries...)
		merged.Warnings = append(merged.Warnings, r.Warnings...)
		merged.Summary.Total += r.Summary.Total
		merged.Summary.OK += r.Summary.OK
		merged.Summary.Static += r.Summary.Static
		merged.Summary.DynamicOK += r.Summary.DynamicOK
		merged.Summary.Errors += r.Summary.Errors
		merged.Summary.Warnings += r.Summary.Warnings
		merged.Summary.SkippedCrossArch += r.Summary.SkippedCrossArch
		if r.Truncated {
			merged.Truncated = true
		}
	}
	return merged
}
