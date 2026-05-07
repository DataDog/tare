package oci

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// FileMeta holds the original tar header metadata for a filesystem entry.
type FileMeta struct {
	Mode       fs.FileMode `json:"mode"`
	UID        int         `json:"uid"`
	GID        int         `json:"gid"`
	Size       int64       `json:"size"`
	LinkTarget string      `json:"linkTarget,omitempty"`
}

// ExtractResult holds the outcome of a rootfs extraction.
type ExtractResult struct {
	Root     *os.Root             // os.Root opened on the extraction directory
	Metadata map[string]*FileMeta // container-absolute path → metadata
}

// metadataFile is the name of the metadata sidecar written to the extraction directory.
const metadataFile = ".tar-fs.json"

// Extract extracts the image rootfs for the given platform into destDir.
// Layers are applied in order with whiteout processing. All filesystem
// writes go through an os.Root opened on destDir.
//
// The metadata map records original tar header values (uid/gid/mode) that
// may not be preserved on disk (uid/gid requires CAP_CHOWN, modes may be
// affected by umask). It is written to destDir/.tar-fs.json.
func (l *Layout) Extract(destDir, goos, goarch string) (*ExtractResult, error) {
	m, err := l.findManifest(goos, goarch)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(destDir)
	if err != nil {
		return nil, fmt.Errorf("opening extraction root: %w", err)
	}

	metadata := make(map[string]*FileMeta)

	for _, layer := range m.Layers {
		if err := extractLayer(l, root, layer, metadata); err != nil {
			root.Close()
			return nil, fmt.Errorf("extracting layer %s: %w", layer.Digest, err)
		}
	}

	// Write metadata sidecar.
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("marshaling metadata: %w", err)
	}
	if err := root.WriteFile(metadataFile, metaJSON, 0o644); err != nil {
		root.Close()
		return nil, fmt.Errorf("writing %s: %w", metadataFile, err)
	}

	return &ExtractResult{Root: root, Metadata: metadata}, nil
}

func extractLayer(l *Layout, root *os.Root, desc descriptor, metadata map[string]*FileMeta) error {
	// Pass 1: collect opaque whiteout directories.
	opaqueDirs, err := collectOpaqueWhiteouts(l, desc)
	if err != nil {
		return fmt.Errorf("scanning whiteouts: %w", err)
	}

	// Apply opaque whiteouts: remove existing children from earlier layers.
	if err := applyOpaqueWhiteouts(root, opaqueDirs, metadata); err != nil {
		return err
	}

	// Pass 2: extract entries.
	rc, err := openLayerTar(l, desc)
	if err != nil {
		return err
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}

		name := cleanTarPath(hdr.Name)
		if name == "" || name == "." {
			continue
		}

		base := path.Base(name)

		// Skip opaque whiteout markers (handled in pass 1).
		if base == ".wh..wh..opq" {
			continue
		}

		// Individual whiteout: delete the named file.
		if strings.HasPrefix(base, ".wh.") {
			target := path.Join(path.Dir(name), strings.TrimPrefix(base, ".wh."))
			root.RemoveAll(target)
			removeMetadataPrefix(metadata, "/"+target)
			continue
		}

		containerPath := "/" + name

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", name, err)
			}
			root.Chmod(name, hdr.FileInfo().Mode().Perm())
			metadata[containerPath] = metaFromHeader(hdr)

		case tar.TypeReg, tar.TypeRegA:
			if err := extractRegularFile(root, name, hdr, tr); err != nil {
				return err
			}
			metadata[containerPath] = metaFromHeader(hdr)

		case tar.TypeSymlink:
			// Remove any existing entry so symlink creation succeeds.
			root.Remove(name)
			if err := root.Symlink(hdr.Linkname, name); err != nil {
				return fmt.Errorf("symlink %s -> %s: %w", name, hdr.Linkname, err)
			}
			meta := metaFromHeader(hdr)
			meta.LinkTarget = hdr.Linkname
			metadata[containerPath] = meta

		case tar.TypeLink:
			linkTarget := cleanTarPath(hdr.Linkname)
			// Remove any existing entry so link creation succeeds.
			root.Remove(name)
			if err := root.Link(linkTarget, name); err != nil {
				return fmt.Errorf("link %s -> %s: %w", name, linkTarget, err)
			}
			// Copy metadata from the link target.
			if m, ok := metadata["/"+linkTarget]; ok {
				cp := *m
				metadata[containerPath] = &cp
			} else {
				metadata[containerPath] = metaFromHeader(hdr)
			}

		case tar.TypeBlock, tar.TypeChar:
			// Can't create device nodes without root. Record metadata only.
			metadata[containerPath] = metaFromHeader(hdr)

		default:
			// Skip fifos, etc.
		}
	}

	return nil
}

func extractRegularFile(root *os.Root, name string, hdr *tar.Header, tr *tar.Reader) error {
	// Ensure parent directory exists.
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", name, err)
		}
	}

	f, err := root.Create(name)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}

	if _, err := io.Copy(f, tr); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}

	if err := root.Chmod(name, hdr.FileInfo().Mode().Perm()); err != nil {
		return fmt.Errorf("chmod %s: %w", name, err)
	}

	return nil
}

// collectOpaqueWhiteouts scans a layer's tar for .wh..wh..opq entries
// and returns the set of directories that should have their prior contents removed.
func collectOpaqueWhiteouts(l *Layout, desc descriptor) (map[string]bool, error) {
	rc, err := openLayerTar(l, desc)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	dirs := make(map[string]bool)
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := cleanTarPath(hdr.Name)
		if path.Base(name) == ".wh..wh..opq" {
			dirs[path.Dir(name)] = true
		}
	}
	return dirs, nil
}

// applyOpaqueWhiteouts removes existing children of opaque directories.
func applyOpaqueWhiteouts(root *os.Root, dirs map[string]bool, metadata map[string]*FileMeta) error {
	fsys, ok := root.FS().(fs.ReadDirFS)
	if !ok {
		return fmt.Errorf("root FS does not support ReadDir")
	}

	for dir := range dirs {
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			// Directory may not exist yet (first layer creating it).
			continue
		}
		for _, entry := range entries {
			child := path.Join(dir, entry.Name())
			if err := root.RemoveAll(child); err != nil {
				return fmt.Errorf("removing %s for opaque whiteout: %w", child, err)
			}
			removeMetadataPrefix(metadata, "/"+child)
		}
	}
	return nil
}

// openLayerTar opens a layer blob and wraps it with the appropriate decompressor.
func openLayerTar(l *Layout, desc descriptor) (io.ReadCloser, error) {
	f, err := l.openBlob(desc.Digest)
	if err != nil {
		return nil, fmt.Errorf("opening layer blob: %w", err)
	}

	switch {
	case isGzipLayer(desc.MediaType):
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("decompressing layer: %w", err)
		}
		return &gzipReadCloser{gz: gz, file: f}, nil

	case isTarLayer(desc.MediaType):
		return f, nil

	case isZstdLayer(desc.MediaType):
		f.Close()
		return nil, fmt.Errorf("zstd layers not yet supported (layer %s)", desc.Digest)

	default:
		// Unknown media type — try gzip, fall back to raw tar.
		gz, err := gzip.NewReader(f)
		if err != nil {
			// Not gzip — seek back and treat as raw tar.
			if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
				f.Close()
				return nil, fmt.Errorf("seeking layer blob: %w", seekErr)
			}
			return f, nil
		}
		return &gzipReadCloser{gz: gz, file: f}, nil
	}
}

type gzipReadCloser struct {
	gz   *gzip.Reader
	file *os.File
}

func (g *gzipReadCloser) Read(p []byte) (int, error) { return g.gz.Read(p) }

func (g *gzipReadCloser) Close() error {
	g.gz.Close()
	return g.file.Close()
}

func isGzipLayer(mt string) bool {
	return strings.HasSuffix(mt, ".tar+gzip") || strings.HasSuffix(mt, ".tar.gzip")
}

func isTarLayer(mt string) bool {
	return strings.HasSuffix(mt, ".tar") && !strings.Contains(mt, "+") && !strings.Contains(mt, ".tar.")
}

func isZstdLayer(mt string) bool {
	return strings.HasSuffix(mt, ".tar+zstd") || strings.HasSuffix(mt, ".tar.zstd")
}

// cleanTarPath sanitizes a tar entry name.
// Rejects entries that attempt path traversal.
func cleanTarPath(name string) string {
	name = path.Clean(strings.TrimPrefix(name, "/"))
	name = strings.TrimPrefix(name, "./")
	if name == ".." || strings.HasPrefix(name, "../") {
		return ""
	}
	return name
}

func metaFromHeader(hdr *tar.Header) *FileMeta {
	return &FileMeta{
		Mode: hdr.FileInfo().Mode(),
		UID:  hdr.Uid,
		GID:  hdr.Gid,
		Size: hdr.Size,
	}
}

// removeMetadataPrefix removes a path and all paths under it from the metadata map.
func removeMetadataPrefix(metadata map[string]*FileMeta, prefix string) {
	delete(metadata, prefix)
	dirPrefix := prefix + "/"
	for k := range metadata {
		if strings.HasPrefix(k, dirPrefix) {
			delete(metadata, k)
		}
	}
}
