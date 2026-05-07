package main

import (
	"flag"
	"fmt"
	"runtime"
	"strings"

	"github.com/DataDog/tare/internal/config"
	"github.com/DataDog/tare/internal/container"
	"github.com/DataDog/tare/internal/oci"
	"github.com/DataDog/tare/internal/scan"
)

// repeatedFlag collects repeated flag values.
type repeatedFlag []string

func (f *repeatedFlag) String() string { return strings.Join(*f, ", ") }
func (f *repeatedFlag) Set(val string) error {
	*f = append(*f, val)
	return nil
}

// sessionFlags are the flags shared between tare check and tare scan —
// everything needed to create and manage a container session.
type sessionFlags struct {
	image       string
	runtimeBin  string
	platform    string
	harnessPath string
	pull        string
	noCleanup   bool
	verbose     bool
}

// loadConfigs loads and merges config files. Returns an empty config if
// no files are provided.
func loadConfigs(files []string) (*config.Config, error) {
	if len(files) == 0 {
		return &config.Config{SchemaVersion: 1}, nil
	}
	var configs []*config.Config
	for _, path := range files {
		c, err := config.Load(path)
		if err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	return config.Merge(configs...), nil
}

// mergeScanFlags merges CLI scan flags into each scan entry in the config.
// paths are appended as new entries. ignore patterns and limit are merged
// into every entry (CLI acts as a default for all entries).
func mergeScanFlags(cfg *config.Config, paths []string, ignore []string, limit int) {
	for _, p := range paths {
		cfg.Scan = append(cfg.Scan, config.ScanEntry{Path: p})
	}
	for i := range cfg.Scan {
		cfg.Scan[i].Ignore = append(cfg.Scan[i].Ignore, ignore...)
		if limit > 0 && cfg.Scan[i].Limit == 0 {
			cfg.Scan[i].Limit = limit
		}
	}
}

// ensureDefaultScanPath detects a scan path from the image config if no
// scan entries exist in the config. Does nothing if paths are already set.
func ensureDefaultScanPath(cfg *config.Config, icfg *oci.ImageConfig) {
	if len(cfg.Scan) > 0 {
		return
	}
	path := detectScanPath(icfg)
	if path == "" {
		return
	}
	cfg.Scan = append(cfg.Scan, config.ScanEntry{Path: path})
}

// applyRuntimeOpts copies tare.runtime fields into SessionOpts. No-op if
// the config has no runtime block.
func applyRuntimeOpts(opts *container.SessionOpts, cfg *config.Config) {
	if cfg.Tare == nil || cfg.Tare.Runtime == nil {
		return
	}
	rt := cfg.Tare.Runtime
	opts.User = rt.User
	opts.CapDrop = rt.CapDrop
	opts.CapAdd = rt.CapAdd
	opts.Binds = rt.Binds
	opts.EnvFile = rt.EnvFile
	if len(rt.Env) > 0 {
		opts.Env = make([]string, len(rt.Env))
		for i, e := range rt.Env {
			opts.Env[i] = e.Key + "=" + e.Value
		}
	}
}

// hasRuntimeOpts reports whether any tare.runtime field is set.
func hasRuntimeOpts(cfg *config.Config) bool {
	return cfg.Tare != nil && cfg.Tare.Runtime != nil
}

// validateIgnorePatterns validates scan ignore patterns, returning an error
// message suitable for user display if any are invalid.
func validateIgnorePatterns(patterns []string) error {
	for _, ig := range patterns {
		if _, err := scan.ParseIgnorePattern(ig); err != nil {
			return err
		}
	}
	return nil
}

// validate checks that required session flags are set and values are valid.
func (sf *sessionFlags) validate() error {
	if sf.image == "" {
		return fmt.Errorf("--image is required")
	}
	switch sf.pull {
	case "never", "missing", "always":
		// ok
	default:
		return fmt.Errorf("invalid --pull value %q (use never, missing, or always)", sf.pull)
	}
	return nil
}

func (sf *sessionFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&sf.image, "image", "", "")
	fs.StringVar(&sf.image, "i", "", "")
	fs.StringVar(&sf.runtimeBin, "runtime", "docker", "")
	fs.StringVar(&sf.platform, "platform", "linux/"+runtime.GOARCH, "")
	fs.StringVar(&sf.harnessPath, "harness", "", "")
	fs.StringVar(&sf.pull, "pull", "never", "")
	fs.BoolVar(&sf.noCleanup, "no-cleanup", false, "")
	fs.BoolVar(&sf.verbose, "verbose", false, "")
}
