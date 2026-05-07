package testexec

import (
	"bytes"
	"flag"
	"fmt"
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

func TestLooksBinary(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    bool
	}{
		{"empty", nil, false},
		{"short text", []byte("hello world"), false},
		{"text with high bytes", []byte("héllo wörld"), false},
		{"single nul", []byte("hello\x00world"), true},
		{"nul beyond sniff window", append(bytes.Repeat([]byte("a"), binarySniffLimit+10), 0), false},
		{"nul inside sniff window", append(bytes.Repeat([]byte("a"), binarySniffLimit-10), 0), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksBinary(tc.content); got != tc.want {
				t.Errorf("looksBinary = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCappedWriter(t *testing.T) {
	t.Run("under cap", func(t *testing.T) {
		w := &cappedWriter{cap: 100}
		n, err := w.Write([]byte("hello"))
		if n != 5 || err != nil {
			t.Fatalf("Write = (%d, %v), want (5, nil)", n, err)
		}
		if w.total != 5 || w.buf.Len() != 5 {
			t.Errorf("total=%d, buf=%d, want total=5, buf=5", w.total, w.buf.Len())
		}
	})
	t.Run("over cap drops bytes but reports full write", func(t *testing.T) {
		w := &cappedWriter{cap: 10}
		n, err := w.Write([]byte("0123456789ABCDEF"))
		if n != 16 || err != nil {
			t.Fatalf("Write = (%d, %v), want (16, nil)", n, err)
		}
		if w.buf.Len() != 10 {
			t.Errorf("buf=%d, want 10", w.buf.Len())
		}
		if w.total != 16 {
			t.Errorf("total=%d, want 16", w.total)
		}
		if got := w.buf.String(); got != "0123456789" {
			t.Errorf("captured = %q, want %q", got, "0123456789")
		}
	})
	t.Run("multiple writes past cap accumulate total", func(t *testing.T) {
		w := &cappedWriter{cap: 5}
		w.Write([]byte("hello"))
		w.Write([]byte(" world"))
		w.Write([]byte("!!!"))
		if w.buf.String() != "hello" {
			t.Errorf("captured = %q", w.buf.String())
		}
		if w.total != 14 {
			t.Errorf("total = %d, want 14", w.total)
		}
	})
}

func TestFormatBody(t *testing.T) {
	cases := []struct {
		name     string
		label    string
		captured []byte
		total    int
		want     string
	}{
		{
			name: "empty",
			want: "--- stdout (empty) ---",
		},
		{
			name:     "short text",
			captured: []byte("hello\n"),
			total:    6,
			want:     "--- stdout (6 bytes) ---\nhello",
		},
		{
			name:     "binary",
			captured: []byte("\x7fELF\x00\x01\x01"),
			total:    7,
			want:     "--- stdout (7 bytes, binary) ---",
		},
		{
			name:     "binary with capture cap exceeded",
			captured: []byte("\x7fELF\x00\x01\x01"),
			total:    1024,
			want:     "--- stdout (1024 bytes, binary, 1017 dropped) ---",
		},
		{
			name:     "render-truncated text",
			captured: bytes.Repeat([]byte("a"), diagnosticBodyLimit+50),
			total:    diagnosticBodyLimit + 50,
		},
		{
			name:     "capture-truncated only",
			captured: []byte("hello"),
			total:    1024,
			want:     "--- stdout (1024 bytes, 1019 dropped) ---\nhello",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatBody("stdout", tc.captured, tc.total)
			if tc.want == "" {
				// Render-truncated case: spot-check the heading and shape
				// rather than reproducing the giant body string here.
				wantHead := fmt.Sprintf("--- stdout (%d bytes, %d truncated) ---", tc.total, tc.total-diagnosticBodyLimit)
				if !strings.HasPrefix(got, wantHead+"\n") {
					t.Errorf("missing/wrong heading\ngot prefix: %q\nwant prefix: %q", got[:min(len(got), len(wantHead)+1)], wantHead)
				}
				if !strings.Contains(got, "\n... [truncated] ...\n") {
					t.Error("missing truncation marker")
				}
				return
			}
			if got != tc.want {
				t.Errorf("\ngot:\n%s\n\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestMatchPatternsBinary(t *testing.T) {
	t.Run("binary content with patterns errors clearly", func(t *testing.T) {
		err := matchPatterns("stdout", []byte("\x7fELF\x00binary"), []string{"foo"}, nil)
		if err == nil || !strings.Contains(err.Error(), "binary") {
			t.Errorf("err = %v, want error containing 'binary'", err)
		}
	})
	t.Run("binary content with no patterns is fine", func(t *testing.T) {
		if err := matchPatterns("stdout", []byte("\x00\x01\x02"), nil, nil); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
	t.Run("text content matches normally", func(t *testing.T) {
		if err := matchPatterns("stdout", []byte("hello world"), []string{"hello"}, nil); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
