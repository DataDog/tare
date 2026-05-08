package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMinimal(t *testing.T) {
	cfg, err := Parse([]byte(`schema_version: 1`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", cfg.SchemaVersion)
	}
}

func TestParseMissingSchemaVersion(t *testing.T) {
	_, err := Parse([]byte(`metadata: []`))
	if err == nil {
		t.Fatal("expected error for missing schema_version")
	}
}

func TestParseUnsupportedSchemaVersion(t *testing.T) {
	_, err := Parse([]byte(`schema_version: 2`))
	if err == nil {
		t.Fatal("expected error for unsupported schema_version")
	}
}

func TestParseRunStringForm(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
commands:
  - name: short
    run: /app/bin/server
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := cfg.Commands[0].Run.Argv
	if len(got) != 1 || got[0] != "/app/bin/server" {
		t.Errorf("argv = %v, want [/app/bin/server]", got)
	}
}

func TestParseRunListForm(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
commands:
  - name: list
    run: ["/app/bin/server", "--version"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := cfg.Commands[0].Run.Argv
	if len(got) != 2 || got[0] != "/app/bin/server" || got[1] != "--version" {
		t.Errorf("argv = %v", got)
	}
}

func TestParseRunRequired(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
commands:
  - name: missing
`))
	if err == nil {
		t.Fatal("expected error for missing run")
	}
}

func TestParsePatternLiteral(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
files:
  - path: /etc/timezone
    contents: ["UTC"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := cfg.Files[0].Contents.Patterns[0]
	if p.Value != "UTC" || p.Match {
		t.Errorf("got %+v, want literal UTC", p)
	}
}

func TestParsePatternMatchMap(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
files:
  - path: /app/version
    contents:
      - "Copyright"
      - {match: "v\\d+\\.\\d+"}
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	patterns := cfg.Files[0].Contents.Patterns
	if len(patterns) != 2 {
		t.Fatalf("got %d patterns, want 2", len(patterns))
	}
	if patterns[0].Match {
		t.Error("patterns[0] should be literal")
	}
	if !patterns[1].Match {
		t.Error("patterns[1] should be match form")
	}
	if patterns[1].Value != `v\d+\.\d+` {
		t.Errorf("patterns[1].Value = %q", patterns[1].Value)
	}
}

func TestParsePatternMatchEmpty(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
files:
  - path: /a
    contents:
      - {match: ""}
`))
	if err == nil {
		t.Fatal("expected error for empty match")
	}
}

func TestParsePatternListEmptyForm(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
files:
  - path: /var/log/quiet
    contents: { empty: true }
commands:
  - name: silent
    run: ["/bin/silent"]
    stdout: { empty: true }
    stderr: { empty: true }
  - name: chatty
    run: ["/bin/chatty"]
    not:
      stdout: { empty: true }
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.Files[0].Contents.Empty {
		t.Error("Files[0].Contents.Empty should be true")
	}
	if !cfg.Commands[0].Stdout.Empty {
		t.Error("Commands[0].Stdout.Empty (positive) should be true")
	}
	if !cfg.Commands[1].Not.Stdout.Empty {
		t.Error("Commands[1].Not.Stdout.Empty (negative) should be true")
	}
}

func TestParsePatternListEmptyFalseRejected(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
commands:
  - name: foo
    run: ["/bin/foo"]
    stdout: { empty: false }
`))
	if err == nil {
		t.Fatal("expected error for {empty: false}")
	}
}

func TestParsePatternListUnknownOptionRejected(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
commands:
  - name: foo
    run: ["/bin/foo"]
    stdout: { empyt: true }
`))
	if err == nil {
		t.Fatal("expected error for unknown option key")
	}
}

func TestParseCommandHarnessFalse(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
commands:
  - name: uses image tools
    run: ["sh", "-c", "df --local -P"]
    harness: false
  - name: uses harness tools
    run: ["du", "-s", "/app"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Commands[0].Harness == nil || *cfg.Commands[0].Harness {
		t.Errorf("Commands[0].Harness = %v, want explicit false", cfg.Commands[0].Harness)
	}
	if cfg.Commands[1].Harness != nil {
		t.Errorf("Commands[1].Harness = %v, want nil (default)", cfg.Commands[1].Harness)
	}
}

func TestValidateContradictoryEmptyAssertions(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
commands:
  - name: contradictory
    run: ["/bin/foo"]
    stdout: { empty: true }
    not:
      stdout: { empty: true }
`))
	if err == nil {
		t.Fatal("expected error for contradictory empty assertions")
	}
}

func TestParseClassListScalar(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
files:
  - path: /bin/server
    executable_by: any
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := cfg.Files[0].ExecutableBy
	if len(got) != 1 || got[0] != "any" {
		t.Errorf("got %v, want [any]", got)
	}
}

func TestParseClassListSequence(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
files:
  - path: /bin/server
    executable_by: [owner, group]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := cfg.Files[0].ExecutableBy
	if len(got) != 2 || got[0] != "owner" || got[1] != "group" {
		t.Errorf("got %v", got)
	}
}

func TestValidateInvalidClass(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
files:
  - path: /a
    readable_by: world
`))
	if err == nil {
		t.Fatal("expected error for invalid class")
	}
}

func TestParsePresentFalse(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
files:
  - path: /bin/sh
    present: false
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Files[0].Present == nil || *cfg.Files[0].Present {
		t.Errorf("want present=false")
	}
}

func TestValidatePresentFalseExcludesOthers(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
files:
  - path: /bin/sh
    present: false
    permissions: "0755"
`))
	if err == nil {
		t.Fatal("expected error: present:false excludes other assertions")
	}
}

func TestValidateFileType(t *testing.T) {
	cfg, err := Parse([]byte(`
schema_version: 1
files:
  - path: /tmp
    type: dir
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Files[0].Type != "dir" {
		t.Errorf("type = %q", cfg.Files[0].Type)
	}
}

func TestValidateFileTypeInvalid(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
files:
  - path: /a
    type: regular
`))
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestValidatePermissionsOctal(t *testing.T) {
	cases := []string{`"0755"`, `"0o755"`, `"755"`, `"0600"`}
	for _, p := range cases {
		_, err := Parse([]byte(`
schema_version: 1
files:
  - path: /a
    permissions: ` + p + `
`))
		if err != nil {
			t.Errorf("permissions %s: %v", p, err)
		}
	}
}

func TestValidatePermissionsRwx(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
files:
  - path: /a
    permissions: "-rwxr-xr-x"
`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidatePermissionsInvalid(t *testing.T) {
	cases := []string{`"abc"`, `"08"`, `"abcdefghij"`, `"0o9"`}
	for _, p := range cases {
		_, err := Parse([]byte(`
schema_version: 1
files:
  - path: /a
    permissions: ` + p + `
`))
		if err == nil {
			t.Errorf("permissions %s: expected error", p)
		}
	}
}

func TestValidateMetadataEnvKey(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
metadata:
  - env:
      - value: bar
`))
	if err == nil {
		t.Fatal("expected error for missing env key")
	}
}

func TestValidateCommandMissingName(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
commands:
  - run: echo
`))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestValidateScanMissingPath(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
scan:
  - name: app
`))
	if err == nil {
		t.Fatal("expected error for missing scan path")
	}
}

func TestValidateScanIgnorePattern(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1
scan:
  - path: /app
    ignore: ["relative/path"]
`))
	if err == nil {
		t.Fatal("expected error for relative ignore path")
	}
}

func TestValidateFullConfig(t *testing.T) {
	_, err := Parse([]byte(`
schema_version: 1

metadata:
  - name: prod
    user: app
    env:
      - key: APP_ENV
        value: production

files:
  - path: /etc/timezone
    contents: ["UTC"]
  - path: /bin/sh
    present: false
  - path: /tmp
    type: dir
    writable_by: any

commands:
  - name: version
    run: ["/app/server", "--version"]
    exit: 0
    stdout: ["v1"]

scan:
  - path: /app

tare:
  runtime:
    user: "1000:1000"
    cap_drop: [ALL]
    binds: ["/host:/container:ro"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestLoadResolvesBindRelativeToConfig(t *testing.T) {
	dir := t.TempDir()
	fixturesDir := filepath.Join(dir, "fixtures")
	if err := os.MkdirAll(fixturesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "check.yaml")
	if err := os.WriteFile(configPath, []byte(`
schema_version: 1
tare:
  runtime:
    binds:
      - "fixtures:/etc/fixtures:ro"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Tare.Runtime.Binds[0]
	want := fixturesDir + ":/etc/fixtures:ro"
	if got != want {
		t.Errorf("bind = %q, want %q", got, want)
	}
}

func TestLoadResolvesBindFromSubdir(t *testing.T) {
	// Common case: config in apps/myapp/check.yaml mounting ../../fixtures.
	dir := t.TempDir()
	fixturesDir := filepath.Join(dir, "fixtures")
	if err := os.MkdirAll(fixturesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(dir, "apps", "myapp")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(subdir, "check.yaml")
	if err := os.WriteFile(configPath, []byte(`
schema_version: 1
tare:
  runtime:
    binds:
      - "../../fixtures:/etc/fixtures"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Tare.Runtime.Binds[0]
	want := fixturesDir + ":/etc/fixtures"
	if got != want {
		t.Errorf("bind = %q, want %q", got, want)
	}
}

func TestLoadRelativeConfigPathYieldsAbsoluteBind(t *testing.T) {
	// Regression: when the user passes a relative config path on the
	// command line (e.g. `tare check -i img _examples/foo/check.yaml`),
	// the resolved bind path must be absolute — docker rejects relative
	// host paths as "named volumes" with cryptic errors.
	dir := t.TempDir()
	fixturesDir := filepath.Join(dir, "fixtures")
	if err := os.MkdirAll(fixturesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "check.yaml")
	if err := os.WriteFile(configPath, []byte(`
schema_version: 1
tare:
  runtime:
    binds:
      - "fixtures:/etc/fixtures:ro"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Chdir to the temp dir's parent and pass a relative path to Load.
	t.Chdir(filepath.Dir(dir))
	rel := filepath.Join(filepath.Base(dir), "check.yaml")

	cfg, err := Load(rel)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Tare.Runtime.Binds[0]
	host, _, _ := strings.Cut(got, ":")
	if !filepath.IsAbs(host) {
		t.Errorf("bind host path must be absolute, got %q", host)
	}
}

func TestLoadAbsoluteBindPathPreserved(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "check.yaml")
	// Use the temp dir itself as the absolute bind so it exists.
	if err := os.WriteFile(configPath, []byte(`
schema_version: 1
tare:
  runtime:
    binds:
      - "`+dir+`:/etc/host"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Tare.Runtime.Binds[0]
	want := dir + ":/etc/host"
	if got != want {
		t.Errorf("bind = %q, want %q", got, want)
	}
}

func TestLoadFailsOnMissingBindPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "check.yaml")
	if err := os.WriteFile(configPath, []byte(`
schema_version: 1
tare:
  runtime:
    binds:
      - "missing:/etc/missing"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil {
		t.Fatal("expected error for nonexistent bind path")
	}
}

func TestLoadResolvesEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "test.env")
	if err := os.WriteFile(envPath, []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "check.yaml")
	if err := os.WriteFile(configPath, []byte(`
schema_version: 1
tare:
  runtime:
    env_file: test.env
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tare.Runtime.EnvFile != envPath {
		t.Errorf("env_file = %q, want %q", cfg.Tare.Runtime.EnvFile, envPath)
	}
}

func TestLoadFailsOnMissingEnvFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "check.yaml")
	if err := os.WriteFile(configPath, []byte(`
schema_version: 1
tare:
  runtime:
    env_file: missing.env
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil {
		t.Fatal("expected error for nonexistent env_file")
	}
}

func TestLoadInvalidBindFormat(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "check.yaml")
	if err := os.WriteFile(configPath, []byte(`
schema_version: 1
tare:
  runtime:
    binds:
      - "no-colon"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil {
		t.Fatal("expected error for bind without colon")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(`
schema_version: 1
commands:
  - name: hello
    run: echo
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Commands) != 1 {
		t.Errorf("got %d commands", len(cfg.Commands))
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/nonexistent/path.yaml"); err == nil {
		t.Fatal("expected error")
	}
}

func TestMergeConcatenates(t *testing.T) {
	a := &Config{
		SchemaVersion: 1,
		Files:         []FileAssertion{{Path: "/a"}},
		Metadata:      []MetadataAssertion{{User: "first"}},
	}
	b := &Config{
		SchemaVersion: 1,
		Files:         []FileAssertion{{Path: "/b"}},
		Metadata:      []MetadataAssertion{{User: "second"}},
		Scan:          []ScanEntry{{Path: "/scan"}},
	}

	merged := Merge(a, b)
	if len(merged.Files) != 2 {
		t.Errorf("files = %d, want 2", len(merged.Files))
	}
	if len(merged.Metadata) != 2 {
		t.Errorf("metadata = %d, want 2 (no last-wins)", len(merged.Metadata))
	}
	if len(merged.Scan) != 1 {
		t.Errorf("scan = %d, want 1", len(merged.Scan))
	}
}

func TestMergeRuntimeOverlay(t *testing.T) {
	a := &Config{
		SchemaVersion: 1,
		Tare: &TareConfig{Runtime: &RuntimeOptions{
			User:    "first",
			CapDrop: []string{"ALL"},
		}},
	}
	b := &Config{
		SchemaVersion: 1,
		Tare: &TareConfig{Runtime: &RuntimeOptions{
			User: "second",
		}},
	}

	merged := Merge(a, b)
	rt := merged.Tare.Runtime
	if rt.User != "second" {
		t.Errorf("user = %q, want second", rt.User)
	}
	if len(rt.CapDrop) != 1 || rt.CapDrop[0] != "ALL" {
		t.Errorf("cap_drop = %v, want preserved from first", rt.CapDrop)
	}
}

func TestMergeNilConfig(t *testing.T) {
	a := &Config{
		SchemaVersion: 1,
		Commands:      []CommandAssertion{{Name: "a", Run: Run{Argv: []string{"echo"}}}},
	}
	merged := Merge(a, nil)
	if len(merged.Commands) != 1 {
		t.Errorf("got %d commands", len(merged.Commands))
	}
}
