package rootfs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStatFollowsAbsoluteSymlinkTargets covers a real-world layout from
// Ubuntu-derived images: /lib64 is a symlink to usr/lib64 (relative
// target), and /lib64/ld-linux-x86-64.so.2 is itself a symlink to an
// absolute path. os.Root rejects absolute symlink targets as escaping
// the rooted tree even when they would resolve back inside it, so
// rootfs.Root resolves symlinks itself with absolute targets treated
// as virtual-root-relative.
func TestStatFollowsAbsoluteSymlinkTargets(t *testing.T) {
	dir := t.TempDir()

	mkdirAll(t, dir, "lib/x86_64-linux-gnu")
	writeFile(t, dir, "lib/x86_64-linux-gnu/ld-linux-x86-64.so.2", "loader")

	mkdirAll(t, dir, "usr/lib64")
	symlink(t, "/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2",
		filepath.Join(dir, "usr/lib64/ld-linux-x86-64.so.2"))
	symlink(t, "usr/lib64", filepath.Join(dir, "lib64"))

	fsys := newRoot(t, dir, "/")

	cases := []string{
		"/lib64",
		"/usr/lib64/ld-linux-x86-64.so.2",
		"/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2",
		"/lib64/ld-linux-x86-64.so.2",
	}
	for _, p := range cases {
		if _, err := fsys.Stat(p); err != nil {
			t.Errorf("Stat(%s): %v", p, err)
		}
		f, err := fsys.Open(p)
		if err != nil {
			t.Errorf("Open(%s): %v", p, err)
			continue
		}
		f.Close()
	}

	data, err := fsys.ReadFile("/lib64/ld-linux-x86-64.so.2")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "loader" {
		t.Errorf("ReadFile content = %q, want %q", data, "loader")
	}

	// Lstat must NOT follow the symlink.
	info, err := fsys.Lstat("/lib64")
	if err != nil {
		t.Fatalf("Lstat(/lib64): %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("Lstat(/lib64) = %v, want symlink", info.Mode())
	}
}

func TestStatNonexistentReturnsNotExist(t *testing.T) {
	dir := t.TempDir()
	fsys := newRoot(t, dir, "/")
	_, err := fsys.Stat("/nope")
	if !os.IsNotExist(err) {
		t.Errorf("Stat(/nope): got %v, want IsNotExist", err)
	}
}

func TestSymlinkLoopDetected(t *testing.T) {
	dir := t.TempDir()
	symlink(t, "b", filepath.Join(dir, "a"))
	symlink(t, "a", filepath.Join(dir, "b"))

	fsys := newRoot(t, dir, "/")
	if _, err := fsys.Stat("/a"); err == nil {
		t.Error("Stat(/a) on a symlink loop: got nil error, want error")
	}
}

func mkdirAll(t *testing.T, dir, sub string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, dir, sub, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, sub), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func newRoot(t *testing.T, dir, cwd string) *Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return New(root, cwd)
}
