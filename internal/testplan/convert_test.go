package testplan

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/tare/internal/config"
)

var update = flag.Bool("update", false, "update golden files")

func TestGoldenFiles(t *testing.T) {
	yamls, err := filepath.Glob(filepath.Join("testdata", "golden", "*.yaml"))
	if err != nil {
		t.Fatalf("globbing golden files: %v", err)
	}
	if len(yamls) == 0 {
		t.Fatal("no golden files found in testdata/golden/")
	}

	for _, yamlPath := range yamls {
		name := strings.TrimSuffix(filepath.Base(yamlPath), ".yaml")
		jsonPath := filepath.Join(filepath.Dir(yamlPath), name+".json")

		t.Run(name, func(t *testing.T) {
			cfg, err := config.Load(yamlPath)
			if err != nil {
				t.Fatalf("loading config: %v", err)
			}

			plan := FromConfig(cfg)
			got, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				t.Fatalf("marshaling plan: %v", err)
			}

			if *update {
				if err := os.WriteFile(jsonPath, got, 0o644); err != nil {
					t.Fatalf("updating golden file: %v", err)
				}
				return
			}

			want, err := os.ReadFile(jsonPath)
			if err != nil {
				t.Fatalf("reading golden file: %v", err)
			}

			if string(got) != string(want) {
				t.Errorf("plan mismatch for %s\n\ngot:\n%s\nwant:\n%s", name, got, want)
			}
		})
	}
}

func TestMetadataFanOut(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Metadata: []config.MetadataAssertion{
			{User: "app", Workdir: "/app"},
		},
	}

	plan := FromConfig(cfg)
	if len(plan.Tests) != 2 {
		t.Fatalf("expected 2 tests, got %d", len(plan.Tests))
	}
	if plan.Tests[0].Type != TypeMetadata || plan.Tests[0].Metadata.Field != "user" {
		t.Error("expected first test to be metadata user")
	}
	if plan.Tests[1].Type != TypeMetadata || plan.Tests[1].Metadata.Field != "workdir" {
		t.Error("expected second test to be metadata workdir")
	}
}

func TestMetadataNegation(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Metadata: []config.MetadataAssertion{
			{Not: &config.MetadataNot{Env: []string{"DEBUG"}}},
		},
	}

	plan := FromConfig(cfg)
	if len(plan.Tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(plan.Tests))
	}
	m := plan.Tests[0].Metadata
	if m.Field != "env" || m.Key != "DEBUG" || !m.Negate {
		t.Errorf("got %+v", m)
	}
}

func TestMetadataMultipleEntriesCompose(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Metadata: []config.MetadataAssertion{
			{User: "app"},
			{Workdir: "/app"},
		},
	}

	plan := FromConfig(cfg)
	if len(plan.Tests) != 2 {
		t.Fatalf("expected 2 tests, got %d", len(plan.Tests))
	}
}

func TestFileWithContents(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Files: []config.FileAssertion{
			{
				Path:     "/etc/timezone",
				Contents: []config.Pattern{{Value: "UTC"}},
			},
		},
	}

	plan := FromConfig(cfg)
	if len(plan.Tests) != 2 {
		t.Fatalf("expected 2 tests (existence + content), got %d", len(plan.Tests))
	}
	if plan.Tests[0].Type != TypeFileExistence {
		t.Errorf("first test type = %s", plan.Tests[0].Type)
	}
	if plan.Tests[1].Type != TypeFileContent {
		t.Errorf("second test type = %s", plan.Tests[1].Type)
	}
}

func TestFilePresentFalseSkipsContents(t *testing.T) {
	pf := false
	cfg := &config.Config{
		SchemaVersion: 1,
		Files: []config.FileAssertion{
			{Path: "/bin/sh", Present: &pf},
		},
	}

	plan := FromConfig(cfg)
	if len(plan.Tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(plan.Tests))
	}
	if plan.Tests[0].FileExistence.ShouldExist {
		t.Error("expected ShouldExist=false")
	}
}

func TestPatternLiteralEscaping(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Files: []config.FileAssertion{
			{
				Path:     "/etc/version",
				Contents: []config.Pattern{{Value: "v1.2.3"}},
			},
		},
	}

	plan := FromConfig(cfg)
	content := plan.Tests[1].FileContent
	got := content.ExpectedContents[0]
	if got != `v1\.2\.3` {
		t.Errorf("literal pattern not escaped: got %q, want %q", got, `v1\.2\.3`)
	}
}

func TestPatternRegexFlagBypassesEscape(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Files: []config.FileAssertion{
			{
				Path:     "/etc/version",
				Contents: []config.Pattern{{Value: `v\d+\.\d+`}},
				Regex:    true,
			},
		},
	}

	plan := FromConfig(cfg)
	got := plan.Tests[1].FileContent.ExpectedContents[0]
	if got != `v\d+\.\d+` {
		t.Errorf("regex pattern was escaped: got %q", got)
	}
}

func TestPatternMatchFormBypassesEscape(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Files: []config.FileAssertion{
			{
				Path: "/etc/version",
				Contents: []config.Pattern{
					{Value: "literal.string"},
					{Value: `v\d+`, Match: true},
				},
			},
		},
	}

	plan := FromConfig(cfg)
	got := plan.Tests[1].FileContent.ExpectedContents
	if got[0] != `literal\.string` {
		t.Errorf("literal not escaped: got %q", got[0])
	}
	if got[1] != `v\d+` {
		t.Errorf("match form was escaped: got %q", got[1])
	}
}

func TestCommandRunOneElement(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Commands: []config.CommandAssertion{
			{Name: "short", Run: config.Run{Argv: []string{"/app/server"}}},
		},
	}

	plan := FromConfig(cfg)
	c := plan.Tests[0].Command
	if c.Command != "/app/server" || len(c.Args) != 0 {
		t.Errorf("got command=%q args=%v", c.Command, c.Args)
	}
}

func TestCommandRunMultiElement(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Commands: []config.CommandAssertion{
			{Name: "long", Run: config.Run{Argv: []string{"/app/server", "--version"}}},
		},
	}

	plan := FromConfig(cfg)
	c := plan.Tests[0].Command
	if c.Command != "/app/server" || len(c.Args) != 1 || c.Args[0] != "--version" {
		t.Errorf("got command=%q args=%v", c.Command, c.Args)
	}
}

func TestCommandEnvWired(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Commands: []config.CommandAssertion{
			{
				Name: "with env",
				Run:  config.Run{Argv: []string{"echo"}},
				Env:  []config.KV{{Key: "FOO", Value: "bar"}},
			},
		},
	}

	plan := FromConfig(cfg)
	env := plan.Tests[0].Command.Env
	if len(env) != 1 || env[0].Key != "FOO" || env[0].Value != "bar" {
		t.Errorf("env = %+v", env)
	}
}

func TestNameDedup(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Files: []config.FileAssertion{
			{Path: "/a"},
			{Path: "/a"},
		},
	}

	plan := FromConfig(cfg)
	if len(plan.Tests) != 2 {
		t.Fatalf("expected 2 tests, got %d", len(plan.Tests))
	}
	if plan.Tests[0].Name != "file: /a" {
		t.Errorf("first test name = %q", plan.Tests[0].Name)
	}
	if plan.Tests[1].Name != "file: /a (2)" {
		t.Errorf("second test name = %q, want deduped", plan.Tests[1].Name)
	}
}

func TestRoundTrip(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: 1,
		Commands: []config.CommandAssertion{
			{Name: "hello", Run: config.Run{Argv: []string{"echo", "hello"}}},
		},
	}

	plan := FromConfig(cfg)
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var plan2 Plan
	if err := json.Unmarshal(data, &plan2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(plan2.Tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(plan2.Tests))
	}
	if plan2.Tests[0].Command.Command != "echo" {
		t.Errorf("command = %q", plan2.Tests[0].Command.Command)
	}
}
