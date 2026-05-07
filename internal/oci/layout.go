// Package oci reads OCI image layout directories and extracts rootfs trees.
package oci

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ImageConfig holds the parsed image configuration. This is the "config"
// section of an OCI image config blob, and matches the "Config" field
// from docker image inspect output.
type ImageConfig struct {
	Entrypoint   []string          `json:"Entrypoint"`
	Cmd          []string          `json:"Cmd"`
	Env          []string          `json:"Env"`
	Labels       map[string]string `json:"Labels"`
	User         string            `json:"User"`
	WorkingDir   string            `json:"WorkingDir"`
	ExposedPorts map[string]any    `json:"ExposedPorts"`
	Volumes      map[string]any    `json:"Volumes"`

	// Architecture is the image's target architecture (amd64, arm64, etc.)
	// in the OCI/Docker convention. Excluded from JSON since it lives at
	// the top level of docker inspect output, not inside the Config sub-object;
	// callers populate this field after parsing.
	Architecture string `json:"-"`
}

// Layout represents an opened OCI image layout directory.
type Layout struct {
	dir string
}

// Open validates and opens an OCI image layout directory.
func Open(dir string) (*Layout, error) {
	data, err := os.ReadFile(filepath.Join(dir, "oci-layout"))
	if err != nil {
		return nil, fmt.Errorf("reading oci-layout: %w", err)
	}
	var layout ociLayout
	if err := json.Unmarshal(data, &layout); err != nil {
		return nil, fmt.Errorf("parsing oci-layout: %w", err)
	}
	if layout.ImageLayoutVersion != "1.0.0" {
		return nil, fmt.Errorf("unsupported imageLayoutVersion: %q", layout.ImageLayoutVersion)
	}
	return &Layout{dir: dir}, nil
}

// ImageConfig reads the image configuration for the given platform.
// The goos/goarch values match Go's GOOS/GOARCH (e.g., "linux", "amd64").
func (l *Layout) ImageConfig(goos, goarch string) (*ImageConfig, error) {
	m, err := l.findManifest(goos, goarch)
	if err != nil {
		return nil, err
	}

	data, err := l.readBlob(m.Config.Digest)
	if err != nil {
		return nil, fmt.Errorf("reading image config: %w", err)
	}

	var ic ociImageConfig
	if err := json.Unmarshal(data, &ic); err != nil {
		return nil, fmt.Errorf("parsing image config: %w", err)
	}

	ic.Config.Architecture = goarch
	return &ic.Config, nil
}

// findManifest reads index.json, matches the platform, and returns the manifest.
//
// For single-platform layouts, index.json points directly to a manifest.
// For multi-arch layouts, index.json points to a nested image index that
// contains per-platform manifest descriptors.
func (l *Layout) findManifest(goos, goarch string) (*manifest, error) {
	data, err := os.ReadFile(filepath.Join(l.dir, "index.json"))
	if err != nil {
		return nil, fmt.Errorf("reading index.json: %w", err)
	}

	var idx index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing index.json: %w", err)
	}

	if len(idx.Manifests) == 0 {
		return nil, fmt.Errorf("index.json contains no manifests")
	}

	desc, err := l.resolveManifestDesc(idx.Manifests, goos, goarch)
	if err != nil {
		return nil, err
	}

	blob, err := l.readBlob(desc.Digest)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	var m manifest
	if err := json.Unmarshal(blob, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	return &m, nil
}

// resolveManifestDesc finds a manifest descriptor for the given platform.
// First tries a direct platform match in the descriptors. If that fails,
// follows any index-type descriptors one level deep to find per-platform
// manifests inside (handles multi-arch layouts where index.json points to
// a nested image index).
func (l *Layout) resolveManifestDesc(descs []descriptor, goos, goarch string) (*descriptor, error) {
	// Try direct platform match. If the result is a manifest, we're done.
	desc, err := matchPlatform(descs, goos, goarch)
	if err == nil && isManifestMediaType(desc.MediaType) {
		return desc, nil
	}

	// Otherwise, follow index-type descriptors to find per-platform manifests inside.
	seen := map[string]bool{}
	for _, d := range descs {
		if !isIndexMediaType(d.MediaType) {
			continue
		}
		if seen[d.Digest] {
			continue
		}
		seen[d.Digest] = true

		innerData, err := l.readBlob(d.Digest)
		if err != nil {
			continue
		}
		var innerIdx index
		if err := json.Unmarshal(innerData, &innerIdx); err != nil {
			continue
		}
		if desc, err := matchPlatform(innerIdx.Manifests, goos, goarch); err == nil {
			return desc, nil
		}
	}

	return nil, fmt.Errorf("no manifest found for %s/%s", goos, goarch)
}

func isManifestMediaType(mt string) bool {
	return mt == "application/vnd.oci.image.manifest.v1+json" ||
		mt == "application/vnd.docker.distribution.manifest.v2+json"
}

func isIndexMediaType(mt string) bool {
	return mt == "application/vnd.oci.image.index.v1+json" ||
		mt == "application/vnd.docker.distribution.manifest.list.v2+json"
}

// matchPlatform selects the manifest descriptor matching the given platform.
// If there is exactly one manifest with no platform field, it is used as-is.
func matchPlatform(descs []descriptor, goos, goarch string) (*descriptor, error) {
	// Single manifest with no platform — use it directly.
	if len(descs) == 1 && descs[0].Platform == nil {
		return &descs[0], nil
	}

	for i := range descs {
		d := &descs[i]
		if d.Platform != nil && d.Platform.OS == goos && d.Platform.Architecture == goarch {
			return d, nil
		}
	}

	return nil, fmt.Errorf("no manifest found for %s/%s", goos, goarch)
}

// readBlob reads a blob from the layout's blobs directory.
// The digest must be in "algorithm:hex" format (e.g., "sha256:abc123...").
func (l *Layout) readBlob(digest string) ([]byte, error) {
	alg, hex, ok := strings.Cut(digest, ":")
	if !ok {
		return nil, fmt.Errorf("invalid digest format: %q", digest)
	}
	return os.ReadFile(filepath.Join(l.dir, "blobs", alg, hex))
}

// openBlob opens a blob for streaming reads.
func (l *Layout) openBlob(digest string) (*os.File, error) {
	alg, hex, ok := strings.Cut(digest, ":")
	if !ok {
		return nil, fmt.Errorf("invalid digest format: %q", digest)
	}
	return os.Open(filepath.Join(l.dir, "blobs", alg, hex))
}

// OCI spec types — only the fields we need.

type ociLayout struct {
	ImageLayoutVersion string `json:"imageLayoutVersion"`
}

type index struct {
	SchemaVersion int          `json:"schemaVersion"`
	Manifests     []descriptor `json:"manifests"`
}

type descriptor struct {
	MediaType string    `json:"mediaType"`
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	Platform  *platform `json:"platform,omitempty"`
}

type platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

type ociImageConfig struct {
	Config ImageConfig `json:"config"`
}
