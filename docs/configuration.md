# Configuration

Tare config files declare what to assert about a container image. Every file is YAML.

```yaml
schema_version: 1

metadata: [...]
files:    [...]
commands: [...]
scan:     [...]

tare:
  runtime: {...}
```

## Conventions

- Field names are `snake_case`.
- Lists across multiple config files are concatenated.
- Negation is expressed with `not:` (a sub-block whose children invert) or `present: false` (the bool short form for binary assertions).
- String pattern lists are interpreted as **literal substrings** by default. Regex is opt-in.

## `schema_version`

```yaml
schema_version: 1
```

Required. Single integer. Bumps on any breaking change to the parser.

## `metadata`

A list of assertions against the OCI image config. Each entry is independent; failures report by entry name.

```yaml
metadata:
  - name: production deployment
    user: app
    workdir: /app
    entrypoint: ["/app/bin/server"]
    cmd: []
    env:
      - key: APP_ENV
        value: production
      - key: JAVA_HOME
        value: /usr/lib/jvm/zulu-(17|25)
        regex: true
    ports: ["8080/tcp"]
    volumes: ["/data"]
    labels:
      - key: org.opencontainers.image.source
        value: github.com/example/app
    not:
      env:    [DEBUG, AWS_SECRET_ACCESS_KEY]
      ports:  ["22/tcp"]
      volumes: ["/host"]
      labels: [internal.staging.only]
```

| Field | Description |
|---|---|
| `name` | Optional name for failure reporting |
| `user` | Expected USER |
| `workdir` | Expected working directory |
| `entrypoint` | Expected entrypoint, exact match |
| `cmd` | Expected CMD, exact match |
| `env` | List of `{key, value, regex?}` |
| `ports` | List of port strings (`"8080/tcp"`) |
| `volumes` | List of volume paths |
| `labels` | List of `{key, value, regex?}` |
| `not.env` | List of keys that must be absent |
| `not.ports` / `not.volumes` | List of strings that must not appear |
| `not.labels` | List of keys that must be absent |

`metadata` is a list specifically to support composition: multiple config files can each contribute assertions without clobbering one another.

`regex: true` on an env or label entry treats `value` as a regular expression. Default is literal equality.

## `files`

Unified existence + content + permissions assertions. Each entry asserts everything you want to know about one path.

Path entries can target files, directories, or symlinks. `present`, `permissions`, `uid`, `gid`, `*_by` predicates, and `type` apply to any path. `contents` only applies to regular files.

```yaml
files:
  - path: /etc/ssl/certs/ca-certificates.crt    # exists (default)

  - path: /bin/sh
    present: false                              # must not exist

  - path: /tmp
    type: dir
    writable_by: any                            # don't care about exact perms

  - path: /etc/secret
    type: file
    not:
      type: symlink                             # reject a symlink dropped in

  - path: /app/bin/server
    permissions: "0755"
    uid: 0
    gid: 0
    executable_by: any

  - path: /app/secret
    permissions: "0600"
    not:
      readable_by: other
      writable_by: [group, other]

  - path: /etc/timezone
    contents: ["UTC"]

  - path: /app/version.txt
    contents: ["^v\\d+\\.\\d+\\.\\d+$"]
    regex: true

  - path: /app/banner.txt
    contents:
      - "Copyright Datadog"
      - {match: "Build [0-9a-f]{7}"}

  - path: /app/config.yaml
    permissions: "-rw-r--r--"
    contents: ["log_level: info"]
    not:
      contents: ["password:", "secret:", "api_key:"]
```

### `permissions`

Two accepted forms:

- **Octal string**: `"0755"`. Asserts permission bits exactly. Does not constrain file type.
- **rwx string**: `"-rwxr-xr-x"`. Same as `ls -l`. The leading character (`-`/`d`/`l`) constrains file type as well.

Parser rule: leading character is a digit → parse as octal; otherwise → parse as rwx. The octal form is quoted to dodge YAML 1.1 vs 1.2 octal-integer differences.

### `type`

Asserts the file type independent of permissions. Accepted values: `file` (regular file), `dir`, `symlink`. Default: no type constraint.

The rwx form of `permissions:` already encodes file type via its leading character (`-`/`d`/`l`). Use `type:` when you want to assert type without locking in exact perms.

### `*_by` predicates

`readable_by`, `writable_by`, `executable_by` are sugar for "this permission bit is set, regardless of others." Single value or list:

```yaml
executable_by: group
executable_by: [owner, group]
```

Class names: `owner`, `group`, `other`, `any`. For exact-mode assertions involving multiple classes ("user and only user"), use `permissions:` directly — it expresses that more clearly than chained `*_by` clauses.

### `contents`

Patterns to find (or, under `not:`, to exclude) in the file contents. See **Regex** below for opt-in semantics.

### Caveats

- `present: false` excludes all other assertions. A non-existent path has no type, perms, owner, or content to check.
- `contents` is regular-file-only. Using it on a directory or symlink is a configuration error.
- Octal `permissions:` checks permission bits only. To assert "this is a regular file with 0755 perms," use the rwx form (`-rwxr-xr-x`) or combine `type: file` with octal perms.

## `commands`

Run a command inside the container; assert exit code and output.

```yaml
commands:
  - name: prints version
    run: ["/app/bin/server", "--version"]
    exit: 0
    stdout: ["^v\\d+\\.\\d+"]
    regex: true
    not:
      stderr: [panic, FATAL]

  - name: starts cleanly
    run: /app/bin/server                        # short form: one-element argv

  - name: validates config under fixture
    setup:
      - ["touch", "/tmp/marker"]
    run: ["/app/bin/server", "--validate-config"]
    env:
      - key: APP_ENV
        value: test
    exit: 0
    stdout: ["config ok"]
    teardown:
      - ["rm", "-f", "/tmp/marker"]
```

### `run` polymorphism

- **String form**: `run: "/app/bin/server"` is treated as a one-element argv. The kernel looks up that exact filename. Whitespace inside the string is part of the filename, not an argument separator.
- **List form**: `run: ["/app/bin/server", "--flag"]` is a standard argv.

There is no shell intermediary in either case. To run a pipeline, write a script and execute it.

### Caveat

Command tests are skipped when checking an OCI layout directory (no container to exec in). They are reported as skipped, not failed.

## `scan`

ELF dependency analysis. Walks paths, opens JARs/WARs/EARs to extract `.so` entries, parses each ELF's `DT_NEEDED`, and verifies all libraries resolve in the image filesystem. Catches missing shared library dependencies before runtime.

```yaml
scan:
  - path: /app

  - name: python native extensions
    path: /opt/venv/lib

  - path: /usr/lib/jvm/zulu-25/lib
    ignore:
      - /usr/lib/jvm/zulu-25/lib/libfontmanager.so   # absolute path: glob match on binary path
      - libfreetype.so.6                              # bare filename: match on dependency name
    limit: 2048
```

| Field | Description |
|---|---|
| `path` | Directory to scan for ELF files |
| `name` | Optional human-readable name (defaults to path) |
| `ignore` | Patterns to exclude (see below) |
| `limit` | Max ELF binaries scanned (default 1024) |

**Ignore patterns:**
- Absolute path (starts with `/`): glob match on the binary's path.
- Bare filename (no `/`): match on dependency library name.
- Relative paths are rejected.

The first scan entry also reports informational warnings for missing common runtime files (CA certificates, `/etc/passwd`, `/etc/group`, `/etc/nsswitch.conf`). These are informational and do not cause the scan to fail.

## `tare.runtime`

Container runtime configuration. Use this to match your deployment environment — k8s pod spec, compose file, bazel `oci_image` config — so tests run under the same constraints production does.

```yaml
tare:
  runtime:
    user: "1000:1000"
    cap_drop: [ALL]
    cap_add:  [NET_BIND_SERVICE]
    binds:
      - /host/fixtures:/etc/fixtures:ro
    env:
      - key: FEATURE_FLAG
        value: "1"
    env_file: .env.test
```

| Field | Description |
|---|---|
| `user` | Override container `USER` (e.g. `"1000"` or `"1000:1000"`) |
| `cap_drop` | List of Linux capabilities to drop |
| `cap_add` | List of Linux capabilities to add |
| `binds` | Bind mounts in `host:container[:opts]` form |
| `env` | List of `{key, value}` runtime env vars |
| `env_file` | Path to a file of `KEY=value` lines |

These settings apply to every command test. They do not affect file or metadata assertions, which run against the static image.

## Cross-cutting: `not:`

`not:` inverts the child assertions. Available at multiple levels:

- `metadata.not.{env,ports,volumes,labels}` — must be absent.
- `files.not.{contents,type,readable_by,writable_by,executable_by}` — patterns/predicates must fail.
- `commands.not.{stdout,stderr}` — output patterns must NOT match.

For binary assertions on a single fact, use the `present: false` short form instead — it composes more cleanly than wrapping in `not:`.

## Cross-cutting: regex

Tare defaults to literal-substring matching for output and content patterns. This avoids the common footgun where `version: 1.2.3` accidentally matches `version: 1Z2Y3` because `.` is a regex metacharacter.

Three ways to opt into regex, matched to the underlying cardinality of the assertion:

| Mechanism | Where | Notes |
|---|---|---|
| Per-item `regex: true` | env, label entries | Naturally per-item; one value per key |
| Test-level `regex: true` | files (`contents`), commands (`stdout`/`stderr`) | Flips every bare string in this test's pattern lists, including `not:` siblings |
| Per-item `{match: "..."}` | pattern list entries | Always regex regardless of the flag — escape hatch for mixed literal+regex tests |

The test-level flag is the common case. The per-item map is for the rare test that mixes literal and regex patterns in one file or one stream.

## Composition

Multiple config files can be passed to `tare check`:

```bash
tare check -i myapp:latest base.yaml app.yaml
```

- `metadata`, `files`, `commands`, `scan` lists are concatenated across configs.
- `tare.runtime` is shallow-merged (last config wins per field).

A common pattern is a shared base config for distroless invariants plus an app-specific overlay.
