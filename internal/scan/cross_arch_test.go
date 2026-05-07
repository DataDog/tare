package scan

import (
	"debug/elf"
	"encoding/binary"
	"path/filepath"
	"testing"
)

// makeMinimalELF builds a 64-byte ELF64 header with the given machine type.
// debug/elf parses it cleanly enough for InfoFromFile to extract the arch.
func makeMinimalELF(machine elf.Machine) []byte {
	h := make([]byte, 64)
	h[0] = 0x7f
	h[1] = 'E'
	h[2] = 'L'
	h[3] = 'F'
	h[4] = 2 // ELFCLASS64
	h[5] = 1 // ELFDATA2LSB
	h[6] = 1 // EV_CURRENT
	binary.LittleEndian.PutUint16(h[16:18], uint16(elf.ET_DYN))
	binary.LittleEndian.PutUint16(h[18:20], uint16(machine))
	binary.LittleEndian.PutUint32(h[20:24], 1)
	binary.LittleEndian.PutUint16(h[52:54], 64) // e_ehsize
	return h
}

func TestRunSkipsCrossArchJAREntries(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "fat.jar"), map[string][]byte{
		"native/amd64/libfoo.so": makeMinimalELF(elf.EM_X86_64),
		"native/arm64/libfoo.so": makeMinimalELF(elf.EM_AARCH64),
	})
	fsys := openTestRoot(t, dir)

	// Target amd64 — arm64 entry should be skipped.
	report, err := Run([]string{"/fat.jar"}, Options{
		FS:         fsys,
		NoRuntime:  true,
		TargetArch: "amd64",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.SkippedCrossArch != 1 {
		t.Errorf("SkippedCrossArch = %d, want 1", report.Summary.SkippedCrossArch)
	}
	if report.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1 (the amd64 entry)", report.Summary.Total)
	}
}

func TestRunNoSkipWhenTargetArchEmpty(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "fat.jar"), map[string][]byte{
		"native/amd64/libfoo.so": makeMinimalELF(elf.EM_X86_64),
		"native/arm64/libfoo.so": makeMinimalELF(elf.EM_AARCH64),
	})
	fsys := openTestRoot(t, dir)

	// No TargetArch — preserve current behavior (no auto-skip).
	report, err := Run([]string{"/fat.jar"}, Options{
		FS:        fsys,
		NoRuntime: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.SkippedCrossArch != 0 {
		t.Errorf("SkippedCrossArch = %d, want 0", report.Summary.SkippedCrossArch)
	}
	if report.Summary.Total != 2 {
		t.Errorf("Total = %d, want 2", report.Summary.Total)
	}
}

func TestArchMatches(t *testing.T) {
	tests := []struct {
		elfArch    string
		targetArch string
		want       bool
	}{
		{"x86_64", "amd64", true},
		{"amd64", "x86_64", true},
		{"aarch64", "arm64", true},
		{"arm64", "aarch64", true},
		{"x86_64", "arm64", false},
		{"aarch64", "amd64", false},
		{"unknown", "amd64", false},
	}
	for _, tt := range tests {
		if got := archMatches(tt.elfArch, tt.targetArch); got != tt.want {
			t.Errorf("archMatches(%q, %q) = %v, want %v", tt.elfArch, tt.targetArch, got, tt.want)
		}
	}
}
