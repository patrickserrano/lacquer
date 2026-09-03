package console

import (
	"errors"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

// rosterWithRepo builds a one-entry roster carrying a repo slug, the shape
// Dispatch's archived check actually needs -- rosterOf (console_test.go)
// leaves Repo empty, which is deliberately how the pre-existing Dispatch
// tests stay silent (the archived check is a no-op with no repo configured).
func rosterWithRepo(name, repo string) fleet.Roster {
	return fleet.Roster{Project: []fleet.Entry{{Name: name, Path: "/w/" + name, Repo: repo}}}
}

// lacquer#227: dispatching against an archived repo did not fail or warn --
// the session just sat there with nothing it could push or open a PR
// against, while `console watch` kept reporting it alive. Dispatch must
// refuse loudly instead.
func TestDispatchRefusesArchivedRepo(t *testing.T) {
	orig := ArchivedRunner
	defer func() { ArchivedRunner = orig }()
	ArchivedRunner = func(repo string) ([]byte, error) {
		if repo != "acme/example" {
			t.Fatalf("repo = %q, want acme/example", repo)
		}
		return []byte(`{"isArchived":true}`), nil
	}

	_, err := Dispatch(rosterWithRepo("example", "acme/example"), nil, "example", "do a thing", Background, true)
	if err == nil {
		t.Fatal("expected dispatch to refuse an archived repo")
	}
	if !strings.Contains(err.Error(), "archived") || !strings.Contains(err.Error(), "acme/example") {
		t.Errorf("error should name the repo and say it is archived, got: %v", err)
	}
}

// A live (unarchived) repo must dispatch normally.
func TestDispatchProceedsWhenRepoNotArchived(t *testing.T) {
	orig := ArchivedRunner
	defer func() { ArchivedRunner = orig }()
	ArchivedRunner = func(repo string) ([]byte, error) {
		return []byte(`{"isArchived":false}`), nil
	}

	out, err := Dispatch(rosterWithRepo("alpha", "acme/alpha"), nil, "alpha", "do a thing", Background, true)
	if err != nil {
		t.Fatalf("a live repo must not be refused: %v", err)
	}
	if !strings.Contains(out, "dispatch:") {
		t.Errorf("it must still dispatch:\n%s", out)
	}
}

// gh being missing, unauthenticated, or offline is not evidence the repo is
// archived -- an inconclusive check must degrade to "proceed", the same
// graceful-degradation this fleet already applies to `gh pr list`, rather
// than blocking every dispatch the moment gh itself is unavailable.
func TestDispatchProceedsWhenArchivedCheckIsInconclusive(t *testing.T) {
	orig := ArchivedRunner
	defer func() { ArchivedRunner = orig }()
	ArchivedRunner = func(repo string) ([]byte, error) {
		return nil, errors.New("gh: command not found")
	}

	out, err := Dispatch(rosterWithRepo("alpha", "acme/alpha"), nil, "alpha", "do a thing", Background, true)
	if err != nil {
		t.Fatalf("an inconclusive check must not block dispatch: %v", err)
	}
	if !strings.Contains(out, "dispatch:") {
		t.Errorf("it must still dispatch:\n%s", out)
	}
}

// An entry with no repo configured has nothing to check -- ArchivedRunner
// must never even be called.
func TestDispatchSkipsArchivedCheckWithNoRepoConfigured(t *testing.T) {
	orig := ArchivedRunner
	defer func() { ArchivedRunner = orig }()
	ArchivedRunner = func(repo string) ([]byte, error) {
		t.Fatal("ArchivedRunner must not be called when the roster entry has no repo")
		return nil, nil
	}

	if _, err := Dispatch(rosterOf("alpha"), nil, "alpha", "do a thing", Background, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestArchivedReportsNotOKOnUnparsableOutput(t *testing.T) {
	orig := ArchivedRunner
	defer func() { ArchivedRunner = orig }()
	ArchivedRunner = func(repo string) ([]byte, error) {
		return []byte("not json"), nil
	}
	if yes, ok := archived("acme/alpha"); ok || yes {
		t.Errorf("archived() = (%v, %v), want (false, false) on unparsable output", yes, ok)
	}
}

func TestArchivedReportsNotOKWithEmptyRepo(t *testing.T) {
	if yes, ok := archived(""); ok || yes {
		t.Errorf("archived(\"\") = (%v, %v), want (false, false)", yes, ok)
	}
}

// The roster-audit path (console.Gather / `console watch`) must surface an
// archived repo too, not only Dispatch -- lacquer#227's other complaint was
// that watch kept reporting an archived project's session "alive" with no
// clue anything was wrong.
func TestArchivedRosterFindsConfirmedArchivedRepos(t *testing.T) {
	orig := ArchivedRunner
	defer func() { ArchivedRunner = orig }()
	ArchivedRunner = func(repo string) ([]byte, error) {
		if repo == "acme/example" {
			return []byte(`{"isArchived":true}`), nil
		}
		return []byte(`{"isArchived":false}`), nil
	}

	roster := fleet.Roster{Project: []fleet.Entry{
		{Name: "example", Path: "/w/example", Repo: "acme/example"},
		{Name: "alpha", Path: "/w/alpha", Repo: "acme/alpha"},
		{Name: "no-repo", Path: "/w/no-repo"},
	}}
	got := archivedRoster(roster)
	if !got["acme/example"] {
		t.Errorf("archivedRoster = %v, want acme/example present", got)
	}
	if got["acme/alpha"] {
		t.Errorf("archivedRoster = %v, want acme/alpha absent (not archived)", got)
	}
	if len(got) != 1 {
		t.Errorf("archivedRoster = %v, want exactly 1 entry", got)
	}
}
