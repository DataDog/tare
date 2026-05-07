package container

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/DataDog/tare/internal/oci"
)

// HarnessPrefix is the path inside the container where the tare harness is installed.
const HarnessPrefix = "/tmp/.tare"

// Runtime wraps os/exec calls to a container runtime binary (docker, podman, nerdctl).
type Runtime struct {
	// Bin is the container runtime binary name or path. Defaults to "docker".
	Bin string
}

// ExecOpts configures a container exec.
type ExecOpts struct {
	ID     string
	Env    []string  // KEY=VALUE environment variables
	Stdout io.Writer // defaults to os.Stdout if nil
	Stderr io.Writer // defaults to os.Stderr if nil
}

// createOpts configures a container create.
type createOpts struct {
	Image    string
	Platform string
	Pull     string // pull policy: "never" (default), "missing", "always"

	// Runtime options. Empty values are ignored.
	User    string
	CapDrop []string
	CapAdd  []string
	Binds   []string
	Env     []string
	EnvFile string
}

// create creates a stopped container and returns its ID.
func (r *Runtime) create(opts createOpts) (string, error) {
	args := []string{"create"}

	if opts.Platform != "" {
		args = append(args, "--platform", opts.Platform)
	}
	pull := opts.Pull
	if pull == "" {
		pull = "never"
	}
	args = append(args, "--pull", pull)

	if opts.User != "" {
		args = append(args, "--user", opts.User)
	}
	for _, cap := range opts.CapDrop {
		args = append(args, "--cap-drop", cap)
	}
	for _, cap := range opts.CapAdd {
		args = append(args, "--cap-add", cap)
	}
	for _, bind := range opts.Binds {
		args = append(args, "-v", bind)
	}
	for _, env := range opts.Env {
		args = append(args, "--env", env)
	}
	if opts.EnvFile != "" {
		args = append(args, "--env-file", opts.EnvFile)
	}

	// Override the entrypoint with tare-tool idle, which blocks on SIGTERM.
	// The harness must be docker cp'd into the container before starting.
	args = append(args, "--entrypoint", HarnessPrefix+"/bin/tare-tool")
	args = append(args, opts.Image)
	args = append(args, "idle")

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(r.bin(), args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s create: %w\n%s", r.bin(), err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (r *Runtime) start(id string) error {
	var stderr bytes.Buffer
	cmd := exec.Command(r.bin(), "start", id)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s start: %w\n%s", r.bin(), err, stderr.String())
	}
	return nil
}

// exec runs a command inside a container with the harness on PATH.
// Returns the exit code.
func (r *Runtime) exec(opts ExecOpts, args ...string) (int, error) {
	opts.Env = append(opts.Env, "PATH="+HarnessPrefix+"/bin:/usr/local/bin:/usr/bin:/bin")
	return r.rawExec(opts, args)
}

// rawExec runs a raw command in the container.
func (r *Runtime) rawExec(opts ExecOpts, cmd []string) (int, error) {
	dockerArgs := []string{"exec"}
	for _, e := range opts.Env {
		dockerArgs = append(dockerArgs, "-e", e)
	}
	dockerArgs = append(dockerArgs, opts.ID)
	dockerArgs = append(dockerArgs, cmd...)

	c := exec.Command(r.bin(), dockerArgs...)
	c.Stdout = opts.Stdout
	c.Stderr = opts.Stderr
	if c.Stdout == nil {
		c.Stdout = os.Stdout
	}
	if c.Stderr == nil {
		c.Stderr = os.Stderr
	}

	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, fmt.Errorf("%s exec: %w", r.bin(), err)
	}

	return 0, nil
}

func (r *Runtime) remove(id string) error {
	var stderr bytes.Buffer
	cmd := exec.Command(r.bin(), "rm", "-f", id)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s rm: %w\n%s", r.bin(), err, stderr.String())
	}
	return nil
}

// copyTar pipes a tar stream into the container, extracting at /.
// All copy operations ultimately go through this method.
func (r *Runtime) copyTar(id string, tarStream io.Reader) error {
	cmd := exec.Command(r.bin(), "cp", "-", id+":/")
	cmd.Stdin = tarStream
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s cp: %w\n%s", r.bin(), err, stderr.String())
	}
	return nil
}

// copyFile copies a host file into the container at destPath.
// The file mode is preserved from the source.
func (r *Runtime) copyFile(id string, srcPath string, destPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", srcPath, err)
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", srcPath, err)
	}
	return r.writeFile(id, destPath, data, int64(info.Mode()))
}

// writeFile writes arbitrary bytes into the container at destPath via an
// in-memory tar stream.
func (r *Runtime) writeFile(id string, destPath string, data []byte, mode int64) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: strings.TrimPrefix(destPath, "/"),
		Size: int64(len(data)),
		Mode: mode,
	}); err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("writing tar data: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar: %w", err)
	}
	return r.copyTar(id, &buf)
}

// imageInspect returns the raw JSON from inspecting an image.
func (r *Runtime) imageInspect(image string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(r.bin(), "image", "inspect", image)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s image inspect: %w\n%s", r.bin(), err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// injectMetadata inspects the image, parses its config, and copies the raw
// JSON into the harness directory. Called before container start.
func (r *Runtime) injectMetadata(id, image string) (*oci.ImageConfig, error) {
	// Resolve the exact image SHA from the container to ensure we inspect
	// the correct platform variant for multi-arch images.
	var shaOut bytes.Buffer
	shaCmd := exec.Command(r.bin(), "inspect", "--format", "{{.Image}}", id)
	shaCmd.Stdout = &shaOut
	if err := shaCmd.Run(); err == nil {
		if sha := strings.TrimSpace(shaOut.String()); sha != "" {
			image = sha
		}
	}

	metaJSON, err := r.imageInspect(image)
	if err != nil {
		return nil, fmt.Errorf("inspecting image metadata: %w", err)
	}

	var inspect []struct {
		Config       *oci.ImageConfig `json:"Config"`
		Architecture string           `json:"Architecture"`
	}
	if err := json.Unmarshal(metaJSON, &inspect); err != nil {
		return nil, fmt.Errorf("parsing image metadata: %w", err)
	}

	var cfg *oci.ImageConfig
	if len(inspect) > 0 && inspect[0].Config != nil {
		cfg = inspect[0].Config
		cfg.Architecture = inspect[0].Architecture
	} else {
		cfg = &oci.ImageConfig{}
	}

	if err := r.writeFile(id, HarnessPrefix+"/meta.json", metaJSON, 0o644); err != nil {
		return nil, fmt.Errorf("copying metadata: %w", err)
	}

	return cfg, nil
}

func (r *Runtime) bin() string {
	if r.Bin != "" {
		return r.Bin
	}
	return "docker"
}
