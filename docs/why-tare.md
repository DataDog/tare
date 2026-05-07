# Why tare

## The problem

Minimal container images — distroless, scratch-based, and hardened base images — are becoming the default for production workloads. They reduce attack surface, eliminate unnecessary CVE exposure, and enforce cleaner dependency boundaries.

But they break the tools we use to verify containers.

### No shell means no tests

Common container testing patterns assume a shell exists inside the image — running commands through `sh -c`, or relying on shell-based test runners. Distroless breaks both. Even a simple `docker exec ... ls /app` fails when there is no `/bin/sh`.

The workarounds are bad:
- Install a shell in the production image just for testing (defeats the purpose of distroless)
- Only test the image from the outside (misses runtime behavior: library resolution, actual binary execution)
- Skip testing entirely and rely on runtime failures to surface problems

### The linkage gap

The most common failure mode in distroless migrations is **shared library resolution**: a binary that linked against `libssl.so.3` during build but the runtime image contains `libssl.so.1.1`, or doesn't contain it at all. The container starts, the binary loads, and then crashes on the first TLS connection.

Detecting this requires running `ldd` against the binary — but `ldd` isn't in a distroless image. And `ldd` itself is a shell script that invokes the dynamic linker, so even copying it in doesn't work without a shell.

Worse, this isn't a one-time migration problem. After the initial switch to distroless, any new dependency that introduces a native extension or shared library can reintroduce the same failure. Without automated checks, these regressions surface at runtime.

## The approach

Tare solves these problems with four ideas:

### 1. Inject, don't modify

Tare injects a temporary **test harness** into the container. The harness includes:

- **toybox** — statically compiled coreutils (`stat`, `grep`, `test`, `cat`, `sh`, etc.)
- **tare-tool** — a static Go binary that serves as the test runner, ELF analyzer, and diagnostic toolkit

The production image is never modified. The harness lives in a temporary path and is removed when the container is cleaned up. This is the same principle as `kubectl debug` ephemeral containers — bring tools to the workload, not the other way around.

This works even on completely empty images (scratch) — the harness tar stream creates its own directory structure during injection.

### 2. ELF analysis in Go

Rather than depending on `ldd` existing in the image, `tare-tool` implements shared library resolution in Go using `debug/elf`. It parses `DT_NEEDED` entries, walks the transitive dependency tree, and resolves libraries against the container's actual filesystem. No shell, no dynamic linker invocation required.

`tare-tool scan` extends this to walk entire directory trees, including `.so` files bundled inside JARs (common in Java projects with JNI dependencies). It also checks for common runtime files (CA certificates, `/etc/passwd`, etc.) that applications may depend on.

### 3. Declarative with an escape hatch

Most container structure tests are simple: does this file exist, does this binary run, does this environment variable have the right value. These should be declarative YAML — easy to write, easy to review, easy to generate.

But some tests need real logic: start the app, wait for it to listen on a port, send a request, check the response. For these, `commands` provide setup/teardown sequences and full stdout/stderr capture with regex assertions. The harness includes toybox coreutils on PATH, so commands have access to `grep`, `stat`, `cat`, `wget`, `sh`, and other standard tools.

All test output is TAP-compliant, so CI integration is straightforward.

### 4. Shared library scanning as a first-class concept

Tare introduces `scan` — a test type that finds every ELF file under a path and verifies all shared library dependencies resolve. This turns shared library verification from a manual exercise into a declarative CI gate:

```yaml
scan:
  - path: /app
  - path: /opt/venv/lib
    ignore:
      - libgif.so.7
```

This is particularly valuable as a **regression gate** after a distroless migration. The initial switch may resolve all dependencies, but three months later someone adds a Python package with a native extension that needs `libjpeg.so` — and the distroless image doesn't have it. Without `scan`, that breaks at runtime. With it, it breaks in CI.

For ad-hoc investigation, `tare scan` provides the same analysis as a standalone command with human-readable or JSON output, without the test framework overhead.

## The name

**Tare** is the weight of the container itself, subtracted to reveal the weight of what's inside. In the same way, tare strips away assumptions about the container's OS and tools to test what actually matters: the application and its dependencies.
