package shipped

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestShippedSuiteStaysFreeOfConcurrency guards the one assumption behind
// running this package WITHOUT the race detector in CI.
//
// `.github/workflows/ci.yml` runs `-race` over every package except this one,
// because -race costs 6.5x here (376s vs 58s, measured) and has no concurrent
// Go to inspect: this package orchestrates git and bash subprocesses and
// renders templates. That is true today. It is not true by construction, and
// nothing else would notice if it stopped being true — a goroutine added here
// later would simply never be race-checked, and the gap would be invisible
// because the suite would still pass.
//
// So this fails the moment the assumption breaks. If you genuinely need
// concurrency in this package, the fix is to put it back under -race in
// ci.yml, not to delete this test.
func TestShippedSuiteStaysFreeOfConcurrency(t *testing.T) {
	// Keyed on the language constructs, never on comments: a comment that
	// mentions a goroutine is not a goroutine, and a detector that reads prose
	// is the defect this repo keeps finding.
	banned := []struct {
		name string
		re   *regexp.Regexp
	}{
		{"goroutine launch (`go func(`)", regexp.MustCompile(`\bgo\s+func\s*\(`)},
		{"channel (`make(chan`)", regexp.MustCompile(`\bmake\s*\(\s*chan\b`)},
		{"sync.Mutex / sync.RWMutex", regexp.MustCompile(`\bsync\.(RW)?Mutex\b`)},
		{"sync.WaitGroup", regexp.MustCompile(`\bsync\.WaitGroup\b`)},
		{"sync/atomic", regexp.MustCompile(`\batomic\.[A-Z]`)},
		{"errgroup", regexp.MustCompile(`\berrgroup\.`)},
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	// This file defines the patterns, so its own regex literals match every one
	// of them. Scanning it would make the guard permanently red against itself.
	const self = "no_concurrency_test.go"

	var scanned int
	var findings []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || e.Name() == self {
			continue
		}
		body, err := os.ReadFile(filepath.Clean(e.Name()))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		scanned++
		for i, line := range strings.Split(string(body), "\n") {
			// Skip comment lines so a sentence ABOUT concurrency — including
			// the one in this file's own doc comment — is not a finding.
			if t := strings.TrimSpace(line); strings.HasPrefix(t, "//") {
				continue
			}
			for _, b := range banned {
				if b.re.MatchString(line) {
					findings = append(findings, e.Name()+":"+itoa(i+1)+"  "+b.name+"\n    "+strings.TrimSpace(line))
				}
			}
		}
	}

	// A scan that reads nothing must fail rather than report a clean result —
	// otherwise a move or a rename turns this guard into a silent pass.
	if scanned == 0 {
		t.Fatal("scanned zero .go files in internal/shipped; this guard is keyed on a layout that has changed, so it is now checking nothing")
	}

	if len(findings) > 0 {
		t.Errorf("internal/shipped has grown concurrency, but CI runs this package WITHOUT -race "+
			"(see .github/workflows/ci.yml, \"Test the e2e suite\"). Either drop the concurrency, "+
			"or move this package back into the race-tested set and delete this guard.\n  %s",
			strings.Join(findings, "\n  "))
	}
	t.Logf("scanned %d files, no concurrency primitives", scanned)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
