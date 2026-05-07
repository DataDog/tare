package tare_test

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			abs, _ := filepath.Abs(dir)
			return abs
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root (no go.mod found)")
		}
		dir = parent
	}
}

func findHarnessDir(t *testing.T) string {
	t.Helper()
	candidate := filepath.Join(findModuleRoot(t), "harness", "linux-"+runtime.GOARCH)
	if _, err := os.Stat(filepath.Join(candidate, "bin", "tare-tool")); err == nil {
		return candidate
	}
	t.Fatalf("no harness found for %s; run 'make harness' first", runtime.GOARCH)
	return ""
}

type tapResult struct {
	ExitCode int
	Output   string
}

func assertGolden(t *testing.T, name string, result tapResult) {
	t.Helper()
	goldenPath := filepath.Join("testdata", "golden", name+".tap")

	if *update {
		if err := os.WriteFile(goldenPath, []byte(result.Output), 0o644); err != nil {
			t.Fatalf("updating golden file: %v", err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file %s: %v (run with -update to create)", goldenPath, err)
	}

	if result.Output != string(want) {
		t.Errorf("TAP output mismatch for %s\n\ngot:\n%s\nwant:\n%s", name, result.Output, string(want))
	}
}

func assertAllPass(t *testing.T, result tapResult) {
	t.Helper()
	if result.ExitCode != 0 {
		t.Errorf("tests exited with code %d", result.ExitCode)
	}
	if strings.Contains(result.Output, "not ok") {
		t.Error("unexpected test failure in TAP output")
	}
	if !strings.Contains(result.Output, "ok ") {
		t.Error("no test results in TAP output")
	}
}
