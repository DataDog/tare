package elf

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/tare/internal/rootfs"
)

func TestCheckELF(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "real.elf"), []byte{0x7f, 'E', 'L', 'F', 0, 0, 0, 0}, 0o644)
	os.WriteFile(filepath.Join(dir, "text.txt"), []byte("hello world"), 0o644)
	os.WriteFile(filepath.Join(dir, "empty"), []byte{}, 0o644)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fsys := rootfs.New(root, "/")

	if ok, err := CheckELF(fsys, "/real.elf"); !ok || err != nil {
		t.Errorf("CheckELF(elf) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := CheckELF(fsys, "/text.txt"); ok || err != nil {
		t.Errorf("CheckELF(text) = %v, %v; want false, nil", ok, err)
	}
	if ok, err := CheckELF(fsys, "/empty"); ok || err != nil {
		t.Errorf("CheckELF(empty) = %v, %v; want false, nil", ok, err)
	}
	if ok, err := CheckELF(fsys, "/nonexistent"); ok || err == nil {
		t.Errorf("CheckELF(nonexistent) = %v, %v; want false, non-nil error", ok, err)
	}

	os.WriteFile(filepath.Join(dir, "noperm.elf"), []byte{0x7f, 'E', 'L', 'F', 0, 0, 0, 0}, 0o000)
	if ok, err := CheckELF(fsys, "/noperm.elf"); ok || err == nil {
		t.Errorf("CheckELF(unreadable) = %v, %v; want false, non-nil error", ok, err)
	}
}

func TestMachineArch(t *testing.T) {
	tests := []struct {
		machine elf.Machine
		want    string
	}{
		{elf.EM_X86_64, "x86_64"},
		{elf.EM_AARCH64, "aarch64"},
		{elf.EM_386, "i386"},
		{elf.EM_ARM, "arm"},
		{elf.EM_MIPS, "unknown(8)"},
	}
	for _, tt := range tests {
		got := machineArch(tt.machine)
		if got != tt.want {
			t.Errorf("machineArch(%v) = %q, want %q", tt.machine, got, tt.want)
		}
	}
}
