package scan

import "testing"

func TestParseIgnorePattern(t *testing.T) {
	tests := []struct {
		input    string
		wantErr  bool
		isBinary bool
	}{
		// Absolute paths → binary ignore.
		{"/usr/lib/libfoo.so", false, true},
		{"/opt/venv/lib/python3/site-packages/PIL/*.so", false, true},
		{"/app/lib/*.so.*", false, true},

		// Bare filenames → dep ignore.
		{"libgif.so.7", false, false},
		{"libc.so.*", false, false},
		{"*.so", false, false},

		// Prefix match (/* suffix).
		{"/opt/venv/*", false, true},

		// Relative paths → error.
		{"lib/libfoo.so", true, false},
		{"./libfoo.so", true, false},
		{"opt/venv/lib/*.so", true, false},

		// ** → error.
		{"/opt/venv/**/*.so", true, false},
		{"/app/**/lib", true, false},
		{"**.so", true, false},

		// Empty → error.
		{"", true, false},
	}
	for _, tt := range tests {
		p, err := ParseIgnorePattern(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseIgnorePattern(%q): expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseIgnorePattern(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if p.IsBinary != tt.isBinary {
			t.Errorf("ParseIgnorePattern(%q).IsBinary = %v, want %v", tt.input, p.IsBinary, tt.isBinary)
		}
	}
}

func TestMatchesBinaryIgnore(t *testing.T) {
	patterns := []IgnorePattern{
		{Pattern: "/opt/venv/lib/python3/site-packages/PIL/*.so", IsBinary: true},
		{Pattern: "/usr/lib/libfoo.so", IsBinary: true},
		// Prefix match: covers all descendants.
		{Pattern: "/opt/conda/*", IsBinary: true},
		// Dep pattern should not match binary paths.
		{Pattern: "libgif.so.7", IsBinary: false},
	}

	tests := []struct {
		path string
		want bool
	}{
		{"/opt/venv/lib/python3/site-packages/PIL/ImagingGif.so", true},
		{"/opt/venv/lib/python3/site-packages/PIL/ImagingJpeg.so", true},
		{"/usr/lib/libfoo.so", true},
		{"/usr/lib/libbar.so", false},
		{"/app/main", false},
		// Prefix match tests.
		{"/opt/conda/lib/libfoo.so", true},
		{"/opt/conda/lib/deep/nested/libbar.so", true},
		{"/opt/conda-alt/lib/libfoo.so", false},
	}
	for _, tt := range tests {
		if got := matchesBinaryIgnore(tt.path, patterns); got != tt.want {
			t.Errorf("matchesBinaryIgnore(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchesDepIgnore(t *testing.T) {
	patterns := []IgnorePattern{
		{Pattern: "libgif.so.*", IsBinary: false},
		{Pattern: "libwebp.so.7", IsBinary: false},
		// Binary pattern should not match dep names.
		{Pattern: "/usr/lib/libfoo.so", IsBinary: true},
	}

	tests := []struct {
		name string
		want bool
	}{
		{"libgif.so.7", true},
		{"libgif.so.8", true},
		{"libwebp.so.7", true},
		{"libwebp.so.8", false},
		{"libc.so.6", false},
	}
	for _, tt := range tests {
		if got := matchesDepIgnore(tt.name, patterns); got != tt.want {
			t.Errorf("matchesDepIgnore(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
