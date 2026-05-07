package oci

import (
	"archive/tar"
	"io/fs"
	"syscall"
	"testing"
)

func TestFSStatReturnsMetadata(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build([]layerEntry{
		{Name: "file.txt", Content: []byte("data"), Mode: 0o644, UID: 1000, GID: 2000},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	fsys := NewFS(result, "/")

	info, err := fsys.Stat("/file.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644", info.Mode().Perm())
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("Sys() did not return *syscall.Stat_t")
	}
	if stat.Uid != 1000 || stat.Gid != 2000 {
		t.Errorf("uid/gid = %d/%d, want 1000/2000", stat.Uid, stat.Gid)
	}
}

func TestFSReadFile(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build([]layerEntry{
		{Name: "hello.txt", Content: []byte("hello from oci"), Mode: 0o644},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	fsys := NewFS(result, "/")

	data, err := fsys.ReadFile("/hello.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello from oci" {
		t.Errorf("content = %q, want %q", data, "hello from oci")
	}
}

func TestFSOpenReaderAt(t *testing.T) {
	tl := newTestLayout(t)
	content := []byte("0123456789abcdef")
	l := tl.build([]layerEntry{
		{Name: "data.bin", Content: content, Mode: 0o644},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	fsys := NewFS(result, "/")

	f, err := fsys.Open("/data.bin")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	buf := make([]byte, 4)
	n, err := f.ReadAt(buf, 10)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != 4 || string(buf) != "abcd" {
		t.Errorf("ReadAt(10) = %q, want %q", buf[:n], "abcd")
	}
}

func TestFSLstat(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build([]layerEntry{
		{Name: "target.txt", Content: []byte("real"), Mode: 0o644},
		{Name: "link.txt", Type: tar.TypeSymlink, Linkname: "target.txt"},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	fsys := NewFS(result, "/")

	info, err := fsys.Lstat("/link.txt")
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Error("Lstat should report symlink mode")
	}

	info, err = fsys.Stat("/link.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		t.Error("Stat should follow symlink")
	}
}

func TestFSWalkDirStatUsesMetadata(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build([]layerEntry{
		{Name: "file.txt", Content: []byte("data"), Mode: 0o644, UID: 42, GID: 42},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	fsys := NewFS(result, "/")
	walkFS := fsys.FS()

	// Stat through the walk FS should return metadata.
	info, err := walkFS.(fs.StatFS).Stat("/file.txt")
	if err != nil {
		t.Fatalf("walkFS.Stat: %v", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("walkFS.Stat Sys() did not return *syscall.Stat_t")
	}
	if stat.Uid != 42 {
		t.Errorf("walkFS.Stat uid = %d, want 42", stat.Uid)
	}
}
