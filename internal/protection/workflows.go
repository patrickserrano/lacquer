package protection

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// workflowFile is the sliver of the Actions schema this needs. Everything else
// in a workflow is irrelevant to the question "what contexts can this repo
// post", and decoding into a narrow struct means an unrelated schema addition
// upstream cannot break the parse.
type workflowFile struct {
	Jobs map[string]struct {
		Name string `yaml:"name"`
		// yaml.Node rather than a typed field for both of these: `if:` is
		// commonly a string (`if: always()`) but is legally a bool, and
		// `strategy.matrix` is a mapping whose shape varies. Decoding into a
		// concrete type would turn a legal workflow into an unreadable one, and
		// an unreadable workflow is reported as such — so a too-strict type here
		// would manufacture "unknown" for files that are perfectly fine.
		If yaml.Node `yaml:"if"`
		// `needs` is legally a single string or a list of them, so it is read as
		// a Node and normalised below.
		Needs    yaml.Node `yaml:"needs"`
		Strategy struct {
			Matrix yaml.Node `yaml:"matrix"`
		} `yaml:"strategy"`
	} `yaml:"jobs"`
}

// alwaysExpr is the one `if:` expression treated as still always reporting.
//
// GitHub's own `always()`, which is what every lacquer profile's `ci-ok` job
// carries. Nothing else is interpreted: evaluating expressions would mean
// guessing, and a wrong guess in this direction produces a false PASS — a check
// reported as a gate that a skip can satisfy, which is the exact defect this
// package exists to find. `if: github.event_name == 'pull_request'` is the
// common near-miss (always true on the only event branch protection cares
// about), and it is deliberately NOT special-cased: one unskippable poster is
// enough to clear a repository, so the conservative reading costs nothing where
// a real gate exists, and the report names the conditional job either way.
func alwaysExpr(n yaml.Node) bool {
	s := strings.TrimSpace(n.Value)
	s = strings.TrimPrefix(s, "${{")
	s = strings.TrimSuffix(s, "}}")
	return strings.EqualFold(strings.TrimSpace(s), "always()")
}

// needsOf normalises a job's `needs:` into job ids.
func needsOf(n yaml.Node) []string {
	if n.IsZero() {
		return nil
	}
	var one string
	if err := n.Decode(&one); err == nil {
		return []string{one}
	}
	var many []string
	if err := n.Decode(&many); err == nil {
		return many
	}
	return nil
}

// Local reads every workflow in the checkout and reports the contexts it can
// post.
//
// A directory that does not exist is not an error: plenty of repositories in a
// roster have no workflows at all, and that is a fact about them worth
// comparing against their branch protection rather than a reason to abort the
// sweep. A file that exists and cannot be PARSED is different, and lands in
// Unreadable — see Workflows.
func Local(projectRoot string) (Workflows, error) {
	var w Workflows
	dir := filepath.Join(projectRoot, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return w, nil
	}
	if err != nil {
		return w, fmt.Errorf("read %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			w.Unreadable = append(w.Unreadable, name)
			continue
		}
		var wf workflowFile
		if err := yaml.Unmarshal(body, &wf); err != nil {
			w.Unreadable = append(w.Unreadable, name)
			continue
		}
		ids := make([]string, 0, len(wf.Jobs))
		for id := range wf.Jobs {
			ids = append(ids, id)
		}
		// Sorted, because Go randomises map iteration and the report would
		// otherwise reorder itself between runs — which makes a diff of two
		// sweeps unreadable, and this output exists to be diffed.
		sort.Strings(ids)

		// Skippability is transitive: a job with no `if:` of its own still skips
		// when something it `needs` skips, because GitHub does not run a job
		// whose dependency was skipped. Resolving the graph rather than reading
		// each job in isolation is what keeps `needs: [changes]` — the shape
		// every managed profile uses for its real jobs — from reading as an
		// unskippable gate.
		always := map[string]bool{}
		var resolve func(id string, seen map[string]bool) bool
		resolve = func(id string, seen map[string]bool) bool {
			if v, done := always[id]; done {
				return v
			}
			if seen[id] {
				// A cycle never runs at all, so it certainly never reports.
				return false
			}
			seen[id] = true
			j, ok := wf.Jobs[id]
			if !ok {
				// `needs` naming a job this file does not define. The workflow is
				// invalid and nothing here will report, so it is not a gate.
				return false
			}
			ok = j.If.IsZero() || alwaysExpr(j.If)
			for _, n := range needsOf(j.Needs) {
				if !resolve(n, seen) {
					ok = false
				}
			}
			always[id] = ok
			return ok
		}
		for _, id := range ids {
			resolve(id, map[string]bool{})
		}

		for _, id := range ids {
			j := wf.Jobs[id]
			// GitHub names the check run after the job's `name:`, falling back to
			// the job KEY when there is none. The fallback is not a curiosity:
			// a job written without a name still posts a context, and missing it
			// would report a required context as posted by nothing.
			jobName := j.Name
			if jobName == "" {
				jobName = id
			}
			w.Jobs = append(w.Jobs, Job{
				Workflow:      name,
				Name:          jobName,
				Conditional:   !j.If.IsZero(),
				AlwaysReports: always[id],
				Matrix:        !j.Strategy.Matrix.IsZero(),
				Dynamic:       strings.Contains(jobName, "${{"),
			})
		}
	}
	sort.Slice(w.Jobs, func(i, k int) bool {
		if w.Jobs[i].Workflow != w.Jobs[k].Workflow {
			return w.Jobs[i].Workflow < w.Jobs[k].Workflow
		}
		return w.Jobs[i].Name < w.Jobs[k].Name
	})
	sort.Strings(w.Unreadable)
	return w, nil
}
