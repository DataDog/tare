package scan

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeJar creates a jar file at path with the given entries.
func writeJar(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, data := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScanJarFindsELF(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "app.jar"), map[string][]byte{
		"META-INF/native/libfoo.so": fakeELF,
		"com/example/Main.class":    []byte("not elf"),
	})

	root := openTestRoot(t, dir)
	entries := scanJar(root, "/app.jar")
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].EntryName != "META-INF/native/libfoo.so" {
		t.Errorf("entry name = %q, want META-INF/native/libfoo.so", entries[0].EntryName)
	}
	wantDisplay := "/app.jar!/META-INF/native/libfoo.so"
	if entries[0].displayPath() != wantDisplay {
		t.Errorf("display path = %q, want %q", entries[0].displayPath(), wantDisplay)
	}
}

func TestScanJarVersionedSO(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "app.jar"), map[string][]byte{
		"lib/libbar.so.1":   fakeELF,
		"lib/libbar.so.1.2": fakeELF,
	})

	root := openTestRoot(t, dir)
	entries := scanJar(root, "/app.jar")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
}

func TestScanJarNoELFs(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "pure.jar"), map[string][]byte{
		"com/example/Main.class": []byte("class data"),
		"META-INF/MANIFEST.MF":  []byte("Manifest-Version: 1.0"),
	})

	root := openTestRoot(t, dir)
	entries := scanJar(root, "/pure.jar")
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
}

func TestScanJarSOWithoutELFMagic(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "fake.jar"), map[string][]byte{
		"lib/notelf.so": []byte("this is not an ELF"),
	})

	root := openTestRoot(t, dir)
	entries := scanJar(root, "/fake.jar")
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0: .so without ELF magic should be skipped", len(entries))
	}
}

func TestScanJarNotAZip(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.jar"), []byte("not a zip"), 0o644)

	root := openTestRoot(t, dir)
	entries := scanJar(root, "/bad.jar")
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0 for invalid jar", len(entries))
	}
}

func TestIsJar(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"app.jar", true},
		{"app.JAR", true},
		{"app.war", true},
		{"app.ear", true},
		{"app.zip", false},
		{"app.so", false},
		{"app.jar.bak", false},
	}
	for _, tt := range tests {
		if got := isJar(tt.path); got != tt.want {
			t.Errorf("isJar(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestIsSO(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"libfoo.so", true},
		{"libfoo.so.1", true},
		{"libfoo.so.1.2.3", true},
		{"Main.class", false},
		{"MANIFEST.MF", false},
		{"lib/nested/libbar.so", true},
	}
	for _, tt := range tests {
		if got := isSO(tt.name); got != tt.want {
			t.Errorf("isSO(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRunWithJar(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "app.jar"), map[string][]byte{
		"lib/libfoo.so": fakeELF,
	})

	root := openTestRoot(t, dir)
	report, err := Run([]string{"/"}, Options{FS: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.Total != 1 {
		t.Fatalf("expected 1 binary, got %d", report.Summary.Total)
	}

	bin := report.Binaries[0]
	wantPath := "/app.jar!/lib/libfoo.so"
	if bin.Info.Path != wantPath {
		t.Errorf("binary path = %q, want %q", bin.Info.Path, wantPath)
	}
	if bin.Err == "" {
		t.Error("expected error for fake ELF magic bytes")
	}
}
