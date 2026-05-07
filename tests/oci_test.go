package tare_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/tare/internal/config"
	"github.com/DataDog/tare/internal/oci"
	"github.com/DataDog/tare/internal/testexec"
	"github.com/DataDog/tare/internal/testplan"
)

// ociHarness builds an OCI layout and runs tests against it.
type ociHarness struct {
	dir string
	t   *testing.T
}

func newOCIHarness(t *testing.T) *ociHarness {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755)
	os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644)
	return &ociHarness{dir: dir, t: t}
}

type layerEntry struct {
	Name     string
	Content  []byte
	Mode     int64
	UID, GID int
	Type     byte
	Linkname string
}

func (h *ociHarness) build(icfg oci.ImageConfig, layers ...[]layerEntry) *oci.Layout {
	h.t.Helper()

	var layerDigests []string
	for _, entries := range layers {
		layerDigests = append(layerDigests, h.addLayer(entries))
	}

	cfgData, _ := json.Marshal(struct {
		Config oci.ImageConfig `json:"config"`
	}{Config: icfg})
	cfgDigest := h.writeBlob(cfgData)

	manifest := struct {
		SchemaVersion int `json:"schemaVersion"`
		Config        struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
		} `json:"layers"`
	}{SchemaVersion: 2}
	manifest.Config.MediaType = "application/vnd.oci.image.config.v1+json"
	manifest.Config.Digest = cfgDigest
	for _, d := range layerDigests {
		manifest.Layers = append(manifest.Layers, struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
		}{
			MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
			Digest:    d,
		})
	}
	manifestData, _ := json.Marshal(manifest)
	manifestDigest := h.writeBlob(manifestData)

	idx := struct {
		SchemaVersion int `json:"schemaVersion"`
		Manifests     []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int    `json:"size"`
			Platform  struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
	}{SchemaVersion: 2}
	idx.Manifests = append(idx.Manifests, struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int    `json:"size"`
		Platform  struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	}{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    manifestDigest,
		Size:      len(manifestData),
		Platform: struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		}{Architecture: "amd64", OS: "linux"},
	})
	idxData, _ := json.Marshal(idx)
	os.WriteFile(filepath.Join(h.dir, "index.json"), idxData, 0o644)

	l, err := oci.Open(h.dir)
	if err != nil {
		h.t.Fatalf("opening layout: %v", err)
	}
	return l
}

func (h *ociHarness) addLayer(entries []layerEntry) string {
	h.t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typeflag := e.Type
		if typeflag == 0 {
			if len(e.Content) > 0 || e.Mode != 0 {
				typeflag = tar.TypeReg
			} else {
				typeflag = tar.TypeDir
			}
		}
		tw.WriteHeader(&tar.Header{
			Name: e.Name, Mode: e.Mode, Uid: e.UID, Gid: e.GID,
			Size: int64(len(e.Content)), Typeflag: typeflag, Linkname: e.Linkname,
		})
		if len(e.Content) > 0 {
			tw.Write(e.Content)
		}
	}
	tw.Close()
	gz.Close()
	return h.writeBlob(buf.Bytes())
}

func (h *ociHarness) writeBlob(data []byte) string {
	h.t.Helper()
	hash := sha256.Sum256(data)
	hex := fmt.Sprintf("%x", hash)
	os.WriteFile(filepath.Join(h.dir, "blobs", "sha256", hex), data, 0o644)
	return "sha256:" + hex
}

func (h *ociHarness) run(t *testing.T, layout *oci.Layout, cfg *config.Config) tapResult {
	t.Helper()

	dest := t.TempDir()
	result, err := layout.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	t.Cleanup(func() { result.Root.Close() })

	icfg, err := layout.ImageConfig("linux", "amd64")
	if err != nil {
		t.Fatalf("ImageConfig: %v", err)
	}

	cwd := icfg.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	fsys := oci.NewFS(result, cwd)

	plan := testplan.FromConfig(cfg)

	var buf bytes.Buffer
	failures := testexec.Run(&buf, plan, icfg, testexec.Options{
		FS:         fsys,
		NoCommands: true,
	})

	return tapResult{
		ExitCode: failures,
		Output:   buf.String(),
	}
}

func TestOCIGeneral(t *testing.T) {
	h := newOCIHarness(t)
	layout := h.build(oci.ImageConfig{
		User:       "1000",
		WorkingDir: "/app",
		Entrypoint: []string{"/app/server"},
		Env:        []string{"PATH=/usr/bin:/bin", "APP_ENV=production"},
	}, []layerEntry{
		{Name: "etc/", Type: tar.TypeDir, Mode: 0o755},
		{Name: "etc/passwd", Content: []byte("root:x:0:0:root:/root:/bin/sh\napp:x:1000:1000::/app:/bin/sh\n"), Mode: 0o644, UID: 0, GID: 0},
		{Name: "etc/ssl/", Type: tar.TypeDir, Mode: 0o755},
		{Name: "etc/ssl/certs/", Type: tar.TypeDir, Mode: 0o755},
		{Name: "etc/ssl/certs/ca-certificates.crt", Content: []byte("--- cert ---"), Mode: 0o644},
		{Name: "app/", Type: tar.TypeDir, Mode: 0o755},
		{Name: "app/server", Content: []byte("#!/bin/sh\necho hello"), Mode: 0o755, UID: 1000, GID: 1000},
	})

	cfg := &config.Config{
		SchemaVersion: 1,
		Metadata: []config.MetadataAssertion{
			{
				User:    "1000",
				Workdir: "/app",
				Env: []config.KV{
					{Key: "APP_ENV", Value: "production"},
				},
			},
		},
		Files: []config.FileAssertion{
			{Path: "/app/server", ExecutableBy: config.ClassList{"owner"}},
			{Path: "/etc/ssl/certs/ca-certificates.crt"},
			{Path: "/bin/sh", Present: boolPtr(false)},
			{Path: "/etc/passwd", Contents: pat("app:x:1000")},
		},
		Commands: []config.CommandAssertion{
			{
				Name:   "echo hello",
				Run:    config.Run{Argv: []string{"echo", "hello"}},
				Stdout: pat("hello"),
			},
		},
	}

	result := h.run(t, layout, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertGolden(t, "oci_general", result)
}

func TestOCIFileExistence(t *testing.T) {
	h := newOCIHarness(t)
	layout := h.build(oci.ImageConfig{}, []layerEntry{
		{Name: "etc/", Type: tar.TypeDir, Mode: 0o755},
		{Name: "etc/passwd", Content: []byte("root:x:0:0:root:/root:/bin/sh\n"), Mode: 0o644, UID: 0, GID: 0},
		{Name: "app/", Type: tar.TypeDir, Mode: 0o755},
		{Name: "app/server", Content: []byte("binary"), Mode: 0o755, UID: 1000, GID: 1000},
	})

	cfg := &config.Config{
		SchemaVersion: 1,
		Files: []config.FileAssertion{
			{Path: "/etc/passwd", UID: intPtr(0), GID: intPtr(0)},
			{Path: "/app/server", UID: intPtr(1000), GID: intPtr(1000)},
			{Path: "/etc/passwd", Permissions: "-rw-r--r--"},
			{Path: "/does/not/exist", Present: boolPtr(false)},
			{Path: "/app/server", ExecutableBy: config.ClassList{"owner"}},
			{Path: "/app/server", ExecutableBy: config.ClassList{"any"}},
		},
	}

	result := h.run(t, layout, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertGolden(t, "oci_file_existence", result)
}

func TestOCIFileContent(t *testing.T) {
	h := newOCIHarness(t)
	layout := h.build(oci.ImageConfig{}, []layerEntry{
		{Name: "etc/", Type: tar.TypeDir, Mode: 0o755},
		{Name: "etc/passwd", Content: []byte("root:x:0:0:root:/root:/bin/sh\napp:x:1000:1000::/app:/bin/sh\n"), Mode: 0o644},
	})

	cfg := &config.Config{
		SchemaVersion: 1,
		Files: []config.FileAssertion{
			{Path: "/etc/passwd", Contents: pat("root", "app")},
			{Path: "/etc/passwd", Not: &config.FileNot{Contents: pat("secretpassword")}},
			{
				Path:     "/etc/passwd",
				Contents: pat("root"),
				Not:      &config.FileNot{Contents: pat("NOPE")},
			},
		},
	}

	result := h.run(t, layout, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertGolden(t, "oci_file_content", result)
}

func TestOCIMetadata(t *testing.T) {
	h := newOCIHarness(t)
	layout := h.build(oci.ImageConfig{
		User:       "app",
		WorkingDir: "/app",
		Entrypoint: []string{"/app/server"},
		Env:        []string{"PATH=/usr/bin:/bin", "APP_ENV=production"},
		Labels:     map[string]string{"version": "1.0"},
	}, []layerEntry{
		{Name: "app/", Type: tar.TypeDir, Mode: 0o755},
	})

	cfg := &config.Config{
		SchemaVersion: 1,
		Metadata: []config.MetadataAssertion{
			{
				User:    "app",
				Workdir: "/app",
				Env: []config.KV{
					{Key: "APP_ENV", Value: "production"},
					{Key: "PATH", Value: "/usr/.*", Regex: true},
				},
				Labels: []config.KV{
					{Key: "version", Value: "1.0"},
				},
			},
		},
	}

	result := h.run(t, layout, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertGolden(t, "oci_metadata", result)
}

func TestOCICommandSkip(t *testing.T) {
	h := newOCIHarness(t)
	layout := h.build(oci.ImageConfig{}, []layerEntry{
		{Name: "app/", Type: tar.TypeDir, Mode: 0o755},
	})

	cfg := &config.Config{
		SchemaVersion: 1,
		Commands: []config.CommandAssertion{
			{Name: "should be skipped", Run: config.Run{Argv: []string{"echo", "hello"}}},
			{Name: "also skipped", Run: config.Run{Argv: []string{"false"}}},
		},
	}

	result := h.run(t, layout, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertGolden(t, "oci_command_skip", result)
}

func TestOCIScan(t *testing.T) {
	h := newOCIHarness(t)
	layout := h.build(oci.ImageConfig{}, []layerEntry{
		{Name: "app/", Type: tar.TypeDir, Mode: 0o755},
		// Empty scan path — no binaries, should pass with 0 scanned.
	})

	cfg := &config.Config{
		SchemaVersion: 1,
		Scan: []config.ScanEntry{
			{Name: "empty app dir", Path: "/app"},
		},
	}

	result := h.run(t, layout, cfg)
	t.Logf("TAP output:\n%s", result.Output)
	assertGolden(t, "oci_scan", result)
}
