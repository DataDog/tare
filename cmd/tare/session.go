package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/DataDog/tare/internal/container"
	"github.com/DataDog/tare/internal/harness"
)

// startSession creates a container session with signal handling and cleanup.
// The returned cleanup function is safe to call concurrently and multiple
// times (via sync.Once). Callers should defer cleanup().
func startSession(sf *sessionFlags) (*container.Session, func(), error) {
	rt := &container.Runtime{Bin: sf.runtimeBin}
	harnessReader, err := harness.Select(sf.harnessPath, sf.platform)
	if err != nil {
		return nil, nil, err
	}

	sess, err := rt.NewSession(container.SessionOpts{
		Image:    sf.image,
		Platform: sf.platform,
		Pull:     sf.pull,
		Harness:  harnessReader,
		Verbose:  sf.verbose,
	})
	if err != nil {
		return nil, nil, err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
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

	go func() {
		<-sigCh
		cleanup()
		os.Exit(130)
	}()

	return sess, cleanup, nil
}
