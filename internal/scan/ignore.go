package scan

import (
	"fmt"
	"path/filepath"
	"strings"
)

// IgnorePattern is a validated ignore pattern. Absolute paths match binary
// paths, bare filenames match library dependency names.
type IgnorePattern struct {
	Pattern string
	IsBinary bool // true = matches binary paths; false = matches dep names
}

// ParseIgnorePattern validates and classifies an ignore pattern.
// Absolute paths (starting with /) match binary paths.
// Bare filenames (no /) match library dependency names.
// Relative paths (containing / but not starting with /) are rejected.
//
// Patterns ending with /* are treated as prefix matches — they match all
// files under that directory recursively. Use /dir/* instead of /dir/**/*.
func ParseIgnorePattern(s string) (IgnorePattern, error) {
	if s == "" {
		return IgnorePattern{}, fmt.Errorf("empty ignore pattern")
	}
	if strings.Contains(s, "**") {
		return IgnorePattern{}, fmt.Errorf("invalid glob pattern %q: ** is not supported; use a /* suffix for recursive directory matching (e.g. /dir/*)", s)
	}
	if strings.HasPrefix(s, "/") {
		// Validate as a glob pattern.
		if _, err := filepath.Match(s, s); err != nil {
			return IgnorePattern{}, fmt.Errorf("invalid glob pattern %q: %w", s, err)
		}
		return IgnorePattern{Pattern: s, IsBinary: true}, nil
	}
	if strings.Contains(s, "/") {
		return IgnorePattern{}, fmt.Errorf("ignore pattern %q looks like a relative path; use an absolute path (starting with /) for binary paths or a bare filename for library names", s)
	}
	if _, err := filepath.Match(s, s); err != nil {
		return IgnorePattern{}, fmt.Errorf("invalid glob pattern %q: %w", s, err)
	}
	return IgnorePattern{Pattern: s, IsBinary: false}, nil
}

// matchesBinaryIgnore returns true if a binary path matches any binary ignore pattern.
// Patterns ending with /* are treated as prefix matches (recursive).
func matchesBinaryIgnore(path string, patterns []IgnorePattern) bool {
	for _, p := range patterns {
		if !p.IsBinary {
			continue
		}
		// Patterns ending with /* match all files under that directory.
		if strings.HasSuffix(p.Pattern, "/*") {
			prefix := p.Pattern[:len(p.Pattern)-1] // "/dir/" from "/dir/*"
			if strings.HasPrefix(path, prefix) {
				return true
			}
			continue
		}
		if matched, _ := filepath.Match(p.Pattern, path); matched {
			return true
		}
	}
	return false
}

// matchesDepIgnore returns true if a library name matches any dep ignore pattern.
func matchesDepIgnore(name string, patterns []IgnorePattern) bool {
	for _, p := range patterns {
		if p.IsBinary {
			continue
		}
		if matched, _ := filepath.Match(p.Pattern, name); matched {
			return true
		}
	}
	return false
}

// matchesWarningIgnore returns true if a warning path matches any binary ignore pattern.
func matchesWarningIgnore(path string, patterns []IgnorePattern) bool {
	return matchesBinaryIgnore(path, patterns)
}
