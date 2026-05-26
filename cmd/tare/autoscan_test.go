package main

import (
	"reflect"
	"testing"

	"github.com/DataDog/tare/internal/oci"
)

func TestParseEnvPaths(t *testing.T) {
	tests := []struct {
		name  string
		var_  string
		value string
		want  []string
	}{
		{
			name:  "pythonpath colon split",
			var_:  "PYTHONPATH",
			value: "/opt/venv/lib/python3.12:/opt/venv/lib/python3.12/site-packages",
			want:  []string{"/opt/venv/lib/python3.12", "/opt/venv/lib/python3.12/site-packages"},
		},
		{
			name:  "ld_library_path with empty entries",
			var_:  "LD_LIBRARY_PATH",
			value: "/usr/local/lib::/opt/lib:",
			want:  []string{"/usr/local/lib", "/opt/lib"},
		},
		{
			name:  "classpath glob expands to dir",
			var_:  "CLASSPATH",
			value: "/app/lib/*",
			want:  []string{"/app/lib"},
		},
		{
			name:  "classpath jar reduces to parent",
			var_:  "CLASSPATH",
			value: "/app/lib/foo.jar:/app/lib/bar.jar",
			want:  []string{"/app/lib", "/app/lib"},
		},
		{
			name:  "classpath plain dir kept",
			var_:  "CLASSPATH",
			value: "/app/classes:/app/lib/*",
			want:  []string{"/app/classes", "/app/lib"},
		},
		{
			name:  "classpath relative dot dropped later (kept by parse)",
			var_:  "CLASSPATH",
			value: ".:/app/classes",
			want:  []string{".", "/app/classes"},
		},
		{
			name:  "empty value",
			var_:  "PYTHONPATH",
			value: "",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEnvPaths(tt.var_, tt.value)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseEnvPaths(%q, %q) = %v, want %v", tt.var_, tt.value, got, tt.want)
			}
		})
	}
}

func TestDetectScanPaths(t *testing.T) {
	existsAll := func(string) bool { return true }
	existsNone := func(string) bool { return false }

	t.Run("entrypoint only", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Entrypoint: []string{"/app/bin/server"},
		}
		got := detectScanPaths(icfg, existsAll, false)
		want := []autoCandidate{
			{Path: "/app/bin", Source: "ENTRYPOINT", From: "/app/bin/server", Exists: true},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("cmd fallback", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Cmd: []string{"/usr/local/bin/myapp"},
		}
		got := detectScanPaths(icfg, existsAll, false)
		if len(got) != 1 || got[0].Source != "CMD" || got[0].Path != "/usr/local/bin" {
			t.Errorf("unexpected result: %+v", got)
		}
	})

	t.Run("entrypoint in system dir is skipped", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Entrypoint: []string{"/usr/bin/python"},
		}
		got := detectScanPaths(icfg, existsAll, false)
		if len(got) != 0 {
			t.Errorf("expected no candidates from /usr/bin entrypoint, got %+v", got)
		}
	})

	t.Run("relative entrypoint resolved against workdir", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Entrypoint: []string{"./bin/server"},
			WorkingDir: "/app",
		}
		got := detectScanPaths(icfg, existsAll, false)
		if len(got) != 1 || got[0].Path != "/app/bin" {
			t.Errorf("unexpected resolution: %+v", got)
		}
	})

	t.Run("env vars contribute paths", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Entrypoint: []string{"/app/bin/server"},
			Env: []string{
				"PYTHONPATH=/opt/venv/lib/python3.12:/opt/venv/lib/python3.12/site-packages",
				"LD_LIBRARY_PATH=/opt/extra/lib",
			},
		}
		got := detectScanPaths(icfg, existsAll, false)
		gotPaths := paths(got)
		wantPaths := []string{
			"/app/bin",
			"/opt/extra/lib",
			"/opt/venv/lib/python3.12",
			"/opt/venv/lib/python3.12/site-packages",
		}
		if !setEqual(gotPaths, wantPaths) {
			t.Errorf("got paths %v, want set %v", gotPaths, wantPaths)
		}
	})

	t.Run("env order follows envVars var list", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Env: []string{
				"PYTHONPATH=/p",
				"LD_LIBRARY_PATH=/l",
				"NODE_PATH=/n",
			},
		}
		got := detectScanPaths(icfg, existsAll, false)
		want := []string{"/l", "/p", "/n"} // LD_LIBRARY_PATH first per envVars order
		if !reflect.DeepEqual(paths(got), want) {
			t.Errorf("got %v, want %v", paths(got), want)
		}
	})

	t.Run("relative env entries are dropped", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Env: []string{"CLASSPATH=.:/app/lib/*:relative/dir"},
		}
		got := detectScanPaths(icfg, existsAll, false)
		if !reflect.DeepEqual(paths(got), []string{"/app/lib"}) {
			t.Errorf("expected only /app/lib, got %v", paths(got))
		}
	})

	t.Run("dedupe across sources", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Entrypoint: []string{"/app/bin/server"},
			Env:        []string{"LD_LIBRARY_PATH=/app/bin:/opt/lib"},
		}
		got := detectScanPaths(icfg, existsAll, false)
		if !reflect.DeepEqual(paths(got), []string{"/app/bin", "/opt/lib"}) {
			t.Errorf("expected /app/bin (from ENTRYPOINT) deduped from LD_LIBRARY_PATH, got %v", paths(got))
		}
		if got[0].Source != "ENTRYPOINT" {
			t.Errorf("first candidate should keep ENTRYPOINT source, got %q", got[0].Source)
		}
	})

	t.Run("missing paths flagged not removed", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Entrypoint: []string{"/app/bin/server"},
			Env:        []string{"PYTHONPATH=/opt/venv/lib"},
		}
		got := detectScanPaths(icfg, existsNone, false)
		if len(got) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(got))
		}
		for _, c := range got {
			if c.Exists {
				t.Errorf("expected Exists=false for %q, got true", c.Path)
			}
		}
	})

	t.Run("nil exists trusts all paths", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Entrypoint: []string{"/app/bin/server"},
		}
		got := detectScanPaths(icfg, nil, false)
		if len(got) != 1 || !got[0].Exists {
			t.Errorf("expected Exists=true with nil callback, got %+v", got)
		}
	})

	t.Run("verbose includes raw env value in From", func(t *testing.T) {
		icfg := &oci.ImageConfig{
			Env: []string{"PYTHONPATH=/opt/venv/lib"},
		}
		got := detectScanPaths(icfg, existsAll, true)
		if len(got) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(got))
		}
		if got[0].From != "PYTHONPATH=/opt/venv/lib" {
			t.Errorf("expected raw env in From, got %q", got[0].From)
		}
	})

	t.Run("nil image config returns nil", func(t *testing.T) {
		got := detectScanPaths(nil, existsAll, false)
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})
}

func TestClasspathToDir(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/app/lib/*", "/app/lib"},
		{"/app/lib/foo.jar", "/app/lib"},
		{"/app/classes", "/app/classes"},
		{"foo.jar", "."},
	}
	for _, tt := range tests {
		if got := classpathToDir(tt.in); got != tt.want {
			t.Errorf("classpathToDir(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// paths extracts the Path field from each candidate.
func paths(cs []autoCandidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Path
	}
	return out
}

func setEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	am := map[string]int{}
	for _, s := range a {
		am[s]++
	}
	for _, s := range b {
		am[s]--
	}
	for _, v := range am {
		if v != 0 {
			return false
		}
	}
	return true
}
