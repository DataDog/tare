// Package testexec executes tare test plans and outputs TAP results.
package testexec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/DataDog/tare/internal/oci"
	"github.com/DataDog/tare/internal/rootfs"
	"github.com/DataDog/tare/internal/scan"
	"github.com/DataDog/tare/internal/testplan"
)

// diagnosticBodyLimit is the per-stream byte budget for failure diagnostics.
// Streams longer than this are rendered as head + tail with a truncation
// marker in between.
const diagnosticBodyLimit = 2048

// binarySniffLimit caps how many leading bytes are inspected for NUL when
// classifying content as binary. Matches git's heuristic.
const binarySniffLimit = 8000

// streamCaptureCap bounds how many bytes of a command's stdout/stderr we
// hold in memory. Past this, writes are silently dropped (the writing
// process never sees a short write) but the dropped count is reported in
// failure diagnostics.
const streamCaptureCap = 1 << 20 // 1 MiB

// fileContentSizeCap is the largest file `files.contents` will load.
// Larger files fail fast with a clear error rather than dragging the
// runner into a multi-GiB regex match or OOMing.
const fileContentSizeCap = 16 << 20 // 16 MiB

// Options configures test execution.
type Options struct {
	// FS is the filesystem to use for file tests and scanning.
	// If nil, defaults to os.OpenRoot("/") wrapped in rootfs.New.
	FS rootfs.FS

	// NoCommands skips command tests with a TAP SKIP directive.
	// Used for OCI layout mode where there is no container to exec in.
	NoCommands bool
}

// Run executes a test plan and writes TAP output to w.
// Returns the number of failures (0 means all passed).
func Run(w io.Writer, plan *testplan.Plan, meta *oci.ImageConfig, opts Options) int {
	fsys := opts.FS
	if fsys == nil {
		root, err := os.OpenRoot("/")
		if err != nil {
			fmt.Fprintf(w, "Bail out! opening root: %v\n", err)
			return 1
		}
		fsys = rootfs.New(root, "/")
	}

	fmt.Fprintf(w, "TAP version 14\n")
	fmt.Fprintf(w, "1..%d\n", len(plan.Tests))

	failures := 0
	for i, t := range plan.Tests {
		num := i + 1
		var testErr error

		switch t.Type {
		case testplan.TypeMetadata:
			testErr = execMetadata(t.Metadata, meta)
		case testplan.TypeFileExistence:
			testErr = execFileExistence(fsys, t.FileExistence)
		case testplan.TypeFileContent:
			testErr = execFileContent(fsys, t.FileContent)
		case testplan.TypeScan:
			testErr = execScan(w, fsys, t.Scan, meta)
		case testplan.TypeCommand:
			if opts.NoCommands {
				fmt.Fprintf(w, "ok %d - %s # SKIP command tests not available for OCI layouts\n", num, t.Name)
				continue
			}
			testErr = execCommand(t.Command)
		default:
			testErr = fmt.Errorf("unknown test type: %s", t.Type)
		}

		if testErr != nil {
			failures++
			fmt.Fprintf(w, "not ok %d - %s\n", num, t.Name)
			for _, line := range strings.Split(testErr.Error(), "\n") {
				fmt.Fprintf(w, "# %s\n", line)
			}
		} else {
			fmt.Fprintf(w, "ok %d - %s\n", num, t.Name)
		}
	}

	return failures
}

func execMetadata(spec *testplan.MetadataSpec, meta *oci.ImageConfig) error {
	if meta == nil {
		return fmt.Errorf("no image metadata available")
	}

	// Negation paths handle absence assertions for env/label/port/volume.
	if spec.Negate {
		switch spec.Field {
		case "env":
			prefix := spec.Key + "="
			for _, e := range meta.Env {
				if strings.HasPrefix(e, prefix) {
					return fmt.Errorf("env %q should be absent but is set", spec.Key)
				}
			}
			return nil
		case "label":
			if _, ok := meta.Labels[spec.Key]; ok {
				return fmt.Errorf("label %q should be absent but is set", spec.Key)
			}
			return nil
		case "port":
			if hasExposedPort(meta, spec.Key) {
				return fmt.Errorf("port %q should not be exposed", spec.Key)
			}
			return nil
		case "volume":
			if _, ok := meta.Volumes[spec.Key]; ok {
				return fmt.Errorf("volume %q should not be declared", spec.Key)
			}
			return nil
		default:
			return fmt.Errorf("negate not supported for field %q", spec.Field)
		}
	}

	switch spec.Field {
	case "user":
		return matchString("user", meta.User, spec)
	case "workdir":
		return matchString("workdir", meta.WorkingDir, spec)
	case "entrypoint":
		b, _ := json.Marshal(meta.Entrypoint)
		return matchString("entrypoint", string(b), spec)
	case "cmd":
		b, _ := json.Marshal(meta.Cmd)
		return matchString("cmd", string(b), spec)
	case "env":
		prefix := spec.Key + "="
		var got string
		var found bool
		for _, e := range meta.Env {
			if strings.HasPrefix(e, prefix) {
				got = e[len(prefix):]
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("env %q not found in image metadata", spec.Key)
		}
		return matchString("env "+spec.Key, got, spec)
	case "label":
		got, ok := meta.Labels[spec.Key]
		if !ok {
			return fmt.Errorf("label %q not found in image metadata", spec.Key)
		}
		return matchString("label "+spec.Key, got, spec)
	case "port":
		if !hasExposedPort(meta, spec.Key) {
			return fmt.Errorf("port %q not exposed", spec.Key)
		}
		return nil
	case "volume":
		if _, ok := meta.Volumes[spec.Key]; !ok {
			return fmt.Errorf("volume %q not declared", spec.Key)
		}
		return nil
	default:
		return fmt.Errorf("unknown metadata field: %s", spec.Field)
	}
}

func matchString(label, got string, spec *testplan.MetadataSpec) error {
	if spec.IsRegex {
		re, err := regexp.Compile(spec.Expected)
		if err != nil {
			return fmt.Errorf("invalid regex %q: %v", spec.Expected, err)
		}
		if !re.MatchString(got) {
			return fmt.Errorf("%s: expected /%s/, got %q", label, spec.Expected, got)
		}
		return nil
	}
	if got != spec.Expected {
		return fmt.Errorf("%s: expected %q, got %q", label, spec.Expected, got)
	}
	return nil
}

func hasExposedPort(meta *oci.ImageConfig, port string) bool {
	for k := range meta.ExposedPorts {
		if k == port {
			return true
		}
		// Allow matching either the bare port or the port/proto form.
		if strings.SplitN(k, "/", 2)[0] == port {
			return true
		}
	}
	return false
}

func execFileExistence(fsys rootfs.FS, spec *testplan.FileExistenceSpec) error {
	info, err := fsys.Lstat(spec.Path)

	if !spec.ShouldExist {
		if err == nil {
			return fmt.Errorf("file should not exist: %s", spec.Path)
		}
		return nil
	}

	if err != nil {
		return fmt.Errorf("file does not exist: %s", spec.Path)
	}

	mode := info.Mode()

	if spec.Type != "" {
		if !typeMatches(mode, spec.Type) {
			return fmt.Errorf("type: expected %s, got %s", spec.Type, modeType(mode))
		}
	}
	if spec.NotType != "" {
		if typeMatches(mode, spec.NotType) {
			return fmt.Errorf("type %s: should not match", spec.NotType)
		}
	}

	if spec.UID != nil || spec.GID != nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("cannot read ownership of %s", spec.Path)
		}
		if spec.UID != nil && int(stat.Uid) != *spec.UID {
			return fmt.Errorf("uid: expected %d, got %d", *spec.UID, stat.Uid)
		}
		if spec.GID != nil && int(stat.Gid) != *spec.GID {
			return fmt.Errorf("gid: expected %d, got %d", *spec.GID, stat.Gid)
		}
	}

	if spec.Permissions != "" {
		if err := checkPermissions(mode, spec.Permissions); err != nil {
			return err
		}
	}

	for _, class := range spec.ReadableBy {
		if !classBitSet(mode, class, 4) {
			return fmt.Errorf("not readable by %s (mode: %s)", class, mode)
		}
	}
	for _, class := range spec.NotReadableBy {
		if classBitSet(mode, class, 4) {
			return fmt.Errorf("must not be readable by %s (mode: %s)", class, mode)
		}
	}
	for _, class := range spec.WritableBy {
		if !classBitSet(mode, class, 2) {
			return fmt.Errorf("not writable by %s (mode: %s)", class, mode)
		}
	}
	for _, class := range spec.NotWritableBy {
		if classBitSet(mode, class, 2) {
			return fmt.Errorf("must not be writable by %s (mode: %s)", class, mode)
		}
	}
	for _, class := range spec.ExecutableBy {
		if !classBitSet(mode, class, 1) {
			return fmt.Errorf("not executable by %s (mode: %s)", class, mode)
		}
	}
	for _, class := range spec.NotExecutableBy {
		if classBitSet(mode, class, 1) {
			return fmt.Errorf("must not be executable by %s (mode: %s)", class, mode)
		}
	}

	return nil
}

func typeMatches(mode os.FileMode, t string) bool {
	switch t {
	case "file":
		return mode.IsRegular()
	case "dir":
		return mode.IsDir()
	case "symlink":
		return mode&os.ModeSymlink != 0
	}
	return false
}

func modeType(mode os.FileMode) string {
	switch {
	case mode&os.ModeSymlink != 0:
		return "symlink"
	case mode.IsDir():
		return "dir"
	case mode.IsRegular():
		return "file"
	}
	return "other"
}

// checkPermissions compares info.Mode() against the spec's permissions
// string. The spec accepts either an octal form (starts with a digit) or
// an rwx form (starts with -, d, or l).
func checkPermissions(mode os.FileMode, spec string) error {
	if spec == "" {
		return nil
	}
	if spec[0] >= '0' && spec[0] <= '9' {
		body := spec
		if strings.HasPrefix(body, "0o") || strings.HasPrefix(body, "0O") {
			body = body[2:]
		}
		want, err := strconv.ParseUint(body, 8, 32)
		if err != nil {
			return fmt.Errorf("invalid octal permissions %q: %v", spec, err)
		}
		got := mode.Perm()
		if uint32(got) != uint32(want) {
			return fmt.Errorf("permissions: expected %s, got %#o", spec, got)
		}
		return nil
	}
	got := mode.String()
	if got != spec {
		return fmt.Errorf("permissions: expected %s, got %s", spec, got)
	}
	return nil
}

// classBitSet reports whether the permission class (owner/group/other/any)
// has the given bit (4=read, 2=write, 1=execute) set in the mode.
func classBitSet(mode os.FileMode, class string, bit os.FileMode) bool {
	switch class {
	case "owner":
		return mode&(bit<<6) != 0
	case "group":
		return mode&(bit<<3) != 0
	case "other":
		return mode&bit != 0
	case "any":
		return mode&((bit<<6)|(bit<<3)|bit) != 0
	}
	return false
}

func execFileContent(fsys rootfs.FS, spec *testplan.FileContentSpec) error {
	info, err := fsys.Stat(spec.Path)
	if err != nil {
		return fmt.Errorf("stat %s: %v", spec.Path, err)
	}
	if info.Size() > fileContentSizeCap {
		return fmt.Errorf("%s: %d bytes exceeds content-check cap of %d bytes; refusing to load",
			spec.Path, info.Size(), fileContentSizeCap)
	}

	data, err := fsys.ReadFile(spec.Path)
	if err != nil {
		return fmt.Errorf("reading %s: %v", spec.Path, err)
	}

	if err := checkEmpty(spec.Path, len(data), spec.Empty); err != nil {
		return fmt.Errorf("%s\n%s", err, formatBody(spec.Path, data, len(data)))
	}
	if err := matchPatterns(spec.Path, data, spec.ExpectedContents, spec.ExcludedContents); err != nil {
		return fmt.Errorf("%s\n%s", err, formatBody(spec.Path, data, len(data)))
	}
	return nil
}

// checkEmpty validates a tri-state empty assertion. nil means no
// assertion. *empty == true means the source must be exactly zero bytes
// (we use total rather than captured length so a capped stream still
// fails correctly). *empty == false means the source must be non-empty.
func checkEmpty(label string, total int, empty *bool) error {
	if empty == nil {
		return nil
	}
	if *empty && total != 0 {
		return fmt.Errorf("%s: expected empty, got %d bytes", label, total)
	}
	if !*empty && total == 0 {
		return fmt.Errorf("%s: expected non-empty, got empty", label)
	}
	return nil
}

func execScan(w io.Writer, fsys rootfs.FS, spec *testplan.ScanSpec, meta *oci.ImageConfig) error {
	var patterns []scan.IgnorePattern
	for _, ig := range spec.Ignore {
		p, err := scan.ParseIgnorePattern(ig)
		if err != nil {
			return fmt.Errorf("invalid ignore pattern %q: %v", ig, err)
		}
		patterns = append(patterns, p)
	}

	var targetArch string
	if meta != nil {
		targetArch = meta.Architecture
	}

	report, err := scan.Run([]string{spec.Path}, scan.Options{
		Ignore:     patterns,
		Limit:      spec.Limit,
		NoRuntime:  spec.NoRuntime,
		FS:         fsys,
		TargetArch: targetArch,
	})
	if err != nil {
		return err
	}

	scan.PrintReport(w, report, "# ")

	if report.Summary.Errors > 0 {
		return fmt.Errorf("%d binaries with unresolved dependencies", report.Summary.Errors)
	}
	return nil
}

func execCommand(spec *testplan.CommandSpec) error {
	for _, args := range spec.Setup {
		if len(args) == 0 {
			continue
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("setup %v: %v", args, err)
		}
	}

	defer func() {
		for _, args := range spec.Teardown {
			if len(args) == 0 {
				continue
			}
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
		}
	}()

	cmd := exec.Command(spec.Command, spec.Args...)
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), envStrings(spec.Env)...)
	}
	stdout := &cappedWriter{cap: streamCaptureCap}
	stderr := &cappedWriter{cap: streamCaptureCap}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	exitCode := 0
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return fmt.Errorf("exec %s: %v", spec.Command, err)
		}
	}

	var failure error
	switch {
	case exitCode != spec.ExitCode:
		failure = fmt.Errorf("exit code: expected %d, got %d", spec.ExitCode, exitCode)
	default:
		if err := checkEmpty("stdout", stdout.total, spec.StdoutEmpty); err != nil {
			failure = err
		} else if err := checkEmpty("stderr", stderr.total, spec.StderrEmpty); err != nil {
			failure = err
		} else if err := matchPatterns("stdout", stdout.buf.Bytes(), spec.ExpectedOutput, spec.ExcludedOutput); err != nil {
			failure = err
		} else if err := matchPatterns("stderr", stderr.buf.Bytes(), spec.ExpectedError, spec.ExcludedError); err != nil {
			failure = err
		}
	}
	if failure == nil {
		return nil
	}
	return fmt.Errorf("%s\nexit code: %d\n%s\n%s",
		failure,
		exitCode,
		formatBody("stdout", stdout.buf.Bytes(), stdout.total),
		formatBody("stderr", stderr.buf.Bytes(), stderr.total),
	)
}

func envStrings(env []testplan.KV) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		out = append(out, e.Key+"="+e.Value)
	}
	return out
}

func matchPatterns(label string, content []byte, expected, excluded []string) error {
	if len(expected) == 0 && len(excluded) == 0 {
		return nil
	}
	if looksBinary(content) {
		return fmt.Errorf("%s: cannot match text patterns against binary content", label)
	}
	s := string(content)
	for _, pattern := range expected {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("invalid regex %q: %v", pattern, err)
		}
		if !re.MatchString(s) {
			return fmt.Errorf("expected %s pattern %q not found", label, pattern)
		}
	}
	for _, pattern := range excluded {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("invalid regex %q: %v", pattern, err)
		}
		if re.MatchString(s) {
			return fmt.Errorf("excluded %s pattern %q found", label, pattern)
		}
	}
	return nil
}

// cappedWriter wraps a bytes.Buffer with a hard byte cap. Writes past the
// cap are silently dropped — Write always returns len(p), nil so the
// writing process never sees a short write and never blocks on a full
// pipe. Total counts every byte that was ever offered, including dropped
// bytes, so callers can report "captured N of M".
type cappedWriter struct {
	buf   bytes.Buffer
	cap   int
	total int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.total += len(p)
	remaining := w.cap - w.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) <= remaining {
		w.buf.Write(p)
		return len(p), nil
	}
	w.buf.Write(p[:remaining])
	return len(p), nil
}

// formatBody renders captured bytes as a TAP-friendly diagnostic block.
//
//   - captured is what the runner has in memory.
//   - total is the original byte count from the source. May exceed
//     len(captured) when capture was capped (commands) or when an
//     undersized read was performed (files). Equal to len(captured)
//     when the whole source fit.
//
// Binary content (NUL byte in the first binarySniffLimit bytes) is
// rendered as a heading-only line — dumping binary into TAP comments
// garbles the log and rarely helps debugging.
//
// Otherwise, content longer than diagnosticBodyLimit is shown as
// head + tail with a "[truncated]" marker between, with sizes in the
// heading.
func formatBody(label string, captured []byte, total int) string {
	n := len(captured)
	if total == 0 {
		return fmt.Sprintf("--- %s (empty) ---", label)
	}

	dropped := total - n
	if dropped < 0 {
		dropped = 0
	}

	binary := looksBinary(captured)

	parts := []string{fmt.Sprintf("%d bytes", total)}
	var body string
	switch {
	case binary:
		parts = append(parts, "binary")
	case n > diagnosticBodyLimit:
		half := diagnosticBodyLimit / 2
		body = string(captured[:half]) + "\n... [truncated] ...\n" + string(captured[n-half:])
		parts = append(parts, fmt.Sprintf("%d truncated", n-2*half))
	default:
		body = string(captured)
	}
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d dropped", dropped))
	}

	heading := fmt.Sprintf("--- %s (%s) ---", label, strings.Join(parts, ", "))
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return heading
	}
	return heading + "\n" + body
}

func looksBinary(content []byte) bool {
	sniff := content
	if len(sniff) > binarySniffLimit {
		sniff = sniff[:binarySniffLimit]
	}
	return bytes.IndexByte(sniff, 0) >= 0
}

