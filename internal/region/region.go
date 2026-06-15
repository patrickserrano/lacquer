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
