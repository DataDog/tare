package testplan

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/DataDog/tare/internal/config"
	"github.com/DataDog/tare/internal/container"
)

// FromConfig converts a parsed YAML config into a JSON test plan.
func FromConfig(cfg *config.Config) *Plan {
	p := &Plan{
		MetadataFile: container.HarnessPrefix + "/meta.json",
	}
	names := &nameDedup{}

	for _, m := range cfg.Metadata {
		appendMetadata(p, &m, names)
	}
	for _, f := range cfg.Files {
		appendFile(p, &f, names)
	}
	for i, s := range cfg.Scan {
		name := s.Name
		if name == "" {
			name = s.Path
		}
		p.Tests = append(p.Tests, Test{
			Name: names.unique("scan: " + name),
			Type: TypeScan,
			Scan: &ScanSpec{
				Path:      s.Path,
				Ignore:    s.Ignore,
				Limit:     s.Limit,
				NoRuntime: i > 0,
			},
		})
	}
	for _, c := range cfg.Commands {
		appendCommand(p, &c, names)
	}
	return p
}

func appendMetadata(p *Plan, m *config.MetadataAssertion, names *nameDedup) {
	prefix := metadataPrefix(m.Name)

	if m.User != "" {
		p.Tests = append(p.Tests, Test{
			Name:     names.unique(prefix + "user is " + m.User),
			Type:     TypeMetadata,
			Metadata: &MetadataSpec{Field: "user", Expected: m.User},
		})
	}
	if m.Workdir != "" {
		p.Tests = append(p.Tests, Test{
			Name:     names.unique(prefix + "workdir is " + m.Workdir),
			Type:     TypeMetadata,
			Metadata: &MetadataSpec{Field: "workdir", Expected: m.Workdir},
		})
	}
	if len(m.Entrypoint) > 0 {
		p.Tests = append(p.Tests, Test{
			Name:     names.unique(prefix + "entrypoint"),
			Type:     TypeMetadata,
			Metadata: &MetadataSpec{Field: "entrypoint", Expected: toJSONArray(m.Entrypoint)},
		})
	}
	if m.Cmd != nil {
		p.Tests = append(p.Tests, Test{
			Name:     names.unique(prefix + "cmd"),
			Type:     TypeMetadata,
			Metadata: &MetadataSpec{Field: "cmd", Expected: toJSONArray(m.Cmd)},
		})
	}
	for _, env := range m.Env {
		p.Tests = append(p.Tests, Test{
			Name: names.unique(prefix + "env " + env.Key),
			Type: TypeMetadata,
			Metadata: &MetadataSpec{
				Field:    "env",
				Key:      env.Key,
				Expected: env.Value,
				IsRegex:  env.Regex,
			},
		})
	}
	for _, label := range m.Labels {
		p.Tests = append(p.Tests, Test{
			Name: names.unique(prefix + "label " + label.Key),
			Type: TypeMetadata,
			Metadata: &MetadataSpec{
				Field:    "label",
				Key:      label.Key,
				Expected: label.Value,
				IsRegex:  label.Regex,
			},
		})
	}
	for _, port := range m.Ports {
		p.Tests = append(p.Tests, Test{
			Name:     names.unique(prefix + "port " + port),
			Type:     TypeMetadata,
			Metadata: &MetadataSpec{Field: "port", Key: port},
		})
	}
	for _, vol := range m.Volumes {
		p.Tests = append(p.Tests, Test{
			Name:     names.unique(prefix + "volume " + vol),
			Type:     TypeMetadata,
			Metadata: &MetadataSpec{Field: "volume", Key: vol},
		})
	}

	if m.Not != nil {
		for _, key := range m.Not.Env {
			p.Tests = append(p.Tests, Test{
				Name:     names.unique(prefix + "env " + key + " absent"),
				Type:     TypeMetadata,
				Metadata: &MetadataSpec{Field: "env", Key: key, Negate: true},
			})
		}
		for _, key := range m.Not.Labels {
			p.Tests = append(p.Tests, Test{
				Name:     names.unique(prefix + "label " + key + " absent"),
				Type:     TypeMetadata,
				Metadata: &MetadataSpec{Field: "label", Key: key, Negate: true},
			})
		}
		for _, port := range m.Not.Ports {
			p.Tests = append(p.Tests, Test{
				Name:     names.unique(prefix + "port " + port + " absent"),
				Type:     TypeMetadata,
				Metadata: &MetadataSpec{Field: "port", Key: port, Negate: true},
			})
		}
		for _, vol := range m.Not.Volumes {
			p.Tests = append(p.Tests, Test{
				Name:     names.unique(prefix + "volume " + vol + " absent"),
				Type:     TypeMetadata,
				Metadata: &MetadataSpec{Field: "volume", Key: vol, Negate: true},
			})
		}
	}
}

func metadataPrefix(name string) string {
	if name != "" {
		return "metadata (" + name + "): "
	}
	return "metadata: "
}

func appendFile(p *Plan, f *config.FileAssertion, names *nameDedup) {
	shouldExist := f.Present == nil || *f.Present
	tag := "file: " + f.Path

	exists := &FileExistenceSpec{
		Path:        f.Path,
		ShouldExist: shouldExist,
	}

	if shouldExist {
		exists.Type = f.Type
		exists.UID = f.UID
		exists.GID = f.GID
		exists.Permissions = f.Permissions
		exists.ReadableBy = []string(f.ReadableBy)
		exists.WritableBy = []string(f.WritableBy)
		exists.ExecutableBy = []string(f.ExecutableBy)
		if f.Not != nil {
			exists.NotType = f.Not.Type
			exists.NotReadableBy = []string(f.Not.ReadableBy)
			exists.NotWritableBy = []string(f.Not.WritableBy)
			exists.NotExecutableBy = []string(f.Not.ExecutableBy)
		}
	}

	p.Tests = append(p.Tests, Test{
		Name:          names.unique(tag),
		Type:          TypeFileExistence,
		FileExistence: exists,
	})

	if !shouldExist {
		return
	}

	// Content assertions become a separate file content test entry so the
	// runner can distinguish "file exists with right perms" from "file
	// exists with right contents."
	expected := patternsToRegex(f.Contents, f.Regex)
	var excluded []string
	if f.Not != nil {
		excluded = patternsToRegex(f.Not.Contents, f.Regex)
	}
	if len(expected) > 0 || len(excluded) > 0 {
		p.Tests = append(p.Tests, Test{
			Name: names.unique("file content: " + f.Path),
			Type: TypeFileContent,
			FileContent: &FileContentSpec{
				Path:             f.Path,
				ExpectedContents: expected,
				ExcludedContents: excluded,
			},
		})
	}
}

func appendCommand(p *Plan, c *config.CommandAssertion, names *nameDedup) {
	exit := 0
	if c.Exit != nil {
		exit = *c.Exit
	}
	expectedOut := patternsToRegex(c.Stdout, c.Regex)
	expectedErr := patternsToRegex(c.Stderr, c.Regex)
	var excludedOut, excludedErr []string
	if c.Not != nil {
		excludedOut = patternsToRegex(c.Not.Stdout, c.Regex)
		excludedErr = patternsToRegex(c.Not.Stderr, c.Regex)
	}

	var env []KV
	for _, e := range c.Env {
		env = append(env, KV{Key: e.Key, Value: e.Value})
	}

	cmd := c.Run.Argv[0]
	args := c.Run.Argv[1:]

	p.Tests = append(p.Tests, Test{
		Name: names.unique("command: " + c.Name),
		Type: TypeCommand,
		Command: &CommandSpec{
			Command:        cmd,
			Args:           args,
			Env:            env,
			ExitCode:       exit,
			ExpectedOutput: expectedOut,
			ExcludedOutput: excludedOut,
			ExpectedError:  expectedErr,
			ExcludedError:  excludedErr,
			Setup:          c.Setup,
			Teardown:       c.Teardown,
		},
	})
}

// patternsToRegex converts a list of config.Pattern entries into regex
// strings the runner can pass to regexp.Compile. Bare-string entries are
// either escaped (when regex flag is unset) or passed through (when set).
// {match: "..."} entries are always passed through unescaped.
func patternsToRegex(patterns []config.Pattern, regex bool) []string {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]string, len(patterns))
	for i, p := range patterns {
		if p.Match || regex {
			out[i] = p.Value
		} else {
			out[i] = regexp.QuoteMeta(p.Value)
		}
	}
	return out
}

func toJSONArray(ss []string) string {
	b, _ := json.Marshal(ss)
	return string(b)
}

// nameDedup tracks test names and appends a suffix on collision.
type nameDedup struct {
	seen map[string]int
}

func (d *nameDedup) unique(name string) string {
	if d.seen == nil {
		d.seen = make(map[string]int)
	}
	d.seen[name]++
	if d.seen[name] == 1 {
		return name
	}
	return fmt.Sprintf("%s (%d)", name, d.seen[name])
}
