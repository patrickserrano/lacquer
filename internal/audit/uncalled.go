package audit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/lock"
)

// UncalledScript is a script the lacquer ships into a project that nothing this
// project renders or owns will ever run.
//
// The measured case is scripts/write-release-config.sh. It is shipped to every
// iOS project, profiles/ios/CLAUDE.ios.md says "`release.yml` then runs
// `scripts/write-release-config.sh`", and profiles/ios/doctor.toml runs it
// against known-bad input on every `lacquer doctor` — so it is present,
// documented, and demonstrably working. Its only real caller is rendered by
// tokens.ProductSecrets, which emits nothing at all unless some [[product]]
// declares `secrets`. For a project that declares none, the file ships, the
// documentation describes it running, and the release workflow never names it.
// That is the defect class in lacquer#333 exactly: a thing that READS as working
// machinery whose working state was never reached.
//
// Reported, never gated. A dead script endangers nothing today — it is dead
// weight plus a documentation lie — and the repo's argument for reporting an
// orphan or a stale exclusion without failing on it applies here unchanged: a
// gate on something harmless is the reliable way to teach people this output is
// noise to route around.
type UncalledScript struct {
	// Dest is the project-relative path of the shipped script.
	Dest string
	// Documented is every managed document whose prose names this script,
	// labelled the way .lacquer.lock keys them ("CLAUDE.md#ios" for a region).
	//
	// This is the sting rather than a detail. A script nobody mentions is merely
	// unused; a script the lacquer's own documentation describes as running is a
	// statement the repository makes and does not keep, and it is what made the
	// write-release-config.sh case survive review for as long as it did.
	Documented []string
	// Unconfirmable is why the sweep could not answer for this script, and "" when
	// it could.
	//
	// The distinction is the whole reason this type has three states rather than
	// two. "I looked everywhere and found no caller" and "I could not finish
	// looking" are different findings with different remedies, and collapsing the
	// second into the first — reporting a script as fine because the search came
	// up empty — is the same defect the detector exists to catch, one level up.
	// internal/testtargets draws the same line for [[project.covered_elsewhere]]
	// and for the same reason.
	Unconfirmable string
}

// Confirmed reports whether this finding is an answer rather than a shrug.
func (u UncalledScript) Confirmed() bool { return u.Unconfirmable == "" }

// scriptSegment is the one path segment that marks a shipped file as a script.
//
// Every script the lacquer ships today sits under one: scripts/,
// .github/scripts/, .claude/scripts/. Keyed on the directory rather than on an
// extension list because the extensions genuinely vary (.sh, .py, .js) and are
// not the thing that makes a file a script — being placed where a caller expects
// to find one is.
const scriptSegment = "scripts"

// UncalledScripts returns every script this project's plan ships that nothing in
// the project will run.
//
// WHAT THIS RENDERS RATHER THAN GREPS, and why it has to. A caller is not in the
// profile source. tokens.ProductSecrets builds the write-release-config.sh
// invocation as a string and substitutes it into release.yml at
// {{IOS_PRODUCT_SECRETS}}, only for products declaring secrets, gated behind an
// `if: matrix.product.name == …`. A grep for "write-release-config.sh" across
// profiles/ finds it in CLAUDE.ios.md and doctor.toml and in no workflow at all,
// so it is a false positive on every project that does declare secrets and a
// false negative for the doctor probe; a grep of the project's checkout instead
// answers for whatever was last synced rather than for what the manifest now
// asks for. The only honest input is the bytes this manifest would produce,
// which is what managed() already computes for Classify.
//
// WHAT COUNTS AS A CALLER, and why it is defined by exclusion. Anything rendered
// or owned by the project that is not documentation and not a declaration.
// Naming the caller kinds — "a workflow, a hook config, .claude/settings.json" —
// is the mistake #319 shipped: it keyed on the managed step's NAME and reported
// a project whose hand-rolled step did the job correctly as broken. The fix
// there was to ask whether the declared path was written by ANYTHING. The same
// move here means a profile that adds a Makefile, a new hook runner, or a
// project that calls a synced script from its own CI is counted without anyone
// remembering to extend a list.
//
// WHAT IT DOES NOT PROVE, and no wording in the report should imply otherwise:
// that a caller that exists ever RUNS. A caller behind a false `if:`, in a job
// nothing triggers, or in a workflow whose own result is required by nothing,
// satisfies this completely. The check separates "shipped with no caller at all"
// from "wired up"; it says nothing about whether the wiring carries current.
// A caller assembled at run time — `bash "$SCRIPT"`, a path built from a
// variable — is invisible to it and would read as no caller. No profile does
// that today, which is why it is stated here rather than guessed at.
func UncalledScripts(lacquerRoot, projectRoot string) ([]UncalledScript, error) {
	cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	units, _, err := managed(lacquerRoot, projectRoot)
	if err != nil {
		return nil, err
	}

	skillDirs := assets.SkillDirs(cfg)
	body := map[string]string{}
	isManaged := map[string]bool{}
	var scripts []string
	var callers []source
	var docs []source

	for _, u := range units {
		dest := filepath.ToSlash(u.dest)
		switch {
		case u.kind == "region":
			// A region lives inside a file the project otherwise owns, so the
			// file is NOT marked managed here: only the marked block is the
			// lacquer's, and the rest still belongs to the project.
			if isProse(dest) {
				docs = append(docs, source{regionKey(dest, u.regionKey), u.content})
			}
		case isShippedScript(dest, skillDirs):
			isManaged[dest] = true
			scripts = append(scripts, dest)
			body[dest] = u.content
		case isProse(dest):
			isManaged[dest] = true
			docs = append(docs, source{dest, u.content})
		case isDeclaration(dest):
			// Declarations state facts; they do not run anything. Counting one
			// as a caller is precisely the confusion this detector exists to
			// break.
			isManaged[dest] = true
		default:
			isManaged[dest] = true
			callers = append(callers, caller(dest, u.content))
		}
	}
	if len(scripts) == 0 {
		return nil, nil
	}
	sort.Strings(scripts)

	// The project's own files, because the lacquer is not the only thing that can
	// call a script it ships. A project that excludes .pre-commit-config.yaml and
	// runs the same script from a workflow of its own is correctly set up, and
	// #319's false positive was exactly this case one layer over: the check asked
	// whether the MANAGED unit did the work instead of whether the work happened.
	//
	// Managed destinations are deliberately skipped here even though they are on
	// disk. The rendered bytes above are what the next sync writes; the checkout
	// may be an older or hand-edited copy, and letting a drifted workflow that
	// still names the script answer the question would hide the very case where
	// the caller was rendered away.
	owned, incomplete := ownedCallers(projectRoot, isManaged)
	callers = append(callers, owned...)

	reached := reachable(scripts, body, callers)

	var out []UncalledScript
	for _, s := range scripts {
		if reached[s] {
			continue
		}
		f := UncalledScript{Dest: s, Documented: documenters(docs, s)}
		switch {
		case incomplete != "":
			f.Unconfirmable = incomplete
		case len(callers) == 0:
			// Nothing in this project can contain a caller, so "no caller found"
			// is not a finding about the script — it is a finding about the
			// sweep. Reporting it as uncalled here would be this tool asserting
			// an all-clear it never earned.
			f.Unconfirmable = "this project renders nothing that could call a script, and no project-owned file was readable"
		}
		out = append(out, f)
	}
	return out, nil
}

// source is one place a call could be written: a label for the report and the
// bytes to search.
//
// A caller's bytes are comment-stripped ONCE, at construction, rather than on
// every (caller, script) comparison. The project sweep reads every tracked file
// in the repository, so the naive form re-split several thousand files ten times
// over on an iOS monorepo — for an answer that cannot change between scripts.
type source struct{ label, body string }

// caller wraps content as a searchable call site.
func caller(label, content string) source { return source{label, codeLines(content)} }

// reachable returns the scripts something actually calls, as a fixed point.
//
// The closure is not decoration. scripts/docs-relaxation.sh has no caller in any
// workflow or hook config on an iOS project — profiles/ios/workflows/ci.yml
// names it only in a comment explaining why that job does NOT use it. Its caller
// is scripts/docs-hook.sh, a sibling script, which .pre-commit-config.yaml runs.
// A one-pass search over non-script callers reports a live, every-commit code
// path as dead.
//
// Transitive rather than "any script counts", because two abandoned scripts
// calling each other must not vouch for one another. A script enters the set
// only when something already in it names it, and the roots are never scripts.
func reachable(scripts []string, body map[string]string, roots []source) map[string]bool {
	reached := map[string]bool{}
	frontier := roots
	for len(frontier) > 0 {
		var next []source
		for _, s := range scripts {
			if reached[s] {
				continue
			}
			for _, c := range frontier {
				if invokes(c.label, c.body, s) {
					reached[s] = true
					next = append(next, caller(s, body[s]))
					break
				}
			}
		}
		frontier = next
	}
	return reached
}

// invokes reports whether caller contains a call to the script at dest.
//
// Three forms, all of them measured in this repo's own profiles:
//
//   - the full destination — `run: scripts/check-commit-msg.sh {1}`,
//     `entry: python3 .github/scripts/test_fetch_testflight_feedback.py`,
//     `"command": "osascript -l JavaScript .claude/scripts/allow_mcp.js"`;
//   - the bare basename, for a sibling calling `./docs-relaxation.sh`. Over-
//     accepting on purpose, exactly as internal/audit/orphan.go's References
//     does: an extra finding costs a look, a missed caller costs a working
//     pipeline to a `rm` somebody felt authorised to run;
//   - a Python import, for a sibling .py. `test_fetch_testflight_feedback.py`
//     reaches its subject as `import fetch_testflight_feedback as tf` — no
//     extension, no path. A module imported by a script a hook runs is not dead
//     weight, and matching only the filename would have relied on that file
//     happening to mention it in a docstring, which is keying on human prose.
//
// Comment lines are dropped first, and that is the part with teeth rather than
// the part being careful. profiles/ios/workflows/ci.yml contains the line
// "Reads the manifest directly rather than via docs-relaxation.sh"; counting it
// would mark that script called on every iOS project and permanently mask the
// day its real caller goes away. A genuine invocation is never on a comment
// line, so nothing true is lost.
func invokes(callerDest, code, dest string) bool {
	if strings.Contains(code, dest) {
		return true
	}
	base := path.Base(dest)
	if strings.Contains(code, base) {
		return true
	}
	if !strings.HasSuffix(callerDest, ".py") || path.Dir(callerDest) != path.Dir(dest) {
		return false
	}
	mod, ok := strings.CutSuffix(base, ".py")
	if !ok || mod == "" {
		return false
	}
	return pythonImport(mod).MatchString(code)
}

// pythonImport matches `import <mod>` / `from <mod> import …` at the head of a
// line, the only two ways a sibling module is pulled in.
func pythonImport(mod string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*(?:import|from)[ \t]+` + regexp.QuoteMeta(mod) + `\b`)
}

// codeLines drops whole-line comments in the two syntaxes every shipped caller
// uses — `#` for YAML, shell and Python, `//` for JavaScript. JSON has neither,
// and a trailing comment after real code keeps the line, which is the safe
// direction: the code on it still counts as a caller.
func codeLines(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// ownedCallers reads the project's own files — every tracked path that is not a
// managed destination, a declaration or a document — and returns them alongside
// the reason the sweep is incomplete, "" when it is not.
//
// An incomplete sweep is reported as incomplete. Without git there is no list of
// project files, and "no caller found" would then mean "I did not look", which
// is the failure this whole detector is about. internal/audit/orphan.go's
// References makes the same call for the same reason, returning nothing rather
// than claiming "unreferenced".
func ownedCallers(projectRoot string, isManaged map[string]bool) ([]source, string) {
	tracked, err := trackedFiles(projectRoot)
	if err != nil {
		return nil, fmt.Sprintf("this project's own files could not be listed (%v), so only the "+
			"lacquer's own rendered units were searched", err)
	}
	var out []source
	var unreadable []string
	for _, rel := range tracked {
		if isManaged[rel] || isDeclaration(rel) || isProse(rel) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				// Tracked in the index, deleted in the worktree. There is nothing
				// there to call anything, which is an answer rather than a gap.
				continue
			}
			unreadable = append(unreadable, rel)
			continue
		}
		out = append(out, caller(rel, string(data)))
	}
	if len(unreadable) > 0 {
		sort.Strings(unreadable)
		return out, fmt.Sprintf("%d project file(s) could not be read (%s), so a caller may have been missed",
			len(unreadable), strings.Join(unreadable, ", "))
	}
	return out, ""
}

// documenters returns the managed documents whose prose names this script.
func documenters(docs []source, dest string) []string {
	var out []string
	base := path.Base(dest)
	for _, d := range docs {
		if strings.Contains(d.body, dest) || strings.Contains(d.body, base) {
			out = append(out, d.label)
		}
	}
	sort.Strings(out)
	return out
}

// isShippedScript reports whether a destination is a script this check speaks
// for.
//
// Skill packages are deliberately out of scope. A skill's script is invoked by
// an agent following the skill's own SKILL.md, so prose IS the caller there and
// the distinction this detector draws — machinery versus a description of
// machinery — does not apply. Including them would report a dozen working skill
// scripts on every project and bury the one real finding, which is the argument
// orphan.go already makes for keeping a retired project's dropped workflows out
// of its report.
//
// The directory set comes from assets.SkillDirs rather than a literal list, so a
// new agent tool cannot add a skills directory this check then treats as a
// scripts tree.
func isShippedScript(dest string, skillDirs []string) bool {
	if isProse(dest) {
		return false
	}
	for _, d := range skillDirs {
		if strings.HasPrefix(dest, filepath.ToSlash(d)+"/") {
			return false
		}
	}
	for _, seg := range strings.Split(path.Dir(dest), "/") {
		if seg == scriptSegment {
			return true
		}
	}
	return false
}

// isProse reports whether a destination is documentation.
func isProse(dest string) bool { return strings.HasSuffix(dest, ".md") }

// isDeclaration reports whether a destination states facts rather than running
// anything.
//
// .gitignore naming a script's path, or .lacquer.lock recording that the lacquer
// wrote it, is not a caller — and treating either as one would silently mark a
// dead script live. orphan.go already refuses the lock for the same reason.
func isDeclaration(dest string) bool {
	switch path.Base(dest) {
	case ".gitignore", ".gitattributes", ".gitmodules", ".lacquer.toml", lock.Name:
		return true
	}
	return false
}

// FormatUncalledScripts renders the report, or "" when there is nothing to say.
func FormatUncalledScripts(scripts []UncalledScript) string {
	if len(scripts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nscripts shipped here that nothing calls:\n")
	documented := false
	unconfirmable := false
	for _, s := range scripts {
		note := "no caller in any rendered workflow, hook config or project file"
		if !s.Confirmed() {
			unconfirmable = true
			note = "UNCONFIRMABLE — " + s.Unconfirmable
		}
		fmt.Fprintf(&b, "  %s — %s\n", s.Dest, note)
		if len(s.Documented) > 0 {
			documented = true
			shown := s.Documented
			extra := ""
			if len(shown) > 3 {
				extra = fmt.Sprintf(" +%d more", len(shown)-3)
				shown = shown[:3]
			}
			fmt.Fprintf(&b, "    but DOCUMENTED AS RUNNING by %s%s\n", strings.Join(shown, ", "), extra)
		}
	}
	b.WriteString("Each of these is synced, executable and inert. Callers are read from the bytes this " +
		"manifest would render — a script invoked only when a [[product]] declares `secrets`, or only " +
		"from a workflow this project opted out of, is correctly counted as called or not for THIS " +
		"configuration rather than in general.\n")
	if documented {
		b.WriteString("A script marked DOCUMENTED AS RUNNING is the expensive half: the documentation " +
			"describes a step that does not exist, so the next reader concludes the work is being done. " +
			"Fix the caller or fix the sentence — leaving both is how this went unnoticed.\n")
	}
	if unconfirmable {
		b.WriteString("UNCONFIRMABLE means the search could not finish, NOT that the script is fine. " +
			"An unfinished search reported as an all-clear is the same defect these findings are about, " +
			"so it is printed rather than dropped.\n")
	}
	b.WriteString("Reported, not gated: a dead script breaks nothing today. It is not proof of absence " +
		"either — a caller assembled at run time (`bash \"$SCRIPT\"`) is invisible here — and a caller " +
		"that exists is not proof the script RUNS: an `if:` that is never true satisfies this check " +
		"completely.\n")
	return b.String()
}
