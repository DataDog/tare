package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// testLayout builds an OCI layout directory for testing.
type testLayout struct {
	dir string
	t   *testing.T
}

func newTestLayout(t *testing.T) *testLayout {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755)
	os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644)
	return &testLayout{dir: dir, t: t}
}

// layerEntry describes a single entry in a layer tar.
type layerEntry struct {
	Name     string
	Content  []byte
	Mode     int64
	UID, GID int
	Type     byte // tar.TypeReg, tar.TypeDir, tar.TypeSymlink, etc.
	Linkname string
}

// addLayer creates a gzip-compressed tar layer blob and returns its digest.
func (tl *testLayout) addLayer(entries []layerEntry) string {
	tl.t.Helper()
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

		hdr := &tar.Header{
			Name:     e.Name,
			Mode:     e.Mode,
			Uid:      e.UID,
			Gid:      e.GID,
			Size:     int64(len(e.Content)),
			Typeflag: typeflag,
			Linkname: e.Linkname,
		}

		if err := tw.WriteHeader(hdr); err != nil {
			tl.t.Fatalf("writing tar header: %v", err)
		}
		if len(e.Content) > 0 {
			if _, err := tw.Write(e.Content); err != nil {
				tl.t.Fatalf("writing tar data: %v", err)
			}
		}
	}

	tw.Close()
	gz.Close()
	return tl.writeBlob(buf.Bytes())
}

// addConfig creates a config blob and returns its digest.
func (tl *testLayout) addConfig(cfg ImageConfig) string {
	tl.t.Helper()
	data, err := json.Marshal(ociImageConfig{Config: cfg})
	if err != nil {
		tl.t.Fatalf("marshaling config: %v", err)
	}
	return tl.writeBlob(data)
}

// addManifest creates a manifest blob and returns its digest.
func (tl *testLayout) addManifest(configDigest string, layerDigests []string) string {
	tl.t.Helper()
	var layers []descriptor
	for _, d := range layerDigests {
		layers = append(layers, descriptor{
			MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
			Digest:    d,
		})
	}
	m := manifest{
		SchemaVersion: 2,
		Config: descriptor{
			MediaType: "application/vnd.oci.image.config.v1+json",
			Digest:    configDigest,
		},
		Layers: layers,
	}
	data, err := json.Marshal(m)
	if err != nil {
		tl.t.Fatalf("marshaling manifest: %v", err)
	}
	return tl.writeBlob(data)
}

// writeIndex writes index.json with the given manifest descriptors.
func (tl *testLayout) writeIndex(descs []descriptor) {
	tl.t.Helper()
	idx := index{SchemaVersion: 2, Manifests: descs}
	data, err := json.Marshal(idx)
	if err != nil {
		tl.t.Fatalf("marshaling index: %v", err)
	}
	os.WriteFile(filepath.Join(tl.dir, "index.json"), data, 0o644)
}

// writeBlob writes data to blobs/sha256/<digest> and returns the digest string.
func (tl *testLayout) writeBlob(data []byte) string {
	tl.t.Helper()
	h := sha256.Sum256(data)
	hex := fmt.Sprintf("%x", h)
	digest := "sha256:" + hex
	if err := os.WriteFile(filepath.Join(tl.dir, "blobs", "sha256", hex), data, 0o644); err != nil {
		tl.t.Fatalf("writing blob: %v", err)
	}
	return digest
}

// build creates a simple single-platform layout and returns it opened.
func (tl *testLayout) build(layerSets ...[]layerEntry) *Layout {
	tl.t.Helper()
	return tl.buildWithConfig(ImageConfig{}, layerSets...)
}

func (tl *testLayout) buildWithConfig(cfg ImageConfig, layerSets ...[]layerEntry) *Layout {
	tl.t.Helper()
	var layerDigests []string
	for _, entries := range layerSets {
		layerDigests = append(layerDigests, tl.addLayer(entries))
	}
	configDigest := tl.addConfig(cfg)
	manifestDigest := tl.addManifest(configDigest, layerDigests)
	tl.writeIndex([]descriptor{
		{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    manifestDigest,
			Platform:  &platform{OS: "linux", Architecture: "amd64"},
		},
	})

	l, err := Open(tl.dir)
	if err != nil {
		tl.t.Fatalf("opening layout: %v", err)
	}
	return l
}

func TestExtractSingleLayer(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build([]layerEntry{
		{Name: "hello.txt", Content: []byte("hello world"), Mode: 0o644, UID: 1000, GID: 1000},
		{Name: "bin/", Type: tar.TypeDir, Mode: 0o755},
		{Name: "bin/app", Content: []byte("#!/bin/sh"), Mode: 0o755, UID: 0, GID: 0},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	// Verify files exist with correct content.
	data, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	if err != nil {
		t.Fatalf("reading hello.txt: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("hello.txt content = %q, want %q", data, "hello world")
	}

	data, err = os.ReadFile(filepath.Join(dest, "bin", "app"))
	if err != nil {
		t.Fatalf("reading bin/app: %v", err)
	}
	if string(data) != "#!/bin/sh" {
		t.Errorf("bin/app content = %q, want %q", data, "#!/bin/sh")
	}

	// Verify metadata.
	meta := result.Metadata["/hello.txt"]
	if meta == nil {
		t.Fatal("no metadata for /hello.txt")
	}
	if meta.UID != 1000 || meta.GID != 1000 {
		t.Errorf("hello.txt uid/gid = %d/%d, want 1000/1000", meta.UID, meta.GID)
	}
	if meta.Mode.Perm() != 0o644 {
		t.Errorf("hello.txt mode = %o, want 644", meta.Mode.Perm())
	}

	meta = result.Metadata["/bin/app"]
	if meta == nil {
		t.Fatal("no metadata for /bin/app")
	}
	if meta.UID != 0 || meta.GID != 0 {
		t.Errorf("bin/app uid/gid = %d/%d, want 0/0", meta.UID, meta.GID)
	}
	if meta.Mode.Perm() != 0o755 {
		t.Errorf("bin/app mode = %o, want 755", meta.Mode.Perm())
	}
}

func TestExtractMultiLayerOverwrite(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build(
		[]layerEntry{
			{Name: "file.txt", Content: []byte("layer1"), Mode: 0o644},
		},
		[]layerEntry{
			{Name: "file.txt", Content: []byte("layer2"), Mode: 0o600, UID: 42},
		},
	)

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	data, err := os.ReadFile(filepath.Join(dest, "file.txt"))
	if err != nil {
		t.Fatalf("reading file.txt: %v", err)
	}
	if string(data) != "layer2" {
		t.Errorf("file.txt = %q, want %q (layer 2 should overwrite)", data, "layer2")
	}

	meta := result.Metadata["/file.txt"]
	if meta.UID != 42 {
		t.Errorf("uid = %d, want 42", meta.UID)
	}
	if meta.Mode.Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", meta.Mode.Perm())
	}
}

func TestExtractIndividualWhiteout(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build(
		[]layerEntry{
			{Name: "keep.txt", Content: []byte("keep"), Mode: 0o644},
			{Name: "delete-me.txt", Content: []byte("gone"), Mode: 0o644},
		},
		[]layerEntry{
			{Name: ".wh.delete-me.txt", Type: tar.TypeReg, Mode: 0o644},
		},
	)

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	// keep.txt should still exist.
	if _, err := os.Stat(filepath.Join(dest, "keep.txt")); err != nil {
		t.Errorf("keep.txt should exist: %v", err)
	}

	// delete-me.txt should be gone.
	if _, err := os.Stat(filepath.Join(dest, "delete-me.txt")); err == nil {
		t.Error("delete-me.txt should not exist after whiteout")
	}

	if _, ok := result.Metadata["/delete-me.txt"]; ok {
		t.Error("metadata should not contain /delete-me.txt after whiteout")
	}
}

func TestExtractOpaqueWhiteout(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build(
		[]layerEntry{
			{Name: "dir/", Type: tar.TypeDir, Mode: 0o755},
			{Name: "dir/old1.txt", Content: []byte("old1"), Mode: 0o644},
			{Name: "dir/old2.txt", Content: []byte("old2"), Mode: 0o644},
		},
		[]layerEntry{
			{Name: "dir/", Type: tar.TypeDir, Mode: 0o755},
			{Name: "dir/.wh..wh..opq", Type: tar.TypeReg, Mode: 0o644},
			{Name: "dir/new.txt", Content: []byte("new"), Mode: 0o644},
		},
	)

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	// Old files should be gone.
	if _, err := os.Stat(filepath.Join(dest, "dir", "old1.txt")); err == nil {
		t.Error("dir/old1.txt should not exist after opaque whiteout")
	}
	if _, err := os.Stat(filepath.Join(dest, "dir", "old2.txt")); err == nil {
		t.Error("dir/old2.txt should not exist after opaque whiteout")
	}

	// New file should exist.
	data, err := os.ReadFile(filepath.Join(dest, "dir", "new.txt"))
	if err != nil {
		t.Fatalf("reading dir/new.txt: %v", err)
	}
	if string(data) != "new" {
		t.Errorf("dir/new.txt = %q, want %q", data, "new")
	}

	// Metadata should reflect the new state.
	if _, ok := result.Metadata["/dir/old1.txt"]; ok {
		t.Error("metadata should not contain /dir/old1.txt")
	}
	if _, ok := result.Metadata["/dir/new.txt"]; !ok {
		t.Error("metadata should contain /dir/new.txt")
	}
}

func TestExtractSymlinks(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build([]layerEntry{
		{Name: "target.txt", Content: []byte("real"), Mode: 0o644},
		{Name: "link.txt", Type: tar.TypeSymlink, Linkname: "target.txt"},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	// Symlink should exist and resolve.
	target, err := os.Readlink(filepath.Join(dest, "link.txt"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "target.txt" {
		t.Errorf("link target = %q, want %q", target, "target.txt")
	}

	// Metadata should record link target.
	meta := result.Metadata["/link.txt"]
	if meta == nil {
		t.Fatal("no metadata for /link.txt")
	}
	if meta.LinkTarget != "target.txt" {
		t.Errorf("metadata LinkTarget = %q, want %q", meta.LinkTarget, "target.txt")
	}
}

func TestExtractHardlinks(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build([]layerEntry{
		{Name: "original.txt", Content: []byte("shared"), Mode: 0o644, UID: 99},
		{Name: "linked.txt", Type: tar.TypeLink, Linkname: "original.txt"},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	// Both files should have same content.
	for _, name := range []string{"original.txt", "linked.txt"} {
		data, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if string(data) != "shared" {
			t.Errorf("%s = %q, want %q", name, data, "shared")
		}
	}

	// Hardlink metadata should copy from original.
	meta := result.Metadata["/linked.txt"]
	if meta == nil {
		t.Fatal("no metadata for /linked.txt")
	}
	if meta.UID != 99 {
		t.Errorf("linked.txt uid = %d, want 99", meta.UID)
	}
}

func TestPlatformMatching(t *testing.T) {
	tl := newTestLayout(t)

	layer := tl.addLayer([]layerEntry{
		{Name: "hello.txt", Content: []byte("hello"), Mode: 0o644},
	})
	cfg := tl.addConfig(ImageConfig{})
	amd64Manifest := tl.addManifest(cfg, []string{layer})
	arm64Manifest := tl.addManifest(cfg, []string{layer})

	tl.writeIndex([]descriptor{
		{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    amd64Manifest,
			Platform:  &platform{OS: "linux", Architecture: "amd64"},
		},
		{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    arm64Manifest,
			Platform:  &platform{OS: "linux", Architecture: "arm64"},
		},
	})

	l, err := Open(tl.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Should find the amd64 manifest.
	_, err = l.ImageConfig("linux", "amd64")
	if err != nil {
		t.Errorf("ImageConfig(linux, amd64): %v", err)
	}

	// Should find the arm64 manifest.
	_, err = l.ImageConfig("linux", "arm64")
	if err != nil {
		t.Errorf("ImageConfig(linux, arm64): %v", err)
	}

	// Should fail for unknown platform.
	_, err = l.ImageConfig("linux", "riscv64")
	if err == nil {
		t.Error("ImageConfig(linux, riscv64) should fail")
	}
}

func TestNestedIndexMultiArch(t *testing.T) {
	tl := newTestLayout(t)

	// Create two platform-specific manifests.
	layer := tl.addLayer([]layerEntry{
		{Name: "hello.txt", Content: []byte("hello"), Mode: 0o644},
	})
	cfg := tl.addConfig(ImageConfig{User: "app"})
	amd64Manifest := tl.addManifest(cfg, []string{layer})
	arm64Manifest := tl.addManifest(cfg, []string{layer})

	// Create a nested image index containing both platforms.
	innerIdx := index{
		SchemaVersion: 2,
		Manifests: []descriptor{
			{
				MediaType: "application/vnd.oci.image.manifest.v1+json",
				Digest:    amd64Manifest,
				Platform:  &platform{OS: "linux", Architecture: "amd64"},
			},
			{
				MediaType: "application/vnd.oci.image.manifest.v1+json",
				Digest:    arm64Manifest,
				Platform:  &platform{OS: "linux", Architecture: "arm64"},
			},
		},
	}
	innerData, _ := json.Marshal(innerIdx)
	innerDigest := tl.writeBlob(innerData)

	// Top-level index.json points to the nested index (no platform).
	// Duplicate entries are valid OCI and occur in some real-world images.
	tl.writeIndex([]descriptor{
		{
			MediaType: "application/vnd.oci.image.index.v1+json",
			Digest:    innerDigest,
		},
		{
			MediaType: "application/vnd.oci.image.index.v1+json",
			Digest:    innerDigest,
		},
	})

	l, err := Open(tl.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Should resolve through the nested index.
	ic, err := l.ImageConfig("linux", "amd64")
	if err != nil {
		t.Fatalf("ImageConfig(linux, amd64): %v", err)
	}
	if ic.User != "app" {
		t.Errorf("User = %q, want %q", ic.User, "app")
	}

	ic, err = l.ImageConfig("linux", "arm64")
	if err != nil {
		t.Fatalf("ImageConfig(linux, arm64): %v", err)
	}
	if ic.User != "app" {
		t.Errorf("User = %q, want %q", ic.User, "app")
	}

	// Extract should work through the nested index too.
	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	data, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	if err != nil {
		t.Fatalf("reading hello.txt: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("hello.txt = %q, want %q", data, "hello")
	}
}

func TestSingleManifestNoPlatform(t *testing.T) {
	tl := newTestLayout(t)

	layer := tl.addLayer([]layerEntry{
		{Name: "hello.txt", Content: []byte("hello"), Mode: 0o644},
	})
	cfg := tl.addConfig(ImageConfig{User: "app"})
	manifestDigest := tl.addManifest(cfg, []string{layer})

	// No platform field on the manifest.
	tl.writeIndex([]descriptor{
		{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    manifestDigest,
		},
	})

	l, err := Open(tl.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ic, err := l.ImageConfig("linux", "amd64")
	if err != nil {
		t.Fatalf("ImageConfig: %v", err)
	}
	if ic.User != "app" {
		t.Errorf("User = %q, want %q", ic.User, "app")
	}
}

func TestMetadataPersistence(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build([]layerEntry{
		{Name: "file.txt", Content: []byte("data"), Mode: 0o644, UID: 1000, GID: 2000},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	result.Root.Close()

	// Read back the persisted metadata.
	data, err := os.ReadFile(filepath.Join(dest, metadataFile))
	if err != nil {
		t.Fatalf("reading %s: %v", metadataFile, err)
	}

	var loaded map[string]*FileMeta
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("parsing %s: %v", metadataFile, err)
	}

	meta, ok := loaded["/file.txt"]
	if !ok {
		t.Fatal("metadata missing /file.txt")
	}
	if meta.UID != 1000 || meta.GID != 2000 {
		t.Errorf("uid/gid = %d/%d, want 1000/2000", meta.UID, meta.GID)
	}
}

func TestFileModes(t *testing.T) {
	tl := newTestLayout(t)
	l := tl.build([]layerEntry{
		{Name: "readonly.txt", Content: []byte("r"), Mode: 0o444},
		{Name: "exec.sh", Content: []byte("#!/bin/sh"), Mode: 0o755},
		{Name: "wide-open.txt", Content: []byte("w"), Mode: 0o666},
	})

	dest := t.TempDir()
	result, err := l.Extract(dest, "linux", "amd64")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	defer result.Root.Close()

	tests := []struct {
		path string
		want fs.FileMode
	}{
		{"/readonly.txt", 0o444},
		{"/exec.sh", 0o755},
		{"/wide-open.txt", 0o666},
	}

	for _, tt := range tests {
		info, err := os.Stat(filepath.Join(dest, tt.path[1:]))
		if err != nil {
			t.Errorf("stat %s: %v", tt.path, err)
			continue
		}
		got := info.Mode().Perm()
		if got != tt.want {
			t.Errorf("%s mode = %o, want %o (on disk)", tt.path, got, tt.want)
		}

		meta := result.Metadata[tt.path]
		if meta == nil {
			t.Errorf("no metadata for %s", tt.path)
			continue
		}
		if meta.Mode.Perm() != tt.want {
			t.Errorf("%s metadata mode = %o, want %o", tt.path, meta.Mode.Perm(), tt.want)
		}
	}
}

func TestImageConfig(t *testing.T) {
	tl := newTestLayout(t)
	tl.buildWithConfig(ImageConfig{
		User:       "app",
		WorkingDir: "/app",
		Entrypoint: []string{"/app/bin/server"},
		Cmd:        []string{"--port", "8080"},
		Env:        []string{"PATH=/usr/bin:/bin", "APP_ENV=production"},
		Labels:     map[string]string{"version": "1.0"},
	})

	l, err := Open(tl.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ic, err := l.ImageConfig("linux", "amd64")
	if err != nil {
		t.Fatalf("ImageConfig: %v", err)
	}

	if ic.User != "app" {
		t.Errorf("User = %q, want %q", ic.User, "app")
	}
	if ic.WorkingDir != "/app" {
		t.Errorf("WorkingDir = %q, want %q", ic.WorkingDir, "/app")
	}
	if len(ic.Entrypoint) != 1 || ic.Entrypoint[0] != "/app/bin/server" {
		t.Errorf("Entrypoint = %v, want [/app/bin/server]", ic.Entrypoint)
	}
	if len(ic.Cmd) != 2 {
		t.Errorf("Cmd = %v, want [--port 8080]", ic.Cmd)
	}
	if len(ic.Env) != 2 {
		t.Errorf("Env = %v, want 2 entries", ic.Env)
	}
	if ic.Labels["version"] != "1.0" {
		t.Errorf("Labels[version] = %q, want %q", ic.Labels["version"], "1.0")
	}
}
