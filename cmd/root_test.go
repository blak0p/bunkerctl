package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// resetRoot clears flag-parse state on the shared rootCmd so successive tests
// in the same package do not inherit the previous Execute() run's flag values
// (e.g. --help leaving helpVal=true, which would shadow --version).
func resetRoot() {
	rootCmd.SetArgs(nil)
	// Re-instantiate flag parsing state by poking the lazily-init flags.
	if rootCmd.Flags().Lookup("help") != nil {
		_ = rootCmd.Flags().Set("help", "false")
	}
	if rootCmd.Flags().Lookup("version") != nil {
		_ = rootCmd.Flags().Set("version", "false")
	}
}

// TestRootCommand_Help verifies the root command prints help text when invoked
// with --help. This is a unit test for the cobra root command wiring.
func TestRootCommand_Help(t *testing.T) {
	resetRoot()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--help"})

	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("root --help returned error: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "bunkerctl") {
		t.Errorf("help output = %q, want substring %q", got, "bunkerctl")
	}
	if !strings.Contains(got, "Backup and restore Podman distrobox bunkers") {
		t.Errorf("help output = %q, want short description substring", got)
	}
}

// TestRootCommand_Version triangulates the root command: --version prints the
// command's Version field, which is initialized from the package-level Version
// variable (overridable at link time via -ldflags).
func TestRootCommand_Version(t *testing.T) {
	// Mirror the real link-time override path: the package var seeds the
	// command field, and --version prints that field.
	rootCmd.Version = "9.9.9-test"
	t.Cleanup(func() { rootCmd.Version = Version })

	resetRoot()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--version"})

	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("root --version returned error: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "9.9.9-test") {
		t.Errorf("version output = %q, want substring %q", got, "9.9.9-test")
	}
}