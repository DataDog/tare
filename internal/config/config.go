// Package config parses and validates tare YAML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/tare/internal/scan"
	"go.yaml.in/yaml/v4"
)

// Config is a parsed tare configuration.
type Config struct {
	SchemaVersion int                 `yaml:"schema_version"`
	Metadata      []MetadataAssertion `yaml:"metadata"`
	Files         []FileAssertion     `yaml:"files"`
	Commands      []CommandAssertion  `yaml:"commands"`
	Scan          []ScanEntry         `yaml:"scan"`
	Tare          *TareConfig         `yaml:"tare"`
}

// MetadataAssertion is one metadata-block assertion. Multiple entries
// compose without clobbering each other.
type MetadataAssertion struct {
	Name       string       `yaml:"name"`
	User       string       `yaml:"user"`
	Workdir    string       `yaml:"workdir"`
	Entrypoint []string     `yaml:"entrypoint"`
	Cmd        []string     `yaml:"cmd"`
	Env        []KV         `yaml:"env"`
	Ports      []string     `yaml:"ports"`
	Volumes    []string     `yaml:"volumes"`
	Labels     []KV         `yaml:"labels"`
	Not        *MetadataNot `yaml:"not"`
}

// MetadataNot lists metadata items that must be absent.
type MetadataNot struct {
	Env     []string `yaml:"env"`
	Ports   []string `yaml:"ports"`
	Volumes []string `yaml:"volumes"`
	Labels  []string `yaml:"labels"`
}

// KV is a key/value pair with optional regex matching on the value.
type KV struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
	Regex bool   `yaml:"regex"`
}

// FileAssertion is a unified existence + content + permission assertion
// against one path. Path entries can target files, directories, or symlinks.
type FileAssertion struct {
	Path         string    `yaml:"path"`
	Present      *bool     `yaml:"present"`
	Type         string    `yaml:"type"`
	UID          *int      `yaml:"uid"`
	GID          *int      `yaml:"gid"`
	Permissions  string    `yaml:"permissions"`
	ReadableBy   ClassList `yaml:"readable_by"`
	WritableBy   ClassList `yaml:"writable_by"`
	ExecutableBy ClassList `yaml:"executable_by"`
	Contents     []Pattern `yaml:"contents"`
	Regex        bool      `yaml:"regex"`
	Not          *FileNot  `yaml:"not"`
}

// FileNot inverts the contained child assertions.
type FileNot struct {
	Type         string    `yaml:"type"`
	ReadableBy   ClassList `yaml:"readable_by"`
	WritableBy   ClassList `yaml:"writable_by"`
	ExecutableBy ClassList `yaml:"executable_by"`
	Contents     []Pattern `yaml:"contents"`
}

// CommandAssertion runs a command in the container and asserts on output.
type CommandAssertion struct {
	Name     string      `yaml:"name"`
	Run      Run         `yaml:"run"`
	Env      []KV        `yaml:"env"`
	Exit     *int        `yaml:"exit"`
	Stdout   []Pattern   `yaml:"stdout"`
	Stderr   []Pattern   `yaml:"stderr"`
	Setup    [][]string  `yaml:"setup"`
	Teardown [][]string  `yaml:"teardown"`
	Regex    bool        `yaml:"regex"`
	Not      *CommandNot `yaml:"not"`
}

// CommandNot lists output patterns that must NOT match.
type CommandNot struct {
	Stdout []Pattern `yaml:"stdout"`
	Stderr []Pattern `yaml:"stderr"`
}

// ScanEntry configures ELF dependency analysis on one path.
type ScanEntry struct {
	Name   string   `yaml:"name"`
	Path   string   `yaml:"path"`
	Ignore []string `yaml:"ignore"`
	Limit  int      `yaml:"limit"`
}

// TareConfig holds tool/runtime configuration that does not produce
// individual test assertions.
type TareConfig struct {
	Runtime *RuntimeOptions `yaml:"runtime"`
}

// RuntimeOptions configures how the test container is created and run.
// Use this to mirror the deployment environment (k8s pod spec, compose
// file, bazel oci_image config) so tests run under production constraints.
type RuntimeOptions struct {
	User    string   `yaml:"user"`
	CapDrop []string `yaml:"cap_drop"`
	CapAdd  []string `yaml:"cap_add"`
	Binds   []string `yaml:"binds"`
	Env     []KV     `yaml:"env"`
	EnvFile string   `yaml:"env_file"`
}

// Run is a command to execute, accepting either a single string (interpreted
// as a one-element argv) or a list of strings (full argv).
type Run struct {
	Argv []string
}

// UnmarshalYAML accepts a scalar string or a sequence of strings.
func (r *Run) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		r.Argv = []string{s}
	case yaml.SequenceNode:
		var ss []string
		if err := node.Decode(&ss); err != nil {
			return err
		}
		r.Argv = ss
	default:
		return fmt.Errorf("run must be a string or list of strings")
	}
	return nil
}

// Pattern is a string-matching pattern in a list assertion. A bare YAML
// scalar is treated as a literal substring (or a regex if the enclosing
// test sets regex: true). A {match: "..."} map is always treated as regex.
type Pattern struct {
	// Value is the raw pattern string. Whether it's interpreted literally
	// or as a regex depends on Match and the enclosing test's Regex flag.
	Value string

	// Match is true when this entry was written as {match: "..."}; the value
	// is always interpreted as a regex regardless of test-level flags.
	Match bool
}

// UnmarshalYAML accepts a scalar string (literal/flag-controlled) or a
// {match: "..."} map (always regex).
func (p *Pattern) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Decode(&p.Value)
	case yaml.MappingNode:
		var raw struct {
			Match string `yaml:"match"`
		}
		if err := node.Decode(&raw); err != nil {
			return err
		}
		if raw.Match == "" {
			return fmt.Errorf("pattern map requires non-empty 'match' field")
		}
		p.Value = raw.Match
		p.Match = true
	default:
		return fmt.Errorf("pattern must be a string or {match: ...}")
	}
	return nil
}

// ClassList is a list of permission classes (owner, group, other, any),
// accepting either a single value or a sequence.
type ClassList []string

// UnmarshalYAML accepts a scalar string or a sequence of strings.
func (cl *ClassList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		*cl = []string{s}
	case yaml.SequenceNode:
		var ss []string
		if err := node.Decode(&ss); err != nil {
			return err
		}
		*cl = ss
	default:
		return fmt.Errorf("must be a string or list of strings")
	}
	return nil
}

// Load reads and parses a YAML config file. Relative paths in
// tare.runtime.binds and tare.runtime.env_file are resolved against the
// config file's directory, then validated for existence.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if err := resolveRuntimePaths(cfg, filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// resolveRuntimePaths converts relative bind host paths and env_file paths
// to absolute, anchored at baseDir, then verifies they exist.
func resolveRuntimePaths(cfg *Config, baseDir string) error {
	if cfg.Tare == nil || cfg.Tare.Runtime == nil {
		return nil
	}
	rt := cfg.Tare.Runtime

	for i, bind := range rt.Binds {
		host, rest, found := strings.Cut(bind, ":")
		if !found || host == "" {
			return fmt.Errorf("tare.runtime.binds[%d]: invalid bind %q (expected host:container[:opts])", i, bind)
		}
		resolved, err := resolvePath(host, baseDir)
		if err != nil {
			return fmt.Errorf("tare.runtime.binds[%d]: %w", i, err)
		}
		if _, err := os.Stat(resolved); err != nil {
			return fmt.Errorf("tare.runtime.binds[%d]: %w", i, err)
		}
		rt.Binds[i] = resolved + ":" + rest
	}

	if rt.EnvFile != "" {
		resolved, err := resolvePath(rt.EnvFile, baseDir)
		if err != nil {
			return fmt.Errorf("tare.runtime.env_file: %w", err)
		}
		if _, err := os.Stat(resolved); err != nil {
			return fmt.Errorf("tare.runtime.env_file: %w", err)
		}
		rt.EnvFile = resolved
	}
	return nil
}

// resolvePath returns p as an absolute path, anchoring relative paths at
// baseDir. baseDir itself is absolutized first, so a relative config-file
// argument still yields an absolute resolved path.
func resolvePath(p, baseDir string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// Parse parses YAML bytes into a Config.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.SchemaVersion == 0 {
		return nil, fmt.Errorf("schema_version is required")
	}
	if cfg.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported schema_version %d (supported: 1)", cfg.SchemaVersion)
	}
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Merge concatenates lists from multiple configs into one. RuntimeOptions
// is shallow-merged (last non-zero value wins per field).
func Merge(configs ...*Config) *Config {
	merged := &Config{SchemaVersion: 1}
	for _, c := range configs {
		if c == nil {
			continue
		}
		merged.Metadata = append(merged.Metadata, c.Metadata...)
		merged.Files = append(merged.Files, c.Files...)
		merged.Commands = append(merged.Commands, c.Commands...)
		merged.Scan = append(merged.Scan, c.Scan...)
		if c.Tare != nil {
			if merged.Tare == nil {
				merged.Tare = &TareConfig{}
			}
			if c.Tare.Runtime != nil {
				merged.Tare.Runtime = mergeRuntime(merged.Tare.Runtime, c.Tare.Runtime)
			}
		}
	}
	return merged
}

func mergeRuntime(a, b *RuntimeOptions) *RuntimeOptions {
	if a == nil {
		return b
	}
	if b.User != "" {
		a.User = b.User
	}
	if len(b.CapDrop) > 0 {
		a.CapDrop = b.CapDrop
	}
	if len(b.CapAdd) > 0 {
		a.CapAdd = b.CapAdd
	}
	if len(b.Binds) > 0 {
		a.Binds = b.Binds
	}
	if len(b.Env) > 0 {
		a.Env = b.Env
	}
	if b.EnvFile != "" {
		a.EnvFile = b.EnvFile
	}
	return a
}

func validate(cfg *Config) error {
	for i, m := range cfg.Metadata {
		if err := validateMetadata(i, &m); err != nil {
			return err
		}
	}
	for i, f := range cfg.Files {
		if err := validateFile(i, &f); err != nil {
			return err
		}
	}
	for i, c := range cfg.Commands {
		if err := validateCommand(i, &c); err != nil {
			return err
		}
	}
	for i, s := range cfg.Scan {
		if err := validateScan(i, &s); err != nil {
			return err
		}
	}
	return nil
}

func validateMetadata(i int, m *MetadataAssertion) error {
	tag := metadataTag(i, m.Name)
	for j, e := range m.Env {
		if e.Key == "" {
			return fmt.Errorf("%s: env[%d]: key is required", tag, j)
		}
	}
	for j, l := range m.Labels {
		if l.Key == "" {
			return fmt.Errorf("%s: labels[%d]: key is required", tag, j)
		}
	}
	return nil
}

func validateFile(i int, f *FileAssertion) error {
	tag := fileTag(i, f.Path)
	if f.Path == "" {
		return fmt.Errorf("files[%d]: path is required", i)
	}

	// present: false excludes all other assertions.
	if f.Present != nil && !*f.Present {
		if f.Type != "" || f.UID != nil || f.GID != nil || f.Permissions != "" ||
			len(f.ReadableBy) > 0 || len(f.WritableBy) > 0 || len(f.ExecutableBy) > 0 ||
			len(f.Contents) > 0 || f.Not != nil {
			return fmt.Errorf("%s: present: false excludes all other assertions", tag)
		}
		return nil
	}

	if f.Type != "" {
		if err := validateFileType(f.Type); err != nil {
			return fmt.Errorf("%s: %w", tag, err)
		}
	}
	if f.Permissions != "" {
		if err := validatePermissions(f.Permissions); err != nil {
			return fmt.Errorf("%s: %w", tag, err)
		}
	}
	if err := validateClasses("readable_by", f.ReadableBy); err != nil {
		return fmt.Errorf("%s: %w", tag, err)
	}
	if err := validateClasses("writable_by", f.WritableBy); err != nil {
		return fmt.Errorf("%s: %w", tag, err)
	}
	if err := validateClasses("executable_by", f.ExecutableBy); err != nil {
		return fmt.Errorf("%s: %w", tag, err)
	}

	if f.Not != nil {
		if f.Not.Type != "" {
			if err := validateFileType(f.Not.Type); err != nil {
				return fmt.Errorf("%s: not.%w", tag, err)
			}
		}
		if err := validateClasses("not.readable_by", f.Not.ReadableBy); err != nil {
			return fmt.Errorf("%s: %w", tag, err)
		}
		if err := validateClasses("not.writable_by", f.Not.WritableBy); err != nil {
			return fmt.Errorf("%s: %w", tag, err)
		}
		if err := validateClasses("not.executable_by", f.Not.ExecutableBy); err != nil {
			return fmt.Errorf("%s: %w", tag, err)
		}
	}
	return nil
}

func validateCommand(i int, c *CommandAssertion) error {
	tag := commandTag(i, c.Name)
	if c.Name == "" {
		return fmt.Errorf("commands[%d]: name is required", i)
	}
	if len(c.Run.Argv) == 0 {
		return fmt.Errorf("%s: run is required", tag)
	}
	for j, e := range c.Env {
		if e.Key == "" {
			return fmt.Errorf("%s: env[%d]: key is required", tag, j)
		}
	}
	return nil
}

func validateScan(i int, s *ScanEntry) error {
	tag := scanTag(i, s.Name, s.Path)
	if s.Path == "" {
		return fmt.Errorf("%s: path is required", tag)
	}
	if s.Limit < 0 {
		return fmt.Errorf("%s: limit must be non-negative", tag)
	}
	for _, ig := range s.Ignore {
		if _, err := scan.ParseIgnorePattern(ig); err != nil {
			return fmt.Errorf("%s: %w", tag, err)
		}
	}
	return nil
}

func validateFileType(t string) error {
	switch t {
	case "file", "dir", "symlink":
		return nil
	default:
		return fmt.Errorf("type must be file, dir, or symlink, got %q", t)
	}
}

// validatePermissions checks the permissions string is either an octal form
// (starts with a digit) or an rwx form (10 chars, starts with -, d, or l).
func validatePermissions(s string) error {
	if s == "" {
		return nil
	}
	if s[0] >= '0' && s[0] <= '9' {
		// Octal: accept "0755", "0o755", "755". Validate digits.
		body := s
		if strings.HasPrefix(body, "0o") || strings.HasPrefix(body, "0O") {
			body = body[2:]
		}
		if body == "" {
			return fmt.Errorf("permissions %q: empty octal value", s)
		}
		for _, ch := range body {
			if ch < '0' || ch > '7' {
				return fmt.Errorf("permissions %q: invalid octal digit %q", s, ch)
			}
		}
		return nil
	}
	// rwx form: must be 10 characters, leading -/d/l, then nine perm chars.
	if len(s) != 10 {
		return fmt.Errorf("permissions %q: rwx form must be 10 characters", s)
	}
	switch s[0] {
	case '-', 'd', 'l':
	default:
		return fmt.Errorf("permissions %q: rwx form must start with -, d, or l", s)
	}
	return nil
}

func validateClasses(field string, classes ClassList) error {
	for _, c := range classes {
		switch c {
		case "owner", "group", "other", "any":
		default:
			return fmt.Errorf("%s: invalid class %q (must be owner, group, other, or any)", field, c)
		}
	}
	return nil
}

func metadataTag(i int, name string) string {
	if name != "" {
		return fmt.Sprintf("metadata[%d] %q", i, name)
	}
	return fmt.Sprintf("metadata[%d]", i)
}

func fileTag(i int, path string) string {
	if path != "" {
		return fmt.Sprintf("files[%d] %q", i, path)
	}
	return fmt.Sprintf("files[%d]", i)
}

func commandTag(i int, name string) string {
	if name != "" {
		return fmt.Sprintf("commands[%d] %q", i, name)
	}
	return fmt.Sprintf("commands[%d]", i)
}

func scanTag(i int, name, path string) string {
	if name != "" {
		return fmt.Sprintf("scan[%d] %q", i, name)
	}
	if path != "" {
		return fmt.Sprintf("scan[%d] %q", i, path)
	}
	return fmt.Sprintf("scan[%d]", i)
}
