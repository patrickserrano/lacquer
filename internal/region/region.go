// Package region implements the managed-region merge that lets lacquer sync
// shared content into a file between markers without touching project-owned text.
package region

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// startRe matches a start marker for the given key, capturing the version int.
func startRe(key string) *regexp.Regexp {
	return regexp.MustCompile(`<!-- lacquer:` + regexp.QuoteMeta(key) + `:start v(\d+) -->`)
}

func endMarker(key string) string {
	return fmt.Sprintf("<!-- lacquer:%s:end -->", key)
}

// StampedVersion returns the version recorded in the key's start marker, and
// whether such a marker was found.
func StampedVersion(content, key string) (int, bool) {
	m := startRe(key).FindStringSubmatch(content)
	if m == nil {
		return 0, false
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return v, true
}

// bodyRe captures the body between a key's start and end markers (the text
// render() wraps in markers). The capture excludes the newline that immediately
// follows the start marker and the one preceding the end marker.
func bodyRe(key string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?s)<!-- lacquer:` + regexp.QuoteMeta(key) + `:start v\d+ -->\n(.*)\n` +
			regexp.QuoteMeta(endMarker(key)))
}

// ExtractBody returns the current body of the key's managed block in content
// (the text between its markers, exactly as render() would have written it), and
// whether such a block was found. It lets a caller compare a project's on-disk
// region body against what the lacquer would render now.
func ExtractBody(content, key string) (string, bool) {
	m := bodyRe(key).FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// render produces a complete managed block for the key/version/body.
func render(key string, version int, body string) string {
	return fmt.Sprintf("<!-- lacquer:%s:start v%d -->\n%s\n<!-- lacquer:%s:end -->",
		key, version, body, key)
}

// blockRe matches an entire existing managed block (start marker through end
// marker, inclusive) for the given key.
func blockRe(key string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?s)<!-- lacquer:` + regexp.QuoteMeta(key) + `:start v\d+ -->.*?` +
			regexp.QuoteMeta(endMarker(key)))
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
func Merge(content, key string, version int, body string) (string, error) {
	startRegex := startRe(key)
	endM := endMarker(key)

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
		return appendBlock(content, key, version, body), nil
	case 1:
		loc := blockRe(key).FindStringIndex(content)
		if loc == nil {
			// Both markers present but not in start-before-end order.
			return "", fmt.Errorf("malformed lacquer:%s region (end marker precedes start)", key)
		}
		return content[:loc[0]] + render(key, version, body) + content[loc[1]:], nil
	default:
		return "", fmt.Errorf("malformed lacquer:%s region (%d duplicate blocks)", key, len(startLocs))
	}
}

// appendBlock adds a new managed block to the end of content, ensuring exactly
// one blank line of separation from any existing text and a trailing newline.
func appendBlock(content, key string, version int, body string) string {
	block := render(key, version, body) + "\n"
	if content == "" {
		return block
	}
	trimmed := strings.TrimRight(content, "\n")
	return trimmed + "\n\n" + block
}
