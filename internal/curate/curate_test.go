package curate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeEditorScript writes a tiny shell script that exits with a configured code
// and optionally appends a marker to the file so we can prove the editor was
// invoked on the right path. It returns the path to the script.
func fakeEditorScript(t *testing.T, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-editor.sh")
	content := "#!/bin/sh\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("writing fake editor: %v", err)
	}
	return script
}

// itoa avoids importing strconv for a one-off.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestShellEditor_EditExit0 verifies that when $EDITOR exits 0, Edit returns
// exit code 0 and no error (REQ-EDIT-1 happy path).
func TestShellEditor_EditExit0(t *testing.T) {
	editor := fakeEditorScript(t, 0)
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bunker.yaml")
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv("EDITOR", editor)
	e := ShellEditor{Fallback: "vi"}
	code, err := e.Edit(target)
	if err != nil {
		t.Fatalf("Edit error: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// TestShellEditor_EditExitNonZero verifies that when $EDITOR exits non-zero,
// Edit returns the non-zero exit code (REQ-EDIT-2: caller aborts on non-zero).
func TestShellEditor_EditExitNonZero(t *testing.T) {
	editor := fakeEditorScript(t, 1)
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bunker.yaml")
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv("EDITOR", editor)
	e := ShellEditor{Fallback: "vi"}
	code, err := e.Edit(target)
	if err != nil {
		t.Fatalf("Edit error: %v", err)
	}
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero (abort)")
	}
}

// TestShellEditor_EditorUnsetFallsBack verifies that when $EDITOR is unset,
// ShellEditor uses Fallback. We set Fallback to the fake script so the test
// does not actually launch vi.
func TestShellEditor_EditorUnsetFallsBack(t *testing.T) {
	editor := fakeEditorScript(t, 0)
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bunker.yaml")
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	os.Unsetenv("EDITOR")
	e := ShellEditor{Fallback: editor}
	code, err := e.Edit(target)
	if err != nil {
		t.Fatalf("Edit error: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (fallback editor)", code)
	}
}

// TestShellEditor_EditorEmptyFallsBack verifies that when $EDITOR is set but
// empty, ShellEditor uses Fallback (defensive: an empty EDITOR env var should
// not spawn an empty command).
func TestShellEditor_EditorEmptyFallsBack(t *testing.T) {
	editor := fakeEditorScript(t, 0)
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bunker.yaml")
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv("EDITOR", "")
	e := ShellEditor{Fallback: editor}
	code, err := e.Edit(target)
	if err != nil {
		t.Fatalf("Edit error: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (fallback when EDITOR empty)", code)
	}
}

// TestShellEditor_NoEditorNoFallback verifies that when neither $EDITOR nor
// Fallback is set, Edit returns ErrNoEditor rather than spawning a shell.
func TestShellEditor_NoEditorNoFallback(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bunker.yaml")
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	os.Unsetenv("EDITOR")
	e := ShellEditor{Fallback: ""}
	_, err := e.Edit(target)
	if err == nil {
		t.Fatalf("Edit err = nil, want ErrNoEditor")
	}
}

// TestShellEditor_PassesPathAsArg verifies the editor is invoked with the
// target path as its first argument. The fake script records the arg to a
// marker file we then inspect.
func TestShellEditor_PassesPathAsArg(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bunker.yaml")
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	marker := filepath.Join(tmp, "arg.txt")
	// Script writes $1 to the marker file then exits 0.
	script := filepath.Join(tmp, "fake-editor.sh")
	content := "#!/bin/sh\necho \"$1\" > \"" + marker + "\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("writing fake editor: %v", err)
	}
	t.Setenv("EDITOR", script)
	e := ShellEditor{Fallback: "vi"}
	if _, err := e.Edit(target); err != nil {
		t.Fatalf("Edit error: %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if string(got) != target+"\n" {
		t.Errorf("editor arg = %q, want %q", string(got), target)
	}
}

// TestShellEditor_NonExistentEditorErrors verifies that when $EDITOR points to
// a binary that does not exist, Edit returns an error (does not silently
// succeed).
func TestShellEditor_NonExistentEditorErrors(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bunker.yaml")
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv("EDITOR", filepath.Join(tmp, "does-not-exist"))
	e := ShellEditor{Fallback: ""}
	_, err := e.Edit(target)
	if err == nil {
		t.Fatalf("Edit(missing editor) err = nil, want error")
	}
}

// compileTimeCheck ensures exec is used (guards against accidental removal of
// the import in a refactor).
var _ = exec.Command