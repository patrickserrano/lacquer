// Package docsmirror keeps the site's rule pages honest about the rule files
// they mirror.
//
// Four pages under site/src/content/docs/guides/ restate, by hand, the contents
// of the profile rule files this lacquer ships. Nothing compared them, so they
// drifted silently: site/src/content/docs/guides/agent-rules.md went on
// documenting `exclude = ["lefthook.yml"]` for months after core/CLAUDE.core.md
// moved to the attributed `{ path, reason, until }` form, and the published docs
// handed readers config the tool no longer accepts.
//
// Two checks live here, deliberately different in strength:
//
//   - Unpaired reports a change that touched a source without its mirror. It
//     proves nothing about the content — it only removes the failure mode that
//     actually happened, which is a person editing one file and forgetting the
//     other.
//   - MissingBlocks compares the fenced code blocks. A mirror may say less than
//     its source, and may say it differently — the pages carry frontmatter, a
//     :::note banner, site-relative links and reworded prose on purpose. What it
//     may not do is show a line of code the source does not show, because a code
//     block is the part a reader copies verbatim, and the exclusion drift was
//     inside one.
//
// Prose is not compared at all. A gate that flags legitimate rewording gets
// disabled, and a disabled gate is the failure this repo keeps rediscovering.
package docsmirror

import (
	"fmt"
	"strings"
)

// Pair is one rule file and the site page that restates it.
type Pair struct {
	// Source is the shipped rule file, relative to the repository root. It is
	// the truth; the mirror follows it.
	Source string
	// Mirror is the site page that restates Source, relative to the repository
	// root.
	Mirror string
}

// Pairs is every source/mirror pair in this repository, and the single place
// the set is written down. The changed-together step in .github/workflows/ci.yml
// repeats it in shell, and TestWorkflowPairsMatchPairs fails if the two ever
// disagree — a drift checker that has itself drifted is worse than none.
var Pairs = []Pair{
	{Source: "core/CLAUDE.core.md", Mirror: "site/src/content/docs/guides/agent-rules.md"},
	{Source: "profiles/ios/CLAUDE.ios.md", Mirror: "site/src/content/docs/guides/ios-rules.md"},
	{Source: "profiles/web/CLAUDE.web.md", Mirror: "site/src/content/docs/guides/web-rules.md"},
	{Source: "profiles/supabase/CLAUDE.supabase.md", Mirror: "site/src/content/docs/guides/supabase-rules.md"},
}

// Unpaired returns one message per pair whose source appears in changed while
// its mirror does not.
//
// The check is deliberately one-directional. A mirror-only edit — fixing a
// typo, retitling the page, adjusting a site link — is legitimate and must not
// fail, so only source-without-mirror is reported. changed holds repository-root
// relative paths, as `git diff --name-only` prints them.
func Unpaired(changed []string) []string {
	touched := make(map[string]bool, len(changed))
	for _, p := range changed {
		if p = strings.TrimSpace(p); p != "" {
			touched[p] = true
		}
	}

	var msgs []string
	for _, p := range Pairs {
		if touched[p.Source] && !touched[p.Mirror] {
			msgs = append(msgs, fmt.Sprintf(
				"%s changed but %s did not. That page restates this file for readers of the published docs; leaving it behind is how it ended up documenting config the tool no longer accepts. Update the mirror in this change, or state in the PR why the edit does not reach it.",
				p.Source, p.Mirror))
		}
	}
	return msgs
}

// Block is one fenced code block lifted out of a markdown file.
type Block struct {
	// Info is the fence's info string, e.g. "toml". Recorded for the failure
	// message only; it is not compared, because a page is free to tag a snippet
	// `sh` where the source tagged it `bash`.
	Info string
	// Line is the 1-based line number of the opening fence, so a failure names
	// a place to look.
	Line int
	// Lines holds the block's content lines, normalized. Blank lines are
	// dropped.
	Lines []string
}

// CodeBlocks returns every fenced code block in md, in order.
//
// Only backtick fences are recognized, which is what these files use. A fence
// closes on a run of backticks at least as long as the one that opened it, so a
// block quoting a shorter fence inside itself stays one block.
func CodeBlocks(md string) []Block {
	var blocks []Block
	var cur *Block
	var fence string

	for i, raw := range strings.Split(md, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimLeft(line, " \t")
		ticks := countLeadingBackticks(trimmed)

		if cur == nil {
			if ticks >= 3 {
				fence = trimmed[:ticks]
				cur = &Block{Info: strings.TrimSpace(trimmed[ticks:]), Line: i + 1}
			}
			continue
		}

		// Inside a block: a closing fence is backticks-only and at least as long
		// as the opener.
		if ticks >= len(fence) && strings.TrimSpace(trimmed[ticks:]) == "" {
			blocks = append(blocks, *cur)
			cur = nil
			continue
		}
		if norm := normalizeLine(line); norm != "" {
			cur.Lines = append(cur.Lines, norm)
		}
	}

	// An unterminated fence still yields what it collected — a truncated block
	// is not a reason to silently check nothing.
	if cur != nil {
		blocks = append(blocks, *cur)
	}
	return blocks
}

func countLeadingBackticks(s string) int {
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}
	return n
}

// normalizeLine reduces a code line to the form the comparison uses: outer
// whitespace trimmed and internal runs of whitespace collapsed to one space.
//
// Collapsing is what lets the mirrors align a column differently from their
// source — supabase-rules.md pads its `comment on` example to a different width
// than CLAUDE.supabase.md does, and that is presentation, not a rule change.
func normalizeLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Finding is one line of code a mirror shows that its source does not.
type Finding struct {
	// Block is the mirror block the line came from.
	Block Block
	// Line is the offending line, normalized.
	Line string
}

// MissingBlocks returns every code line in mirror that does not appear in any
// code block of source.
//
// The comparison is line-level, not block-level, on purpose. A mirror routinely
// shows a shorter version of a source snippet — dropping an explanatory comment,
// or one of three example entries — and demanding whole-block equality would
// fail on all of that. Requiring each line it does show to exist in the source
// still catches the drift that matters: a value, flag or field the tool stopped
// accepting.
//
// Because it is one-directional, a source snippet the mirror omits entirely is
// not reported. That is the deliberate cost of letting a page be an abridgement.
func MissingBlocks(source, mirror string) []Finding {
	known := map[string]bool{}
	for _, b := range CodeBlocks(source) {
		for _, l := range b.Lines {
			known[l] = true
		}
	}

	var findings []Finding
	for _, b := range CodeBlocks(mirror) {
		for _, l := range b.Lines {
			if !known[l] {
				findings = append(findings, Finding{Block: b, Line: l})
			}
		}
	}
	return findings
}
