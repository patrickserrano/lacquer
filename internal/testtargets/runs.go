package testtargets

import (
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// A local package's test suite is not a native target, and a `-only-testing:`
// selector is not the only way it can run: `swift test` in the package's
// directory runs it with no scheme and no selector involved. So before the
// audit reports a package suite as running nowhere, it reads the project's
// workflows for a command that runs it. This file is that read.
//
// WHAT COUNTS AS RUNNING A SUITE. A workflow under .github/workflows that is
// triggered by a code change (the same `on:` test covered_elsewhere uses), with
// a step that runs, outside a comment, one of:
//
//   - `swift test`, where the package it runs is the suite's package. That
//     package is `--package-path` (or `--chdir` / `-C`) if given, else the
//     directory the command runs in. A `--filter` narrows it to the suites the
//     filter names, and a `--skip` matching the suite's name removes it.
//   - `xcodebuild test` (or test-without-building) or `flowdeck test` that
//     selects the suite: `-only-testing:<suite>[/…]`; for flowdeck `--only`,
//     `--test-cases` or `--test-targets`. A skip of the whole suite removes it.
//   - either of those with no selection at all, on a scheme that tests the
//     suite: a committed shared .xcscheme listing it as a testable it does not
//     skip, or — for a package's own generated scheme, which is not committed —
//     the scheme Xcode generates to test a package. Measured with Xcode on
//     2026-09-18: a package with one product gets one scheme, named after the
//     PACKAGE, and it runs every test target; with several products it is
//     `<package>-Package` that runs them, and a product's own scheme has no test
//     action (xcodebuild refuses, loudly). sleevetap runs its SleevetapNFC
//     package's 91 tests exactly this way, through `flowdeck test -w
//     .swiftpm/xcode/package.xcworkspace -s SleevetapNFC`.
//
// Where a command runs is the step's `working-directory:`, else the job's
// `defaults.run.working-directory`, else the workflow's, else the repository
// root; then moved by `cd` / `pushd` earlier in the same step. A step that runs
// a script in the repository (`./Scripts/verify.sh`, `bash scripts/x.sh`,
// `bash -c '…'`) is read into, so the arrangement "CI calls the script a
// developer runs" is seen for what it is — the script starts where the step
// runs, and `cd "$(dirname "$0")/.."` inside it is understood. A plain
// `name=value` assignment is substituted where it is later used. Heredoc bodies
// are data, not commands, and are skipped.
//
// WHAT DOES NOT: `swift build --build-tests` (it compiles the suite and runs
// none of it — momfriend does exactly this, deliberately), a `swift test` in any
// other directory, an `echo` of the command, anything in a comment, a workflow
// only `workflow_dispatch` or `schedule` starts, and build-for-testing. Nor a
// command the reader does not recognise — `make`, fastlane, a script outside the
// repository. A suite run only through one of those is reported, and the fix is
// to declare it in [[project.covered_elsewhere]], which Verify checks.
//
// WHAT IT DOES NOT PROVE, as with covered_elsewhere: that the step's job runs
// (an `if:` can skip it), that the tests pass, that a `|| true` is not
// swallowing them, or that the workflow's check is required to merge. It is
// evidence that a pull request starts a command that runs the suite.
//
// "COULD NOT LOOK" STAYS "COULD NOT LOOK". A workflow that cannot be read or
// parsed; a `swift test` whose directory is a `${{ matrix… }}` or `$VAR` this
// audit cannot evaluate; a test run whose scheme is not committed and is not a
// package's generated one (XcodeGen generates schemes too), or that runs a test
// plan: each might be running the suite. A suite nothing is SEEN running, but
// that one of these might be, is reported as not checked, with the reason —
// never as running nowhere, and never as covered.

// suite is a package test suite to look for: its name and the package's
// directory relative to the project root, slash-separated and cleaned.
type suite struct{ name, dir string }

// outcome is what the workflows say about one suite.
type outcome struct {
	// workflow and command are the first seen running it, "" when none was.
	workflow, command string
	// unsure is every reason the answer could not be decided, in the order met.
	unsure []string
}

// findRuns reads every workflow under root/.github/workflows and reports, per
// suite name, whether one runs it.
func findRuns(root string, suites []suite) map[string]*outcome {
	out := map[string]*outcome{}
	for _, s := range suites {
		out[s.name] = &outcome{}
	}
	if len(suites) == 0 {
		return out
	}
	unsureAll := func(reason string) {
		for _, s := range suites {
			out[s.name].unsure = append(out[s.name].unsure, reason)
		}
	}

	var files []string
	for _, pat := range []string{"*.yml", "*.yaml"} {
		m, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", pat))
		files = append(files, m...)
	}
	sort.Strings(files)

	for _, f := range files {
		rel := ".github/workflows/" + filepath.Base(f)
		body, err := os.ReadFile(f)
		if err != nil {
			unsureAll(fmt.Sprintf("%s could not be read: %v", rel, err))
			continue
		}
		if !anyAuto(triggers(string(body))) {
			continue // runs on no pull request, so it covers nothing here
		}
		var wf workflowFile
		if err := yaml.Unmarshal(body, &wf); err != nil {
			unsureAll(fmt.Sprintf("%s could not be parsed: %v", rel, err))
			continue
		}
		jobs := make([]string, 0, len(wf.Jobs))
		for name := range wf.Jobs {
			jobs = append(jobs, name)
		}
		sort.Strings(jobs)
		for _, jn := range jobs {
			job := wf.Jobs[jn]
			for _, st := range job.Steps {
				if st.Run == "" {
					continue
				}
				wd := firstNonEmpty(st.WorkingDirectory, job.Defaults.Run.WorkingDirectory, wf.Defaults.Run.WorkingDirectory)
				sh := newShell(root, wd)
				for _, inv := range sh.read(st.Run) {
					for _, s := range suites {
						o := out[s.name]
						if o.workflow != "" {
							continue
						}
						switch reached, why := inv.reaches(root, s); {
						case reached:
							o.workflow, o.command = rel, inv.describe()
						case why != "":
							o.unsure = append(o.unsure, rel+": "+why)
						}
					}
				}
			}
		}
	}
	return out
}

// workflowFile is the part of a GitHub Actions workflow this reader needs.
type workflowFile struct {
	Defaults defaults `yaml:"defaults"`
	Jobs     map[string]struct {
		Defaults defaults `yaml:"defaults"`
		Steps    []struct {
			Run              string `yaml:"run"`
			WorkingDirectory string `yaml:"working-directory"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

type defaults struct {
	Run struct {
		WorkingDirectory string `yaml:"working-directory"`
	} `yaml:"run"`
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// workspace spellings that mean the repository root, which is where
// actions/checkout puts it by default.
var workspace = regexp.MustCompile(`^(\$GITHUB_WORKSPACE|\$\{GITHUB_WORKSPACE\}|\$\{\{\s*github\.workspace\s*\}\})(/|$)`)

// resolve applies p to the directory dir (root-relative, cleaned), returning the
// result and whether it is known. An expression, a variable, or an absolute or
// home-relative path is unknown: the audit cannot say where it points.
func resolve(dir string, known bool, p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return dir, known
	}
	if m := workspace.FindString(p); m != "" {
		dir, known, p = ".", true, strings.TrimPrefix(p, m)
		if p == "" {
			return ".", true
		}
	}
	if !known || strings.Contains(p, "$") || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return "", false
	}
	return path.Clean(path.Join(dir, p)), true
}

// invocation is one test-running command found in a step.
type invocation struct {
	text  string   // the logical line, for the report
	via   string   // the script it was read from, "" for the step itself
	tool  string   // "swift", "xcodebuild" or "flowdeck"
	args  []string // the words after `swift test`, `xcodebuild`, or `flowdeck test`
	cwd   string   // where it runs, root-relative
	known bool     // whether cwd is known
}

func (inv invocation) describe() string {
	// A joined continuation keeps each line's leading space; one is enough.
	text := "`" + strings.Join(strings.Fields(inv.text), " ") + "`"
	if inv.via != "" {
		return text + " (in " + inv.via + ")"
	}
	return text
}

// shellPrefixes may stand before a command without changing what it runs.
var shellPrefixes = map[string]bool{
	"do": true, "then": true, "else": true, "time": true, "exec": true,
	"xcrun": true, "env": true, "command": true, "sudo": true, "nice": true, "!": true,
	"export": true, "local": true, "readonly": true,
}

var (
	assignment = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	variable   = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	// The spellings of "the directory this script is in".
	scriptDir = regexp.MustCompile(`\$\(dirname (\$0|\$\{0\}|\$\{BASH_SOURCE\[0\]\}|\$BASH_SOURCE)\)|\$\{0%/\*\}`)
	// `<<TAG`, `<<-TAG`, `<<'TAG'`; not the here-string `<<<`, which has no body.
	heredoc = regexp.MustCompile(`(?:^|[^<])<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)
)

// shell is the little of a POSIX shell's state this reader follows.
type shell struct {
	root   string // the project root on disk
	cwd    string // root-relative
	known  bool
	vars   map[string]string
	script string // the repo-relative script being read, "" for a step's run block
	depth  int
	seen   map[string]bool
}

func newShell(root, wd string) *shell {
	cwd, known := resolve(".", true, wd)
	return &shell{root: root, cwd: cwd, known: known, vars: map[string]string{}, seen: map[string]bool{}}
}

// expand substitutes what this reader knows: the script's own directory, and
// variables assigned a plain value earlier. Anything else is left as written,
// so a later resolve() sees the `$` and reports it cannot evaluate it.
func (sh *shell) expand(w string) string {
	if sh.script != "" {
		w = scriptDir.ReplaceAllLiteralString(w, "$GITHUB_WORKSPACE/"+path.Dir(sh.script))
	}
	return variable.ReplaceAllStringFunc(w, func(m string) string {
		name := strings.Trim(m, "${}")
		if v, ok := sh.vars[name]; ok {
			return v
		}
		return m
	})
}

// read reads a script: logical lines (comments dropped, continuations joined,
// heredoc bodies skipped), split into simple commands on && || ; | & ( ). It
// tracks `cd`/`pushd` and plain assignments, follows scripts in the
// repository, and returns every test-running command.
func (sh *shell) read(script string) []invocation {
	var out []invocation
	endOfHeredoc := ""
	for _, line := range commands(script) {
		if endOfHeredoc != "" {
			if strings.TrimSpace(line) == endOfHeredoc {
				endOfHeredoc = ""
			}
			continue
		}
		if m := heredoc.FindStringSubmatch(line); m != nil {
			endOfHeredoc = m[1]
		}
		for _, words := range simpleCommands(line) {
			out = append(out, sh.command(line, words)...)
		}
	}
	return out
}

func (sh *shell) command(line string, words []string) []invocation {
	for i := range words {
		words[i] = sh.expand(words[i])
	}
	i := 0
	for i < len(words) && (shellPrefixes[words[i]] || assignment.MatchString(words[i])) {
		i++
	}
	if i >= len(words) {
		// Nothing but assignments: they persist, where a `X=1 cmd` prefix does not.
		for _, w := range words {
			if m := assignment.FindStringSubmatch(w); m != nil {
				if strings.Contains(m[2], "$") {
					delete(sh.vars, m[1])
				} else {
					sh.vars[m[1]] = m[2]
				}
			}
		}
		return nil
	}
	rest := words[i+1:]
	inv := func(tool string, args []string) []invocation {
		return []invocation{{text: line, via: sh.script, tool: tool, args: args, cwd: sh.cwd, known: sh.known}}
	}
	switch cmd := path.Base(words[i]); {
	case cmd == "cd" || cmd == "pushd":
		if len(rest) > 0 {
			sh.cwd, sh.known = resolve(sh.cwd, sh.known, rest[0])
		} else {
			sh.known = false // `cd` alone goes to $HOME
		}
	case cmd == "popd":
		sh.known = false
	case cmd == "swift" && len(rest) > 0 && rest[0] == "test":
		return inv("swift", rest[1:])
	case cmd == "xcodebuild" && (hasWord(rest, "test") || hasWord(rest, "test-without-building")):
		return inv("xcodebuild", rest)
	case cmd == "flowdeck" && len(rest) > 0 && rest[0] == "test" &&
		!(len(rest) > 1 && (rest[1] == "discover" || rest[1] == "plans")):
		return inv("flowdeck", rest[1:])
	case cmd == "bash" || cmd == "sh" || cmd == "zsh":
		for j := 0; j < len(rest); j++ {
			if rest[j] == "-c" && j+1 < len(rest) {
				// A child process: its cd and its assignments stay in it.
				child := *sh
				child.vars = map[string]string{}
				for k, v := range sh.vars {
					child.vars[k] = v
				}
				return child.read(rest[j+1])
			}
			if !strings.HasPrefix(rest[j], "-") {
				return sh.follow(rest[j])
			}
		}
	case strings.Contains(words[i], "/"):
		return sh.follow(words[i])
	}
	return nil
}

// follow reads a script in the repository as the commands it runs. It starts
// in the caller's directory, with none of the caller's variables, and its `cd`s
// do not come back out — it is a child process.
func (sh *shell) follow(p string) []invocation {
	rel, ok := resolve(sh.cwd, sh.known, p)
	if !ok || rel == ".." || strings.HasPrefix(rel, "../") || sh.depth >= 4 || sh.seen[rel] {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(sh.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil // not a file here: some other program, which this does not recognise
	}
	sh.seen[rel] = true
	child := &shell{root: sh.root, cwd: sh.cwd, known: sh.known, vars: map[string]string{},
		script: rel, depth: sh.depth + 1, seen: sh.seen}
	return child.read(string(body))
}

func hasWord(words []string, w string) bool {
	for _, x := range words {
		if x == w {
			return true
		}
	}
	return false
}

// simpleCommands splits one logical shell line into its simple commands, each
// as unquoted words. Quotes group and are removed, a backslash escapes, and a
// word starting with # begins a comment.
func simpleCommands(line string) [][]string {
	var cmds [][]string
	var words []string
	var cur strings.Builder
	inWord := false
	flushWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	flushCmd := func() {
		flushWord()
		if len(words) > 0 {
			cmds = append(cmds, words)
			words = nil
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\'' || c == '"':
			end := strings.IndexByte(line[i+1:], c)
			if end < 0 {
				end = len(line) - i - 1
			}
			cur.WriteString(line[i+1 : i+1+end])
			inWord = true
			i += end + 1
		case c == '\\' && i+1 < len(line):
			cur.WriteByte(line[i+1])
			inWord = true
			i++
		case c == '#' && !inWord:
			flushCmd()
			return cmds
		case c == ' ' || c == '\t':
			flushWord()
		case c == ';' || c == '&' || c == '|' || c == '(' || c == ')':
			flushCmd()
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	flushCmd()
	return cmds
}

// flagValues returns every value of a flag written `name value` or `name=value`
// (or, for xcodebuild's `-only-testing:X`, `name:value`).
func flagValues(args []string, names ...string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		for _, n := range names {
			switch {
			case args[i] == n && i+1 < len(args):
				out = append(out, args[i+1])
			case strings.HasPrefix(args[i], n+"="), strings.HasPrefix(args[i], n+":"):
				out = append(out, args[i][len(n)+1:])
			}
		}
	}
	return out
}

// repeatedValues is flagValues for a flag that also takes several
// space-separated values (flowdeck's `--only A/B C/D`): every word after it up
// to the next flag.
func repeatedValues(args []string, name string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], name+"=") {
			out = append(out, args[i][len(name)+1:])
			continue
		}
		if args[i] != name {
			continue
		}
		for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			out = append(out, args[i])
		}
	}
	return out
}

// reaches reports whether this command runs suite s. When it cannot tell, it
// returns false with the reason.
func (inv invocation) reaches(root string, s suite) (bool, string) {
	switch inv.tool {
	case "swift":
		return inv.swiftReaches(s)
	case "xcodebuild":
		return inv.xcodebuildReaches(root, s)
	}
	return inv.flowdeckReaches(root, s)
}

func (inv invocation) swiftReaches(s suite) (bool, string) {
	for _, a := range inv.args {
		if a == "--list-tests" || strings.HasPrefix(a, "--show-code") {
			return false, "" // prints, runs nothing
		}
	}
	pkg, known := inv.cwd, inv.known
	if p := flagValues(inv.args, "--package-path", "--chdir", "-C"); len(p) > 0 {
		pkg, known = resolve(inv.cwd, inv.known, p[len(p)-1])
	}
	if !known {
		return false, "runs `swift test` in a directory this audit cannot resolve: " + inv.describe()
	}
	if pkg != s.dir {
		return false, ""
	}
	filters, skips := flagValues(inv.args, "--filter"), flagValues(inv.args, "--skip")
	if anyVariable(filters, skips) {
		return false, "runs `swift test` with a filter this audit cannot resolve: " + inv.describe()
	}
	for _, f := range skips {
		if matchesName(f, s.name) {
			return false, ""
		}
	}
	if len(filters) == 0 {
		return true, ""
	}
	for _, f := range filters {
		if filterReaches(f, s.name) {
			return true, ""
		}
	}
	return false, ""
}

func anyVariable(lists ...[]string) bool {
	for _, l := range lists {
		for _, v := range l {
			if strings.Contains(v, "$") {
				return true
			}
		}
	}
	return false
}

// filterReaches reports whether a `swift test --filter` pattern selects any of
// suite's tests. SwiftPM matches it as a regular expression against
// `Suite.Class/method`, which the audit cannot enumerate, so it accepts the two
// shapes it can decide: the suite named outright (`Suite`, `Suite.Class`,
// `Suite\.Class`, `Suite/…`), or a pattern that matches within the suite's name
// itself, and so every test in it.
func filterReaches(f, name string) bool {
	f = strings.TrimPrefix(f, "^")
	if rest, ok := strings.CutPrefix(f, name); ok && (rest == "" || rest == "$" ||
		strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, `\.`) || strings.HasPrefix(rest, "/")) {
		return true
	}
	return matchesName(f, name)
}

// matchesName reports whether pattern matches within name — as a regular
// expression, or literally if it is not a valid one.
func matchesName(pattern, name string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return strings.Contains(name, pattern)
	}
	return re.MatchString(name)
}

// selects reports whether a test identifier (`Suite`, `Suite/Class`, …) names
// the suite or something in it.
func selects(id, name string) bool { return id == name || strings.HasPrefix(id, name+"/") }

func (inv invocation) xcodebuildReaches(root string, s suite) (bool, string) {
	only := flagValues(inv.args, "-only-testing")
	skip := flagValues(inv.args, "-skip-testing")
	if anyVariable(only, skip) {
		return false, "runs `xcodebuild test` with a selector this audit cannot resolve: " + inv.describe()
	}
	if hasWord(skip, s.name) {
		return false, ""
	}
	if len(only) > 0 {
		for _, v := range only {
			if selects(v, s.name) {
				return true, ""
			}
		}
		return false, ""
	}
	if len(flagValues(inv.args, "-testPlan")) > 0 {
		return false, "runs `xcodebuild test` with a test plan, which this audit does not read: " + inv.describe()
	}
	schemes := flagValues(inv.args, "-scheme")
	if len(schemes) == 0 {
		return false, "" // xcodebuild test refuses to run without one
	}
	containers, ok := inv.containers(root, "-project", "-workspace")
	if !ok {
		return false, "runs `xcodebuild test` in a place this audit cannot resolve: " + inv.describe()
	}
	if len(containers) == 0 {
		// No project and no package where it runs: xcodebuild fails, loudly.
		return false, ""
	}
	return schemeReaches(root, containers, schemes[len(schemes)-1], s, inv)
}

func (inv invocation) flowdeckReaches(root string, s suite) (bool, string) {
	only := append(repeatedValues(inv.args, "--only"), flagValues(inv.args, "--test-cases")...)
	var targets []string
	for _, v := range flagValues(inv.args, "--test-targets") {
		targets = append(targets, strings.Split(v, ",")...)
	}
	skip := repeatedValues(inv.args, "--skip")
	if anyVariable(only, targets, skip) {
		return false, "runs `flowdeck test` with a selection this audit cannot resolve: " + inv.describe()
	}
	if hasWord(skip, s.name) {
		return false, ""
	}
	if len(only)+len(targets) > 0 {
		for _, v := range append(only, targets...) {
			if selects(strings.TrimSpace(v), s.name) {
				return true, ""
			}
		}
		return false, ""
	}
	if len(flagValues(inv.args, "--plan")) > 0 {
		return false, "runs `flowdeck test` with a test plan, which this audit does not read: " + inv.describe()
	}
	schemes := flagValues(inv.args, "-s", "--scheme")
	containers, ok := inv.containers(root, "-w", "--workspace", "-p", "--project")
	if len(schemes) == 0 || len(flagValues(inv.args, "-w", "--workspace", "-p", "--project")) == 0 {
		return false, "runs `flowdeck test` on the project and scheme saved by `flowdeck config`, which " +
			"this audit does not read: " + inv.describe()
	}
	if !ok || len(containers) == 0 {
		return false, "runs `flowdeck test` in a place this audit cannot resolve: " + inv.describe()
	}
	return schemeReaches(root, containers, schemes[len(schemes)-1], s, inv)
}

// containers returns, root-relative, what a test command builds from: the
// projects, workspaces or directories its flags name, else the .xcodeproj in
// the directory it runs in, else that directory if it is a package. ok is false
// when a flag's value, or the directory, cannot be resolved.
func (inv invocation) containers(root string, flags ...string) ([]string, bool) {
	var out []string
	named := flagValues(inv.args, flags...)
	for _, v := range named {
		p, ok := resolve(inv.cwd, inv.known, v)
		if !ok {
			return nil, false
		}
		out = append(out, p)
	}
	if len(named) > 0 {
		return out, true
	}
	if !inv.known {
		return nil, false
	}
	m, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(inv.cwd), "*.xcodeproj"))
	for _, p := range m {
		out = append(out, path.Join(inv.cwd, filepath.Base(p)))
	}
	if len(out) == 0 && isFile(root, path.Join(inv.cwd, "Package.swift")) {
		out = append(out, inv.cwd)
	}
	return out, true
}

func isFile(root, rel string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && st.Mode().IsRegular()
}

// packageWorkspace is the workspace wrapper Xcode (and sleevetap's verify.sh)
// puts inside a package so xcodebuild -workspace can open it.
const packageWorkspace = ".swiftpm/xcode/package.xcworkspace"

// schemeReaches reports whether running scheme's tests in one of containers
// runs suite s: from the committed shared scheme when there is one, else from
// what Xcode generates for a package.
func schemeReaches(root string, containers []string, scheme string, s suite, inv invocation) (bool, string) {
	if strings.Contains(scheme, "$") {
		return false, "runs tests on a scheme this audit cannot resolve: " + inv.describe()
	}
	var tried []string
	for i := 0; i < len(containers); i++ {
		c, pkg := containers[i], ""
		switch {
		case strings.HasSuffix(c, "/"+packageWorkspace) || c == packageWorkspace:
			pkg = path.Clean(strings.TrimSuffix(c, packageWorkspace))
		case !strings.HasSuffix(c, ".xcodeproj") && !strings.HasSuffix(c, ".xcworkspace"):
			if isFile(root, path.Join(c, "Package.swift")) {
				pkg = c
			} else {
				// flowdeck -p names a directory holding the project.
				m, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(c), "*.xcodeproj"))
				for _, p := range m {
					containers = append(containers, path.Join(c, filepath.Base(p)))
				}
				continue
			}
		}
		file := path.Join(c, "xcshareddata", "xcschemes", scheme+".xcscheme")
		if pkg != "" {
			file = path.Join(pkg, ".swiftpm", "xcode", "xcshareddata", "xcschemes", scheme+".xcscheme")
		}
		tried = append(tried, file)
		if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file))); err == nil {
			listed, why := schemeTests(b, s.name)
			if why != "" {
				return false, fmt.Sprintf("%s %s, which this audit does not read", file, why)
			}
			return listed, ""
		}
		if pkg != "" {
			// Not committed, so Xcode generates it. The generated scheme that
			// tests is named after the package (one product) or is
			// `<package>-Package` (several), and runs every test target of that
			// package — and only that package's.
			name, ok := packageName(root, pkg)
			if !ok {
				return false, fmt.Sprintf("runs the generated scheme %q of the package at %s, whose name "+
					"this audit cannot read from Package.swift", scheme, pkg)
			}
			return (scheme == name || scheme == name+"-Package") && pkg == s.dir, ""
		}
	}
	return false, fmt.Sprintf("runs tests on scheme %q with nothing selected, and %s is not committed "+
		"(a generated scheme, e.g. XcodeGen's, is not in the repository), so what it tests cannot be read: %s",
		scheme, strings.Join(tried, " or "), inv.describe())
}

var packageNameDecl = regexp.MustCompile(`\bPackage\s*\(\s*name\s*:\s*"([^"\\]+)"\s*[,)]`)

// packageName reads the literal `Package(name: "…")` from a package's manifest.
func packageName(root, dir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "Package.swift"))
	if err != nil {
		return "", false
	}
	m := packageNameDecl.FindStringSubmatch(stripSwiftComments(string(b)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// schemeXML is the part of an .xcscheme this reader needs.
type schemeXML struct {
	TestAction struct {
		TestPlans *struct{} `xml:"TestPlans"`
		Testables []struct {
			Skipped   string `xml:"skipped,attr"`
			Buildable struct {
				BlueprintName string `xml:"BlueprintName,attr"`
			} `xml:"BuildableReference"`
		} `xml:"Testables>TestableReference"`
	} `xml:"TestAction"`
}

// schemeTests reports whether a scheme's TestAction runs the named suite, or,
// when that lives somewhere this does not read, what it could not read.
func schemeTests(b []byte, name string) (bool, string) {
	var s schemeXML
	if err := xml.Unmarshal(b, &s); err != nil {
		return false, "could not be parsed"
	}
	if s.TestAction.TestPlans != nil {
		return false, "tests through a test plan"
	}
	for _, t := range s.TestAction.Testables {
		if t.Buildable.BlueprintName == name && t.Skipped != "YES" {
			return true, ""
		}
	}
	return false, ""
}
