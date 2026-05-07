package config

import (
	"path/filepath"
	"testing"
)

// TestExamplesParse parses every check.yaml in _examples/ to catch
// schema drift between the documented examples and the parser.
func TestExamplesParse(t *testing.T) {
	matches, err := filepath.Glob("../../_examples/*/check.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Skip("no example configs found")
	}
	for _, path := range matches {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			if _, err := Load(path); err != nil {
				t.Errorf("loading %s: %v", path, err)
			}
		})
	}
}
