// Package rootfs provides a filesystem abstraction that anchors all
// paths to an os.Root.
//
// Absolute paths (e.g., "/app/myservice") are resolved relative to the root.
// Relative paths (e.g., "lib/") are joined with a configured working
// directory (typically the image's WorkingDir) before resolution.
//
// Symlinks within the rootfs are resolved virtually: a symlink whose
// target is an absolute path (e.g., "/lib/x86_64-linux-gnu/libc.so.6")
// is interpreted as relative to the virtual root, not the host root.
// This is necessary because os.Root rejects absolute symlink targets
// as escaping the rooted tree, even when the target would resolve back
// inside it.
//
// This includes the fs.FS returned by FS(), so fs.WalkDir works with
// absolute paths too.
package rootfs

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
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

func (r *Root) Open(name string) (*os.File, error) {
	resolved, err := r.resolve(name)
	if err != nil {
		return nil, err
	}
	return r.root.Open(resolved)
}

func (r *Root) Stat(name string) (fs.FileInfo, error) {
	resolved, err := r.resolve(name)
	if err != nil {
		return nil, err
	}
	return r.root.Stat(resolved)
}

func (r *Root) Lstat(name string) (fs.FileInfo, error) {
	return r.root.Lstat(fix(name, r.cwd))
}

func (r *Root) ReadFile(name string) ([]byte, error) {
	resolved, err := r.resolve(name)
	if err != nil {
		return nil, err
	}
	return r.root.ReadFile(resolved)
}

// FS returns an fs.FS that accepts absolute paths, suitable for fs.WalkDir.
func (r *Root) FS() fs.FS {
	return &absFS{inner: r.root.FS(), cwd: r.cwd}
}

// resolve walks name component-by-component, following symlinks rooted
// at the virtual root. Absolute symlink targets are reinterpreted as
// virtual-root-relative. Returns a path with no remaining symlinks. If
// a component does not exist, the path is returned unresolved so the
// caller's underlying op produces a proper not-exist error.
func (r *Root) resolve(name string) (string, error) {
	return r.resolveDepth(fix(name, r.cwd), 0)
}

const maxSymlinkDepth = 40

var errPathEscapes = errors.New("path escapes from parent")

func (r *Root) resolveDepth(name string, depth int) (string, error) {
	if depth > maxSymlinkDepth {
		return "", &fs.PathError{Op: "resolve", Path: name, Err: syscall.ELOOP}
	}
	if name == "" || name == "." {
		return ".", nil
	}
	parts := strings.Split(name, "/")
	var built string
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if built == "" {
				return "", &fs.PathError{Op: "resolve", Path: name, Err: errPathEscapes}
			}
			built = path.Dir(built)
			if built == "." {
				built = ""
			}
			continue
		}
		candidate := part
		if built != "" {
			candidate = built + "/" + part
		}
		info, err := r.root.Lstat(candidate)
		if err != nil {
			// Component doesn't exist: return the path unresolved so
			// the caller's operation produces ENOENT.
			if i+1 < len(parts) {
				return candidate + "/" + strings.Join(parts[i+1:], "/"), nil
			}
			return candidate, nil
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			built = candidate
			continue
		}
		target, err := r.root.Readlink(candidate)
		if err != nil {
			return "", err
		}
		var next string
		if path.IsAbs(target) {
			next = strings.TrimPrefix(path.Clean(target), "/")
		} else {
			next = path.Join(built, target)
		}
		if i+1 < len(parts) {
			next = path.Join(next, strings.Join(parts[i+1:], "/"))
		}
		return r.resolveDepth(next, depth+1)
	}
	if built == "" {
		return ".", nil
	}
	return built, nil
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

func (a *absFS) Open(name string) (fs.File, error)         { return a.inner.Open(fix(name, a.cwd)) }
func (a *absFS) Stat(name string) (fs.FileInfo, error)     { return a.inner.(fs.StatFS).Stat(fix(name, a.cwd)) }
func (a *absFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return a.inner.(fs.ReadDirFS).ReadDir(fix(name, a.cwd))
}

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
