// Package ignore compiles path patterns (doublestar v4 syntax, including `**`)
// and answers whether a given relative path should be excluded from the backup
// file copy. It is the host-side defense-in-depth layer: the primary filter is
// tar's --exclude-from on the container side, but this package lets the host
// validate that no ignored path leaked through and lets callers merge the
// default 14 patterns (REQ-COPY-4) with user-supplied --ignore-extra entries.
package ignore

import (
	"path"

	"github.com/bmatcuk/doublestar/v4"
)

// defaultPatterns is the fixed REQ-COPY-4 list. Kept as a package-level value so
// DefaultPatterns can return a fresh copy, preventing callers from mutating the
// shared default.
//
// "/tmp" is intentionally last: it is an absolute-path pattern (the only one in
// the list) and is grouped after the relative-path/glob defaults to keep the
// user-facing defaults readable. Container-side temp files live under /tmp and
// must never be copied into a .bunker archive.
var defaultPatterns = []string{
	".cache",
	"node_modules",
	"target",
	".cargo/registry",
	".npm",
	".next",
	".git",
	"Downloads",
	".local/share/Trash",
	"__pycache__",
	".venv",
	"venv",
	"*.log",
	"*.tmp",
	"/tmp",
}

// DefaultPatterns returns a copy of the 15 default ignore patterns (REQ-COPY-4).
// The returned slice is safe to mutate without affecting the package default.
func DefaultPatterns() []string {
	out := make([]string, len(defaultPatterns))
	copy(out, defaultPatterns)
	return out
}

// Matcher holds a compiled set of patterns and answers ShouldIgnore queries.
// A Matcher is safe for concurrent use after construction: ShouldIgnore only
// reads the immutable pattern slice.
type Matcher struct {
	patterns []string
}

// NewMatcher validates each pattern with doublestar and returns a Matcher. A
// malformed pattern (e.g. an unclosed bracket) causes an error so a typo in the
// ignore list is surfaced at startup rather than silently never matching.
func NewMatcher(patterns []string) (*Matcher, error) {
	for _, p := range patterns {
		if !doublestar.ValidatePattern(p) {
			return nil, &InvalidPatternError{Pattern: p}
		}
	}
	cp := make([]string, len(patterns))
	copy(cp, patterns)
	return &Matcher{patterns: cp}, nil
}

// InvalidPatternError is returned by NewMatcher when a pattern fails doublestar
// validation.
type InvalidPatternError struct {
	Pattern string
}

func (e *InvalidPatternError) Error() string {
	return "invalid ignore pattern: " + e.Pattern
}

// ShouldIgnore reports whether the given relative path matches any compiled
// pattern. Matching is case-sensitive (doublestar is case-sensitive on Linux),
// supports `**` for recursive directory globs, and is anchored at the path root.
//
// A path matches if ANY of the following hold for a pattern P:
//   - the path equals P (literal match, e.g. "Downloads"),
//   - the path is inside the P directory tree (e.g. P=".cache", path=".cache/x"),
//   - doublestar matches P against the path or any of its ancestor directories.
//
// The ancestor-directory check is what makes a bare directory pattern like
// ".cache" exclude every file nested under it without requiring the user to
// write ".cache/**".
func (m *Matcher) ShouldIgnore(p string) bool {
	if p == "" {
		return false
	}
	p = path.Clean(p)
	for _, pat := range m.patterns {
		if matchPattern(pat, p) {
			return true
		}
	}
	return false
}

// matchPattern applies one pattern against the path and each of its ancestor
// directories. This is what turns a directory pattern like "node_modules" into a
// full-tree exclusion: the pattern matches the directory itself, and then
// ShouldIgnore returns true for every file whose path has that directory as an
// ancestor.
func matchPattern(pattern, p string) bool {
	// Direct match: the path itself is the pattern target (file or dir).
	if ok, _ := doublestar.Match(pattern, p); ok {
		return true
	}
	// Basename match: a pattern like "*.log" or "Downloads" matches a path
	// whose final element equals/matches the pattern, regardless of depth. This
	// is what makes a literal directory name like "Downloads" exclude every
	// directory of that name anywhere in the tree, and "*.log" exclude every
	// log file in any subdirectory.
	if base := path.Base(p); base != "." && base != "/" {
		if ok, _ := doublestar.Match(pattern, base); ok {
			return true
		}
	}
	// Ancestor match: walk up the path, testing each parent directory. If any
	// parent matches the pattern (by full path or by basename), the file lives
	// under an ignored directory. This is what turns a bare directory pattern
	// like "node_modules" into a full-tree exclusion: the pattern matches the
	// directory by basename, and every file nested under it is then ignored.
	cur := p
	for cur != "." && cur != "/" && cur != "" {
		parent := path.Dir(cur)
		if parent == cur {
			break
		}
		if ok, _ := doublestar.Match(pattern, parent); ok {
			return true
		}
		if parent != "." && parent != "/" {
			if ok, _ := doublestar.Match(pattern, path.Base(parent)); ok {
				return true
			}
		}
		cur = parent
	}
	return false
}

// MergePatterns merges defaults with extras, removing duplicates while
// preserving order: all defaults (in order) first, then any extra that is not
// already present. nil extras returns a copy of defaults.
func MergePatterns(defaults, extras []string) []string {
	seen := make(map[string]bool, len(defaults)+len(extras))
	out := make([]string, 0, len(defaults)+len(extras))
	for _, p := range defaults {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range extras {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}