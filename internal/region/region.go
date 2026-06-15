// Package region implements the managed-region merge that lets harness sync
// shared content into a file between markers without touching project-owned text.
package region

import (
	"fmt"
	"regexp"
	"strconv"
)

// startRe matches a start marker for the given key, capturing the version int.
func startRe(key string) *regexp.Regexp {
	return regexp.MustCompile(`<!-- harness:` + regexp.QuoteMeta(key) + `:start v(\d+) -->`)
}

func endMarker(key string) string {
	return fmt.Sprintf("<!-- harness:%s:end -->", key)
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

// render produces a complete managed block for the key/version/body.
func render(key string, version int, body string) string {
	return fmt.Sprintf("<!-- harness:%s:start v%d -->\n%s\n<!-- harness:%s:end -->",
		key, version, body, key)
}

// blockRe matches an entire existing managed block (start marker through end
// marker, inclusive) for the given key.
func blockRe(key string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?s)<!-- harness:` + regexp.QuoteMeta(key) + `:start v\d+ -->.*?` +
			regexp.QuoteMeta(endMarker(key)))
}

// Merge replaces the managed block for key in content with a freshly rendered
// block at the given version and body. If no block exists yet, the block is
// appended (see Task 4). Project-owned text outside the block is never touched.
func Merge(content, key string, version int, body string) (string, error) {
	if loc := blockRe(key).FindStringIndex(content); loc != nil {
		return content[:loc[0]] + render(key, version, body) + content[loc[1]:], nil
	}
	return appendBlock(content, key, version, body), nil
}

// appendBlock is a temporary placeholder; Task 4 replaces it with the real
// implementation.
func appendBlock(content, key string, version int, body string) string {
	return content
}
