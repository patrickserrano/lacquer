package audit

import (
	"fmt"
	"path"
	"strings"
)

// MachineWide is a workflow step that kills or wipes simulator infrastructure
// for the whole machine rather than for the job running it.
//
// The fleet's iOS CI shares one self-hosted Mac, and every runner on it runs as
// the same user. So a step that kills "every xctest" or "every simulator" does
// not stop at its own job: it kills whatever every other repository's job has
// running at that moment. The victims do not fail in the step that did it. They
// fail in their own test step, with
//
//	Test crashed with signal trap before establishing connection
//
// which reads as a flaky test in the victim rather than as a neighbour's
// cleanup. That is how it went unnoticed. momfriend's EXCLUDED ios-ci.yml still
// carried the kills the lacquer's own template had dropped long before (an
// exclusion freezes a file), and on 2026-09-17 its runs matched all four
// CoreSimulatorService restarts to the minute. The lacquer's own cleanup-ci.yml
// did the same thing nightly.
//
// Reported, not gated. Most of the fleet cannot fix this in its own tree today —
// the offender is a file the lacquer renders into it — and failing every iOS
// repository's audit over the lacquer's own template would be gating people on
// something only an upstream change can fix.
type MachineWide struct {
	// Workflow is the repo-relative path of the file.
	Workflow string
	// Line is the 1-based line the command starts on.
	Line int
	// Command is that source line, whitespace-trimmed (continuations joined).
	Command string
	// Kind is one of the Kind* constants.
	Kind string
}

// What a flagged command is. Each has its own sentence of harm and its own
// scoped replacement in the report, because the replacement differs by kind.
const (
	// KindKillall is any killall that names a process.
	//
	// ANY, deliberately, not just a list of simulator process names. killall
	// selects by process name across every process the user owns, and on a
	// shared runner every runner is that user, so no argument to killall can
	// narrow it to one job — there is no scoped spelling to steer people to,
	// only a different command. A name allowlist would also be the wrong shape
	// for the failure: the fleet's lines already named six different processes
	// (CoreSimulatorService, Simulator, testmanagerd, launchd_sim, simctl,
	// SimulatorTrampoline), and the next one will name a seventh.
	KindKillall = "killall"
	// KindPkill is a pkill whose pattern is not scoped to this job's workspace.
	// Unlike killall it CAN be scoped — `pkill -f "$GITHUB_WORKSPACE"` matches
	// only processes whose command line carries this runner's own checkout path
	// — so only the unscoped form is flagged.
	KindPkill = "pkill"
	// KindSimctlAll is `simctl shutdown|erase|delete|boot all`: every simulator
	// on the machine, including the ones other jobs are testing on.
	// `simctl delete unavailable` only removes devices whose runtime is gone,
	// which no job can be using, and is not flagged.
	KindSimctlAll = "simctl-all"
	// KindKillAll is `kill -1` as a PID: every process the user can signal.
	// `kill <pid>` and `kill "$pid"` are scoped by construction and are not
	// flagged — a job signalling PIDs it selected is the scoped replacement.
	KindKillAll = "kill-all"
	// KindSharedCache is an rm of a cache every job on the machine uses:
	// DerivedData or the module cache wholesale, or CoreSimulator (the whole
	// directory, or any directory directly under it: Devices, Caches, …).
	// Deleting one project's DerivedData folder or one device's directory is
	// scoped and is not flagged, and neither is ~/Library/Logs/CoreSimulator,
	// which no running simulator reads back.
	KindSharedCache = "shared-cache"
)

// kindOrder is the report order: the kills first, then the wipes.
var kindOrder = []string{KindKillall, KindPkill, KindKillAll, KindSimctlAll, KindSharedCache}

// kindHarm is one sentence on what the command does to OTHER jobs on the runner.
var kindHarm = map[string]string{
	KindKillall:     "killall matches by process name across the whole machine, so it kills that process for every other job on this runner too.",
	KindPkill:       "an unscoped pkill matches every process on the machine whose name or command line fits, including other jobs' builds and test runners.",
	KindSimctlAll:   "acts on every simulator on the machine, including the devices other jobs are testing on right now.",
	KindKillAll:     "kill -1 signals every process the runner user owns — every other job's build and test run included.",
	KindSharedCache: "deletes a cache every job on the machine builds or tests against, underneath whichever jobs are using it.",
}

// kindInstead is the scoped alternative the report points to.
var kindInstead = map[string]string{
	KindKillall:     `pkill -f "$GITHUB_WORKSPACE" (only processes started from this job's checkout), or kill the PIDs this job started.`,
	KindPkill:       `pkill -f "$GITHUB_WORKSPACE" — put this job's workspace path in the pattern.`,
	KindSimctlAll:   "shut down / delete only this run's CI-* devices, by UDID (xcrun simctl delete unavailable is also fine).",
	KindKillAll:     "kill the PIDs this job started (kill \"$pid\").",
	KindSharedCache: "build into a per-job -derivedDataPath under $RUNNER_TEMP and delete that, or delete only this run's CI-* devices.",
}

// MachineWideSteps returns every command in every workflow file of the project
// that kills or wipes simulator infrastructure machine-wide. Managed, project-
// owned and excluded workflows are all read: the offender that prompted this
// was an excluded one.
func MachineWideSteps(projectRoot string) []MachineWide {
	var out []MachineWide
	for _, wf := range workflowFiles(projectRoot) {
		out = append(out, machineWideIn(wf.path, wf.body)...)
	}
	return out
}

// machineWideIn classifies every command in one workflow body, reusing the
// tokeniser the inert-secrets audit was hardened on (#378, #391): a `#` comment
// is not a command, quotes are removed, and `a || b` / `a | b` are two.
func machineWideIn(workflow, body string) []MachineWide {
	var out []MachineWide
	for _, sl := range shellLines(body) {
		for i, c := range simpleCommands(shellWords(sl.text)) {
			argv := c.argv
			if i == 0 {
				argv = dropYAMLPrefix(argv)
				if argv == nil {
					break // a YAML mapping line (name:, if:, description: …), not shell
				}
			}
			if kind := machineWideKind(argv); kind != "" {
				out = append(out, MachineWide{Workflow: workflow, Line: sl.line, Command: strings.TrimSpace(sl.text), Kind: kind})
			}
		}
	}
	return out
}

// dropYAMLPrefix strips the YAML around an inline command (`- run: cmd`), and
// returns nil for any other mapping line. A step's `name: killall Simulator` or
// an input's `description:` is prose, not something a runner executes.
func dropYAMLPrefix(argv []string) []string {
	if len(argv) > 0 && argv[0] == "-" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return argv
	}
	if argv[0] == "run:" {
		return argv[1:]
	}
	if w := argv[0]; len(w) > 1 && strings.HasSuffix(w, ":") {
		return nil
	}
	return argv
}

// Words that precede the command word without being it.
var (
	shellKeywords = map[string]bool{"then": true, "else": true, "elif": true, "do": true, "if": true, "while": true, "until": true, "!": true, "{": true, "time": true}
	// Wrappers run their argument as the command. Their own flags are skipped
	// with them; timeout also takes a duration.
	shellWrappers = map[string]bool{"sudo": true, "command": true, "exec": true, "nohup": true, "env": true, "xargs": true, "nice": true, "timeout": true}
)

// commandWord returns the index of the word the shell will execute, skipping
// keywords, wrappers (and their flags), and leading VAR=value assignments.
func commandWord(argv []string) int {
	for i := 0; i < len(argv); i++ {
		w := argv[i]
		switch {
		case shellKeywords[w]:
		case isAssignment(w):
		case shellWrappers[w]:
			for i+1 < len(argv) && strings.HasPrefix(argv[i+1], "-") {
				i++
			}
			if w == "timeout" && i+1 < len(argv) {
				i++ // the duration
			}
		default:
			return i
		}
	}
	return -1
}

func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for _, r := range w[:eq] {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// machineWideKind returns the Kind of one simple command, or "" if it reaches
// only this job.
func machineWideKind(argv []string) string {
	i := commandWord(argv)
	if i < 0 {
		return ""
	}
	cmd, args := path.Base(argv[i]), argv[i+1:]
	switch cmd {
	case "killall":
		// Any named process — see KindKillall. `killall -l` lists signals and
		// names nothing; under xargs the names arrive on stdin.
		if len(operands(args)) > 0 || underXargs(argv[:i]) {
			return KindKillall
		}
	case "pkill":
		if !scopedToJob(args) {
			return KindPkill
		}
	case "xcrun", "simctl":
		if simctlAll(argv[i:]) {
			return KindSimctlAll
		}
	case "kill":
		if killsEverything(args) {
			return KindKillAll
		}
	case "rm":
		for _, op := range operands(args) {
			if sharedCache(op) {
				return KindSharedCache
			}
		}
	}
	return ""
}

// underXargs reports whether the words before a command word include xargs,
// which appends the command's operands from stdin.
func underXargs(before []string) bool {
	for _, w := range before {
		if w == "xargs" {
			return true
		}
	}
	return false
}

// jobScopes are what makes a pkill pattern this job's own: the runner's
// checkout, its work directory, or its temp directory (which lives inside the
// work directory). Each runner on a machine has its own, so a pattern carrying
// one cannot match another runner's processes.
var jobScopes = []string{"GITHUB_WORKSPACE", "RUNNER_WORKSPACE", "RUNNER_TEMP", "github.workspace", "runner.workspace", "runner.temp", "/_work/"}

func scopedToJob(args []string) bool {
	for _, a := range args {
		for _, s := range jobScopes {
			if strings.Contains(a, s) {
				return true
			}
		}
	}
	return false
}

// simctlAll reports whether argv (from xcrun or simctl onwards) is
// `simctl <shutdown|erase|delete|boot> all`.
func simctlAll(argv []string) bool {
	for i, w := range argv {
		if path.Base(w) != "simctl" {
			continue
		}
		ops := operands(argv[i+1:])
		if len(ops) < 2 {
			return false
		}
		switch ops[0] {
		case "shutdown", "erase", "delete", "boot":
			return ops[1] == "all"
		}
		return false
	}
	return false
}

// killsEverything reports whether a kill's PID operands include -1, which POSIX
// defines as every process the caller may signal. The first word, when it is a
// flag, is the signal (`kill -1 "$pid"` is SIGHUP to one PID). `-s SIG` needs no
// case of its own: a signal name is never "-1".
func killsEverything(args []string) bool {
	i := 0
	if len(args) > 0 && strings.HasPrefix(args[0], "-") && args[0] != "--" {
		i = 1
	}
	for ; i < len(args); i++ {
		if args[i] == "--" {
			continue
		}
		if args[i] == "-1" {
			return true
		}
	}
	return false
}

// Home-relative directories every job on the machine shares.
const (
	derivedData   = "Library/Developer/Xcode/DerivedData"
	coreSimulator = "Library/Developer/CoreSimulator"
	xcodeDir      = "Library/Developer/Xcode"
)

// sharedCache reports whether an rm operand deletes a machine-wide cache
// wholesale: DerivedData, CoreSimulator or any directory directly under it,
// a ModuleCache* under Xcode or DerivedData, or any ancestor of those.
func sharedCache(op string) bool {
	rel, ok := homeRelative(op)
	if !ok {
		return false
	}
	rel = strings.TrimRight(rel, "/*")
	for _, root := range []string{derivedData, coreSimulator} {
		if rel == "" || rel == root || strings.HasPrefix(root, rel+"/") {
			return true
		}
	}
	dir, base := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	if dir == coreSimulator {
		return true
	}
	return strings.HasPrefix(base, "ModuleCache") && (dir == xcodeDir || dir == derivedData)
}

// homeRelative returns p relative to the home directory, for the spellings a
// workflow uses for it: ~, $HOME, ${HOME}, or /Users/<name>.
func homeRelative(p string) (string, bool) {
	for _, h := range []string{"~", "$HOME", "${HOME}"} {
		if p == h {
			return "", true
		}
		if strings.HasPrefix(p, h+"/") {
			return p[len(h)+1:], true
		}
	}
	if rest, ok := strings.CutPrefix(p, "/Users/"); ok {
		if _, after, found := strings.Cut(rest, "/"); found {
			return after, true
		}
		return "", true
	}
	return "", false
}

// FormatMachineWide renders the report, or "" when there is nothing to say.
func FormatMachineWide(fs []MachineWide) string {
	if len(fs) == 0 {
		return ""
	}
	// Grouped by kind, so the harm and the replacement are said once per kind
	// rather than once per line: an iOS repository carrying the v1.37.10
	// cleanup-ci.yml has sixteen of these, in five kinds.
	byKind := map[string][]MachineWide{}
	for _, f := range fs {
		byKind[f.Kind] = append(byKind[f.Kind], f)
	}
	var b strings.Builder
	b.WriteString("\nworkflow steps that kill or wipe simulators machine-wide on a shared runner:\n")
	for _, k := range kindOrder {
		if len(byKind[k]) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s: %s\n", k, kindHarm[k])
		fmt.Fprintf(&b, "    Instead: %s\n", kindInstead[k])
		for _, f := range byKind[k] {
			fmt.Fprintf(&b, "    %s:%d  %s\n", f.Workflow, f.Line, f.Command)
		}
	}
	b.WriteString("Every runner on a shared Mac is the same user, so these reach every other job running\n" +
		"there, from any repository. The victims fail in their OWN test step, typically with\n" +
		"\"Test crashed with signal trap before establishing connection\", which reads as a flaky\n" +
		"test rather than as this step. Excluded and project-owned workflows are included: an\n" +
		"exclusion freezes a file, so it keeps kills the lacquer's own template has since dropped.\n")
	return b.String()
}
