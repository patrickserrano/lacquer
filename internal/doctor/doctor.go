// Package doctor proves that a project's checks can actually fail.
//
// Every serious defect found while onboarding this fleet was the same shape: a
// check that ran, reported success, and verified nothing.
//
//   - The editor hook called `swiftlint lint --path FILE`. `--path` had been
//     removed from SwiftLint; the command errored on every invocation and
//     `2>/dev/null || true` ate the message. It linted nothing for months and
//     looked healthy doing it.
//   - The DocC gate passed `DOCC_FLAGS=--warnings-as-errors`. That is a real
//     build setting name and it is silently ignored; a deliberately broken
//     symbol link still exited 0.
//   - The drift job ran `go build ./.lacquer-checkout/cmd/lacquer` from a
//     non-module directory. It failed in every repo, on every PR, for a reason
//     that had nothing to do with drift.
//
// None of these was caught by running the check. They were caught by accident,
// long after. A green check is not evidence unless you know it can go red.
//
// So doctor does not ask "does the check pass on this project" — CI already
// answers that. It writes a fixture that is KNOWN BAD, runs the check against
// it, and asserts the check FAILS. A probe that passes on known-bad input is
// reported as broken, because it is.
package doctor

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/safepath"
)

// Probe is one self-test: a fixture, a command, and the outcome that proves the
// check is wired up.
type Probe struct {
	Name string `toml:"name"`
	// Why this probe exists — printed on failure, so the person reading the
	// output learns what broke rather than just which assertion tripped.
	Why string `toml:"why"`
	// File and Content describe the fixture written into a scratch directory.
	// A probe with no File runs its command with no fixture (a tool-presence
	// check, say).
	File    string `toml:"file"`
	Content string `toml:"content"`
	// Argv is the command. Placeholders: {dir} is the scratch directory,
	// {component} the absolute component directory holding the synced configs.
	Argv []string `toml:"argv"`
	// Requires names tools the probe cannot run without: a bare name is looked
	// up on PATH, anything containing "/" is a path (placeholders substituted)
	// that must exist and be executable.
	//
	// Without this, a probe whose argv[0] is a shell wrapper is unsound in the
	// direction that matters. Argv[0] gets a PATH check below, which is enough
	// when argv[0] IS the tool — but a probe that runs `sh -c "…biome ci…"`
	// passes that check via /bin/sh, and then a MISSING biome exits non-zero,
	// which an expect="fail" probe reads as "the check correctly rejected the
	// bad input". A silent pass, produced by the very absence the probe exists
	// to notice. Every node-based check is in that shape, since its binary lives
	// in node_modules/.bin rather than on PATH.
	Requires []string `toml:"requires"`
	// Expect is "fail" (the usual case: known-bad input must be rejected) or
	// "pass" (the command must succeed, e.g. a tool exists and runs).
	Expect string `toml:"expect"`
	// ExpectOutput is a regular expression that must match the command's
	// combined output. It is what makes expect="fail" an assertion about WHY
	// the command failed rather than merely THAT it did.
	//
	// An exit status is one bit, and every way a probe can go wrong sets it:
	// a config that will not parse, a binary that is not there, a script with a
	// syntax error, exit 127, a tool that aborted before it ever read the
	// fixture. All of those look exactly like "the check correctly rejected the
	// bad input". `Requires` closes the missing-tool case only; it says nothing
	// about the tool that ran and then gave up early, which is the failure this
	// package shipped twice in the web profile — three times counting the core
	// probes that ran `bash {component}/scripts/check-secrets.sh` and would have
	// reported all five green had that script been deleted.
	//
	// So a probe names the diagnostic only its INTENDED failure produces: the
	// rule id, the specific message, the file the tool says it rejected. A
	// pattern loose enough to match any error output rebuilds the hole one layer
	// up, which is why the shipped set is held to that in doctor_test.go.
	//
	// It applies to expect="pass" too, and for the same reason: exit 0 is also
	// one bit. A shell probe can reach it by never running the tool, by a
	// swallowed `|| true`, or — the case the web deprecation probe shipped — by
	// an inverted pipeline where any non-deprecation failure produced no output,
	// grep found nothing, and `!` turned that into success.
	ExpectOutput string `toml:"expect_output"`
}

type probeFile struct {
	Probe []Probe `toml:"probe"`
}

// Result is one probe's outcome.
type Result struct {
	Component string
	Profile   string
	Name      string
	OK        bool
	Detail    string
}

// CoreLayer is the pseudo-profile name for core's own self-tests. core ships
// checks every project gets — the secrets scanner, the commit-message check —
// and until this existed they had nowhere to put a probe, so the highest-stakes
// check in the harness was the one nothing tested.
const CoreLayer = "core"

// LoadProbes reads a layer's self-tests. A layer shipping none is fine.
// The core layer lives at core/doctor.toml; every other name is a profile.
func LoadProbes(lacquerRoot, profile string) ([]Probe, error) {
	path := filepath.Join(lacquerRoot, "profiles", profile, "doctor.toml")
	if profile == CoreLayer {
		path = filepath.Join(lacquerRoot, "core", "doctor.toml")
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f probeFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i, p := range f.Probe {
		if p.Name == "" || len(p.Argv) == 0 {
			return nil, fmt.Errorf("%s: probe %d needs a name and argv", path, i)
		}
		switch p.Expect {
		case "fail", "pass":
		default:
			return nil, fmt.Errorf("%s: probe %q has expect=%q, want \"fail\" or \"pass\"", path, p.Name, p.Expect)
		}
		if _, err := compileExpectOutput(p.ExpectOutput); err != nil {
			return nil, fmt.Errorf("%s: probe %q: %w", path, p.Name, err)
		}
	}
	return f.Probe, nil
}

// Run executes probes for the project's declared profiles.
//
// `only` restricts which profiles are proved. Empty means all of them, which is
// right when a human runs `lacquer doctor` on a machine with every toolchain.
// It is wrong in CI, and rail showed why: it declares one component with
// profiles ["ios", "supabase"], and the supabase CI job runs on ubuntu-latest.
// Doctor dutifully ran the iOS probes there, found no swiftlint or swiftformat,
// and failed six checks — correctly, since a missing tool IS a real finding, but
// uselessly, because that runner was never going to have Xcode.
//
// The fix is scoping rather than tolerance. Making a missing tool skip quietly
// would be the exact silent pass this package exists to eliminate; the iOS
// probes were not wrong, they were simply nobody's business on a Linux runner
// proving database checks. So the CALLER declares which toolchain it has, and
// anything it names had better work.
//
// Core probes always run: they are shell and git, present everywhere.
//
// Probes run in a scratch directory OUTSIDE the project, so a fixture that is
// deliberately malformed can never be mistaken for project source, land in a
// commit, or be picked up by a watcher.
func Run(lacquerRoot, projectRoot string, cfg *config.Config, only []string, out io.Writer) ([]Result, error) {
	want := map[string]bool{}
	for _, p := range only {
		want[p] = true
	}
	var results []Result

	comps := append([]config.Component(nil), cfg.Components...)
	sort.Slice(comps, func(i, j int) bool { return comps[i].Path < comps[j].Path })

	// core's probes run once, against the project root — its scripts are
	// repo-wide, not per-component.
	coreProbes, err := LoadProbes(lacquerRoot, CoreLayer)
	if err != nil {
		return nil, err
	}
	for _, pr := range coreProbes {
		r := runProbe(pr, ".", CoreLayer, projectRoot)
		results = append(results, r)
		report(out, pr, r)
	}

	for _, c := range comps {
		compDir := projectRoot
		if c.Path != "." {
			resolved, err := safepath.Resolve(projectRoot, c.Path)
			if err != nil {
				return nil, fmt.Errorf("resolve component %s: %w", c.Path, err)
			}
			compDir = resolved
		}
		profiles := append([]string(nil), c.Profiles...)
		sort.Strings(profiles)

		for _, p := range profiles {
			if len(want) > 0 && !want[p] {
				continue
			}
			probes, err := LoadProbes(lacquerRoot, p)
			if err != nil {
				return nil, err
			}
			for _, pr := range probes {
				r := runProbe(pr, c.Path, p, compDir)
				results = append(results, r)
				mark := "ok  "
				if !r.OK {
					mark = "FAIL"
				}
				fmt.Fprintf(out, "  %s  %s\n", mark, r.Name)
				if !r.OK {
					fmt.Fprintf(out, "        %s\n", r.Detail)
					if pr.Why != "" {
						fmt.Fprintf(out, "        why this matters: %s\n", pr.Why)
					}
				}
			}
		}
	}
	return results, nil
}

// report prints one probe result, and on failure the reason plus the probe's
// own `why` — the output is where a reader learns what broke, not just which
// assertion tripped.
func report(out io.Writer, p Probe, r Result) {
	mark := "ok  "
	if !r.OK {
		mark = "FAIL"
	}
	fmt.Fprintf(out, "  %s  %s\n", mark, r.Name)
	if !r.OK {
		fmt.Fprintf(out, "        %s\n", r.Detail)
		if p.Why != "" {
			fmt.Fprintf(out, "        why this matters: %s\n", p.Why)
		}
	}
}

func runProbe(p Probe, compPath, profile, compDir string) Result {
	r := Result{Component: compPath, Profile: profile, Name: p.Name}

	dir, err := os.MkdirTemp("", "lacquer-doctor-")
	if err != nil {
		r.Detail = fmt.Sprintf("could not create a scratch dir: %v", err)
		return r
	}
	defer os.RemoveAll(dir)

	if p.File != "" {
		if err := os.WriteFile(filepath.Join(dir, p.File), []byte(p.Content), 0o644); err != nil {
			r.Detail = fmt.Sprintf("could not write the fixture: %v", err)
			return r
		}
	}

	subst := func(s string) string {
		s = strings.ReplaceAll(s, "{dir}", dir)
		return strings.ReplaceAll(s, "{component}", compDir)
	}

	argv := make([]string, len(p.Argv))
	for i, a := range p.Argv {
		argv[i] = subst(a)
	}

	// Requirements first: a missing tool must be reported as a missing tool
	// whatever the probe expects, never allowed to satisfy an expect="fail".
	for _, req := range p.Requires {
		if missing := missingTool(subst(req)); missing != "" {
			r.Detail = missing
			return r
		}
	}

	if _, err := exec.LookPath(argv[0]); err != nil {
		// A missing tool is a real finding: the check it backs cannot be running
		// wherever this is true. Not the same as the check being mis-wired, so
		// it is reported distinctly.
		r.Detail = fmt.Sprintf("%s is not installed, so this check cannot be running at all", argv[0])
		return r
	}

	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv comes from a lacquer-owned doctor.toml
	cmd.Dir = dir
	cmd.Env = probeEnv(os.Environ())
	raw, runErr := cmd.CombinedOutput()
	// Everything downstream — the expect_output match AND the two human-facing
	// diagnostics — reads the stripped form. Stripping for the match but not the
	// excerpt would be the worst of both: the operator would be handed escape
	// soup to compare against a pattern by eye, with the invisible bytes that
	// explain the mismatch being precisely the ones they cannot see.
	output := stripANSI(raw)
	failed := runErr != nil

	switch p.Expect {
	case "fail":
		if !failed {
			r.Detail = "the check PASSED on deliberately broken input — it is not verifying what it claims to"
			if s := firstLine(output); s != "" {
				r.Detail += "\n        tool said: " + s
			}
			return r
		}
	case "pass":
		if failed {
			r.Detail = fmt.Sprintf("expected success, got %v", runErr)
			if s := firstLine(output); s != "" {
				r.Detail += "\n        tool said: " + s
			}
			return r
		}
	}

	// The exit status came out the way the probe expects. That is one bit, and
	// on its own it does not say the tool ever reached the fixture — so a probe
	// may also name the diagnostic its intended outcome produces.
	re, err := compileExpectOutput(p.ExpectOutput)
	if err != nil {
		// Unreachable via LoadProbes, which rejects this at load. Kept because
		// the alternative to failing here is passing here, and a probe whose
		// assertion could not be evaluated has proved nothing.
		r.Detail = fmt.Sprintf("this probe's expect_output could not be used: %v", err)
		return r
	}
	if re == nil || re.Match(output) {
		r.OK = true
		return r
	}
	if p.Expect == "fail" {
		r.Detail = fmt.Sprintf("the command failed, but NOT for the reason this probe asserts:"+
			" its output never matched %s.\n        A check that aborts before it reads the fixture"+
			" also exits non-zero, and that is not evidence the check works.", strconv.Quote(p.ExpectOutput))
	} else {
		r.Detail = fmt.Sprintf("the command succeeded, but NOT by doing what this probe asserts:"+
			" its output never matched %s.", strconv.Quote(p.ExpectOutput))
	}
	r.Detail += excerpt(output)
	return r
}

// colourForcing names the variables that make a tool colourise output it would
// otherwise leave plain. They are REMOVED from the probe environment rather than
// merely countered, because FORCE_COLOR wins against NO_COLOR and appending
// NO_COLOR=1 would therefore have changed nothing.
//
// Measured with deno 2.9.6 against the supabase fmt probe's own fixture:
//
//	NO_COLOR=1                deno fmt --check   → clean text
//	NO_COLOR=1 FORCE_COLOR=1  deno fmt --check   → \x1b[0m\x1b[1m\x1b[32m+\x1b[0m…
//	env -u FORCE_COLOR NO_COLOR=1 …              → clean text again
//
// So deno honours NO_COLOR correctly; FORCE_COLOR overriding it is the whole
// effect. That matters for where the fix belongs: NO_COLOR is not broken and no
// amount of exporting it harder would have helped.
var colourForcing = []string{"FORCE_COLOR", "CLICOLOR_FORCE"}

// probeEnv builds the environment a probe runs under.
//
// Probes used to inherit os.Environ() untouched, which made a probe's verdict
// depend on the operator's shell profile. A person with FORCE_COLOR exported —
// an ordinary thing to want, it is how you get colour out of piped tools — saw
// the supabase `deno fmt --check` probe report the check broken, because the
// escapes landed BETWEEN the `+` and the text the expect_output pattern had to
// match. Nothing about the check was wrong. That is a false FAILURE, and this
// package's whole claim is that its verdicts distinguish real findings from
// noise; a verdict that turns on an inherited variable does not.
//
// It went unnoticed because CI installs no deno, so the three supabase probes
// SKIP there and main stays green. The bug only ever bit on a machine that has
// deno — that is, a developer's own.
//
// Doing this here rather than per-probe is the point: those probes already
// `export NO_COLOR=1` inside their argv and it was not enough. Patching the
// three would leave the identical trap armed for every probe added later and
// every other tool that colourises — ripgrep, cargo, jest, biome.
// Those in-argv exports are left in place: they are harmless now that this
// exists, and they document the requirement at the point of use.
func probeEnv(parent []string) []string {
	env := make([]string, 0, len(parent)+1)
	for _, kv := range parent {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		drop := name == "NO_COLOR" // re-added below, so a parent NO_COLOR=0 cannot win
		for _, f := range colourForcing {
			if name == f {
				drop = true
			}
		}
		if !drop {
			env = append(env, kv)
		}
	}
	return append(env, "NO_COLOR=1")
}

// ansiEscape matches a CSI sequence: ESC '[', parameter bytes, intermediate
// bytes, then one final byte.
//
// Deliberately wider than the `\x1b\[[0-9;]*m` that colour alone would need. SGR
// is only the sequence a colouriser reaches for FIRST; the same tools also emit
// cursor motion and line erasure (\x1b[2K, \x1b[1A) when they rewrite a progress
// line, and deno's own diff parameters include the `;` and `5` of 256-colour
// selectors like \x1b[38;5;245m. A pattern covering only SGR would leave those
// other sequences embedded in the text an expect_output pattern must match, which
// is the exact defect being fixed — just triggered by a different tool.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// stripANSI removes escape sequences so an expect_output pattern is matched
// against what the tool MEANT to say.
//
// Belt to probeEnv's braces: environment normalisation handles every tool that
// respects the convention, and this handles the ones that colourise regardless.
// It keys on the ESC byte, so output that merely contains the text "[0m" is
// untouched and no shipped probe's assertion changes meaning.
func stripANSI(b []byte) []byte {
	if !bytes.Contains(b, []byte{0x1b}) {
		return b // the overwhelmingly common case; do not copy for nothing
	}
	return ansiEscape.ReplaceAll(b, nil)
}

// compileExpectOutput turns a probe's pattern into a matcher, or nil when the
// probe declares none. A pattern that matches empty output is refused: it would
// accept a command that printed nothing at all, which is the silent pass the
// field exists to prevent.
func compileExpectOutput(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("expect_output is not a valid regexp: %w", err)
	}
	if re.MatchString("") {
		return nil, fmt.Errorf("expect_output %s matches empty output, so it would accept a command that printed nothing;"+
			" name the diagnostic the intended failure actually produces", strconv.Quote(pattern))
	}
	return re, nil
}

// missingTool returns a human-readable finding when req is unavailable, or "".
// A bare name is resolved on PATH; anything with a "/" must exist and carry an
// executable bit.
func missingTool(req string) string {
	if !strings.Contains(req, "/") {
		if _, err := exec.LookPath(req); err != nil {
			return fmt.Sprintf("%s is not installed, so this check cannot be running at all", req)
		}
		return ""
	}
	fi, err := os.Stat(req)
	if err != nil {
		return fmt.Sprintf("%s does not exist, so this check cannot be running at all (run the project's install step first)", req)
	}
	if fi.IsDir() || fi.Mode()&0o111 == 0 {
		return fmt.Sprintf("%s is not executable, so this check cannot be running at all", req)
	}
	return ""
}

// Failures returns the probes that did not behave as expected.
func Failures(rs []Result) []Result {
	var out []Result
	for _, r := range rs {
		if !r.OK {
			out = append(out, r)
		}
	}
	return out
}

// excerpt renders what the command actually said, so a reader diagnosing an
// expect_output miss can see the gap between the assertion and reality. A first
// line alone is not enough here: the interesting diagnostic is usually further
// down, and several tools open with a blank line.
func excerpt(b []byte) string {
	const max = 8
	var kept []string
	for _, ln := range strings.Split(string(b), "\n") {
		if ln = strings.TrimRight(ln, " \t\r"); strings.TrimSpace(ln) == "" {
			continue
		}
		kept = append(kept, ln)
		if len(kept) == max {
			break
		}
	}
	if len(kept) == 0 {
		return "\n        the command printed nothing at all"
	}
	out := "\n        it said:"
	for _, ln := range kept {
		out += "\n          " + ln
	}
	return out
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
