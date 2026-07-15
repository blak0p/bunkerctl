package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// resetRoot clears flag-parse state on the shared rootCmd so successive tests
// in the same package do not inherit the previous Execute() run's flag values
// (e.g. --help leaving helpVal=true, which would shadow --version). It also
// resets the backup subcommand's help flag so a --help run does not leak into
// the next backup invocation.
func resetRoot() {
	rootCmd.SetArgs(nil)
	// Re-instantiate flag parsing state by poking the lazily-init flags.
	if rootCmd.Flags().Lookup("help") != nil {
		_ = rootCmd.Flags().Set("help", "false")
	}
	if rootCmd.Flags().Lookup("version") != nil {
		_ = rootCmd.Flags().Set("version", "false")
	}
	if backupCmd.Flags().Lookup("help") != nil {
		_ = backupCmd.Flags().Set("help", "false")
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

// TestVersion_DefaultIsRelease is a RED test (Slice 7): the default package
// Version MUST be a concrete release value (0.1.0), NOT the "0.0.0-dev"
// placeholder from early development. This is the first user-visible release
// of bunkerctl with the backup-core feature.
func TestVersion_DefaultIsRelease(t *testing.T) {
	if Version == "0.0.0-dev" || strings.Contains(Version, "dev") {
		t.Errorf("Version = %q, want a concrete release value (e.g. 0.1.0)", Version)
	}
	if !strings.HasPrefix(Version, "0.1.") {
		t.Errorf("Version = %q, want prefix %q for the first backup-core release", Version, "0.1.")
	}
}
