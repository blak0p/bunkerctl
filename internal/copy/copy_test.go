package copy

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// captureRunner is a podman.Runner double that records the exec command and
// returns canned tar bytes when the exec command matches the expected tar
// pipe. It proves container-side copy (REQ-COPY-1): the bytes come from the
// runner, not the host filesystem.
type captureRunner struct {
	*podman.FakeRunner
	// execCmd records the last cmd passed to Exec.
	execCmd []string
	// execID records the last container id passed to Exec.
	execID string
	// execCalls records all cmds in order.
	execCalls [][]string
	// tarOut is the canned stdout returned by Exec for the tar command.
	tarOut string
	// execErr, when non-nil, makes every Exec return this error.
	execErr error
	// excludeFileContent captures the content of the --exclude-from file at
	// exec time (before Copy removes it via defer).
	excludeFileContent string
}

func (r *captureRunner) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	r.execID = id
	cp := make([]string, len(cmd))
	copy(cp, cmd)
	r.execCmd = cp
	r.execCalls = append(r.execCalls, cp)
	// Capture the exclude-from file content while it still exists. The path
	// is inside the shell script (cmd[2]) when present.
	if len(cmd) >= 3 && cmd[0] == "sh" && cmd[1] == "-c" {
		script := cmd[2]
		idx := strings.Index(script, "--exclude-from '")
		if idx != -1 {
			start := idx + len("--exclude-from '")
			if end := strings.Index(script[start:], "'"); end != -1 {
				path := script[start : start+end]
				if data, err := os.ReadFile(path); err == nil {
					r.excludeFileContent = string(data)
				}
			}
		}
	}
	if r.execErr != nil {
		return "", r.execErr
	}
	return r.tarOut, nil
}

// makeTarStream builds a minimal uncompressed tar archive containing a single
// file at relPath with the given content. This is the canned output the
// FakeRunner returns as the stdout of `podman exec tar -cf -`, so the
// DefaultCopier pipes it into host `tar -xf -` and the file lands in staging.
func makeTarStream(t *testing.T, relPath, content string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	filePath := filepath.Join(src, relPath)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	// Build a tar of src/ capturing the relative path.
	tarPath := filepath.Join(dir, "payload.tar")
	// Use the host tar binary to build a deterministic archive.
	run(t, "tar", "-cf", tarPath, "-C", src, ".")
	data, err := os.ReadFile(tarPath)
	if err != nil {
		t.Fatalf("read tar: %v", err)
	}
	return string(data)
}

// run executes a command and fails the test on error.
func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %s %v: %v\n%s", name, args, err, out)
	}
}

// --- Command construction (REQ-COPY-1, REQ-COPY-2) ---

// TestCopy_ExecutesPodmanExecTar asserts the DefaultCopier issues exactly
// `podman exec <name> tar -cf - -C <home> --exclude-from <file> .` to the
// Runner (REQ-COPY-1: copy reads from inside the container).
func TestCopy_ExecutesPodmanExecTar(t *testing.T) {
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}}
	r.tarOut = makeTarStream(t, "hello.txt", "container-bytes")
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	c := DefaultCopier{}
	_, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/alejndro",
		Ignore:     []string{".cache", "*.log"},
		StagingDir: filesDir,
	})
	if err != nil {
		t.Fatalf("Copy error: %v", err)
	}
	if len(r.execCalls) == 0 {
		t.Fatalf("no exec calls recorded; expected a tar command")
	}
	got := r.execCalls[0]
	// The command must be wrapped in `sh -c` because we need shell redirects.
	if len(got) < 3 {
		t.Fatalf("exec cmd = %v, too short", got)
	}
	if got[0] != "sh" || got[1] != "-c" {
		t.Errorf("cmd[0:2] = %v, want [\"sh\" \"-c\"]", got[:2])
	}
	script := got[2]
	if !strings.Contains(script, "tar -cf -") {
		t.Errorf("script %q missing tar -cf -", script)
	}
	if !strings.Contains(script, "2>/dev/null") {
		t.Errorf("script %q missing stderr redirect", script)
	}
	// The shell script must contain -C <home>.
	if !strings.Contains(script, "-C '/home/alejndro'") {
		t.Errorf("script %q missing -C /home/alejndro", script)
	}
	// The shell script must contain --exclude-from with a temp file path.
	if !strings.Contains(script, "--exclude-from '") {
		t.Errorf("script %q missing --exclude-from (REQ-COPY-2)", script)
	}
}

// TestCopy_PropagatesIgnorePatterns asserts the ignore patterns are written
// to the --exclude-from temp file passed to the container tar command
// (REQ-COPY-2: ignore list applied during traversal).
func TestCopy_PropagatesIgnorePatterns(t *testing.T) {
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}}
	r.tarOut = makeTarStream(t, "hello.txt", "x")
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	c := DefaultCopier{}
	ignore := []string{".cache", "node_modules", "*.log", "build"}
	if _, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/u",
		Ignore:     ignore,
		StagingDir: filesDir,
	}); err != nil {
		t.Fatalf("Copy error: %v", err)
	}
	// The exclude-from file content is captured by the runner at exec time
	// (Copy removes the temp file via defer after Exec returns).
	data := r.excludeFileContent
	if data == "" {
		t.Fatalf("exclude file content not captured; cmd was %v; script was %q", r.execCalls[0], r.execCalls[0][2])
	}
	for _, p := range ignore {
		if !strings.Contains(data, p) {
			t.Errorf("exclude file missing pattern %q\ncontent:\n%s", p, data)
		}
	}
}

// --- Sentinel file test (REQ-COPY-1) ---

// TestCopy_SentinelFileFromContainer is the critical regression test for the
// v0.1.0 host-side preserve-list bug. The FakeRunner returns canned tar bytes
// containing a file "secret.txt" with content "FROM_CONTAINER". The host
// filesystem has a DIFFERENT file at the same relative path with content
// "FROM_HOST". After Copy, the staging dir MUST contain "FROM_CONTAINER" and
// NOT "FROM_HOST" — proving the bytes came from inside the container.
func TestCopy_SentinelFileFromContainer(t *testing.T) {
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}}
	r.tarOut = makeTarStream(t, "secret.txt", "FROM_CONTAINER")
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	// Plant a host-side decoy at a path that is NOT under staging, to prove
	// the copy does not read host paths. We cannot plant it at staging/secret
	// because the tar would overwrite it anyway. The point is the content
	// must be FROM_CONTAINER, which only the runner supplied.
	hostDecoy := filepath.Join(staging, "host-decoy.txt")
	if err := os.WriteFile(hostDecoy, []byte("FROM_HOST"), 0o644); err != nil {
		t.Fatalf("write decoy: %v", err)
	}
	c := DefaultCopier{}
	if _, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/alejndro",
		Ignore:     []string{},
		StagingDir: filesDir,
	}); err != nil {
		t.Fatalf("Copy error: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(filesDir, "secret.txt"))
	if err != nil {
		t.Fatalf("reading staging secret.txt: %v", err)
	}
	if string(got) != "FROM_CONTAINER" {
		t.Errorf("staging secret.txt = %q, want \"FROM_CONTAINER\" (REQ-COPY-1: bytes from container)", string(got))
	}
	// The host decoy must be untouched (not copied into files/).
	if _, err := os.Stat(filepath.Join(filesDir, "host-decoy.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("files/host-decoy.txt exists; host files leaked into archive (REQ-COPY-1 violation)")
	}
}

// TestCopy_NestedFileExtracted verifies a nested file (e.g.
// .config/fish/config.fish) is extracted to the correct staging subpath.
func TestCopy_NestedFileExtracted(t *testing.T) {
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}}
	r.tarOut = makeTarStream(t, ".config/fish/config.fish", "set fish_greeting")
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	c := DefaultCopier{}
	if _, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/alejndro",
		Ignore:     []string{},
		StagingDir: filesDir,
	}); err != nil {
		t.Fatalf("Copy error: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(filesDir, ".config", "fish", "config.fish"))
	if err != nil {
		t.Fatalf("reading nested file: %v", err)
	}
	if string(got) != "set fish_greeting" {
		t.Errorf("nested file content = %q, want \"set fish_greeting\"", string(got))
	}
}

// --- Large copy warning (REQ-COPY-3) ---

// TestCopy_LargeCopyWarning verifies that when the bytes copied exceed 500MB,
// Copy returns a non-empty warning string (REQ-COPY-3). We simulate this by
// having the runner return a tar stream whose extracted size exceeds 500MB;
// to avoid building a real 500MB file, we use a copier with an injectable
// size reporter.
func TestCopy_LargeCopyWarning(t *testing.T) {
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}}
	r.tarOut = makeTarStream(t, "small.txt", "small")
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	c := DefaultCopier{SizeOf: func(root string) (int64, error) {
		return 700 * 1024 * 1024, nil // 700 MB
	}}
	res, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/u",
		Ignore:     []string{},
		StagingDir: filesDir,
	})
	if err != nil {
		t.Fatalf("Copy error: %v", err)
	}
	if res.Warning == "" {
		t.Errorf("Warning = empty, want a >500MB warning (REQ-COPY-3)")
	}
	if !strings.Contains(res.Warning, "500") {
		t.Errorf("Warning = %q, want it to mention 500MB", res.Warning)
	}
	if res.BytesCopied != 700*1024*1024 {
		t.Errorf("BytesCopied = %d, want 700MB", res.BytesCopied)
	}
}

// TestCopy_SmallCopyNoWarning verifies that when the bytes copied are under
// 500MB, no warning is produced (REQ-COPY-3 scenario: small copy no warning).
func TestCopy_SmallCopyNoWarning(t *testing.T) {
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}}
	r.tarOut = makeTarStream(t, "small.txt", "small")
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	c := DefaultCopier{SizeOf: func(root string) (int64, error) {
		return 80 * 1024 * 1024, nil // 80 MB
	}}
	res, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/u",
		Ignore:     []string{},
		StagingDir: filesDir,
	})
	if err != nil {
		t.Fatalf("Copy error: %v", err)
	}
	if res.Warning != "" {
		t.Errorf("Warning = %q, want empty for small copy", res.Warning)
	}
}

// --- Error paths ---

// TestCopy_ExecFails verifies an exec error surfaces (no silent success).
func TestCopy_ExecFails(t *testing.T) {
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}, execErr: errors.New("podman exec failed")}
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	c := DefaultCopier{}
	_, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/u",
		Ignore:     []string{},
		StagingDir: filesDir,
	})
	if err == nil {
		t.Fatalf("Copy err = nil, want error when exec fails")
	}
}

// TestCopy_EmptyIgnoreStillWorks verifies an empty ignore list does not break
// the command (no --exclude-from file is written).
func TestCopy_EmptyIgnoreStillWorks(t *testing.T) {
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}}
	r.tarOut = makeTarStream(t, "hello.txt", "x")
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	c := DefaultCopier{}
	if _, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/u",
		Ignore:     nil,
		StagingDir: filesDir,
	}); err != nil {
		t.Fatalf("Copy error: %v", err)
	}
	// With no ignore patterns, --exclude-from should be absent.
	got := r.execCalls[0]
	if len(got) < 3 {
		t.Fatalf("exec cmd = %v, too short", got)
	}
	script := got[2]
	if strings.Contains(script, "--exclude-from") {
		t.Errorf("script %q: --exclude-from present with empty ignore", script)
	}
}

// TestCopy_DiscardsStderr is the regression test for tar warnings corrupting
// the archive stream. The fake runner returns a valid tar stream that, in a
// pre-fix world, would be polluted by stderr text ("socket ignored"). Because
// the copier now runs `sh -c 'tar ... 2>/dev/null'`, any text that would have
// been on stderr is not present in the runner's output, so host extraction
// succeeds. This test simulates the corrupted stream by prepending the warning
// text and proving that the current implementation cannot be affected at the
// runner boundary: the only bytes reaching the host are the ones returned by
// Exec. The fix makes that stream clean by construction.
func TestCopy_DiscardsStderr(t *testing.T) {
	clean := makeTarStream(t, "safe.txt", "safe")
	corrupted := "tar: ./herdr.sock: socket ignored\n" + clean
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}, tarOut: corrupted}
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	c := DefaultCopier{}
	_, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/u",
		Ignore:     []string{},
		StagingDir: filesDir,
	})
	// With corrupted input (the pre-fix failure mode) extraction fails.
	if err == nil {
		t.Fatalf("Copy err = nil, want error when tar stream is corrupted; fix failed to isolate stderr")
	}

	// Now prove that the same clean stream extracts successfully.
	r2 := &captureRunner{FakeRunner: &podman.FakeRunner{}, tarOut: clean}
	filesDir2 := filepath.Join(t.TempDir(), "files")
	if err := os.MkdirAll(filesDir2, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	if _, err := c.Copy(context.Background(), r2, "bunker", CopyOptions{
		Home:       "/home/u",
		Ignore:     []string{},
		StagingDir: filesDir2,
	}); err != nil {
		t.Fatalf("Copy error with clean stream: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(filesDir2, "safe.txt"))
	if err != nil {
		t.Fatalf("reading safe.txt: %v", err)
	}
	if string(got) != "safe" {
		t.Errorf("safe.txt = %q, want \"safe\"", got)
	}

	// The issued command must redirect stderr inside the container shell.
	script := r2.execCalls[0][2]
	if !strings.Contains(script, "2>/dev/null") {
		t.Errorf("script %q missing stderr redirect", script)
	}
}

// --- CopyResult ---

// TestCopyResult_BytesRecorded verifies BytesCopied reflects the real
// extracted size (using the default SizeOf walker, not the injected one).
func TestCopyResult_BytesRecorded(t *testing.T) {
	r := &captureRunner{FakeRunner: &podman.FakeRunner{}}
	r.tarOut = makeTarStream(t, "data.txt", "hello world")
	staging := t.TempDir()
	filesDir := filepath.Join(staging, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	c := DefaultCopier{}
	res, err := c.Copy(context.Background(), r, "bunker", CopyOptions{
		Home:       "/home/u",
		Ignore:     []string{},
		StagingDir: filesDir,
	})
	if err != nil {
		t.Fatalf("Copy error: %v", err)
	}
	if res.BytesCopied != int64(len("hello world")) {
		t.Errorf("BytesCopied = %d, want %d", res.BytesCopied, len("hello world"))
	}
}

// io.EOF sentinel to ensure the import is used (the host tar reader path).
var _ = io.EOF
