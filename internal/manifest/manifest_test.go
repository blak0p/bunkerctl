package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTOMLWriter_WritesPackages verifies the writer produces a TOML file with a
// [packages] table and one entry per package.
func TestTOMLWriter_WritesPackages(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.toml")
	w := TOMLWriter{}
	if err := w.Write(p, []string{"vim", "curl", "git"}); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "[packages]") {
		t.Errorf("manifest = %q, want [packages] table", got)
	}
	// Each package must appear (as a key).
	for _, pkg := range []string{"vim", "curl", "git"} {
		if !strings.Contains(got, pkg) {
			t.Errorf("manifest = %q, want package %q", got, pkg)
		}
	}
}

// TestTOMLWriter_EmptyList triangulates: an empty package list writes a valid
// TOML file with an empty [packages] table and no error.
func TestTOMLWriter_EmptyList(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.toml")
	w := TOMLWriter{}
	if err := w.Write(p, []string{}); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "[packages]") {
		t.Errorf("manifest = %q, want [packages] table", got)
	}
}

// fakeEditor is a test double for Editor that rewrites the file with a known
// body, simulating the user editing and saving the manifest. It records the
// path it was asked to edit so tests can assert the editor was invoked on the
// expected path.
type fakeEditor struct {
	body    string
	called  bool
	calledPath string
}

func (f *fakeEditor) Edit(p string) (bool, error) {
	f.called = true
	f.calledPath = p
	if f.body != "" {
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}

// TestShellEditor_NoEditorReturnsError verifies: when $EDITOR is unset and no
// fallback is configured, the ShellEditor returns a clear "no editor
// configured" error, NOT a panic.
func TestShellEditor_NoEditorReturnsError(t *testing.T) {
	t.Setenv("EDITOR", "")
	e := ShellEditor{Fallback: ""}
	_, err := e.Edit("/tmp/nonexistent-manifest.toml")
	if err == nil {
		t.Fatalf("Edit with no editor returned nil error, want non-nil")
	}
	if !errors.Is(err, ErrNoEditor) {
		t.Errorf("Edit err = %v, want ErrNoEditor", err)
	}
}

// TestShellEditor_UsesEnvEditor triangulates: with $EDITOR set, the ShellEditor
// records that it would invoke that editor. Since we cannot run a real
// interactive editor in a test, we assert the editor resolution reads the env
// var by checking that ErrNoEditor is NOT returned when EDITOR is set.
func TestShellEditor_UsesEnvEditor(t *testing.T) {
	t.Setenv("EDITOR", "true-editor")
	// Create a temp file so the editor has a target. We use a fake command
	// that exits 0 immediately ("true") to simulate a successful edit.
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.toml")
	if err := os.WriteFile(p, []byte("[packages]\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	// "true" is universally available and exits 0, simulating a successful
	// editor session with no changes.
	t.Setenv("EDITOR", "true")
	e := ShellEditor{Fallback: ""}
	changed, err := e.Edit(p)
	if err != nil {
		t.Fatalf("Edit with EDITOR=true error: %v", err)
	}
	_ = changed // true command exits 0; changed flag is best-effort.
}

// TestShellEditor_FallbackVi triangulates: with $EDITOR unset and Fallback set
// to a harmless command, the editor does NOT return ErrNoEditor and exits 0.
func TestShellEditor_FallbackVi(t *testing.T) {
	t.Setenv("EDITOR", "")
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.toml")
	if err := os.WriteFile(p, []byte("[packages]\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	e := ShellEditor{Fallback: "true"}
	_, err := e.Edit(p)
	if err != nil {
		t.Fatalf("Edit with fallback error: %v", err)
	}
}

// TestRoundTrip_WriteThenParse verifies the manifest TOML written by TOMLWriter
// can be re-parsed into the same package list, which is what the backup
// pipeline does after the editor closes.
func TestRoundTrip_WriteThenParse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.toml")
	w := TOMLWriter{}
	want := []string{"vim", "curl", "git"}
	if err := w.Write(p, want); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Read len = %d %v, want %d", len(got), got, len(want))
	}
	for i, pkg := range want {
		if got[i] != pkg {
			t.Errorf("Read[%d] = %q, want %q", i, got[i], pkg)
		}
	}
}

