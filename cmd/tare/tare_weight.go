package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/DataDog/tare/internal/config"
	"github.com/DataDog/tare/internal/container"
)

// printTareWeight measures the image filesystem and scan path sizes using du,
// computes the tare weight (image size minus application size), and prints a
// summary to stderr.
func printTareWeight(sess *container.Session, cfg *config.Config) {
	if len(cfg.Scan) == 0 {
		return
	}

	// Measure total image filesystem size. The -x flag avoids crossing
	// filesystem boundaries, naturally excluding /proc, /sys, /dev, etc.
	imageKB, ok := duSize(sess, "-skx", "/")
	if !ok || imageKB == 0 {
		return
	}

	// Measure application size under scan paths.
	duArgs := []string{"-sk"}
	scanPaths := make([]string, len(cfg.Scan))
	for i, entry := range cfg.Scan {
		duArgs = append(duArgs, entry.Path)
		scanPaths[i] = entry.Path
	}
	appKB, ok := duSize(sess, duArgs...)
	if !ok {
		return
	}

	imageFiles := fileCount(sess, "/")
	appFiles := fileCount(sess, scanPaths...)

	imageBytes := imageKB * 1024
	appBytes := appKB * 1024
	tareWeight := max(imageBytes-appBytes, 0)
	tareFiles := max(imageFiles-appFiles, 0)

	fmt.Fprintf(os.Stderr, "\ntare weight: %s, %s (image: %s, %s; application: %s, %s)\n",
		formatBytes(tareWeight), formatCount(tareFiles),
		formatBytes(imageBytes), formatCount(imageFiles),
		formatBytes(appBytes), formatCount(appFiles))
}

// duSize runs du inside the container and returns the total size in KB.
// For multiple paths, sizes are summed.
func duSize(sess *container.Session, args ...string) (int64, bool) {
	cmd := append([]string{"du"}, args...)
	var stdout bytes.Buffer
	// Ignore exit code — busybox du exits non-zero on permission errors
	// but still prints valid totals for accessible paths.
	_, err := sess.Exec(container.ExecOpts{
		Stdout: &stdout,
		Stderr: io.Discard,
	}, cmd...)
	if err != nil {
		return 0, false
	}

	var total int64
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0, false
		}
		total += kb
	}
	return total, true
}

// fileCount runs find -type f inside the container and returns the number of
// regular files under the given paths. Returns 0 on any error.
func fileCount(sess *container.Session, paths ...string) int64 {
	cmd := []string{"find"}
	cmd = append(cmd, paths...)
	cmd = append(cmd, "-xdev", "-type", "f")
	var stdout bytes.Buffer
	_, err := sess.Exec(container.ExecOpts{
		Stdout: &stdout,
		Stderr: io.Discard,
	}, cmd...)
	if err != nil {
		return 0
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return 0
	}
	return int64(strings.Count(out, "\n") + 1)
}

// formatCount returns a human-readable file count string.
func formatCount(n int64) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// formatBytes returns a human-readable byte size string.
func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
