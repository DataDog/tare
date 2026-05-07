# Architecture

Tare has two main components — a host-side orchestrator (`tare`) and a container-side toolkit (`tare-tool`) — connected by an injected test harness. This document describes how the pieces fit together.

## Overview

```
┌──────────────────────────────────────────────────────┐
│  Host                                                │
│                                                      │
│  tare check -i app:latest tests.yaml                 │
│    │                                                 │
│    ├─ 1. Create container from target image          │
│    ├─ 2. Inject harness (toybox, tare-tool,          │
│    │      image metadata)                            │
│    ├─ 3. Start container                             │
│    ├─ 4. Convert YAML config → JSON test plan        │
│    ├─ 5. Write test plan into container              │
│    ├─ 6. Run tare-tool run-tests inside container    │
│    ├─ 7. Collect TAP output                          │
│    └─ 8. Remove container                            │
│                                                      │
└──────────────────────────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────────────┐
│  Container (target image + injected harness)         │
│                                                      │
│  Harness directory:                                  │
│    ├─ bin/                                           │
│    │   ├─ tare-tool     (static Go binary)           │
│    │   ├─ toybox        (static, coreutils)          │
│    │   └─ <applet symlinks>  (cat, grep, stat, ...)  │
│    └─ meta.json         (image inspect output)       │
│                                                      │
└──────────────────────────────────────────────────────┘
```

## Session lifecycle

All test execution goes through a **session** — a running container with the harness installed. Both `tare check` and `tare scan` create a session.

### 1. Create container

The orchestrator creates a stopped container from the target image, overriding the entrypoint with `tare-tool idle` — a Go program that blocks on SIGTERM. No shell is needed for the idle loop. The image is pulled if needed (controlled by `--pull`).

### 2. Inject harness

Before starting the container, two things are copied in via `docker cp`:

- **Harness tar** — a gzipped tarball embedded in the `tare` binary, containing toybox (coreutils) and tare-tool for the target architecture (amd64 or arm64). Extracts to a temporary path inside the container.
- **Image metadata** — the output of `docker image inspect`, written as `meta.json`. This is parsed by the orchestrator (for scan path autodetection) and by `tare-tool run-tests` (for metadata test assertions).

Both are written via in-memory tar streams — no temp files are created on the host.

### 3. Start container

The container is started with `tare-tool idle` as its entrypoint, which keeps it alive for exec calls.

### 4. Execute tests

The orchestrator converts the YAML config into a JSON test plan, writes it into the container, and runs `tare-tool run-tests` via `docker exec`. The test runner executes each test and outputs TAP (Test Anything Protocol) results directly to the host's stdout/stderr.

Test types are executed natively in Go:

| Test type | How it runs |
|---|---|
| `metadata` | Parse `meta.json`, compare fields directly |
| `files` (existence/perms) | `os.Lstat()`, syscall for UID/GID, mode bits |
| `files` (contents) | `os.ReadFile()` + `regexp.MatchString()` |
| `scan` | `scan.Run()` in-process (ELF analysis) |
| `commands` | `os/exec.Command()` with output capture |

Metadata, file existence, file content, and scan tests run entirely in Go with no subprocesses. Only command tests spawn external processes.

### 5. Cleanup

The container is removed. If `--no-cleanup` is set, it's left running for debugging — toybox coreutils and `tare-tool` are available for interactive investigation.

## OCI layout mode

When `--image` points to a directory containing an `oci-layout` file, tare operates without a container runtime. Instead of creating a container and injecting a harness, it extracts the image rootfs and runs tests in-process.

The layout must be a complete [OCI image layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) with all layer blobs present in `blobs/`. This is what `crane pull --format=oci` or `docker buildx --output type=oci` produces. Note that raw `rules_oci` build output (`bazel-bin/...`) is **not** a complete layout — it contains application layers but base image layers remain in bazel's external cache and are only assembled when pushing to a registry.

```
┌──────────────────────────────────────────────────────┐
│  Host                                                │
│                                                      │
│  tare check -i ./oci-layout tests.yaml               │
│    │                                                 │
│    ├─ 1. Open OCI layout directory                   │
│    ├─ 2. Resolve manifest (platform matching)        │
│    ├─ 3. Read image config                           │
│    ├─ 4. Extract rootfs to temp directory             │
│    │     (layers applied bottom-to-top,              │
│    │      whiteouts processed, metadata recorded)    │
│    ├─ 5. Convert YAML config → JSON test plan        │
│    ├─ 6. Execute tests in-process                    │
│    │     (file tests via rootfs.FS,                  │
│    │      command tests skipped)                     │
│    ├─ 7. Output TAP results                          │
│    └─ 8. Remove temp directory                       │
│                                                      │
└──────────────────────────────────────────────────────┘
```

### Rootfs extraction

The image rootfs is extracted by iterating layers in manifest order (bottom to top). Each layer is a gzip-compressed tar archive stored in the layout's `blobs/` directory. All writes go through an `os.Root` opened on the extraction directory, which prevents path traversal.

Layer extraction handles OCI whiteout files:
- `.wh.<name>` — deletes the named file from earlier layers
- `.wh..wh..opq` — deletes all prior contents of the directory (opaque whiteout)

A metadata sidecar (`.tar-fs.json`) records the original tar header values (uid, gid, mode) for each entry. These may differ from on-disk values when extracting without root privileges, since `chown` requires `CAP_CHOWN`. The metadata is used by file existence tests to report correct ownership.

### Manifest resolution

For single-platform layouts, `index.json` points directly to a manifest. For multi-arch layouts, `index.json` may point to a nested image index containing per-platform manifest descriptors. Tare follows index descriptors one level deep to find the manifest matching `--platform` (default: `linux/<host arch>`).

### Filesystem abstraction

Tests and scans operate through a `rootfs.FS` interface rather than direct `os.*` calls. In container mode, this wraps an `os.Root` opened on `/` (the container root). In OCI layout mode, it wraps the extracted rootfs with a metadata overlay — `Stat` and `Lstat` return uid/gid/mode from the tar headers rather than the on-disk values.

The interface accepts absolute container paths (e.g., `/app/myservice`). Relative paths are resolved against the image's `WorkingDir`.

### Limitations

- **Command tests** are skipped — there is no container to exec in. They appear as `# SKIP` in the TAP output.
- **Tare weight** is not reported for OCI layouts.
- **zstd-compressed layers** are not yet supported.

## Components

### `tare` (host)

The orchestrator. Subcommands:

- **`tare check`** — converts YAML config into a JSON test plan and runs it. In container mode, delegates to `tare-tool run-tests` inside the container. In OCI layout mode, executes tests in-process via the `testexec` package.
- **`tare scan`** — scans for shared library issues and renders the results. In container mode, runs `tare-tool scan` inside the container. In OCI layout mode, calls `scan.Run` directly.

### `tare-tool` (container)

A static Go binary (`CGO_ENABLED=0`) injected into the container as part of the harness. Subcommands:

- **`run-tests`** — execute a JSON test plan and output TAP results. This is the test runner used by `tare check`.
- **`idle`** — block until SIGTERM. Used as the container entrypoint.
- **`scan`** — walk directories for ELF binaries (including inside JARs/WARs/EARs), resolve shared library dependencies, check runtime files.
- **`elf info`** — print ELF binary metadata (architecture, linkage).
- **`elf deps`** — resolve and print shared library dependencies for a single binary.

### Shared core

The ELF parsing and library resolution logic (`internal/elf`) is shared between `tare` and `tare-tool`. The `internal/scan` package builds on this to provide directory walking, JAR scanning, runtime file checks, and report formatting. The `internal/testplan` package defines the JSON test plan structure. The `internal/testexec` package implements test plan execution, used by both `tare-tool` (in container mode) and `tare` directly (in OCI layout mode). The `internal/oci` package handles OCI image layout parsing, rootfs extraction, and provides the metadata-aware filesystem implementation. The `internal/rootfs` package defines the filesystem interface (`rootfs.FS`) that abstracts over container and OCI layout filesystems.

## Harness

The test harness is a gzipped tar embedded in the `tare` binary at build time. It contains:

- **toybox** — statically compiled coreutils (`stat`, `grep`, `test`, `cat`, `sh`, etc.)
- **tare-tool** — the test runner and diagnostic toolkit

The harness is built for both linux/amd64 and linux/arm64. The correct variant is selected based on the `--platform` flag (defaults to the host architecture).

The harness is injected via `docker cp - container:/` with a tar stream, so it works even on completely empty images (scratch). No shell, no `/tmp` directory, no utilities need to exist in the target image.

## Container runtime

Tare shells out to a container runtime binary (default: `docker`). The `--runtime` flag allows using alternatives like `podman` or `nerdctl`. All container operations go through the `Runtime` type, which constructs and executes CLI commands.
