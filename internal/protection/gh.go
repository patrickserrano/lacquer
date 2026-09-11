package protection

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// runGH executes `gh` and returns stdout, stderr and the exit error.
//
// stdout is returned EVEN ON ERROR, because that is where `gh api` writes the
// response body for a non-2xx: a 404 arrives as exit 1 with
// `{"message":"Branch not protected","status":"404"}` on stdout and a one-line
// summary on stderr. The status code is the whole answer here — 404 means "no
// protection" (a finding) and 403 means "not allowed to look" (not a finding
// and not a pass) — so discarding the body would erase the distinction this
// package exists to preserve.
//
// A package var so tests can drive every branch without a network, an account,
// or a `gh` binary.
var runGH = func(args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.Command("gh", args...) // #nosec G204 -- args are literals plus a slug/branch validated below
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

var (
	slugRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

// ghError is a non-2xx from the API, carrying the status so callers can tell
// "no protection" from "not allowed to look".
type ghError struct {
	Status  int
	Message string
	Path    string
}

func (e *ghError) Error() string {
	switch e.Status {
	case 403:
		return fmt.Sprintf("HTTP 403 reading %s: %s (a personal account on GitHub Free cannot use branch protection on a private repo, and reading it otherwise needs admin)", e.Path, e.Message)
	case 404:
		// Only reached for a 404 that is NOT "Branch not protected" — see
		// unprotectedAnswer. The hint matters because the obvious reading of a
		// 404 here ("there is nothing there") is the wrong one.
		return fmt.Sprintf("HTTP 404 reading %s: %s (reading branch protection needs ADMIN, and GitHub answers 404 rather than 403 when you are not allowed to look)", e.Path, e.Message)
	}
	return fmt.Sprintf("HTTP %d reading %s: %s", e.Status, e.Path, e.Message)
}

// apiBody is the error envelope every GitHub REST error shares. `status` is a
// STRING in the body, not a number.
type apiBody struct {
	Message string `json:"message"`
	Status  string `json:"status"`
}

// api calls `gh api <path>` and returns the response body.
func api(path string) ([]byte, error) {
	out, errOut, err := runGH("api", path)
	if err == nil {
		return out, nil
	}
	var body apiBody
	if jsonErr := json.Unmarshal(out, &body); jsonErr == nil && body.Status != "" {
		code := 0
		if _, scanErr := fmt.Sscanf(body.Status, "%d", &code); scanErr == nil {
			return nil, &ghError{Status: code, Message: body.Message, Path: path}
		}
	}
	// No parseable body: `gh` is missing, logged out, or the network is down.
	// Whatever it was, nothing was learned — which is the Unavailable verdict,
	// never a pass.
	msg := strings.TrimSpace(string(errOut))
	if msg == "" {
		msg = err.Error()
	}
	return nil, fmt.Errorf("gh api %s: %s", path, msg)
}

// DefaultBranch asks the API which branch a repository's protection would have
// to cover.
//
// Asked rather than assumed. Guessing "main" would report a repository still on
// `master` as UNPROTECTED — a false finding indistinguishable from the true one
// this command is for, which is the fastest way to teach an operator the output
// is noise.
func DefaultBranch(repo string) (string, error) {
	if !slugRe.MatchString(repo) {
		return "", fmt.Errorf("invalid repository %q (expected owner/name)", repo)
	}
	body, err := api("repos/" + repo)
	if err != nil {
		return "", err
	}
	var v struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", fmt.Errorf("parse repo metadata for %s: %w", repo, err)
	}
	if v.DefaultBranch == "" {
		return "", fmt.Errorf("%s reports no default branch", repo)
	}
	return v.DefaultBranch, nil
}

// Fetch reads the required status-check contexts for one branch, from BOTH
// protection systems.
//
// Classic branch protection and repository rulesets are independent, and a repo
// can be governed by either. Reading only the classic endpoint would report a
// ruleset-protected repository as unprotected — the mirror image of the defect
// being hunted, and just as wrong.
func Fetch(repo, branch string) (Requirements, error) {
	if !slugRe.MatchString(repo) {
		return Requirements{}, fmt.Errorf("invalid repository %q (expected owner/name)", repo)
	}
	if !branchRe.MatchString(branch) {
		return Requirements{}, fmt.Errorf("invalid branch %q", branch)
	}

	var req Requirements
	var sources []string

	classic, err := api("repos/" + repo + "/branches/" + branch + "/protection")
	switch {
	case err == nil:
		var v struct {
			RequiredStatusChecks struct {
				Contexts []string `json:"contexts"`
				Checks   []struct {
					Context string `json:"context"`
				} `json:"checks"`
			} `json:"required_status_checks"`
		}
		if err := json.Unmarshal(classic, &v); err != nil {
			return Requirements{}, fmt.Errorf("parse protection for %s@%s: %w", repo, branch, err)
		}
		req.Protected = true
		sources = append(sources, "branch protection")
		// `contexts` is deprecated in favour of `checks`, and a repo may answer
		// with either or both. Taking the union rather than picking one means a
		// future removal of the deprecated field cannot silently turn a
		// correctly-protected repo into a finding.
		req.Contexts = append(req.Contexts, v.RequiredStatusChecks.Contexts...)
		for _, c := range v.RequiredStatusChecks.Checks {
			req.Contexts = append(req.Contexts, c.Context)
		}
	case unprotectedAnswer(err):
		// A real, readable answer — not a failure. See unprotectedAnswer for why
		// the status code alone is not enough to establish that.
	default:
		return Requirements{}, err
	}

	rules, rulesErr := api("repos/" + repo + "/rules/branches/" + branch)
	if rulesErr != nil {
		// A ruleset can only ADD requirements, never remove one. So when the
		// classic endpoint has already produced a passing answer, failing to read
		// rulesets cannot change it and the sweep continues with a note. When it
		// has NOT, the finding would rest on something unread — so the whole
		// repository is reported as unchecked instead. This is the one place the
		// two answers could quietly merge, and it is deliberately the place they
		// are kept apart.
		if contains(req.Contexts, Gate) {
			req.Source = "branch protection (rulesets unreadable: " + rulesErr.Error() + ")"
			req.Contexts = dedupe(req.Contexts)
			return req, nil
		}
		return Requirements{}, rulesErr
	}
	var rs []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(rules, &rs); err != nil {
		return Requirements{}, fmt.Errorf("parse rules for %s@%s: %w", repo, branch, err)
	}
	if len(rs) > 0 {
		req.Protected = true
		sources = append(sources, "ruleset")
	}
	for _, r := range rs {
		if r.Type != "required_status_checks" {
			continue
		}
		for _, c := range r.Parameters.RequiredStatusChecks {
			req.Contexts = append(req.Contexts, c.Context)
		}
	}

	req.Contexts = dedupe(req.Contexts)
	req.Source = strings.Join(sources, " + ")
	if req.Source == "" {
		req.Source = "nothing (no protection, no ruleset)"
	}
	return req, nil
}

// Check performs the whole comparison for one repository: read what the local
// checkout can post, read what the branch requires, compare.
//
// branch may be empty, in which case the repository's default branch is asked
// for. Every failure below produces an Unavailable report rather than an error
// return, because a sweep over a roster must not stop at the first repository an
// operator lacks admin on — the other sixteen answers are still worth having,
// and the one that could not be read says so on its own line.
func Check(projectRoot, repo, branch string) Report {
	w, err := Local(projectRoot)
	if err != nil {
		// The workflow side failed, so "posted by nothing here" could not be
		// established for any context. Refusing to render a verdict off half the
		// evidence is the same rule the rest of this package follows.
		return Compare(repo, branch, Requirements{}, err, Workflows{})
	}
	if branch == "" {
		b, err := DefaultBranch(repo)
		if err != nil {
			return Compare(repo, "?", Requirements{}, err, w)
		}
		branch = b
	}
	req, err := Fetch(repo, branch)
	return Compare(repo, branch, req, err, w)
}

// unprotectedAnswer reports whether err is the API saying, readably, that this
// branch has no classic protection.
//
// The status code alone does not establish that, and getting this wrong would
// have made the whole command lie. Measured on the live API:
//
//	repos/PixelFoxStudio/Windsock/branches/main/protection
//	  -> 404 {"message":"Branch not protected"}      genuinely unprotected
//	repos/cli/cli/branches/trunk/protection
//	  -> 404 {"message":"Not Found"}                 no admin, so not allowed to look
//
// Reading protection requires ADMIN, and GitHub hides what you may not see
// behind 404 rather than 403 — so "there is no protection" and "you cannot be
// told whether there is protection" arrive with the same status. Treating every
// 404 as unprotected would report every repository an operator lacks admin on
// as a finding: a confident, false accusation, generated by not looking. The
// message is the only thing that separates them, so it is what this keys on.
// Anything else 404 falls through to the error path and becomes Unavailable.
func unprotectedAnswer(err error) bool {
	var g *ghError
	return errors.As(err, &g) && g.Status == 404 && strings.EqualFold(g.Message, "Branch not protected")
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Slug derives "owner/name" from the checkout's origin remote.
//
// Derived rather than configured because .lacquer.toml records `github_org` and
// `name` but not the pair, and in this fleet three of seventeen checkouts sit in
// a directory named differently from their repository — so composing the slug
// from a manifest name would point the API at the wrong repo and report a
// verdict about somebody else's protection.
func Slug(dir string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("no origin remote in %s (pass --repo owner/name)", dir)
	}
	url := strings.TrimSpace(string(out))
	url = strings.TrimSuffix(url, ".git")
	switch {
	case strings.HasPrefix(url, "git@"):
		if _, path, ok := strings.Cut(url, ":"); ok {
			url = path
		}
	case strings.Contains(url, "://"):
		if _, path, ok := strings.Cut(url, "github.com/"); ok {
			url = path
		}
	}
	if !slugRe.MatchString(url) {
		return "", fmt.Errorf("cannot derive owner/name from origin %q (pass --repo owner/name)", strings.TrimSpace(string(out)))
	}
	return url, nil
}
