package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/tare/internal/rootfs"
)

var fakeELF = []byte{0x7f, 'E', 'L', 'F', 0, 0, 0, 0}

func collectDiscovered(fsys rootfs.FS, paths []string, limit int) ([]workItem, discoverResult) {
	ch := make(chan workItem, limit+1)
	result := discover(fsys, paths, limit, ch)
	close(ch)
	var items []workItem
	for item := range ch {
		items = append(items, item)
	}
	return items, result
}

func TestDiscoverELFs(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "binary1"), fakeELF, 0o755)
	os.WriteFile(filepath.Join(dir, "script.sh"), []byte("#!/bin/bash\necho hi"), 0o755)

	subdir := filepath.Join(dir, "sub")
	os.MkdirAll(subdir, 0o755)
	os.WriteFile(filepath.Join(subdir, "binary2"), fakeELF, 0o755)

	root := openTestRoot(t, dir)
	items, result := collectDiscovered(root, []string{"/"}, DefaultLimit)

	if result.truncated {
		t.Error("unexpected truncation")
	}
	if len(items) != 2 {
		t.Fatalf("discovered %d items, want 2: %v", len(items), items)
	}

	names := map[string]bool{}
	for _, item := range items {
		names[filepath.Base(item.path)] = true
	}
	if !names["binary1"] || !names["binary2"] {
		t.Errorf("expected binary1 and binary2, got %v", names)
	}
}

func TestDiscoverMultiplePaths(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a"), 0o755)
	os.MkdirAll(filepath.Join(dir, "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "bin1"), fakeELF, 0o755)
	os.WriteFile(filepath.Join(dir, "b", "bin2"), fakeELF, 0o755)

	root := openTestRoot(t, dir)
	items, _ := collectDiscovered(root, []string{"/a", "/b"}, DefaultLimit)
	if len(items) != 2 {
		t.Fatalf("discovered %d items, want 2", len(items))
	}
}

func TestDiscoverTruncated(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("bin%d", i)), fakeELF, 0o755)
	}

	root := openTestRoot(t, dir)
	items, result := collectDiscovered(root, []string{"/"}, 3)
	if !result.truncated {
		t.Error("expected truncation with limit 3 and 5 binaries")
	}
	if len(items) != 3 {
		t.Errorf("got %d items, want 3", len(items))
	}
}

func TestDiscoverJars(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "app.jar"), map[string][]byte{
		"lib/libfoo.so": fakeELF,
		"lib/libbar.so": fakeELF,
	})

	root := openTestRoot(t, dir)
	items, _ := collectDiscovered(root, []string{"/"}, DefaultLimit)
	if len(items) != 2 {
		t.Fatalf("discovered %d items, want 2", len(items))
	}
	for _, item := range items {
		if item.jarEntry == nil {
			t.Error("expected jar entry, got filesystem path")
		}
	}
}

func TestDiscoverMixed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app"), fakeELF, 0o755)
	writeJar(t, filepath.Join(dir, "lib.jar"), map[string][]byte{
		"native/libfoo.so": fakeELF,
	})

	root := openTestRoot(t, dir)
	items, _ := collectDiscovered(root, []string{"/"}, DefaultLimit)
	if len(items) != 2 {
		t.Fatalf("discovered %d items, want 2", len(items))
	}

	hasELF, hasJar := false, false
	for _, item := range items {
		if item.path != "" {
			hasELF = true
		}
		if item.jarEntry != nil {
			hasJar = true
		}
	}
	if !hasELF || !hasJar {
		t.Error("expected both ELF and jar entries")
	}
}

func TestDiscoverJarTruncated(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "big.jar"), map[string][]byte{
		"lib/a.so": fakeELF,
		"lib/b.so": fakeELF,
		"lib/c.so": fakeELF,
	})

	root := openTestRoot(t, dir)
	items, result := collectDiscovered(root, []string{"/"}, 2)
	if !result.truncated {
		t.Error("expected truncation with limit 2 and 3 jar entries")
	}
	if len(items) != 2 {
		t.Errorf("got %d items, want 2", len(items))
	}
}

func TestRunEmptyDir(t *testing.T) {
	root := openTestRoot(t, t.TempDir())
	report, err := Run([]string{"/"}, Options{FS: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.Total != 0 {
		t.Errorf("expected 0 binaries, got %d", report.Summary.Total)
	}
	if report.Binaries == nil {
		t.Error("Binaries should be empty slice, not nil")
	}
}
