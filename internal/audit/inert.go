package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
)

// InertSecrets is a product whose declared `secrets` nothing will ever write.
//
// `[[product]].secrets` is consumed in exactly one place: the release workflow's
// rendered "Write release configuration" step. If that workflow is excluded, or
// simply is not in the project, the declaration is inert — it reads as
// configured, it survives review, and it produces nothing.
//
// a-bible-verse-each-day is the live case. It declares three keys with
// `secret_formats` shape checks:
//
//	secrets = { REVENUECAT_PUBLIC_SDK_KEY = ..., ADMOB_APPLICATION_ID = ..., ... }
//	secret_formats = { REVENUECAT_PUBLIC_SDK_KEY = "appl_*", ... }
//
// and excludes `.github/workflows/ios-release.yml`. Its releases archive with
// every one of those keys undefined, and nothing anywhere says so. The
// declaration is the strongest available evidence that somebody INTENDED the
// keys to be written, which is what makes the silence worth breaking.
//
// Deliberately not the same finding as a shadow release workflow
// (internal/shadow): that one fires when a managed release exists ALONGSIDE
// another, this one when the managed release is gone and a declaration is left
// pointing at nothing.
type InertSecrets struct {
	// Product is the [[product]] name, or the project name for a single-product
	// repository.
	Product string
	// Keys are the declared secret names, sorted.
	Keys []string
	// Workflow is the release workflow that would have consumed them.
	Workflow string
	// SecretsFile is the path the product declared, which nothing writes.
	SecretsFile string
	// Excluded is true when .lacquer.toml excludes that workflow, false when it
	// is simply absent. The distinction is the whole remedy: an exclusion is a
	// decision to revisit, an absence is a sync away.
	Excluded bool
	// FromProject is true when the declaration is [project].secrets — the
	// single-product spelling, carried by the product Products() synthesises —
	// rather than a [[product]] block. The report names the table the reader
	// has to open, and a single-app manifest has no [[product]] to open.
	FromProject bool
}

// releaseWorkflowFor names the workflow whose rendered step consumes
// [[product]].secrets. Only the iOS profile has one today.
const releaseWorkflowFor = ".github/workflows/ios-release.yml"

// writesPath reports whether body, a workflow's raw text, carries a shell
// command that WRITES file: not one that names it, and not one that copies the
// committed example into place.
//
// The version before this asked whether the name appeared on any line that was
// not a `#` comment (issue #363). The managed ios-ci.yml answered that for
// every iOS repository in the fleet. Its placeholder step names
// Secrets.xcconfig in its title, in `find -name 'Secrets.xcconfig.example'`, in
// an existence test, and in the seed itself:
//
//	cp "Secrets.xcconfig.example" "$scheme_dir/Secrets.xcconfig"
//
// Any one of those satisfied a substring match, so on every project using the
// default path CI's placeholders counted as the release writing real values,
// and the audit could not fire. flare shipped a REPLACE_ME RevenueCat key; kit,
// port-of-entry and multimeter archive with empty keys.
//
// A write, here, is exactly one of:
//
//   - the managed writer, scripts/write-release-config.sh, whose first argument
//     is its destination;
//   - an output redirection (`>`, `>>`, `>|`, `&>`) into the file;
//   - the destination of `cp`, `mv` or `install`, unless every source is a
//     `.example` — copying the committed template into place is a placeholder
//     seed, whichever workflow does it (momfriend's release seeds first and
//     writes afterwards, and the write is what counts);
//   - a `tee` operand, or the file operand of an in-place `sed -i`.
//
// And "the file" means that path, not a path containing its name:
// Secrets.xcconfig.example, Secrets.xcconfig.tmp and OldSecrets.xcconfig are
// other files. The declared path is relative to the component while a
// workflow runs from the repository root, so a writer naming it under a
// directory (Flare/Secrets.xcconfig, ios/MomFriend/Secrets.xcconfig) counts.
//
// The rule is decided by what a command does, never by which workflow holds it
// or what a step is called: the first version of this audit keyed on the
// managed step's NAME and reported a correct hand-rolled writer as broken
// (CLAUDE.md, "Three defects"). Every writer the fleet actually has is one of
// the shapes above: the managed call (Steps, flare, dailybread's testflight.yml),
// a redirection after a multi-line sed (rail), an awk into a .tmp then `mv`
// (momfriend), a redirection from a block (a-bible-verse-each-day).
//
// What it still over-accepts, deliberately, because a false "not written" is
// what teaches people the finding is noise: the managed writer called with no
// keys (a seed-only call, rendered for a sibling product that shares the file),
// and `cat X.example > file`. What it cannot see is a write through a variable
// (`dest=Secrets.xcconfig; … > "$dest"`); no workflow in the fleet does that,
// and one that did would be reported rather than silently accepted.
func writesPath(body, file string) bool {
	for _, line := range shellLines(body) {
		for _, c := range simpleCommands(shellWords(line.text)) {
			if c.writes(file) {
				return true
			}
		}
	}
	return false
}

// shellLine is one logical shell line and the 1-based line of the file it
// starts on.
type shellLine struct {
	text string
	line int
}

// shellLines splits body into lines, joining a line that ends in a backslash to
// the next — the way `sed \` / `-e …` / `src > dest` is one command to the shell.
// A joined line keeps the number of the line it starts on, which is where a
// reader looking for the command will find it.
func shellLines(body string) []shellLine {
	var out []shellLine
	var cur strings.Builder
	start := 0
	for i, line := range strings.Split(body, "\n") {
		if cur.Len() == 0 {
			start = i + 1
		}
		if t := strings.TrimRight(line, " \t"); strings.HasSuffix(t, "\\") {
			cur.WriteString(strings.TrimSuffix(t, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(line)
		out = append(out, shellLine{cur.String(), start})
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, shellLine{cur.String(), start})
	}
	return out
}

// shellWord is a word with its quoting removed, or an operator.
type shellWord struct {
	text string
	op   string
}

// Longest first, so `>>` is not read as two `>`.
var shellOps = []string{"<<<", "&>>", ">>", ">|", "&>", ">&", "<<", "&&", "||", "|&", ";;", ">", "<", ";", "&", "|", "(", ")"}

// shellWords tokenises one line the way the shell would, closely enough to find
// commands and their operands: quotes are removed, operators are split out, and
// a `#` that begins a word ends the line — a comment cannot write anything
// (issue #363). A quote left open at the end of the line (the opening line of
// a multi-line awk program) swallows the rest of the line and no more.
//
// The line is YAML as well as shell. `- run:` and `name: …` tokenise as
// ordinary words, which is harmless: only a writer's operands are looked at.
func shellWords(line string) []shellWord {
	var out []shellWord
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			out = append(out, shellWord{text: cur.String()})
		}
		cur.Reset()
		inWord = false
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\'':
			inWord = true
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				cur.WriteString(line[i+1:])
				i = len(line)
				continue
			}
			cur.WriteString(line[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			inWord = true
			for i++; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' && i+1 < len(line) && strings.IndexByte("\"\\$`", line[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(line[i])
			}
		case c == '\\':
			inWord = true
			if i+1 < len(line) {
				i++
				cur.WriteByte(line[i])
			}
		case c == ' ' || c == '\t':
			flush()
		case c == '#' && !inWord:
			flush()
			return out
		case strings.IndexByte(";&|()<>", c) >= 0:
			// Bare digits directly before a redirection are its file descriptor
			// (the 2 in 2>/dev/null), not a word of the command.
			if (c == '>' || c == '<') && inWord && strings.Trim(cur.String(), "0123456789") == "" {
				cur.Reset()
				inWord = false
			}
			flush()
			for _, op := range shellOps {
				if strings.HasPrefix(line[i:], op) {
					out = append(out, shellWord{op: op})
					i += len(op) - 1
					break
				}
			}
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// shellCommand is one simple command: its words, and where its output goes.
type shellCommand struct {
	argv    []string
	outputs []string
}

// simpleCommands splits a line's words at `;`, `&&`, `|` and the like, and
// separates each command's output redirections from its arguments. An input
// redirection (`<`, `<<EOF`) splits too, which keeps its operand out of the
// arguments before it.
func simpleCommands(words []shellWord) []shellCommand {
	var out []shellCommand
	var cur shellCommand
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch w.op {
		case "":
			cur.argv = append(cur.argv, w.text)
		case ">", ">>", ">|", "&>", "&>>", ">&":
			if i+1 < len(words) && words[i+1].op == "" {
				i++
				cur.outputs = append(cur.outputs, words[i].text)
			}
		default:
			out = append(out, cur)
			cur = shellCommand{}
		}
	}
	return append(out, cur)
}

// writes reports whether this command writes file.
func (c shellCommand) writes(file string) bool {
	for _, o := range c.outputs {
		if samePath(o, file) {
			return true
		}
	}
	// The command word is the first word that names a writer. What precedes it
	// is YAML (`- run:`), a shell keyword (`then`), or a wrapper (`sudo`).
	for i, w := range c.argv {
		ops := operands(c.argv[i+1:])
		switch w[strings.LastIndexByte(w, '/')+1:] {
		case "write-release-config.sh":
			// Usage: write-release-config.sh <dest.xcconfig> [KEY[=GLOB] ...]
			return len(ops) > 0 && samePath(ops[0], file)
		case "cp", "mv", "install":
			if len(ops) < 2 || !samePath(ops[len(ops)-1], file) {
				return false
			}
			for _, src := range ops[:len(ops)-1] {
				if !strings.HasSuffix(src, ".example") {
					return true
				}
			}
			return false // every source is the committed example: a placeholder seed.
		case "tee":
			for _, o := range ops {
				if samePath(o, file) {
					return true
				}
			}
			return false
		case "sed":
			for _, a := range c.argv[i+1:] {
				if strings.HasPrefix(a, "-i") || strings.HasPrefix(a, "--in-place") {
					return len(ops) > 0 && samePath(ops[len(ops)-1], file)
				}
			}
			return false
		}
	}
	return false
}

// operands drops flags.
func operands(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// samePath reports whether word names file: the path itself, or the path under
// a directory (a component prefix, or where the Xcode project reads it). A name
// that merely CONTAINS file — file.example, file.tmp, Oldfile — is another file.
func samePath(word, file string) bool {
	word = strings.TrimPrefix(word, "./")
	file = strings.TrimPrefix(file, "./")
	return word == file || strings.HasSuffix(word, "/"+file)
}

// InertSecretDeclarations returns every product declaring secrets that nothing
// in this project will write.
func InertSecretDeclarations(projectRoot string, cfg *config.Config) []InertSecrets {
	if cfg == nil {
		return nil
	}
	products := cfg.Products()
	if len(products) == 0 {
		return nil
	}

	// Read EVERY workflow, not just the managed release. The first version of
	// this asked whether ios-release.yml contained the literal string
	// "Write release configuration" — the managed step's name — and that was
	// wrong in the direction that matters.
	//
	// a-bible-verse-each-day declares secrets_file = "Config/Monetization.xcconfig"
	// and excludes ios-release.yml, so the managed step is genuinely absent. But
	// its project-owned release carries a hand-rolled step, "Create protected
	// runtime configuration", that reads all three secrets, FAILS CLOSED on any
	// unset (`: "${VAR:?Missing VAR}"`), and writes exactly that file. The
	// project is not exposed at all. Keying on the managed step's NAME reported
	// a correct setup as broken — a false positive on the one project the check
	// was built for.
	//
	// The question is not "does the managed step exist" but "does anything write
	// the file the product declared". That is what the release actually depends
	// on, and it is agnostic about who writes it.
	workflows := workflowFiles(projectRoot)

	excluded := false
	for _, e := range cfg.Project.Exclude {
		if strings.Contains(e.Path, "ios-release.yml") {
			excluded = true
			break
		}
	}

	var out []InertSecrets
	for _, p := range products {
		if len(p.Secrets) == 0 {
			continue
		}
		// Does any workflow WRITE the declared file (writesPath)? Not mention it:
		// the managed ios-ci.yml mentions Secrets.xcconfig everywhere, and its
		// placeholder seed counting as a write is what kept this audit silent
		// across the fleet. Not "is the managed step there" either — see above.
		// Every workflow is read, CI included, and the seed is excluded by what
		// it does (copies the committed example), not by where it lives: a
		// release that only seeds from the example ships placeholders exactly
		// as CI does, and a project's own writer can live in any workflow.
		written := false
		for _, wf := range workflows {
			if writesPath(wf.body, p.SecretsPath()) {
				written = true
				break
			}
		}
		if written {
			continue
		}
		keys := make([]string, 0, len(p.Secrets))
		for k := range p.Secrets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out = append(out, InertSecrets{
			Product:     p.Name,
			Keys:        keys,
			Workflow:    releaseWorkflowFor,
			SecretsFile: p.SecretsPath(),
			Excluded:    excluded,
			FromProject: len(cfg.Product) == 0,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Product < out[j].Product })
	return out
}

// FormatInertSecrets renders the report, or "" when there is nothing to say.
func FormatInertSecrets(fs []InertSecrets) string {
	if len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\ndeclared secrets that nothing will write:\n")
	for _, f := range fs {
		if f.FromProject {
			fmt.Fprintf(&b, "  [project] declares %d secret(s): %s\n", len(f.Keys), strings.Join(f.Keys, ", "))
		} else {
			fmt.Fprintf(&b, "  [[product]] %s declares %d secret(s): %s\n", f.Product, len(f.Keys), strings.Join(f.Keys, ", "))
		}
		fmt.Fprintf(&b, "    Nothing in .github/workflows writes %s.\n", f.SecretsFile)
		if f.Excluded {
			fmt.Fprintf(&b, "    %s is EXCLUDED in .lacquer.toml, so the managed step that would write it is never rendered.\n", f.Workflow)
		}
	}
	b.WriteString("A release built here archives with every one of those keys UNDEFINED. The declaration\n" +
		"reads as configured and produces nothing — which is worse than declaring none, because it\n" +
		"is the evidence somebody intended them to be written.\n" +
		"Either re-adopt the managed release workflow, or write the keys from whatever workflow you\n" +
		"release from — scripts/write-release-config.sh is already synced here and fails closed on\n" +
		"an unset or wrong-shaped value. If the keys genuinely are not needed, remove the\n" +
		"declaration so it stops claiming otherwise.\n")
	return b.String()
}

// workflowFile is one file under .github/workflows, by its repo-relative path.
type workflowFile struct {
	path string
	body string
}

// workflowFiles reads every workflow in the project — managed, project-owned
// and excluded alike, because what a runner executes does not depend on who
// owns the file — sorted by path. A missing directory is no workflows.
func workflowFiles(projectRoot string) []workflowFile {
	dir := filepath.Join(projectRoot, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []workflowFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			out = append(out, workflowFile{path: ".github/workflows/" + e.Name(), body: string(b)})
		}
	}
	return out
}
