// Package retire decides which lacquer assets a retired project stops
// receiving, and renders the notice that says so.
//
// [project].retired means STOP THE SPEND, STAY CONSISTENT (see
// config.Retirement). Everything the lacquer ships keeps syncing except the
// things that cost money or attention on a timer:
//
//   - any workflow whose `on:` block carries a `schedule:` trigger, and
//   - .github/dependabot.yml, which is a schedule wearing different YAML.
//
// "Is scheduled" is derived from the workflow's CONTENT, never from a list of
// filenames. A filename list is correct exactly until someone adds the next
// scheduled workflow, at which point it silently does nothing — the class of
// bug this repository keeps finding, and the reason [project].optional_workflows
// errors on a name that matches no file instead of installing nothing.
//
// Nothing here deletes. A retired repository that already has these files keeps
// them; sync simply stops distributing and audit stops tracking them, which is
// the same contract [project].exclude has always had.
package retire

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/patrickserrano/lacquer/internal/config"
)

// Dependabot is the one non-workflow asset retirement drops. It is a schedule in
// every sense that matters — `schedule.interval` is a required key of every
// update entry — and a retired project that keeps it keeps getting PRs, review
// requests and CI runs for dependencies of an app nobody ships.
const Dependabot = ".github/dependabot.yml"

// workflowDir is where a GitHub Actions workflow has to live to be one. A file
// with a `schedule:` trigger anywhere else is not a workflow and is left alone.
const workflowDir = ".github/workflows"

// Drops reports whether a retired project stops receiving this asset. src is the
// asset's source path in the lacquer, dest its project-relative destination.
//
// An unreadable or unparseable workflow is an ERROR, not a "probably fine".
// Guessing here would either keep paying for a nightly job on a dead project or
// silently withhold a PR gate from a live one, and both failures are invisible.
// TestEveryShippedWorkflowClassifies covers every workflow this repo ships, so a
// new one that this cannot read fails in the lacquer's own CI rather than in a
// project's.
func Drops(src, dest string) (bool, error) {
	slash := filepath.ToSlash(dest)
	if slash == Dependabot {
		return true, nil
	}
	if path.Dir(slash) != workflowDir {
		return false, nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return false, fmt.Errorf("read workflow %s: %w", src, err)
	}
	scheduled, err := Scheduled(string(data))
	if err != nil {
		return false, fmt.Errorf("%s: %w", dest, err)
	}
	return scheduled, nil
}

// Scheduled reports whether a workflow's `on:` block declares a `schedule:`
// trigger.
//
// A workflow may declare several triggers — `workflow_dispatch:` beside
// `schedule:` is the norm in this repo, so a human can also run it on demand —
// and a scheduled one is dropped regardless. The dispatch entry is a convenience
// on top of the cron; the cron is the whole point, and it is what runs (and
// bills) unattended.
func Scheduled(content string) (bool, error) {
	block, err := onBlock(content)
	if err != nil {
		return false, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(block), &doc); err != nil {
		return false, fmt.Errorf("parse `on:` block: %w", err)
	}
	// yaml.v3 follows the 1.2 core schema, so the bare key `on` stays the string
	// "on" rather than resolving to the YAML 1.1 boolean. GitHub also accepts the
	// quoted spelling, which unquotes to the same key.
	switch v := doc["on"].(type) {
	case map[string]any:
		// on:
		//   schedule: [...]
		_, ok := v["schedule"]
		return ok, nil
	case []any:
		// on: [push, schedule]  — legal, and carries no cron, but a project
		// spelling it this way still means the workflow runs on a timer.
		for _, e := range v {
			if s, ok := e.(string); ok && s == "schedule" {
				return true, nil
			}
		}
		return false, nil
	case string:
		// on: schedule
		return v == "schedule", nil
	case nil:
		return false, fmt.Errorf("`on:` block is empty")
	default:
		return false, fmt.Errorf("`on:` is a %T, which is not a workflow trigger", v)
	}
}

// tokenLine is a lacquer placeholder standing alone at column 0, owning its own
// indentation — {{IOS_RELEASE_TAGS}} inside the release workflow's `on: push:
// tags:` is the live example. It is the one place a shipped template stops being
// parseable YAML on its own (see internal/tokens), so it is removed before the
// block is parsed rather than being allowed to truncate it.
//
// Dropping the line cannot manufacture a schedule: it only ever removes list
// items or mapping entries from a block whose KEYS are what this reads.
var tokenLine = regexp.MustCompile(`^\{\{[A-Z0-9_]+\}\}\s*$`)

// onBlock extracts the top-level `on:` key and its value as a standalone YAML
// document.
//
// Parsing the whole file is not an option: two shipped workflows are not valid
// YAML until their tokens are substituted, and substitution needs a project. The
// trigger block is the only part this package cares about, so it is cut out
// textually — bounded by column-0 keys, which is a property YAML guarantees for
// a top-level mapping — and then handed to a real parser. Comments, flow style,
// quoting and nesting inside the block are the parser's problem, not this
// function's.
func onBlock(content string) (string, error) {
	lines := strings.Split(content, "\n")
	start := -1
	for i, line := range lines {
		if onKey.MatchString(line) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("no top-level `on:` block")
	}
	out := []string{lines[start]}
	for _, line := range lines[start+1:] {
		switch {
		case tokenLine.MatchString(line):
			continue // a placeholder that owns its own indentation; not structure
		case strings.TrimSpace(line) == "":
			out = append(out, line)
		case line[0] == ' ' || line[0] == '\t' || line[0] == '#':
			out = append(out, line)
		default:
			// A non-blank, non-comment line at column 0 is the next top-level key.
			return strings.Join(out, "\n") + "\n", nil
		}
	}
	return strings.Join(out, "\n") + "\n", nil
}

// onKey matches the top-level trigger key in each spelling GitHub accepts. The
// quoted forms exist because `on` is a YAML 1.1 boolean, and some linters insist
// on quoting it.
var onKey = regexp.MustCompile(`^(on|"on"|'on'):`)

// Notice renders the banner for a retired project, or "" for a live one.
//
// It leads with the state and the date and says what is no longer happening,
// because the failure this guards against is a retired project being read as a
// healthy one — a short, clean report is exactly what a healthy project produces.
func Notice(cfg *config.Config) string {
	r := cfg.Project.Retired
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "RETIRED since %s — %s\n", r.Since, strings.TrimSpace(r.Reason))
	fmt.Fprintf(&b, "  Scheduled work is no longer synced: workflows with a `schedule:` trigger, and %s.\n", Dependabot)
	b.WriteString("  Everything else still syncs — PR CI, lint and format configs, agent rules,\n")
	b.WriteString("  .gitignore, .gitattributes —\n")
	b.WriteString("  so the repo stays consistent with the fleet. Retirement has no expiry; remove\n")
	b.WriteString("  [project].retired to bring the schedules back.\n")
	return b.String()
}
