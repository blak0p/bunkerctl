// Package preserve handles parsing and expanding preserve-list entries.
//
// Each entry in the user's preserve-list is either a literal path or a glob
// pattern. ParseLine classifies an entry; Expander.Expand resolves an entry to
// concrete filesystem paths, applying a default set of build-artifact/VCS
// exclusions when walking globs.
package preserve

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ErrGlobNoMatch is returned by Expander.Expand when a glob entry matched no
// files and no directories. Callers may treat this as a warning rather than a
// hard failure (the spec requires globs that match nothing to not fail the
// backup).
var ErrGlobNoMatch = errors.New("glob matched no files")

// Entry is a single classified preserve-list line.
type Entry struct {
	// Raw is the line as written, with surrounding whitespace trimmed. Empty
	// for blank lines and comments (callers skip those).
	Raw string
	// IsGlob is true when Raw contains any glob metacharacter (* ? [ {). When
	// false, Raw is treated as a literal path.
	IsGlob bool
}

// ParseLine trims whitespace and classifies a single preserve-list line. Blank
// lines and lines beginning with '#' return a zero Entry (Raw == "") for
// callers to skip. No error is returned for ordinary lines; the error return is
// reserved for future extension (e.g. malformed escape sequences).
func ParseLine(line string) (Entry, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return Entry{}, nil
	}
	if strings.HasPrefix(trimmed, "#") {
		return Entry{}, nil
	}
	return Entry{Raw: trimmed, IsGlob: hasGlobMeta(trimmed)}, nil
}

// hasGlobMeta reports whether s contains any glob metacharacter. The set is
// small and explicit: * ? [ { . Brace expansion and character classes are
// supported by doublestar.
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// defaultExclusions is the set of directory names skipped silently during glob
// expansion. Matching is case-insensitive so both "node_modules" and
// "Node_Modules" are excluded.
var defaultExclusions = []string{
	"node_modules",
	".git",
	"target",
	"dist",
	"__pycache__",
	"vendor",
	".cache",
}

// DefaultExclusions returns a copy of the default exclusion list. Exported for
// testability and so callers can surface to the user which dirs were skipped.
func DefaultExclusions() []string {
	out := make([]string, len(defaultExclusions))
	copy(out, defaultExclusions)
	return out
}

// isExcluded reports whether any path component (case-insensitive) matches a
// default exclusion name.
func isExcluded(p string) bool {
	parts := strings.Split(p, "/")
	excl := DefaultExclusions()
	for _, part := range parts {
		lower := strings.ToLower(part)
		for _, ex := range excl {
			if lower == ex {
				return true
			}
		}
	}
	return false
}

// Expander resolves preserve-list Entries to concrete filesystem paths.
type Expander struct {
	// FS is the filesystem to walk. Defaults to os.DirFS("/") in production; tests
	// inject a testing/fstest.MapFS.
	FS fs.FS
}

// Expand resolves a single Entry to concrete paths.
//
//   - A literal entry (IsGlob == false) returns [Raw] if the path exists in FS,
//     otherwise an empty slice and nil (missing literals are skipped silently).
//   - A glob entry (IsGlob == true) walks the FS using doublestar with recursive
//     ** support, skipping default-excluded directories. If no non-excluded match
//     is found, it returns an empty slice and ErrGlobNoMatch.
func (e Expander) Expand(entry Entry) ([]string, error) {
	if entry.Raw == "" {
		return nil, nil
	}
	if !entry.IsGlob {
		if exists(e.FS, entry.Raw) {
			return []string{entry.Raw}, nil
		}
		return nil, nil
	}
	var matches []string
	err := doublestar.GlobWalk(e.FS, entry.Raw, func(p string, d fs.DirEntry) error {
		if isExcluded(p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		matches = append(matches, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, ErrGlobNoMatch
	}
	return matches, nil
}

// exists reports whether a path names an existing file or directory in FS.
func exists(filesystem fs.FS, p string) bool {
	if p == "" {
		return false
	}
	// fs.FS paths are slash-separated and must not be absolute. Strip a leading
	// slash so an absolute literal still resolves on a chrooted test FS.
	rel := strings.TrimPrefix(p, "/")
	_, err := fs.Stat(filesystem, rel)
	return err == nil
}

