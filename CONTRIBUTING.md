# Contributing to tare

## Prerequisites

- **Go 1.26+**
- **GNU tar** — required for harness assembly (`brew install gnu-tar` on macOS, then set `TAR=gtar`)
- **Docker** — for running integration tests

## Commit messages

This project uses [conventional commits](https://www.conventionalcommits.org/). PR titles must follow the format:

```
type(scope): description
```

The scope is optional. Allowed types:

| Type | Purpose |
|------|---------|
| `feat` | New feature |
| `fix` | Bug fix |
| `docs` | Documentation only |
| `chore` | Maintenance (deps, CI, etc.) |
| `refactor` | Code change that neither fixes a bug nor adds a feature |
| `test` | Adding or updating tests |
| `ci` | CI/CD changes |
| `build` | Build system changes |
| `perf` | Performance improvement |
| `style` | Formatting, whitespace, etc. |

A CI check enforces this on all pull requests. The PR title becomes the squash-merge commit message, which is used to generate changelogs and determine release versions.

## Building

Tare embeds a prebuilt test harness (toybox, tare-tool) into the `tare` binary at compile time. A plain `go build` will not produce a working binary — use `make` instead:

```bash
make tare
```

This builds the harness for both linux/amd64 and linux/arm64, assembles the embedded tarballs, and compiles the `tare` binary.

To build the harness for a single architecture:

```bash
make harness-arm64
make harness-amd64
```

## Running tests

```bash
go test ./...
```

## Project layout

```
cmd/tare/         Host-side orchestrator (check, scan)
cmd/tare-tool/    Container-side toolkit (run-tests, scan, elf, idle)
internal/         Shared packages (elf parsing, scan, config, harness, testplan)
_examples/        Example test configs and Dockerfiles
docs/             Documentation
```

See [docs/architecture.md](docs/architecture.md) for the full design.

## Cleaning up

```bash
make clean
```

This removes the `harness/`, `.cache/`, and `dist/` directories and restores the embedded harness tarballs to their checked-in state.
