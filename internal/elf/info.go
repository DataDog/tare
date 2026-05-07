package elf

import (
	"debug/elf"
	"fmt"

	"github.com/DataDog/tare/internal/rootfs"
)

// BinaryType indicates whether a binary is statically or dynamically linked.
type BinaryType string

const (
	TypeStatic  BinaryType = "static"
	TypeDynamic BinaryType = "dynamic"
)

// BinaryInfo contains metadata about an ELF binary.
type BinaryInfo struct {
	Path string     `json:"path"`
	Type BinaryType `json:"type"`
	Arch string     `json:"arch"`
}

// Info extracts metadata from an ELF binary.
func Info(fsys rootfs.FS, path string) (*BinaryInfo, error) {
	f, err := openELF(fsys, path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return InfoFromFile(f, path), nil
}

// InfoFromFile extracts metadata from an already-opened ELF file.
// path is used for the BinaryInfo.Path field.
func InfoFromFile(f *elf.File, path string) *BinaryInfo {
	info := &BinaryInfo{
		Path: path,
		Arch: machineArch(f.Machine),
	}

	libs, _ := f.DynString(elf.DT_NEEDED)
	if len(libs) > 0 || getInterp(f) != "" {
		info.Type = TypeDynamic
	} else {
		info.Type = TypeStatic
	}

	return info
}

// CheckELF reports whether path is an ELF binary by reading its magic bytes.
// This is cheaper than elf.Open for scanning directories.
// If the file could not be read (e.g. permission denied), err is non-nil.
func CheckELF(fsys rootfs.FS, path string) (isELF bool, err error) {
	f, err := fsys.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var magic [4]byte
	if _, err := f.Read(magic[:]); err != nil {
		return false, nil
	}
	return magic == [4]byte{0x7f, 'E', 'L', 'F'}, nil
}

func machineArch(m elf.Machine) string {
	switch m {
	case elf.EM_X86_64:
		return "x86_64"
	case elf.EM_AARCH64:
		return "aarch64"
	case elf.EM_386:
		return "i386"
	case elf.EM_ARM:
		return "arm"
	default:
		return fmt.Sprintf("unknown(%d)", m)
	}
}
