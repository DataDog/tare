package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchFromPlatform(t *testing.T) {
	tests := []struct {
		platform string
		want     string
	}{
		{"linux/amd64", "amd64"},
		{"linux/arm64", "arm64"},
		{"linux/386", "386"},
		{"amd64", "amd64"},
		{"", ""},
	}
	for _, tt := range tests {
		got := archFromPlatform(tt.platform)
		if got != tt.want {
			t.Errorf("archFromPlatform(%q) = %q, want %q", tt.platform, got, tt.want)
		}
	}
}

func TestIsHarnessDir(t *testing.T) {
	dir := t.TempDir()

	// Not a harness dir — no bin/bash.
	if isHarnessDir(dir) {
		t.Error("empty dir should not be a harness dir")
	}

	// Create bin/bash.
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)
	os.WriteFile(filepath.Join(binDir, "bash"), []byte("fake"), 0o755)

	if !isHarnessDir(dir) {
		t.Error("dir with bin/bash should be a harness dir")
	}
}

func TestResolveDir(t *testing.T) {
	dir := t.TempDir()

	// Create a harness at linux-amd64/bin/bash.
	harnessDir := filepath.Join(dir, "linux-amd64")
	os.MkdirAll(filepath.Join(harnessDir, "bin"), 0o755)
	os.WriteFile(filepath.Join(harnessDir, "bin", "bash"), []byte("fake"), 0o755)

	// Should find the subdirectory.
	got, err := resolveDir(dir, "linux/amd64")
	if err != nil {
		t.Fatalf("resolveDir: %v", err)
	}
	abs, _ := filepath.Abs(harnessDir)
	if got != abs {
		t.Errorf("resolveDir = %q, want %q", got, abs)
	}

	// Direct path should also work.
	got, err = resolveDir(harnessDir, "linux/amd64")
	if err != nil {
		t.Fatalf("resolveDir direct: %v", err)
	}
	if got != abs {
		t.Errorf("resolveDir direct = %q, want %q", got, abs)
	}

	// Missing harness should error.
	_, err = resolveDir(dir, "linux/arm64")
	if err == nil {
		t.Error("expected error for missing harness")
	}
}
