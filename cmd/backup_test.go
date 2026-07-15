package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestBackupCommand_Registered asserts the backup command is attached to the
// root command (its parent is rootCmd). RED: references backupCmd which does
// not exist yet.
func TestBackupCommand_Registered(t *testing.T) {
	if backupCmd.Parent() != rootCmd {
		t.Errorf("backupCmd.Parent() = %v, want rootCmd", backupCmd.Parent())
	}
}

// TestBackupCommand_Use triangulates: the registered command's Use string is
// "backup", proving the right command was wired.
func TestBackupCommand_Use(t *testing.T) {
	if backupCmd.Use != "backup" {
		t.Errorf("backupCmd.Use = %q, want %q", backupCmd.Use, "backup")
	}
	if !strings.Contains(backupCmd.Short, "Backup") {
		t.Errorf("backupCmd.Short = %q, want substring %q", backupCmd.Short, "Backup")
	}
}

// TestBackupCommand_NotYetImplemented asserts the placeholder Run prints the
// "not yet implemented" message. PR 2 replaces this with real behavior.
func TestBackupCommand_NotYetImplemented(t *testing.T) {
	resetRoot()
	buf := new(bytes.Buffer)
	backupCmd.SetOut(buf)
	backupCmd.SetErr(buf)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"backup"})

	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("backup returned error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "backup: not yet implemented") {
		t.Errorf("backup output = %q, want substring %q", got, "backup: not yet implemented")
	}
}