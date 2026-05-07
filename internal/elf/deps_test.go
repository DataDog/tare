package elf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/tare/internal/rootfs"
)

func TestExpandOrigin(t *testing.T) {
	tests := []struct {
		path   string
		origin string
		want   string
	}{
		{"$ORIGIN/../lib", "/usr/bin", "/usr/bin/../lib"},
		{"${ORIGIN}/lib", "/usr/bin", "/usr/bin/lib"},
		{"$ORIGIN:$ORIGIN/../lib", "/opt/app/bin", "/opt/app/bin:/opt/app/bin/../lib"},
		{"/usr/lib", "/usr/bin", "/usr/lib"},
	}
	for _, tt := range tests {
		got := expandOrigin(tt.path, tt.origin)
		if got != tt.want {
			t.Errorf("expandOrigin(%q, %q) = %q, want %q", tt.path, tt.origin, got, tt.want)
		}
	}
}

func TestResolveLib(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "lib")
	os.MkdirAll(libDir, 0o755)
	os.WriteFile(filepath.Join(libDir, "libfoo.so"), []byte("fake"), 0o644)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fsys := rootfs.New(root, "/")

	got := resolveLib(fsys, "libfoo.so", []string{"/nonexistent", "/lib"})
	if got != filepath.Join("/lib", "libfoo.so") {
		t.Errorf("resolveLib = %q, want %q", got, filepath.Join("/lib", "libfoo.so"))
	}

	got = resolveLib(fsys, "libbar.so", []string{"/lib"})
	if got != "" {
		t.Errorf("resolveLib for missing lib = %q, want empty", got)
	}
}

func TestHasMissing(t *testing.T) {
	tests := []struct {
		name   string
		result DepsResult
		want   bool
	}{
		{"empty", DepsResult{}, false},
		{"all ok", DepsResult{Deps: []Dep{{Name: "libc.so.6", Status: DepOK}}}, false},
		{"missing dep", DepsResult{Deps: []Dep{{Name: "libc.so.6", Status: DepMissing}}}, true},
		{"missing interp", DepsResult{Interp: &Interp{Path: "/lib64/ld-linux.so.2", Status: DepMissing}}, true},
		{"ok interp", DepsResult{Interp: &Interp{Path: "/lib64/ld-linux.so.2", Status: DepOK}}, false},
		{"mixed", DepsResult{
			Interp: &Interp{Path: "/lib64/ld-linux.so.2", Status: DepOK},
			Deps:   []Dep{{Name: "libc.so.6", Status: DepOK}, {Name: "libm.so.6", Status: DepMissing}},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.HasMissing(); got != tt.want {
				t.Errorf("HasMissing() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsDynamic(t *testing.T) {
	tests := []struct {
		name   string
		result DepsResult
		want   bool
	}{
		{"empty", DepsResult{}, false},
		{"has deps", DepsResult{Deps: []Dep{{Name: "libc.so.6"}}}, true},
		{"has interp only", DepsResult{Interp: &Interp{Path: "/lib64/ld-linux.so.2"}}, true},
		{"both", DepsResult{
			Interp: &Interp{Path: "/lib64/ld-linux.so.2"},
			Deps:   []Dep{{Name: "libc.so.6"}},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.IsDynamic(); got != tt.want {
				t.Errorf("IsDynamic() = %v, want %v", got, tt.want)
			}
		})
	}
}
