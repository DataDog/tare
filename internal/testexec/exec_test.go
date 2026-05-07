package testexec

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/tare/internal/oci"
	"github.com/DataDog/tare/internal/rootfs"
	"github.com/DataDog/tare/internal/testplan"
)

var update = flag.Bool("update", false, "update golden files")

func setupTestRoot(t *testing.T) rootfs.FS {
	t.Helper()
	dir := t.TempDir()

	// /etc/passwd with known content and mode
	os.MkdirAll(filepath.Join(dir, "etc"), 0o755)
	os.WriteFile(filepath.Join(dir, "etc", "passwd"), []byte("root:x:0:0:root:/root:/bin/sh\napp:x:1000:1000::/home/app:/bin/sh\n"), 0o644)

	// /app/bin/myservice
	os.MkdirAll(filepath.Join(dir, "app", "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "bin", "myservice"), []byte("#!/bin/sh\necho hello"), 0o755)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return rootfs.New(root, "/app")
}

func TestGoldenFiles(t *testing.T) {
	goldenDir := filepath.Join("testdata", "golden")
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatalf("reading golden dir: %v", err)
	}

	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tap") {
			names = append(names, strings.TrimSuffix(e.Name(), ".tap"))
		}
	}
	if len(names) == 0 {
		t.Fatal("no golden .tap files found in testdata/golden/")
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			tapPath := filepath.Join(goldenDir, name+".tap")
			fn := goldenTests[name]
			if fn == nil {
				t.Fatalf("no test function registered for %q", name)
			}

			var buf bytes.Buffer
			fn(t, &buf)

			got := buf.String()

			if *update {
				if err := os.WriteFile(tapPath, []byte(got), 0o644); err != nil {
					t.Fatalf("updating golden file: %v", err)
				}
				return
			}

			want, err := os.ReadFile(tapPath)
			if err != nil {
				t.Fatalf("reading golden file: %v", err)
			}

			if got != string(want) {
				t.Errorf("TAP output mismatch for %s\n\ngot:\n%s\nwant:\n%s", name, got, string(want))
			}
		})
	}
}

// goldenTests maps golden file names to functions that produce TAP output.
var goldenTests = map[string]func(t *testing.T, buf *bytes.Buffer){
	"no_commands": testNoCommands,
	"file_tests":  testFileTests,
	"metadata":    testMetadata,
}

func testNoCommands(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	fsys := setupTestRoot(t)

	plan := &testplan.Plan{
		Tests: []testplan.Test{
			{
				Name: "file existence: passwd exists",
				Type: testplan.TypeFileExistence,
				FileExistence: &testplan.FileExistenceSpec{
					Path:        "/etc/passwd",
					ShouldExist: true,
				},
			},
			{
				Name: "command: echo hello",
				Type: testplan.TypeCommand,
				Command: &testplan.CommandSpec{
					Command: "echo",
					Args:    []string{"hello"},
				},
			},
			{
				Name: "file content: passwd has root",
				Type: testplan.TypeFileContent,
				FileContent: &testplan.FileContentSpec{
					Path:             "/etc/passwd",
					ExpectedContents: []string{"root"},
				},
			},
		},
	}

	Run(buf, plan, nil, Options{FS: fsys, NoCommands: true})
}

func testFileTests(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	fsys := setupTestRoot(t)

	plan := &testplan.Plan{
		Tests: []testplan.Test{
			{
				Name: "file existence: passwd exists",
				Type: testplan.TypeFileExistence,
				FileExistence: &testplan.FileExistenceSpec{
					Path:        "/etc/passwd",
					ShouldExist: true,
					Permissions: "-rw-r--r--",
				},
			},
			{
				Name: "file existence: missing file absent",
				Type: testplan.TypeFileExistence,
				FileExistence: &testplan.FileExistenceSpec{
					Path:        "/nonexistent",
					ShouldExist: false,
				},
			},
			{
				Name: "file existence: myservice is executable",
				Type: testplan.TypeFileExistence,
				FileExistence: &testplan.FileExistenceSpec{
					Path:         "/app/bin/myservice",
					ShouldExist:  true,
					ExecutableBy: []string{"owner"},
				},
			},
			{
				Name: "file content: passwd has app user",
				Type: testplan.TypeFileContent,
				FileContent: &testplan.FileContentSpec{
					Path:             "/etc/passwd",
					ExpectedContents: []string{"app:x:1000"},
					ExcludedContents: []string{"secretpassword"},
				},
			},
		},
	}

	Run(buf, plan, nil, Options{FS: fsys})
}

func testMetadata(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	fsys := setupTestRoot(t)

	meta := &oci.ImageConfig{
		User:       "app",
		WorkingDir: "/app",
		Entrypoint: []string{"/app/bin/myservice"},
		Env:        []string{"PATH=/usr/bin:/bin", "APP_ENV=production"},
		Labels:     map[string]string{"version": "1.0"},
	}

	plan := &testplan.Plan{
		Tests: []testplan.Test{
			{
				Name:     "metadata: user is app",
				Type:     testplan.TypeMetadata,
				Metadata: &testplan.MetadataSpec{Field: "user", Expected: "app"},
			},
			{
				Name:     "metadata: workdir is /app",
				Type:     testplan.TypeMetadata,
				Metadata: &testplan.MetadataSpec{Field: "workdir", Expected: "/app"},
			},
			{
				Name:     "metadata: env APP_ENV",
				Type:     testplan.TypeMetadata,
				Metadata: &testplan.MetadataSpec{Field: "env", Key: "APP_ENV", Expected: "production"},
			},
			{
				Name:     "metadata: label version",
				Type:     testplan.TypeMetadata,
				Metadata: &testplan.MetadataSpec{Field: "label", Key: "version", Expected: "1.0"},
			},
		},
	}

	Run(buf, plan, meta, Options{FS: fsys})
}
