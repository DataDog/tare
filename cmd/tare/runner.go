package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/DataDog/tare/internal/config"
	"github.com/DataDog/tare/internal/container"
	"github.com/DataDog/tare/internal/harness"
	"github.com/DataDog/tare/internal/oci"
)

// withSession runs fn with a running container session. It owns harness
// selection, session creation, SIGINT/SIGTERM handling, and cleanup —
// callers focus on the operation that runs inside the container. The
// returned int is fn's exit code (or non-zero on setup failure).
func withSession(sf *sessionFlags, cfg *config.Config, fn func(*container.Session) int) int {
	rt := &container.Runtime{Bin: sf.runtimeBin}
	harnessReader, err := harness.Select(sf.harnessPath, sf.platform)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	sessOpts := container.SessionOpts{
		Image:    sf.image,
		Platform: sf.platform,
		Pull:     sf.pull,
		Harness:  harnessReader,
		Verbose:  sf.verbose,
	}
	applyRuntimeOpts(&sessOpts, cfg)
	sess, err := rt.NewSession(sessOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			signal.Stop(sigCh)
			if sf.noCleanup {
				fmt.Fprintf(os.Stderr, "container left running: %s\n", sess.ID())
				return
			}
			if sf.verbose {
				fmt.Fprintf(os.Stderr, "removing container %s...\n", sess.ID()[:12])
			}
			_ = sess.Close()
		})
	}
	defer cleanup()

	go func() {
		<-sigCh
		cleanup()
		os.Exit(130)
	}()

	return fn(sess)
}

// withOCIRootfs runs fn with the parsed image config and an extracted
// rootfs FS. It owns layout parsing, temp dir lifecycle, rootfs extraction,
// and FS construction. The returned int is fn's exit code.
func withOCIRootfs(sf *sessionFlags, fn func(*oci.ImageConfig, *oci.FS) int) int {
	layout, err := oci.Open(sf.image)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	arch := archFromPlatform(sf.platform)
	icfg, err := layout.ImageConfig("linux", arch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	destDir, err := os.MkdirTemp("", "tare-oci-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: creating temp dir: %v\n", err)
		return 1
	}
	defer func() {
		if sf.noCleanup {
			fmt.Fprintf(os.Stderr, "extracted rootfs left at: %s\n", destDir)
			return
		}
		os.RemoveAll(destDir)
	}()

	if sf.verbose {
		fmt.Fprintf(os.Stderr, "extracting rootfs to %s...\n", destDir)
	}

	result, err := layout.Extract(destDir, "linux", arch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: extracting rootfs: %v\n", err)
		return 1
	}
	defer result.Root.Close()

	cwd := icfg.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	fsys := oci.NewFS(result, cwd)

	return fn(icfg, fsys)
}
