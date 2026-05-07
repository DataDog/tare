// Package rootfs provides a filesystem abstraction that anchors all
// paths to an os.Root.
//
// Absolute paths (e.g., "/app/myservice") are resolved relative to the root.
// Relative paths (e.g., "lib/") are joined with a configured working
// directory (typically the image's WorkingDir) before resolution.
//
// This includes the fs.FS returned by FS(), so fs.WalkDir works with
// absolute paths too.
package rootfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FS provides filesystem access scoped to a root directory.
type FS interface {
	Open(name string) (*os.File, error)
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	ReadFile(name string) ([]byte, error)

	// FS returns an fs.FS for use with fs.WalkDir.
	// Unlike a raw os.Root.FS(), this accepts absolute paths.
	FS() fs.FS
}

// Root wraps an *os.Root to accept absolute and relative container paths.
// Relative paths are resolved against the working directory (cwd).
type Root struct {
	root *os.Root
	cwd  string
}

var _ FS = (*Root)(nil)

// New wraps an *os.Root as a rootfs.FS.
// cwd is the container working directory used to resolve relative paths
// (typically the image config's WorkingDir). Use "/" if unknown.
func New(root *os.Root, cwd string) *Root {
	if cwd == "" {
		cwd = "/"
	}
	return &Root{root: root, cwd: cwd}
}

func (r *Root) Open(name string) (*os.File, error)    { return r.root.Open(fix(name, r.cwd)) }
func (r *Root) Stat(name string) (fs.FileInfo, error)  { return r.root.Stat(fix(name, r.cwd)) }
func (r *Root) Lstat(name string) (fs.FileInfo, error) { return r.root.Lstat(fix(name, r.cwd)) }
func (r *Root) ReadFile(name string) ([]byte, error)   { return r.root.ReadFile(fix(name, r.cwd)) }

// FS returns an fs.FS that accepts absolute paths, suitable for fs.WalkDir.
func (r *Root) FS() fs.FS {
	return &absFS{inner: r.root.FS(), cwd: r.cwd}
}

// absFS wraps an fs.FS to accept absolute paths and resolve relative
// paths against a working directory.
type absFS struct {
	inner fs.FS
	cwd   string
}

var (
	_ fs.FS        = (*absFS)(nil)
	_ fs.StatFS    = (*absFS)(nil)
	_ fs.ReadDirFS = (*absFS)(nil)
)

func (a *absFS) Open(name string) (fs.File, error)            { return a.inner.Open(fix(name, a.cwd)) }
func (a *absFS) Stat(name string) (fs.FileInfo, error)         { return a.inner.(fs.StatFS).Stat(fix(name, a.cwd)) }
func (a *absFS) ReadDir(name string) ([]fs.DirEntry, error)    { return a.inner.(fs.ReadDirFS).ReadDir(fix(name, a.cwd)) }

// fix resolves a path for os.Root:
//   - Relative paths are joined with cwd first.
//   - The leading "/" is stripped.
//   - "/" and "" become ".".
func fix(name, cwd string) string {
	if !filepath.IsAbs(name) {
		name = filepath.Join(cwd, name)
	}
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "."
	}
	return name
}
