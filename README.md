# tare

> **tare** /tɛr/ — the weight of the container itself, subtracted to reveal what's inside.

A modern container structure testing tool. Tare validates container images — including shell-less (distroless) images — by injecting a temporary test harness and running declarative YAML tests inside the target container. First-class support for minimal and distroless images, plus shared library dependency analysis.

## How it works

Tare supports two modes:

- **Container mode** — injects a temporary test harness into a running container. Your production image is never modified. Works even on completely empty images (scratch).
- **OCI layout mode** — reads an OCI image layout directory directly, extracts the rootfs, and runs tests without a container runtime. Useful for images produced by build systems like bazel.

If `--image` points to a directory containing an `oci-layout` file, tare uses OCI layout mode automatically. Otherwise it uses a container runtime.

## Quick start

```bash
# Check an image — scans for shared library issues and runs structure tests
tare check -i myapp:latest

# Check with a test config
tare check -i myapp:latest tests.yaml

# Check with additional scan paths
tare check -i myapp:latest --scan /app --scan /opt/venv

# Scan shared library dependencies (human-readable output, no test framework)
tare scan -i myapp:latest --path /app

# Scan with JSON output
tare scan -i myapp:latest --path /app --json

# Check an OCI layout directory (no container runtime needed)
tare check -i path/to/oci-layout tests.yaml

# Check a multi-arch OCI layout for a specific platform
tare check -i path/to/oci-layout --platform linux/amd64 tests.yaml
```

## Example test config

```yaml
schema_version: 1

metadata:
  - user: app
    entrypoint: ["/app/myservice"]
    env:
      - key: APP_ENV
        value: production

files:
  - path: /etc/ssl/certs/ca-certificates.crt
  - path: /bin/sh
    present: false

scan:
  - name: app shared libraries resolve
    path: /app
  - name: python native extensions resolve
    path: /opt/venv/lib
    ignore:
      - libgif.so.7

commands:
  - name: binary starts
    run: ["/app/myservice", "--version"]
    exit: 0
```

For the full YAML reference, see [docs/configuration.md](docs/configuration.md).

## Shared library scanning

The most common failure mode in distroless migrations is missing shared libraries — a binary or native extension needs `libfoo.so`, but it's not in the minimal base image.

Tare scans every ELF file under a given path (including `.so` files bundled inside JARs), resolves all shared library dependencies, and fails if anything is missing.

```bash
# Standalone scan
tare scan -i myapp:latest --path /app --path /opt/venv/lib

# As a CI gate in your test config
tare check -i myapp:latest tests.yaml
```

```yaml
scan:
  - path: /app
  - path: /opt/venv/lib
```

Without explicit paths, tare autoscans the image: the `ENTRYPOINT`/`CMD` directory plus library-path env vars (`PYTHONPATH`, `LD_LIBRARY_PATH`, `CLASSPATH`, `NODE_PATH`, `PERL5LIB`, `GEM_PATH`). Run `tare scan -i IMAGE` to see what it finds, or pass `--no-autoscan` to disable detection and scan only paths from `--path`/`--scan` or `tare.scan`.

See [docs/configuration.md](docs/configuration.md#scan) for the full scan configuration reference.

## Building from source

Tare embeds a prebuilt test harness into the binary at compile time. Use `make`, not `go build`:

```bash
make tare
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for prerequisites and details.

## OCI layout support

Tare can check [OCI image layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) directories directly — no container runtime needed.

If `--image` points to a directory containing an `oci-layout` file, tare extracts the rootfs from the layer blobs and runs tests against it. All layer blobs must be present in the layout's `blobs/` directory.

```bash
# Check a complete OCI layout
tare check -i ./my-image-layout tests.yaml

# Multi-arch layout — select a platform
tare check -i ./my-image-layout --platform linux/amd64 tests.yaml
```

### Getting a complete OCI layout

Tare needs a **complete** layout with all layer blobs present, including base image layers. How you get one depends on your build system:

**From a registry** — use [crane](https://github.com/google/go-containerregistry/tree/main/cmd/crane) to pull the full image:

```bash
crane pull --format=oci --platform=linux/amd64 registry.example.com/myapp:latest ./myapp-layout
tare check -i ./myapp-layout tests.yaml
```

**From `docker buildx`:**

```bash
docker buildx build --output type=oci,dest=image.tar .
mkdir image && tar -xf image.tar -C image
tare check -i ./image tests.yaml
```

**Bazel `rules_oci`** — the local build output (`bazel-bin/...`) is not a complete OCI layout. It contains application layers and manifests, but base image layers only exist in bazel's external repository cache. These outputs are designed for pushing to a registry, not for standalone consumption. To check a bazel-built image with tare, push it to a registry first and then pull with crane, or use `tare check -i <image>` with the container runtime after loading the image.

### Limitations

- **Command tests** are skipped — there is no container to exec in. They appear as `# SKIP` in the TAP output.
- **Tare weight** is not reported for OCI layouts.
- **zstd-compressed layers** are not yet supported (gzip and uncompressed are).

See `_examples/oci-layout/` for working examples.

## Documentation

- [Configuration](docs/configuration.md) — YAML test format and scan configuration
- [Architecture](docs/architecture.md) — design, session lifecycle, test execution model, OCI layout mode
- [Motivation](docs/why-tare.md) — the problem landscape and design rationale

## License

[Apache-2.0](LICENSE)
