package suppress

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, files map[string]string) []Suppression {
	t.Helper()
	dir := t.TempDir()
	for p, b := range files {
		write(t, filepath.Join(dir, p), b)
	}
	got, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The distinction that makes the count meaningful. `disable:next` covers one
// line; a bare `disable` covers the rest of the file, so one of them can hide
// any number of violations.
func TestFileScopedIsDistinguishedFromLineScoped(t *testing.T) {
	got := scan(t, map[string]string{"A.swift": `
// swiftlint:disable force_try
let a = 1
// swiftlint:disable:next force_try
let b = 2
`})
	if len(got) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(got), got)
	}
	var file, line int
	for _, s := range got {
		switch s.Scope {
		case File:
			file++
		case Line:
			line++
		}
	}
	if file != 1 || line != 1 {
		t.Errorf("scope split = %d file / %d line, want 1/1: %+v", file, line, got)
	}
}

// A suppression carrying a reason is a decision; one carrying nothing is a
// mystery the next reader has to re-derive from the code.
func TestReasonIsExtractedHoweverItIsIntroduced(t *testing.T) {
	got := scan(t, map[string]string{"A.swift": `
// swiftlint:disable:next no_singleton_abuse - vendor SDK, no public init
let a = 1
// swiftlint:disable:next no_singleton_abuse: framework mandated
let b = 2
// swiftlint:disable:next no_singleton_abuse
let c = 2
`})
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if got[0].Reason != "vendor SDK, no public init" {
		t.Errorf("dash-introduced reason = %q", got[0].Reason)
	}
	if got[1].Reason != "framework mandated" {
		t.Errorf("colon-introduced reason = %q", got[1].Reason)
	}
	if got[2].Reason != "" {
		t.Errorf("a bare suppression must report no reason, got %q", got[2].Reason)
	}
}

func TestMultipleRulesOnOneLine(t *testing.T) {
	got := scan(t, map[string]string{"A.swift": "// swiftlint:disable:next force_try, line_length - both apply\nlet a = 1\n"})
	if len(got) != 1 || len(got[0].Rules) != 2 {
		t.Fatalf("rules = %+v", got)
	}
	if got[0].Rules[0] != "force_try" || got[0].Rules[1] != "line_length" {
		t.Errorf("rules = %v", got[0].Rules)
	}
	if got[0].Reason != "both apply" {
		t.Errorf("reason = %q", got[0].Reason)
	}
}

// A naive walk of one real project returned 5,634 hits, nearly all inside
// dependency checkouts and build output. Counting other people's suppressions
// as your own makes the number useless, and alarming.
func TestVendoredAndBuildDirectoriesAreSkipped(t *testing.T) {
	got := scan(t, map[string]string{
		"Sources/A.swift":                    "// swiftlint:disable:next force_try - mine\nlet a = 1\n",
		".build/checkouts/Dep/B.swift":       "// swiftlint:disable force_try\nlet b = 1\n",
		"DerivedData/C.swift":                "// swiftlint:disable force_try\nlet c = 1\n",
		".worktrees/feature/Sources/D.swift": "// swiftlint:disable force_try\nlet d = 1\n",
		"node_modules/e.ts":                  "// biome-ignore lint/style/noVar: x\nvar e = 1\n",
	})
	if len(got) != 1 {
		t.Fatalf("got %d suppressions, want 1 (only project source): %+v", len(got), got)
	}
	if got[0].Reason != "mine" {
		t.Errorf("wrong file matched: %+v", got[0])
	}
}

func TestBiomeIgnoreIsFound(t *testing.T) {
	got := scan(t, map[string]string{"src/a.ts": "// biome-ignore lint/suspicious/noExplicitAny: third-party shape\nconst a: any = 1\n"})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Scope != Line {
		t.Errorf("biome-ignore is single-line, got scope %q", got[0].Scope)
	}
	if got[0].Reason != "third-party shape" {
		t.Errorf("reason = %q", got[0].Reason)
	}
}

func TestSummariseCountsWhatNeedsADecision(t *testing.T) {
	sum := Summarise([]Suppression{
		{Rules: []string{"force_try"}, Scope: File},
		{Rules: []string{"force_try"}, Scope: Line, Reason: "ok"},
		{Rules: []string{"no_singleton_abuse"}, Scope: Line},
	})
	if sum.Total != 3 {
		t.Errorf("total = %d", sum.Total)
	}
	if sum.FileScoped != 1 {
		t.Errorf("file-scoped = %d, want 1", sum.FileScoped)
	}
	if sum.Unattributed != 2 {
		t.Errorf("unattributed = %d, want 2", sum.Unattributed)
	}
	if sum.ByRule["force_try"] != 2 {
		t.Errorf("by-rule = %v", sum.ByRule)
	}
	if top := sum.TopRules(1); len(top) != 1 || top[0] != "force_try" {
		t.Errorf("TopRules = %v, want the most-suppressed first", top)
	}
}

// A well-behaved project must produce nothing to report, or the line becomes
// permanent noise everywhere.
func TestAttributedLineScopedSuppressionsAreNotFlagged(t *testing.T) {
	sum := Summarise(scan(t, map[string]string{
		"A.swift": "// swiftlint:disable:next no_singleton_abuse - vendor SDK, injected below\nlet a = 1\n",
	}))
	if sum.FileScoped != 0 || sum.Unattributed != 0 {
		t.Errorf("a properly attributed line-scoped suppression must not be flagged: %+v", sum)
	}
	if sum.Total != 1 {
		t.Errorf("it should still be counted: %+v", sum)
	}
}
