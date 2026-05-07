package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/tare/internal/rootfs"
)

func TestCheckRuntimeKnownFiles(t *testing.T) {
	paths := make(map[string]bool)
	for _, rf := range runtimeFiles {
		paths[rf.path] = true
	}

	expected := []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/nsswitch.conf",
		"/etc/passwd",
		"/etc/group",
	}
	for _, p := range expected {
		if !paths[p] {
			t.Errorf("expected %s in runtime file checks", p)
		}
	}
}

func openTestRoot(t *testing.T, dir string) *rootfs.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return rootfs.New(root, "/")
}

func TestCheckFilesMissing(t *testing.T) {
	root := openTestRoot(t, t.TempDir())

	files := []runtimeFile{
		{path: "/nonexistent", message: "test warning"},
	}

	warnings := checkFiles(root, files)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Message != "test warning" {
		t.Errorf("unexpected message: %s", warnings[0].Message)
	}
}

func TestCheckFilesExisting(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "exists"), []byte("ok"), 0o644)
	root := openTestRoot(t, dir)

	files := []runtimeFile{
		{path: "/exists", message: "should not appear"},
	}

	warnings := checkFiles(root, files)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d", len(warnings))
	}
}

func TestCheckFilesMixed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "exists"), []byte("ok"), 0o644)
	root := openTestRoot(t, dir)

	files := []runtimeFile{
		{path: "/exists", message: "exists"},
		{path: "/missing", message: "missing file"},
	}

	warnings := checkFiles(root, files)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Message != "missing file" {
		t.Errorf("unexpected message: %s", warnings[0].Message)
	}
}
