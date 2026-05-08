package harness

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/tare/internal/container"
)

// harnessFS is the filesystem interface needed to build a harness tar.
// os.OpenRoot().FS() satisfies this.
type harnessFS interface {
	fs.ReadFileFS
	fs.ReadLinkFS
	fs.ReadDirFS
}

// resolveDir resolves a harness path for the given platform.
// If dir already contains bin/bash, it's used as-is.
// Otherwise, it looks for a linux-<arch> subdirectory.
// Platform should be in "linux/amd64" format.
func resolveDir(dir string, platform string) (string, error) {
	if isHarnessDir(dir) {
		return filepath.Abs(dir)
	}

	arch := archFromPlatform(platform)
	candidate := filepath.Join(dir, "linux-"+arch)
	if isHarnessDir(candidate) {
		return filepath.Abs(candidate)
	}

	return "", fmt.Errorf("harness not found at %s or %s (looked for bin/bash)", dir, candidate)
}

// Select returns a harness tar reader for the given platform.
// If dir is non-empty, the harness is built from the local directory.
// Otherwise, the embedded harness is used.
func Select(dir string, platform string) (io.Reader, error) {
	if dir != "" {
		return Dir(dir, platform)
	}
	return Embedded(platform)
}

// Dir builds a tar from a local harness directory, ready to pipe to
// docker cp - container:/. Returns an io.Reader of the tar stream.
func Dir(dir string, platform string) (io.Reader, error) {
	resolved, err := resolveDir(dir, platform)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, fmt.Errorf("opening harness dir: %w", err)
	}
	defer root.Close()

	fsys, ok := root.FS().(harnessFS)
	if !ok {
		return nil, fmt.Errorf("harness dir filesystem does not support symlinks")
	}
	return readerFromFS(fsys)
}

// Embedded returns a reader for the embedded harness tar,
// decompressed and ready to pipe to docker cp - container:/.
func Embedded(platform string) (io.Reader, error) {
	data, err := embedded(platform)
	if err != nil {
		return nil, err
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decompressing embedded harness: %w", err)
	}
	return gz, nil
}

func readerFromFS(fsys harnessFS) (io.Reader, error) {
	prefix := strings.TrimPrefix(container.HarnessPrefix, "/")
	data, err := tarFromFS(fsys, prefix)
	if err != nil {
		return nil, fmt.Errorf("building harness tar: %w", err)
	}
	return bytes.NewReader(data), nil
}

// tarFromFS creates a tar archive from an fs.FS, with each entry
// prefixed by the given path. Symlinks are preserved.
func tarFromFS(fsys harnessFS, prefix string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		info, err := fsys.Lstat(path)
		if err != nil {
			return err
		}

		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = fsys.ReadLink(path)
			if err != nil {
				return err
			}
		}

		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}

		if path == "." {
			header.Name = prefix + "/"
		} else {
			header.Name = prefix + "/" + path
		}

		// Strip macOS xattrs that cause issues with docker cp.
		header.Xattrs = nil

		// Force ownership to root:0 so harness files don't trip
		// `find -nouser`/`find -nogroup` checks against images whose
		// /etc/passwd doesn't contain the build host's UID.
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "root", "root"

		// Ensure world-readable for nonroot containers.
		if header.Typeflag == tar.TypeDir {
			header.Mode |= 0o555
		} else if header.Typeflag == tar.TypeReg {
			header.Mode |= 0o444
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			data, err := fsys.ReadFile(path)
			if err != nil {
				return err
			}
			if _, err := tw.Write(data); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func isHarnessDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "bin", "bash"))
	return err == nil && !info.IsDir()
}

func archFromPlatform(platform string) string {
	if i := strings.Index(platform, "/"); i >= 0 {
		return platform[i+1:]
	}
	return platform
}
