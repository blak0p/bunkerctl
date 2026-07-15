package podman

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestValidateContainerName_RejectsEmpty is the RED anchor for the threat
// matrix: an empty container name/ID MUST be rejected with
// ErrInvalidContainerName. validateContainerName does not exist yet.
func TestValidateContainerName_RejectsEmpty(t *testing.T) {
	if err := validateContainerName(""); !errors.Is(err, ErrInvalidContainerName) {
		t.Errorf("validateContainerName(\"\") err = %v, want ErrInvalidContainerName", err)
	}
}

// TestValidateContainerName_RejectsInjectionChars triangulates the injection
// threat matrix: each shell metacharacter MUST be rejected.
func TestValidateContainerName_RejectsInjectionChars(t *testing.T) {
	bad := []string{
		"foo;rm -rf /",
		"foo&bar",
		"foo|bar",
		"foo`whoami`",
		"foo$HOME",
		"foo(echo)",
		"foo)bar",
		"foo<bar",
		"foo>bar",
		"foo\nbar",
		"foo\rbar",
	}
	for _, name := range bad {
		if err := validateContainerName(name); !errors.Is(err, ErrInvalidContainerName) {
			t.Errorf("validateContainerName(%q) err = %v, want ErrInvalidContainerName", name, err)
		}
	}
}

// TestValidateContainerName_RejectsTooLong triangulates the length boundary:
// names exceeding 256 chars MUST be rejected (Podman constraint).
func TestValidateContainerName_RejectsTooLong(t *testing.T) {
	long := strings.Repeat("a", 257)
	if err := validateContainerName(long); !errors.Is(err, ErrInvalidContainerName) {
		t.Errorf("validateContainerName(257 chars) err = %v, want ErrInvalidContainerName", err)
	}
}

// TestValidateContainerName_AcceptsValidNames triangulates the happy paths:
// alphanumeric, '-', '_', '.', and a full 64-hex container ID.
func TestValidateContainerName_AcceptsValidNames(t *testing.T) {
	valid := []string{
		"mybunker",
		"dev-env_1",
		"bunker.local",
		"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}
	for _, name := range valid {
		if err := validateContainerName(name); err != nil {
			t.Errorf("validateContainerName(%q) err = %v, want nil", name, err)
		}
	}
}

// TestValidateContainerName_AcceptsMaxLength triangulates the exact length
// boundary: 256 chars is still valid (only 257+ is rejected).
func TestValidateContainerName_AcceptsMaxLength(t *testing.T) {
	max := strings.Repeat("a", 256)
	if err := validateContainerName(max); err != nil {
		t.Errorf("validateContainerName(256 chars) err = %v, want nil", err)
	}
}

// TestCLIRunner_Inspect_RejectsInvalidName proves the threat matrix is wired
// end-to-end: CLIRunner methods MUST validate the container name before any
// exec call and return ErrInvalidContainerName on bad input.
func TestCLIRunner_Inspect_RejectsInvalidName(t *testing.T) {
	r := &CLIRunner{bin: "podman", exec: &fakeBackend{out: "{}"}}
	_, err := r.Inspect(context.Background(), "foo;rm -rf /")
	if !errors.Is(err, ErrInvalidContainerName) {
		t.Errorf("Inspect(inject) err = %v, want ErrInvalidContainerName", err)
	}
}

// TestCLIRunner_Exec_RejectsInvalidName triangulates the wiring to Exec.
func TestCLIRunner_Exec_RejectsInvalidName(t *testing.T) {
	r := &CLIRunner{bin: "podman", exec: &fakeBackend{out: ""}}
	if _, err := r.Exec(context.Background(), "foo`bar", []string{"ls"}); !errors.Is(err, ErrInvalidContainerName) {
		t.Errorf("Exec(inject) err = %v, want ErrInvalidContainerName", err)
	}
}