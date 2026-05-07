package rootfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "main"), []byte("bin"), 0o644)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	fsys := New(root, "/")

	data, err := fsys.ReadFile("/app/main")
	if err != nil {
		t.Fatalf("ReadFile(/app/main): %v", err)
	}
	if string(data) != "bin" {
		t.Errorf("content = %q, want %q", data, "bin")
	}
}

func TestRelativePathResolvedAgainstCWD(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app", "lib"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "lib", "data.txt"), []byte("hello"), 0o644)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// CWD is /app — relative path "lib/data.txt" should resolve to /app/lib/data.txt
	fsys := New(root, "/app")

	data, err := fsys.ReadFile("lib/data.txt")
	if err != nil {
		t.Fatalf("ReadFile(lib/data.txt) with cwd=/app: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}
}

func TestRelativePathWithDotSlash(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "home", "app"), 0o755)
	os.WriteFile(filepath.Join(dir, "home", "app", "config.yaml"), []byte("key: val"), 0o644)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	fsys := New(root, "/home/app")

	data, err := fsys.ReadFile("./config.yaml")
	if err != nil {
		t.Fatalf("ReadFile(./config.yaml) with cwd=/home/app: %v", err)
	}
	if string(data) != "key: val" {
		t.Errorf("content = %q, want %q", data, "key: val")
	}
}

func TestRootPath(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file"), []byte("ok"), 0o644)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	fsys := New(root, "/")

	// Stat "/" should work (the root directory).
	info, err := fsys.Stat("/")
	if err != nil {
		t.Fatalf("Stat(/): %v", err)
	}
	if !info.IsDir() {
		t.Error("/ should be a directory")
	}
}

func TestEmptyCWDDefaultsToRoot(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "top.txt"), []byte("top"), 0o644)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Empty cwd should default to "/"
	fsys := New(root, "")

	data, err := fsys.ReadFile("top.txt")
	if err != nil {
		t.Fatalf("ReadFile(top.txt) with empty cwd: %v", err)
	}
	if string(data) != "top" {
		t.Errorf("content = %q, want %q", data, "top")
	}
}

func TestWalkDirWithAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app", "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "bin", "tool"), []byte("x"), 0o644)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	fsys := New(root, "/")

	var found []string
	walkFS := fsys.FS()
	_, err = walkFS.(interface {
		Stat(string) (os.FileInfo, error)
	}).Stat("/app")
	if err != nil {
		t.Fatalf("walkFS.Stat(/app): %v", err)
	}

	// Verify Open works through the walk FS too.
	f, err := walkFS.Open("/app/bin/tool")
	if err != nil {
		t.Fatalf("walkFS.Open(/app/bin/tool): %v", err)
	}
	f.Close()
	_ = found
}
