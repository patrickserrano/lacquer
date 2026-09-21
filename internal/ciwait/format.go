package ciwait

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func nameList(cs []Check) string {
	n := make([]string, len(cs))
	for i, c := range cs {
		n[i] = c.Name
	}
	return strings.Join(n, ", ")
}

func supersededList(cs []Check) string {
	n := make([]string, len(cs))
	for i, c := range cs {
		n[i] = fmt.Sprintf("%s (run %d, %s)", c.Name, c.RunID, c.Label)
	}
	return strings.Join(n, ", ")
}

func fmtDur(c Check) string {
	if !c.HasDuration {
		return "-"
	}
	return c.Duration.String()
}

// Format renders a Result for a person or an agent: a table of every check with
// its name, conclusion and duration, then a verdict line that says which of the
// outcomes this was. Skipped checks are named on their own line whatever the
// outcome, because a skipped required job is how a PR looks green untested.
func Format(r Result) string {
	var b strings.Builder
	repo := ""
	if r.Repo != "" {
		repo = " (" + r.Repo + ")"
	}
	if r.Outcome == Error {
		fmt.Fprintf(&b, "lacquer wait: PR #%d%s\n", r.PR, repo)
		for _, h := range r.HeadChanges {
			fmt.Fprintf(&b, "head moved %s\n", h)
		}
		fmt.Fprintf(&b, "ERROR: %s\n", r.Message)
		return b.String()
	}

	fmt.Fprintf(&b, "lacquer wait: PR #%d%s head %s, waited %s\n", r.PR, repo, short(r.Head), r.Elapsed.Round(time.Second))
	for _, h := range r.HeadChanges {
		fmt.Fprintf(&b, "head moved %s: results for the old commit were discarded, this is the new commit's\n", h)
	}
	if len(r.Checks) > 0 {
		fmt.Fprintf(&b, "\n%-14s %-8s %s\n", "CONCLUSION", "TIME", "CHECK")
		for _, c := range r.Checks {
			label := c.Label
			if !c.Terminal {
				label = c.Label + "*"
			}
			fmt.Fprintf(&b, "%-14s %-8s %s\n", label, fmtDur(c), c.Name)
		}
		if len(r.Running()) > 0 {
			b.WriteString("(* not finished; time is elapsed so far)\n")
		}
		b.WriteString("\n")
	}

	if len(r.Superseded) > 0 {
		fmt.Fprintf(&b, "ignored, superseded by a newer run of the same workflow: %s\n\n", supersededList(r.Superseded))
	}

	failed, running, skipped := r.Failed(), r.Running(), r.Skipped()
	total := len(r.Checks)
	switch r.Outcome {
	case Passed:
		if len(skipped) == total {
			fmt.Fprintf(&b, "PASSED, BUT NOTHING RAN: every check was skipped (%d of %d).\n", total, total)
		} else {
			fmt.Fprintf(&b, "PASSED: %d checks finished, none failed.\n", total)
		}
	case Failed:
		fmt.Fprintf(&b, "FAILED: %d of %d checks failed: %s\n", len(failed), total, nameList(failed))
		if len(running) > 0 {
			fmt.Fprintf(&b, "The failure is decisive. The ceiling hit with %d checks still running; they were abandoned and their results do not matter: %s\n", len(running), nameList(running))
		}
	case TimedOut:
		fmt.Fprintf(&b, "TIMED OUT after %s: %d of %d checks were still running. This is not a failure and not a pass: the result is unknown.\n", r.Elapsed.Round(time.Second), len(running), total)
		fmt.Fprintf(&b, "still running: %s\n", nameList(running))
	case NoChecks:
		fmt.Fprintf(&b, "NO CHECKS: PR #%d reports no checks (%s). This is not a pass: nothing tested this commit. "+
			"Either its workflows have not registered, none matched (a path filter, a misconfiguration), or checks are not configured.\n", r.PR, r.Message)
	}
	if len(skipped) > 0 && total > 0 && len(skipped) != total {
		fmt.Fprintf(&b, "skipped: %s\n", nameList(skipped))
		b.WriteString("A skipped job did not run: if any of these is required, the PR looks green without having been tested.\n")
	} else if len(skipped) == total && total > 0 {
		fmt.Fprintf(&b, "skipped: %s\n", nameList(skipped))
	}
	return b.String()
}

// FormatJSON is the machine-readable form of a Result (`--json`).
func FormatJSON(r Result) string {
	type jcheck struct {
		Name       string   `json:"name"`
		Kind       Kind     `json:"kind"`
		Status     string   `json:"status"`
		Conclusion string   `json:"conclusion"`
		Terminal   bool     `json:"terminal"`
		Seconds    *float64 `json:"duration_seconds,omitempty"`
		URL        string   `json:"url,omitempty"`
	}
	strs := func(cs []Check) []string {
		out := []string{}
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return out
	}
	checks := []jcheck{}
	for _, c := range r.Checks {
		j := jcheck{Name: c.Name, Kind: c.Kind, Terminal: c.Terminal, URL: c.URL}
		if c.Terminal {
			j.Status, j.Conclusion = "completed", c.Label
		} else {
			j.Status = c.Label
		}
		if c.HasDuration {
			s := c.Duration.Seconds()
			j.Seconds = &s
		}
		checks = append(checks, j)
	}
	changes := r.HeadChanges
	if changes == nil {
		changes = []string{}
	}
	out, _ := json.MarshalIndent(struct {
		Outcome     string   `json:"outcome"`
		ExitCode    int      `json:"exit_code"`
		PR          int      `json:"pr"`
		Repo        string   `json:"repo,omitempty"`
		Head        string   `json:"head"`
		Message     string   `json:"message,omitempty"`
		Elapsed     float64  `json:"elapsed_seconds"`
		Failed      []string `json:"failed"`
		Running     []string `json:"running"`
		Skipped     []string `json:"skipped"`
		Superseded  []string `json:"superseded"`
		HeadChanges []string `json:"head_changes"`
		Checks      []jcheck `json:"checks"`
	}{r.Outcome.String(), r.Outcome.ExitCode(), r.PR, r.Repo, r.Head, r.Message, r.Elapsed.Seconds(),
		strs(r.Failed()), strs(r.Running()), strs(r.Skipped()), strs(r.Superseded), changes, checks}, "", "  ")
	return string(out) + "\n"
}
