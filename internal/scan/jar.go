package scan

import (
	"archive/zip"
	"bytes"
	"debug/elf"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	tareelf "github.com/DataDog/tare/internal/elf"
	"github.com/DataDog/tare/internal/rootfs"
)

// jarEntry represents an ELF shared object found inside a jar.
type jarEntry struct {
	JarPath   string // absolute path to the jar (e.g., "/app/lib.jar")
	EntryName string // zip entry name (e.g. "META-INF/native/libfoo.so")
	Data      []byte // raw bytes of the entry
}

// displayPath returns the path as shown in analysis output.
func (j jarEntry) displayPath() string {
	return j.JarPath + "!/" + j.EntryName
}

// scanJar opens a jar and returns entries for any ELF shared objects inside.
func scanJar(fsys rootfs.FS, path string) []jarEntry {
	f, err := fsys.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil
	}

	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return nil
	}

	var entries []jarEntry
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		if !isSO(zf.Name) {
			continue
		}

		data, err := readZipEntry(zf)
		if err != nil {
			continue
		}
		if !hasELFMagic(data) {
			continue
		}

		entries = append(entries, jarEntry{
			JarPath:   path,
			EntryName: zf.Name,
			Data:      data,
		})
	}

	return entries
}

// analyzeJarEntry analyzes an ELF shared object read from a jar.
func analyzeJarEntry(fsys rootfs.FS, entry jarEntry) *BinaryResult {
	displayPath := entry.displayPath()

	r := bytes.NewReader(entry.Data)
	f, err := elf.NewFile(r)
	if err != nil {
		return &BinaryResult{
			Info: &tareelf.BinaryInfo{Path: displayPath},
			Err:  err.Error(),
		}
	}
	defer f.Close()

	info := tareelf.InfoFromFile(f, displayPath)
	result := &BinaryResult{Info: info}

	if info.Type == tareelf.TypeDynamic {
		// Use the jar's directory for $ORIGIN expansion.
		deps, err := tareelf.DepsFromFile(fsys, f, entry.JarPath)
		if err != nil {
			result.Err = err.Error()
		} else {
			result.Deps = deps
		}
	}

	return result
}

func isJar(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".jar" || ext == ".war" || ext == ".ear"
}

func isSO(name string) bool {
	base := filepath.Base(name)
	return strings.HasSuffix(base, ".so") || strings.Contains(base, ".so.")
}

func hasELFMagic(data []byte) bool {
	return len(data) >= 4 && data[0] == 0x7f && data[1] == 'E' && data[2] == 'L' && data[3] == 'F'
}

// maxSOSize is the maximum decompressed size we'll read for a single .so
// entry. Anything larger is skipped to guard against zip bombs.
const maxSOSize = 256 << 20 // 256 MiB

func readZipEntry(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxSOSize {
		return nil, fmt.Errorf("entry too large: %d bytes", f.UncompressedSize64)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// LimitReader guards against entries that lie about their uncompressed size.
	data, err := io.ReadAll(io.LimitReader(rc, maxSOSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSOSize {
		return nil, fmt.Errorf("entry exceeds %d bytes after decompression", maxSOSize)
	}
	return data, nil
}
