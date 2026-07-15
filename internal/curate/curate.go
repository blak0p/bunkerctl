// Package curate opens the generated bunker.yaml in the user's $EDITOR before
// the backup archive is finalized (REQ-EDIT-1). A non-zero editor exit signals
// the user aborted; the caller is responsible for cleaning up staging and
// aborting the backup (REQ-EDIT-2). The --no-edit flag skips curation
// entirely (REQ-EDIT-3) — the caller simply does not invoke Edit.
package curate

import (
	"errors"
	"os"
	"os/exec"
)

// Editor opens a file in the user's $EDITOR and waits for it to exit.
type Editor interface {
	// Edit opens path in the editor and blocks until it exits. Returns the
	// exit code (0 = success, non-zero = user aborted) and an error only when
	// the editor could not be started (not when it exits non-zero).
	Edit(path string) (exitCode int, err error)
}

// ErrNoEditor is returned when neither $EDITOR nor Fallback is configured.
var ErrNoEditor = errors.New("no editor configured: set $EDITOR or provide a fallback")

// ShellEditor resolves $EDITOR at Edit time, falling back to Fallback when
// $EDITOR is unset or empty. The default Fallback is "vi".
type ShellEditor struct {
	Fallback string
}

// Edit opens path in the resolved editor and waits. It returns the editor's
// exit code and an error only when the editor binary cannot be started (e.g.
// not found on PATH). A non-zero exit is NOT an error from Edit's perspective
// — it is a legitimate "user aborted" signal the caller checks via the exit
// code (REQ-EDIT-2).
func (e ShellEditor) Edit(path string) (int, error) {
	editor := resolveEditor(os.Getenv("EDITOR"), e.Fallback)
	if editor == "" {
		return 1, ErrNoEditor
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// A non-zero exit is reported as an *exec.ExitError; surface its code.
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		// Any other error (binary not found, etc.) is a real failure.
		return 1, err
	}
	return 0, nil
}

// resolveEditor picks $EDITOR when non-empty, else fallback. Returns empty
// when neither is set.
func resolveEditor(env, fallback string) string {
	if env != "" {
		return env
	}
	return fallback
}