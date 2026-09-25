package producers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

func result(o ciwait.Outcome) ciwait.Result {
	return ciwait.Result{Outcome: o, PR: 12, Repo: "acme/widgets", Head: "abc123def", URL: "https://github.com/acme/widgets/pull/12", State: "OPEN", Message: "why"}
}

func TestGateRejectionByOutcome(t *testing.T) {
	cases := []struct {
		name string
		res  ciwait.Result
		intr bool
		want bool
	}{
		{"passed", result(ciwait.Passed), false, false},
		{"failed", result(ciwait.Failed), false, false},
		{"timed out", result(ciwait.TimedOut), false, true},
		{"no checks", result(ciwait.NoChecks), false, true},
		{"error", result(ciwait.Error), false, true},
		{"error, interrupted by the operator", result(ciwait.Error), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "inbox.jsonl")
			e, added, err := GateRejection(path, tc.res, tc.intr)
			if err != nil || added != tc.want {
				t.Fatalf("added=%v err=%v, want added=%v", added, err, tc.want)
			}
			if tc.want && e.Type != inbox.Action {
				t.Errorf("type %q, want action", e.Type)
			}
			if !tc.want {
				if _, err := os.Stat(path); err == nil {
					t.Error("a no-entry outcome created the inbox file")
				}
			}
		})
	}
}

func TestGateRejectionErrorOnAClosedPRWritesNothing(t *testing.T) {
	for _, st := range []string{"MERGED", "CLOSED"} {
		r := result(ciwait.Error)
		r.State = st
		if _, added, err := GateRejection(filepath.Join(t.TempDir(), "i.jsonl"), r, false); added || err != nil {
			t.Errorf("%s: added=%v err=%v", st, added, err)
		}
	}
}

func TestGateRejectionDedupesOnRefTitleAndHead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	r := result(ciwait.TimedOut)
	if _, added, _ := GateRejection(path, r, false); !added {
		t.Fatal("first must add")
	}
	if _, added, _ := GateRejection(path, r, false); added {
		t.Fatal("identical second must not add")
	}
	r.Head = "999999999"
	if _, added, _ := GateRejection(path, r, false); !added {
		t.Fatal("a different head is a different problem")
	}
	other := result(ciwait.NoChecks) // same PR and head, different title
	if _, added, _ := GateRejection(path, other, false); !added {
		t.Fatal("a different outcome on the same PR is a different entry")
	}
}
