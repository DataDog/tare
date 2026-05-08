package tare_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/tare/internal/config"
	"github.com/DataDog/tare/internal/container"
	"github.com/DataDog/tare/internal/harness"
	"github.com/DataDog/tare/internal/testplan"
)

type containerHarness struct {
	sess *container.Session
	seq  int
}

func newContainerHarness(t *testing.T, image string) *containerHarness {
	return newContainerHarnessWithEnv(t, image, nil)
}

func newContainerHarnessWithEnv(t *testing.T, image string, env []string) *containerHarness {
	t.Helper()

	harnessDir := findHarnessDir(t)
	platform := "linux/" + runtime.GOARCH

	harnessReader, err := harness.Dir(harnessDir, platform)
	if err != nil {
		t.Fatalf("building harness tar: %v", err)
	}

	rt := &container.Runtime{}
	sess, err := rt.NewSession(container.SessionOpts{
		Image:    image,
		Platform: platform,
		Pull:     "missing",
		Harness:  harnessReader,
		Env:      env,
	})
	if err != nil {
		t.Fatalf("creating session: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	return &containerHarness{sess: sess}
}

func (h *containerHarness) run(t *testing.T, cfg *config.Config) tapResult {
	t.Helper()

	h.seq++
	planFile := fmt.Sprintf("test_%d.json", h.seq)

	plan := testplan.FromConfig(cfg)
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("generating plan: %v", err)
	}

	containerPath := "/tmp/" + planFile
	if err := h.sess.WriteFile(containerPath, planJSON, 0o644); err != nil {
		t.Fatalf("writing test plan: %v", err)
	}

	var output bytes.Buffer
	exitCode, err := h.sess.Exec(container.ExecOpts{
		Stdout: &output,
		Stderr: &output,
	}, container.HarnessBin("tare-tool"), "run-tests", containerPath)
	if err != nil {
		t.Fatalf("running tests: %v", err)
	}

	return tapResult{
		ExitCode: exitCode,
		Output:   output.String(),
	}
}

func TestContainerGeneral(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	h := newContainerHarness(t, "gcr.io/distroless/static:nonroot")

	cfg := &config.Config{
		SchemaVersion: 1,
		Metadata: []config.MetadataAssertion{
			{
				User:    "65532",
				Workdir: "/home/nonroot",
				Env: []config.KV{
					{Key: "SSL_CERT_FILE", Value: "/etc/ssl/certs/ca-certificates.crt"},
				},
			},
		},
		Files: []config.FileAssertion{
			{Path: "/etc/ssl/certs/ca-certificates.crt"},
			{Path: "/bin/sh", Present: boolPtr(false)},
			{Path: "/etc/passwd"},
			{Path: "/etc/passwd", Contents: pat("nonroot")},
		},
		Commands: []config.CommandAssertion{
			{
				Name:   "echo works",
				Run:    config.Run{Argv: []string{"echo", "hello"}},
				Stdout: pat("hello"),
			},
		},
	}

	result := h.run(t, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertAllPass(t, result)
}

func TestContainerFileExistence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	h := newContainerHarness(t, "gcr.io/distroless/static:nonroot")

	t.Run("ownership_and_permissions", func(t *testing.T) {
		cfg := &config.Config{
			SchemaVersion: 1,
			Files: []config.FileAssertion{
				{Path: "/etc/passwd", UID: intPtr(0), GID: intPtr(0)},
				{Path: "/etc/passwd", Permissions: "-rw-r--r--"},
				{Path: "/does/not/exist", Present: boolPtr(false)},
			},
		}
		result := h.run(t, cfg)
		t.Logf("TAP output:\n%s", result.Output)
		assertAllPass(t, result)
	})

	t.Run("executable_by", func(t *testing.T) {
		cfg := &config.Config{
			SchemaVersion: 1,
			Files: []config.FileAssertion{
				{Path: "/tmp/.tare/bin/tare-tool", ExecutableBy: config.ClassList{"owner"}},
				{Path: "/tmp/.tare/bin/tare-tool", ExecutableBy: config.ClassList{"group"}},
				{Path: "/tmp/.tare/bin/tare-tool", ExecutableBy: config.ClassList{"other"}},
				{Path: "/tmp/.tare/bin/tare-tool", ExecutableBy: config.ClassList{"any"}},
			},
		}
		result := h.run(t, cfg)
		t.Logf("TAP output:\n%s", result.Output)
		assertAllPass(t, result)
	})
}

func TestContainerFileContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	h := newContainerHarness(t, "gcr.io/distroless/static:nonroot")

	cfg := &config.Config{
		SchemaVersion: 1,
		Files: []config.FileAssertion{
			{Path: "/etc/passwd", Contents: pat("root", "nonroot")},
			{Path: "/etc/passwd", Not: &config.FileNot{Contents: pat("supersecretpassword")}},
			{
				Path:     "/etc/passwd",
				Contents: pat("root"),
				Not:      &config.FileNot{Contents: pat("NOPE")},
			},
		},
	}

	result := h.run(t, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertAllPass(t, result)
}

func TestContainerCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	h := newContainerHarness(t, "gcr.io/distroless/static:nonroot")

	one := 1
	cfg := &config.Config{
		SchemaVersion: 1,
		Commands: []config.CommandAssertion{
			{
				Name: "non-zero exit code",
				Run:  config.Run{Argv: []string{"false"}},
				Exit: &one,
			},
			{
				Name: "excluded output",
				Run:  config.Run{Argv: []string{"echo", "good output"}},
				Not:  &config.CommandNot{Stdout: pat("bad")},
			},
			{
				Name:     "setup and teardown",
				Setup:    [][]string{{"touch", "/tmp/setup_marker"}},
				Run:      config.Run{Argv: []string{"stat", "/tmp/setup_marker"}},
				Teardown: [][]string{{"rm", "-f", "/tmp/setup_marker"}},
			},
		},
	}

	result := h.run(t, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertAllPass(t, result)
}

func TestContainerCommandHarnessOptOut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	h := newContainerHarness(t, "gcr.io/distroless/static:nonroot")

	no := false
	cfg := &config.Config{
		SchemaVersion: 1,
		Commands: []config.CommandAssertion{
			{
				Name: "default: harness on PATH",
				Run: config.Run{Argv: []string{
					container.HarnessBin("sh"), "-c", "echo $PATH",
				}},
				Stdout: pat(container.HarnessBinDir),
			},
			{
				Name: "harness false: harness stripped from PATH",
				Run: config.Run{Argv: []string{
					container.HarnessBin("sh"), "-c", "echo $PATH",
				}},
				Harness: &no,
				Not: &config.CommandNot{
					Stdout: pat(container.HarnessBinDir),
				},
			},
		},
	}

	result := h.run(t, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertAllPass(t, result)
}

func TestContainerRuntimeEnvPATHPrecedence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	t.Run("default harness PATH is on PATH", func(t *testing.T) {
		h := newContainerHarness(t, "gcr.io/distroless/static:nonroot")
		cfg := &config.Config{
			SchemaVersion: 1,
			Commands: []config.CommandAssertion{
				{
					Name: "harness directory present in PATH",
					Run: config.Run{Argv: []string{
						container.HarnessBin("sh"), "-c", "echo $PATH",
					}},
					Stdout: pat(container.HarnessBinDir),
				},
			},
		}
		result := h.run(t, cfg)
		t.Logf("TAP output:\n%s", result.Output)
		assertAllPass(t, result)
	})

	t.Run("runtime.env PATH overrides harness default", func(t *testing.T) {
		userPATH := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		h := newContainerHarnessWithEnv(t, "gcr.io/distroless/static:nonroot",
			[]string{"PATH=" + userPATH})
		cfg := &config.Config{
			SchemaVersion: 1,
			Commands: []config.CommandAssertion{
				{
					Name: "user PATH wins over harness PATH",
					Run: config.Run{Argv: []string{
						container.HarnessBin("sh"), "-c", "echo $PATH",
					}},
					// Exact match: assert the harness prefix is *not* present
					// and the value is exactly what runtime.env requested.
					Stdout: config.PatternList{
						Patterns: []config.Pattern{{Value: `^` + userPATH + `\s*$`, Match: true}},
					},
					Not: &config.CommandNot{
						Stdout: pat(container.HarnessPrefix),
					},
				},
			},
		}
		result := h.run(t, cfg)
		t.Logf("TAP output:\n%s", result.Output)
		assertAllPass(t, result)
	})
}

func TestContainerMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	h := newContainerHarness(t, "gcr.io/distroless/static:nonroot")

	t.Run("basic", func(t *testing.T) {
		cfg := &config.Config{
			SchemaVersion: 1,
			Metadata: []config.MetadataAssertion{
				{
					User:    "65532",
					Workdir: "/home/nonroot",
					Env: []config.KV{
						{Key: "PATH", Value: "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
						{Key: "SSL_CERT_FILE", Value: "/etc/ssl/certs/ca-certificates.crt"},
					},
				},
			},
		}
		result := h.run(t, cfg)
		t.Logf("TAP output:\n%s", result.Output)
		assertAllPass(t, result)
	})

	t.Run("regex_env", func(t *testing.T) {
		cfg := &config.Config{
			SchemaVersion: 1,
			Metadata: []config.MetadataAssertion{
				{
					Env: []config.KV{
						{Key: "PATH", Value: "/usr/local/.*", Regex: true},
					},
				},
			},
		}
		result := h.run(t, cfg)
		t.Logf("TAP output:\n%s", result.Output)
		assertAllPass(t, result)
	})
}

func TestContainerScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	h := newContainerHarness(t, "gcr.io/distroless/static:nonroot")

	cfg := &config.Config{
		SchemaVersion: 1,
		Scan: []config.ScanEntry{
			{Name: "harness binaries resolve", Path: "/tmp/.tare/bin"},
		},
	}

	result := h.run(t, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertAllPass(t, result)
}

func TestContainerScratch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	buildScratchImage(t)
	h := newContainerHarness(t, "tare-test-scratch")

	t.Run("file_existence", func(t *testing.T) {
		cfg := &config.Config{
			SchemaVersion: 1,
			Files: []config.FileAssertion{
				{Path: "/tmp/.tare/bin/tare-tool"},
				{Path: "/bin/sh", Present: boolPtr(false)},
				{Path: "/etc/passwd", Present: boolPtr(false)},
			},
		}
		result := h.run(t, cfg)
		t.Logf("TAP output:\n%s", result.Output)
		assertAllPass(t, result)
	})

	t.Run("command", func(t *testing.T) {
		cfg := &config.Config{
			SchemaVersion: 1,
			Commands: []config.CommandAssertion{
				{
					Name:   "echo works",
					Run:    config.Run{Argv: []string{"echo", "hello from scratch"}},
					Stdout: pat("hello from scratch"),
				},
			},
		}
		result := h.run(t, cfg)
		t.Logf("TAP output:\n%s", result.Output)
		assertAllPass(t, result)
	})
}

func TestContainerRootImage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container integration tests in short mode")
	}

	h := newContainerHarness(t, "gcr.io/distroless/static:latest")

	cfg := &config.Config{
		SchemaVersion: 1,
		Metadata: []config.MetadataAssertion{
			{User: "0"},
		},
	}

	result := h.run(t, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertAllPass(t, result)
}

func buildScratchImage(t *testing.T) {
	t.Helper()
	cmd := exec.Command("docker", "build", "--platform", "linux/"+runtime.GOARCH, "-t", "tare-test-scratch", "-")
	cmd.Stdin = strings.NewReader("FROM scratch\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("building scratch image: %v\n%s", err, stderr.String())
	}
}

func intPtr(n int) *int    { return &n }
func boolPtr(b bool) *bool { return &b }

// pat is a small helper to build a list of literal-string Patterns.
func pat(values ...string) config.PatternList {
	out := make([]config.Pattern, len(values))
	for i, v := range values {
		out[i] = config.Pattern{Value: v}
	}
	return config.PatternList{Patterns: out}
}
