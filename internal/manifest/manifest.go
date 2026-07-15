// Package manifest writes and edits the editable package manifest (TOML).
//
// The backup pipeline writes the detected package list to a TOML file, opens
// it in the user's $EDITOR (falling back to vi), waits for the editor to exit,
// and re-parses the file so the user can curate the list before archive
// production.
package manifest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrNoEditor is returned when neither $EDITOR nor a fallback editor is
// configured, so the manifest cannot be opened for editing.
var ErrNoEditor = errors.New("no editor configured")

// Writer writes the package manifest to a TOML file.
type Writer interface {
	Write(path string, pkgs []string) error
}

// Reader reads (re-parses) the package manifest from a TOML file.
type Reader interface {
	Read(path string) ([]string, error)
}

// TOMLWriter writes a [packages] TOML table with one key per package (value
// is an empty string, recording presence only).
type TOMLWriter struct{}

// Compile-time guarantees.
var (
	_ Writer = TOMLWriter{}
	_ Reader = TOMLReader{}
)

// Write writes the manifest to path. Each package becomes a TOML key under
// [packages]; the value is the empty string (a placeholder recording presence,
// per the design's "pkgname = \"\"" convention).
func (TOMLWriter) Write(path string, pkgs []string) error {
	var b []byte
	b = append(b, []byte("[packages]\n")...)
	for _, pkg := range pkgs {
		// TOML bare keys allow alphanumerics, underscores, and hyphens. We
		// quote anything else to stay valid.
		if isBareKey(pkg) {
			b = append(b, []byte(pkg)...)
		} else {
			b = append(b, '"')
			b = append(b, []byte(pkg)...)
			b = append(b, '"')
		}
		b = append(b, []byte(" = \"\"\n")...)
	}
	return os.WriteFile(path, b, 0o644)
}

// isBareKey reports whether s is a valid TOML bare key (letters, digits,
// underscore, hyphen).
func isBareKey(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// TOMLReader re-parses a manifest TOML file into a package-name slice.
type TOMLReader struct{}

// Read parses the [packages] table from path and returns the package names in
// declaration order.
func (TOMLReader) Read(path string) ([]string, error) {
	return Read(path)
}

// Read parses the [packages] table from a TOML file and returns the package
// names in declaration order. Exported so the backup pipeline can re-read the
// manifest after the editor closes without constructing a TOMLReader value.
func Read(path string) ([]string, error) {
	var doc struct {
		Packages map[string]string `toml:"packages"`
	}
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		return nil, fmt.Errorf("parsing manifest %s: %w", path, err)
	}
	// toml.Decode populates a map, which is unordered. For deterministic output
	// we re-read the file lines to preserve declaration order.
	ordered, err := readOrderedPackages(path)
	if err != nil {
		// Fall back to map keys if ordered read fails.
		pkgs := make([]string, 0, len(doc.Packages))
		for k := range doc.Packages {
			pkgs = append(pkgs, k)
		}
		return pkgs, nil
	}
	return ordered, nil
}

// readOrderedPackages reads the TOML file line by line and extracts package
// names under the [packages] table in declaration order. This preserves the
// user's ordering after editing.
func readOrderedPackages(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pkgs []string
	inPackages := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] == '#' {
			continue
		}
		if trimmed[0] == '[' {
			inPackages = trimmed == "[packages]"
			continue
		}
		if !inPackages {
			continue
		}
		// A package line looks like:  name = ""  or  "name" = ""
		name := parseKey(trimmed)
		if name != "" {
			pkgs = append(pkgs, name)
		}
	}
	return pkgs, nil
}

// parseKey extracts the TOML key from a line of the form `key = "value"` or
// `"quoted key" = "value"`. Returns the unquoted key, or empty if the line is
// not a key-value pair.
func parseKey(line string) string {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return ""
	}
	key := strings.TrimSpace(line[:eq])
	if key == "" {
		return ""
	}
	if key[0] == '"' && len(key) >= 2 && key[len(key)-1] == '"' {
		return key[1 : len(key)-1]
	}
	return key
}

// Editor opens the manifest in the user's editor and waits for it to exit.
type Editor interface {
	Edit(path string) (changed bool, err error)
}

// ShellEditor resolves the editor from $EDITOR, falling back to a configured
// Fallback (e.g. "vi") when unset. When neither is set, Edit returns
// ErrNoEditor.
type ShellEditor struct {
	// Fallback is the editor used when $EDITOR is unset. Defaults to "vi" in
	// production (left empty in tests to assert the no-editor error path).
	Fallback string
}

// Compile-time guarantee.
var _ Editor = ShellEditor{}

// Edit resolves the editor command, runs it with path as its sole argument,
// and waits for it to exit. Returns ErrNoEditor when no editor is available.
// The changed flag is best-effort (true after a successful exit); callers that
// care about diffing should compare file contents before/after.
func (e ShellEditor) Edit(path string) (bool, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = e.Fallback
	}
	if editor == "" {
		return false, ErrNoEditor
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("running editor %q: %w", editor, err)
	}
	return true, nil
}