package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a small helper to create a file with given content in a temp dir.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestFileLoader_MissingFile_ReturnsNotFound verifies the first-time-user
// behavior: a missing config file yields an empty Config and ErrConfigNotFound
// (callers may treat that as "use defaults"), NOT a hard error.
func TestFileLoader_MissingFile_ReturnsNotFound(t *testing.T) {
	loader := FileLoader{Path: filepath.Join(t.TempDir(), "does-not-exist.toml")}
	cfg, err := loader.Load()
	if !errors.Is(err, ErrConfigNotFound) {
		t.Errorf("Load(missing) err = %v, want ErrConfigNotFound", err)
	}
	if len(cfg.Preserve) != 0 {
		t.Errorf("Load(missing) Preserve = %v, want empty", cfg.Preserve)
	}
}

// TestFileLoader_ValidFile_TwoEntries triangulates: a real TOML with two
// preserve entries is parsed into a Config with exactly those entries.
func TestFileLoader_ValidFile_TwoEntries(t *testing.T) {
	p := writeFile(t, t.TempDir(), "config.toml", `preserve = ["~/projects/**", "/etc/bunkerctl/notes"]
`)
	loader := FileLoader{Path: p}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load(valid) error: %v", err)
	}
	if len(cfg.Preserve) != 2 {
		t.Fatalf("Load(valid) Preserve len = %d, want 2", len(cfg.Preserve))
	}
	if cfg.Preserve[0] != "~/projects/**" {
		t.Errorf("Load(valid) Preserve[0] = %q, want %q", cfg.Preserve[0], "~/projects/**")
	}
	if cfg.Preserve[1] != "/etc/bunkerctl/notes" {
		t.Errorf("Load(valid) Preserve[1] = %q, want %q", cfg.Preserve[1], "/etc/bunkerctl/notes")
	}
}

// TestFileLoader_EmptyFile_NoPreserveKey triangulates: a file with no preserve
// key returns a Config with nil/empty Preserve and no error.
func TestFileLoader_EmptyFile_NoPreserveKey(t *testing.T) {
	p := writeFile(t, t.TempDir(), "config.toml", `# just a comment, no preserve key
`)
	loader := FileLoader{Path: p}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load(empty) error: %v", err)
	}
	if len(cfg.Preserve) != 0 {
		t.Errorf("Load(empty) Preserve = %v, want empty", cfg.Preserve)
	}
}

// TestFileLoader_MalformedTOML triangulates: a malformed TOML file MUST return
// a parse error, NOT ErrConfigNotFound.
func TestFileLoader_MalformedTOML(t *testing.T) {
	p := writeFile(t, t.TempDir(), "config.toml", `preserve = [oops not valid toml
`)
	loader := FileLoader{Path: p}
	_, err := loader.Load()
	if err == nil {
		t.Fatalf("Load(malformed) err = nil, want parse error")
	}
	if errors.Is(err, ErrConfigNotFound) {
		t.Errorf("Load(malformed) err = ErrConfigNotFound, want a parse error")
	}
}

// TestFileLoader_EmptyPreserveArray triangulates: an explicit empty array is a
// valid empty preserve-list.
func TestFileLoader_EmptyPreserveArray(t *testing.T) {
	p := writeFile(t, t.TempDir(), "config.toml", `preserve = []
`)
	loader := FileLoader{Path: p}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load(empty-array) error: %v", err)
	}
	if len(cfg.Preserve) != 0 {
		t.Errorf("Load(empty-array) Preserve = %v, want empty", cfg.Preserve)
	}
}