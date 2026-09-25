package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/decisions"
	"github.com/patrickserrano/lacquer/internal/fleet"
)

// ghScript stands in for gh in `lacquer decisions`: replies by argument list, and
// a record of every call, so a test can say what was asked and of which repository.
func ghScript(t *testing.T, replies map[string]string, errs map[string]error) *[]string {
	t.Helper()
	var calls []string
	old := decisionsRunner
	t.Cleanup(func() { decisionsRunner = old })
	decisionsRunner = func(_ context.Context, args ...string) ([]byte, error) {
		key := strings.Join(args, " ")
		calls = append(calls, key)
		if err := errs[key]; err != nil {
			return nil, err
		}
		out, ok := replies[key]
		if !ok {
			return nil, fmt.Errorf("unscripted gh %s", key)
		}
		return []byte(out), nil
	}
	return &calls
}

func listFor(repo string) string {
	return "issue list -R " + repo + " --label decisions --state open --json number,title,url --limit 100"
}

func closedFor(repo string) string {
	return "issue list -R " + repo + " --label decisions --state closed --json number,title,url --limit 100"
}

func viewFor(repo string, n int) string {
	return fmt.Sprintf("issue view %d -R %s --json comments", n, repo)
}

func commentsFor(repo string, bodies ...string) string {
	var parts []string
	for i, b := range bodies {
		parts = append(parts, fmt.Sprintf(`{"author":{"login":"op"},"body":%q,"url":"https://github.com/%s/issues/7#issuecomment-%d","createdAt":"2026-09-2%dT04:10:00Z"}`, b, repo, i, i+1))
	}
	return `{"comments":[` + strings.Join(parts, ",") + `]}`
}

const oneIssueJSON = `[{"number":7,"title":"Decisions","url":"https://github.com/%s/issues/7"}]`

func runDecisions(t *testing.T, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(append([]string{"decisions"}, args...), envMap(env), &out, &errb)
	return code, out.String(), errb.String()
}

func TestDecisionsPrintsARepositorysDecisionsOldestFirst(t *testing.T) {
	older := decisions.Body(decisions.Record{Text: "first call", From: decisions.NoLink, At: time.Date(2026, 9, 21, 4, 10, 0, 0, time.UTC)})
	newer := decisions.Body(decisions.Record{Text: "second call", Basis: "a measurement", From: decisions.NoLink, At: time.Date(2026, 9, 22, 4, 10, 0, 0, time.UTC)})
	calls := ghScript(t, map[string]string{
		listFor("o/r"):    fmt.Sprintf(oneIssueJSON, "o/r"),
		viewFor("o/r", 7): commentsFor("o/r", older, newer),
	}, nil)
	code, stdout, stderr := runDecisions(t, nil, "o/r")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if i, j := strings.Index(stdout, "first call"), strings.Index(stdout, "second call"); i < 0 || j < i || !strings.Contains(stdout, "a measurement") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if got := strings.Join(*calls, "|"); got != listFor("o/r")+"|"+viewFor("o/r", 7) {
		t.Errorf("gh calls: %s", got)
	}
}

// "Nothing recorded" is an answer; "could not ask" is a failure. They must not look alike.
func TestDecisionsSeparatesNoneRecordedFromAFailure(t *testing.T) {
	ghScript(t, map[string]string{listFor("o/r"): `[]`, closedFor("o/r"): `[]`}, nil)
	code, stdout, stderr := runDecisions(t, nil, "o/r")
	if code != 0 || stdout != "no decisions recorded for o/r\n" || stderr != "" {
		t.Errorf("none: code %d stdout %q stderr %q", code, stdout, stderr)
	}

	ghScript(t, nil, map[string]error{listFor("o/r"): errors.New("gh: HTTP 401: bad credentials")})
	code, stdout, stderr = runDecisions(t, nil, "o/r")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "could not read the decisions for o/r") || !strings.Contains(stderr, "bad credentials") || strings.Contains(stderr+stdout, "no decisions recorded") {
		t.Errorf("failure: code %d stdout %q stderr %q", code, stdout, stderr)
	}

	// Two open decisions issues is refused by name, not read as none.
	ghScript(t, map[string]string{listFor("o/r"): `[{"number":3,"title":"a","url":"u"},{"number":9,"title":"b","url":"u"}]`}, nil)
	code, stdout, stderr = runDecisions(t, nil, "o/r")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "o/r#3, o/r#9") {
		t.Errorf("two issues: code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestDecisionsFleetReadsTheConfiguredFleetRepository(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
		repo string
	}{
		"the default": {nil, []string{"--fleet"}, defaultFleetRepo},
		"the env":     {map[string]string{"LACQUER_FLEET_REPO": "acme/ops"}, []string{"--fleet"}, "acme/ops"},
		"the flag":    {map[string]string{"LACQUER_FLEET_REPO": "acme/ops"}, []string{"--fleet", "--fleet-repo", "acme/other"}, "acme/other"},
		"flag first":  {nil, []string{"--fleet-repo=acme/x", "--fleet"}, "acme/x"},
	} {
		calls := ghScript(t, map[string]string{listFor(tc.repo): `[]`, closedFor(tc.repo): `[]`}, nil)
		want := 2
		code, stdout, _ := runDecisions(t, tc.env, tc.args...)
		if code != 0 || stdout != "no decisions recorded for "+tc.repo+"\n" || len(*calls) != want {
			t.Errorf("%s: code %d stdout %q calls %v", name, code, stdout, *calls)
		}
	}
	// --fleet and a repository together is ambiguous, and asks gh nothing.
	calls := ghScript(t, nil, nil)
	if code, _, stderr := runDecisions(t, nil, "--fleet", "o/r"); code != 2 || !strings.Contains(stderr, "usage") || len(*calls) != 0 {
		t.Errorf("--fleet o/r: code %d stderr %q calls %v", code, stderr, *calls)
	}
}

func TestDecisionsWithNoArgumentUsesTheCheckoutsOrigin(t *testing.T) {
	old := originSlug
	defer func() { originSlug = old }()
	var asked string
	originSlug = func(dir string) (string, error) { asked = dir; return "acme/here", nil }
	ghScript(t, map[string]string{listFor("acme/here"): `[]`, closedFor("acme/here"): `[]`}, nil)
	if code, stdout, _ := runDecisions(t, nil); code != 0 || stdout != "no decisions recorded for acme/here\n" || asked == "" {
		t.Errorf("code %d stdout %q asked %q", code, stdout, asked)
	}
	// A checkout with no origin says so and points at the alternatives.
	originSlug = func(string) (string, error) { return "", errors.New("no origin remote") }
	calls := ghScript(t, nil, nil)
	if code, _, stderr := runDecisions(t, nil); code == 0 || !strings.Contains(stderr, "no GitHub origin remote") || !strings.Contains(stderr, "lacquer decisions owner/name") || !strings.Contains(stderr, "--fleet") || strings.Contains(stderr, "--repo") || len(*calls) != 0 {
		t.Errorf("code %d stderr %q calls %v", code, stderr, *calls)
	}
}

// What is passed to gh after -R is checked first: a repository that gh would read as
// a flag, or that has whitespace or a path in it, never gets there.
func TestDecisionsRefusesARepositoryThatIsNotOwnerName(t *testing.T) {
	// Un-quoted, a leading dash is a flag, and refused as one.
	if code, _, _ := runDecisions(t, nil, "-x/y"); code != 2 {
		t.Errorf("a flag-shaped repository: code %d", code)
	}
	for _, bad := range []string{"-x/y", "o", "o/", "/r", "o/r/extra", "o/r --limit", "o/r\n"} {
		calls := ghScript(t, nil, nil)
		if code, _, stderr := runDecisions(t, nil, "--", bad); code == 0 || !strings.Contains(stderr, "not owner/name") || len(*calls) != 0 {
			t.Errorf("%q: code %d stderr %q calls %v", bad, code, stderr, *calls)
		}
	}
}

// The popup is a separate process started by the tmux server: the roster's
// project-to-repository mapping, and the fleet repository when the operator named
// one, travel on its command line. The default does not, so the argv is otherwise
// unchanged.
func TestPopupCommandCarriesWhatARecordedDecisionNeeds(t *testing.T) {
	old := executablePath
	defer func() { executablePath = old }()
	executablePath = func() (string, error) { return "lacquer", nil }
	roster := fleet.Roster{Project: []fleet.Entry{{Name: "Widgets #1", Repo: "acme/widgets"}, {Name: "local"}}}
	env := newWatchEnv("/state/inbox.jsonl", false, overseerFlags{}, roster, envMap(map[string]string{"LACQUER_FLEET_REPO": "acme/ops"}))
	got := strings.Join(env.PopupArgv("a1"), " ")
	name := hex.EncodeToString([]byte("Widgets #1"))
	if !strings.Contains(got, "--project-repo="+name+"=acme/widgets ") || !strings.Contains(got, "--fleet-repo=acme/ops ") || strings.Contains(got, "local") {
		t.Errorf("popup argv: %s", got)
	}
	if strings.Contains(got, "#") {
		t.Errorf("a # reached the popup command: %s", got)
	}
	if env.FleetRepo != "acme/ops" {
		t.Errorf("FleetRepo = %q", env.FleetRepo)
	}
	// Left to default, the list names the default for itself and does not pass it.
	def := newWatchEnv("/state/inbox.jsonl", false, overseerFlags{}, fleet.Roster{}, envMap(nil))
	if def.FleetRepo != defaultFleetRepo || strings.Contains(strings.Join(def.PopupArgv("a1"), " "), "fleet-repo") {
		t.Errorf("default: %q %v", def.FleetRepo, def.PopupArgv("a1"))
	}
	// The popup accepts both flags: with no terminal it gets as far as saying so.
	var stderr bytes.Buffer
	args := []string{"--inbox", "/x/inbox.jsonl", "--project-repo=" + name + "=acme/widgets", "--fleet-repo=acme/ops", "--id-hex=6131"}
	if code := popupMain(args, envMap(nil), &stderr); code == 0 || !strings.Contains(stderr.String(), "needs a terminal") {
		t.Errorf("code %d: %s", code, stderr.String())
	}
	// A malformed one is refused rather than dropped.
	stderr.Reset()
	if code := popupMain([]string{"--project-repo=zz=acme/x", "--id-hex=6131"}, envMap(nil), &stderr); code == 0 || strings.Contains(stderr.String(), "needs a terminal") {
		t.Errorf("a bad --project-repo: code %d: %s", code, stderr.String())
	}
}

// --fleet-repo names where --fleet reads; alone it would be silently ignored.
func TestDecisionsRefusesAFleetRepoWithoutFleet(t *testing.T) {
	calls := ghScript(t, nil, nil)
	for _, args := range [][]string{{"--fleet-repo", "acme/ops"}, {"o/r", "--fleet-repo=acme/ops"}} {
		if code, _, stderr := runDecisions(t, nil, args...); code != 2 || !strings.Contains(stderr, "usage") || len(*calls) != 0 {
			t.Errorf("%v: code %d stderr %q calls %v", args, code, stderr, *calls)
		}
	}
}

// A closed decisions issue is not "none recorded".
func TestDecisionsSaysAClosedIssueIsClosed(t *testing.T) {
	ghScript(t, map[string]string{listFor("o/r"): `[]`, closedFor("o/r"): `[{"number":4,"title":"Decisions","url":"u"}]`}, nil)
	code, stdout, stderr := runDecisions(t, nil, "o/r")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "#4 is closed") || !strings.Contains(stderr, "reopen it") || strings.Contains(stdout+stderr, "no decisions recorded") {
		t.Errorf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}
