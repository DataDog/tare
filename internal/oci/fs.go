package oci

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/DataDog/tare/internal/rootfs"
)

// Compile-time interface check.
var _ rootfs.FS = (*FS)(nil)

// FS provides filesystem access to an extracted OCI rootfs.
//
// File contents come from the inner rootfs.FS.
// Stat and Lstat return metadata from the original tar headers (correct
// uid/gid/mode) rather than the on-disk values, which may differ when
// extracting without root privileges.
type FS struct {
	inner    rootfs.FS
	metadata map[string]*FileMeta // container-absolute path → metadata
}

// NewFS creates an FS from an extraction result.
// cwd is the container working directory (from image config WorkingDir)
// used to resolve relative paths.
func NewFS(result *ExtractResult, cwd string) *FS {
	return &FS{
		inner:    rootfs.New(result.Root, cwd),
		metadata: result.Metadata,
	}
}

func (f *FS) Open(name string) (*os.File, error)  { return f.inner.Open(name) }
func (f *FS) ReadFile(name string) ([]byte, error) { return f.inner.ReadFile(name) }

// FS returns an fs.FS that routes Stat through the metadata overlay.
func (f *FS) FS() fs.FS {
	return &metaFS{inner: f.inner.FS(), metadata: f.metadata}
}

// Stat returns file info, following symlinks.
// If the resolved target has metadata, it is returned with correct uid/gid/mode.
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	realInfo, err := f.inner.Stat(name)
	if err != nil {
		return nil, err
	}

	if meta, ok := f.metadata[name]; ok && meta.Mode.Type()&fs.ModeSymlink == 0 {
		return &fileInfo{name: realInfo.Name(), meta: meta}, nil
	}

	// Path is a symlink in metadata — try to resolve the target.
	if meta, ok := f.metadata[name]; ok && meta.LinkTarget != "" {
		target := meta.LinkTarget
		if !strings.HasPrefix(target, "/") {
			target = path.Join(path.Dir(name), target)
		}
		target = path.Clean(target)
		if targetMeta, ok := f.metadata[target]; ok && targetMeta.Mode.Type()&fs.ModeSymlink == 0 {
			return &fileInfo{name: realInfo.Name(), meta: targetMeta}, nil
		}
	}

	return realInfo, nil
}

// Lstat returns file info from the metadata map without following symlinks.
func (f *FS) Lstat(name string) (fs.FileInfo, error) {
	if meta, ok := f.metadata[name]; ok {
		return &fileInfo{name: path.Base(name), meta: meta}, nil
	}
	return f.inner.Lstat(name)
}

// metaFS wraps an fs.FS to overlay tar metadata on Stat.
type metaFS struct {
	inner    fs.FS
	metadata map[string]*FileMeta
}

func (m *metaFS) Open(name string) (fs.File, error) { return m.inner.Open(name) }

func (m *metaFS) Stat(name string) (fs.FileInfo, error) {
	if meta, ok := m.metadata[name]; ok && meta.Mode.Type()&fs.ModeSymlink == 0 {
		return &fileInfo{name: path.Base(name), meta: meta}, nil
	}
	return m.inner.(fs.StatFS).Stat(name)
}

func (m *metaFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return m.inner.(fs.ReadDirFS).ReadDir(name)
}

// fileInfo implements fs.FileInfo using metadata from tar headers.
type fileInfo struct {
	name string
	meta *FileMeta
}

func (fi *fileInfo) Name() string       { return fi.name }
func (fi *fileInfo) Size() int64        { return fi.meta.Size }
func (fi *fileInfo) Mode() fs.FileMode  { return fi.meta.Mode }
func (fi *fileInfo) ModTime() time.Time { return time.Time{} }
func (fi *fileInfo) IsDir() bool        { return fi.meta.Mode.IsDir() }

// Sys returns a *syscall.Stat_t with Uid and Gid populated from the
// tar metadata, so existing uid/gid checks work without modification.
func (fi *fileInfo) Sys() any {
	return &syscall.Stat_t{
		Uid: uint32(fi.meta.UID),
		Gid: uint32(fi.meta.GID),
	}
}
