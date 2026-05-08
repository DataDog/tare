package container

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/DataDog/tare/internal/oci"
)

// SessionOpts configures a new container session.
type SessionOpts struct {
	Image    string
	Platform string    // defaults to linux/GOARCH
	Pull     string    // never (default), missing, always
	Harness  io.Reader // harness tar to copy into the container
	Verbose  bool      // print lifecycle messages to stderr

	// Runtime options applied at container create time. Empty values
	// are ignored.
	User    string   // --user
	CapDrop []string // --cap-drop entries
	CapAdd  []string // --cap-add entries
	Binds   []string // -v entries (host:container[:opts])
	Env     []string // --env entries (KEY=VALUE)
	EnvFile string   // --env-file path
}

// Session represents a running container with the harness installed.
type Session struct {
	rt    *Runtime
	id    string
	image string

	// Config is the parsed image configuration, available after session creation.
	Config *oci.ImageConfig
}

// NewSession creates a container, prepares the harness and metadata, and starts it.
func (r *Runtime) NewSession(opts SessionOpts) (*Session, error) {
	platform := opts.Platform
	if platform == "" {
		platform = "linux/" + runtime.GOARCH
	}
	pull := opts.Pull
	if pull == "" {
		pull = "never"
	}

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "creating container from %s...\n", opts.Image)
	}
	// Prepend the harness PATH so tare-tool and toybox applets resolve
	// inside the container. User-supplied entries from runtime.env follow,
	// and docker --env semantics are last-write-wins per key — so a
	// user-provided PATH overrides this default cleanly.
	env := []string{"PATH=" + HarnessBinDir + ":" + DefaultContainerPATH}
	env = append(env, opts.Env...)

	id, err := r.create(createOpts{
		Image:    opts.Image,
		Platform: platform,
		Pull:     pull,
		User:     opts.User,
		CapDrop:  opts.CapDrop,
		CapAdd:   opts.CapAdd,
		Binds:    opts.Binds,
		Env:      env,
		EnvFile:  opts.EnvFile,
	})
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}

	// If anything fails after create, clean up the container.
	success := false
	defer func() {
		if !success {
			_ = r.remove(id)
		}
	}()

	// Copy harness into the container.
	if opts.Harness != nil {
		if opts.Verbose {
			fmt.Fprintf(os.Stderr, "copying harness into container...\n")
		}
		if err := r.copyTar(id, opts.Harness); err != nil {
			return nil, fmt.Errorf("copying harness: %w", err)
		}
	}

	// Inspect image and inject metadata before starting.
	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "inspecting image metadata...\n")
	}
	imgConfig, err := r.injectMetadata(id, opts.Image)
	if err != nil {
		return nil, err
	}

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "starting container...\n")
	}
	if err := r.start(id); err != nil {
		return nil, fmt.Errorf("starting container: %w", err)
	}

	success = true
	return &Session{rt: r, id: id, image: opts.Image, Config: imgConfig}, nil
}

// ID returns the container ID.
func (s *Session) ID() string {
	return s.id
}

// Image returns the image the session was created from.
func (s *Session) Image() string {
	return s.image
}

// Exec runs a command inside the container via the harness shell.
func (s *Session) Exec(opts ExecOpts, args ...string) (int, error) {
	opts.ID = s.id
	return s.rt.exec(opts, args...)
}

// CopyFile copies a host file into the container, preserving its mode.
func (s *Session) CopyFile(srcPath, destPath string) error {
	return s.rt.copyFile(s.id, srcPath, destPath)
}

// WriteFile writes data into the container at destPath via an in-memory tar stream.
func (s *Session) WriteFile(destPath string, data []byte, mode int64) error {
	return s.rt.writeFile(s.id, destPath, data, mode)
}

// Close removes the container.
func (s *Session) Close() error {
	return s.rt.remove(s.id)
}
