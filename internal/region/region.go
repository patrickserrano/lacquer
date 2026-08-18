// Package region implements the managed-region merge that lets lacquer sync
// shared content into a file between markers without touching project-owned text.
//
// The marker text is `lacquer:<key>:start v<version>` / `lacquer:<key>:end`,
// wrapped in whatever comment the destination file's syntax uses. Markdown
// (CLAUDE.md, AGENTS.md) wraps it in an HTML comment; a .gitignore wraps it in a
// `#` line comment. Only the wrapper varies — the merge, the version stamp and
// the fail-loud malformed-region checks are one implementation, because a second
// copy of them is how the two forms would drift apart.
package region

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/patrickserrano/lacquer/internal/version"
)

// stampPat matches either stamp form: the legacy bare counter (`v72`) or semver
// (`v0.73.0`). Defined once because it appears in three regexes — start-marker
// parsing, body extraction, and whole-block matching — and widening only some of
// them fails silently: ExtractBody would report every region as changed, and
// Merge would append a second block instead of replacing the existing one.
const stampPat = `v[0-9]+(?:\.[0-9]+\.[0-9]+)?`

// Syntax is how a destination file spells a comment: the text before a marker
// and the text after it. Everything else about a managed region is identical
// across file types.
type Syntax struct {
	Open  string // text preceding the marker, e.g. "<!-- "
	Close string // text following the marker, e.g. " -->" (empty for line comments)
}

// Markdown is the comment syntax for CLAUDE.md / AGENTS.md — the form every
// project in the fleet already carries on disk. Its rendered markers must stay
// byte-identical to what pre-Syntax lacquer wrote, or every project's regions
// would fail to match and get duplicated on the next sync.
var Markdown = Syntax{Open: "<!-- ", Close: " -->"}

// Hash is the comment syntax for files whose comments run to end of line:
// .gitignore, and anything else `#` shaped. Close is empty because a trailing
// ` -->` on a .gitignore line would be part of the comment, not a delimiter.
var Hash = Syntax{Open: "# ", Close: ""}

// marker renders one marker line's full text (wrapper included).
func (s Syntax) marker(inner string) string { return s.Open + inner + s.Close }

// markerRe renders a regex matching a marker whose inner text is the given
// pattern. The wrapper is quoted; inner is passed through as a pattern.
func (s Syntax) markerRe(innerPat string) string {
	return regexp.QuoteMeta(s.Open) + innerPat + regexp.QuoteMeta(s.Close)
}

// startRe matches a start marker for the given key, capturing the version stamp.
func (s Syntax) startRe(key string) *regexp.Regexp {
	return regexp.MustCompile(s.markerRe(`lacquer:` + regexp.QuoteMeta(key) + `:start (` + stampPat + `)`))
}

func (s Syntax) endMarker(key string) string {
	return s.marker(fmt.Sprintf("lacquer:%s:end", key))
}

// StampedVersion returns the version recorded in the key's start marker, and
// whether such a marker was found.
func (s Syntax) StampedVersion(content, key string) (version.Version, bool) {
	m := s.startRe(key).FindStringSubmatch(content)
	if m == nil {
		return version.Version{}, false
	}
	// A legacy `v72` stamp parses as 0.72.0, so it orders against current semver
	// versions rather than reading as absent.
	v, err := version.Parse(m[1])
	if err != nil {
		return version.Version{}, false
	}
	return v, true
}

// bodyRe captures the body between a key's start and end markers (the text
// render() wraps in markers). The capture excludes the newline that immediately
// follows the start marker and the one preceding the end marker.
func (s Syntax) bodyRe(key string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?s)` + s.markerRe(`lacquer:`+regexp.QuoteMeta(key)+`:start `+stampPat) + `\n(.*)\n` +
			regexp.QuoteMeta(s.endMarker(key)))
}

// ExtractBody returns the current body of the key's managed block in content
// (the text between its markers, exactly as render() would have written it), and
// whether such a block was found. It lets a caller compare a project's on-disk
// region body against what the lacquer would render now.
func (s Syntax) ExtractBody(content, key string) (string, bool) {
	m := s.bodyRe(key).FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// render produces a complete managed block for the key/version/body.
func (s Syntax) render(key string, v version.Version, body string) string {
	return s.marker(fmt.Sprintf("lacquer:%s:start v%s", key, v)) + "\n" +
		body + "\n" + s.endMarker(key)
}

// blockRe matches an entire existing managed block (start marker through end
// marker, inclusive) for the given key.
func (s Syntax) blockRe(key string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?s)` + s.markerRe(`lacquer:`+regexp.QuoteMeta(key)+`:start `+stampPat) + `.*?` +
			regexp.QuoteMeta(s.endMarker(key)))
}

// Merge replaces the managed block for key in content with a freshly rendered
// block at the given version and body. If no block exists yet, the block is
// appended. Project-owned text outside the block is never touched.
//
// Merge fails loud (returns an error, writes nothing) on any input it cannot
// represent safely: a body that itself contains a lacquer marker for this key
// (which would truncate on the next parse), an unbalanced number of start/end
// markers (a dangling marker), an end marker that precedes its start, or more
// than one block for the same key.
func (s Syntax) Merge(content, key string, v version.Version, body string) (string, error) {
	startRegex := s.startRe(key)
	endM := s.endMarker(key)

	// A body containing this key's markers is unrepresentable and would corrupt
	// the file on the next parse. Refuse rather than silently truncate.
	if strings.Contains(body, endM) || startRegex.MatchString(body) {
		return "", fmt.Errorf("lacquer:%s body contains a lacquer marker literal", key)
	}

	startLocs := startRegex.FindAllStringIndex(content, -1)
	endCount := strings.Count(content, endM)
	if len(startLocs) != endCount {
		return "", fmt.Errorf("malformed lacquer:%s region (%d start markers, %d end markers)",
			key, len(startLocs), endCount)
	}

	switch len(startLocs) {
	case 0:
		return s.appendBlock(content, key, v, body), nil
	case 1:
		loc := s.blockRe(key).FindStringIndex(content)
		if loc == nil {
			// Both markers present but not in start-before-end order.
			return "", fmt.Errorf("malformed lacquer:%s region (end marker precedes start)", key)
		}
		return content[:loc[0]] + s.render(key, v, body) + content[loc[1]:], nil
	default:
		return "", fmt.Errorf("malformed lacquer:%s region (%d duplicate blocks)", key, len(startLocs))
	}
}

// appendBlock adds a new managed block to the end of content, ensuring exactly
// one blank line of separation from any existing text and a trailing newline.
func (s Syntax) appendBlock(content, key string, v version.Version, body string) string {
	block := s.render(key, v, body) + "\n"
	if content == "" {
		return block
	}
	trimmed := strings.TrimRight(content, "\n")
	return trimmed + "\n\n" + block
}

// The package-level functions operate on Markdown, which is what every existing
// caller (sync, audit, status) means. Kept so the CLAUDE.md/AGENTS.md path reads
// exactly as it did before Syntax existed.

// StampedVersion is Markdown.StampedVersion.
func StampedVersion(content, key string) (version.Version, bool) {
	return Markdown.StampedVersion(content, key)
}

// ExtractBody is Markdown.ExtractBody.
func ExtractBody(content, key string) (string, bool) { return Markdown.ExtractBody(content, key) }

// Merge is Markdown.Merge.
func Merge(content, key string, v version.Version, body string) (string, error) {
	return Markdown.Merge(content, key, v, body)
}
