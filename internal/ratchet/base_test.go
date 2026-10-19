package ratchet

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

// ReadBase's three outcomes (#571) are kept apart, each by a named test: the
// file is absent at the base (first enrollment, passes), the base could not be
// fetched or read (fails), the base's file does not parse (fails). A failed
// fetch must never read as "the base was not enrolled".

const enrolled100 = "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\nios_uncovered_lines = 100\n"

func TestReadBaseFileAbsentAtTheBaseIsNotEnrolled(t *testing.T) {
	pr := gittest.NewPR(t, map[string]string{"README.md": "x\n"}, func(work string) {
		gittest.Write(t, work, map[string]string{Name: enrolled100})
	})
	base, err := ReadBase(pr.Checkout)
	if err != nil {
		t.Fatalf("a base with no ratchet file is a first enrollment, not an error: %v", err)
	}
	if base.Baseline != nil || base.Commit != pr.Base {
		t.Fatalf("got %+v, want no baseline at %s", base, pr.Base)
	}
}

func TestReadBaseFetchFailureIsAnErrorNotUnenrolled(t *testing.T) {
	pr := gittest.NewPR(t, map[string]string{Name: enrolled100}, func(work string) {
		gittest.Write(t, work, map[string]string{Name: ""})
	})
	gittest.Git(t, pr.Checkout, "remote", "set-url", "origin", "file://"+filepath.Join(t.TempDir(), "gone.git"))
	base, err := ReadBase(pr.Checkout)
	if err == nil || !strings.Contains(err.Error(), "could not fetch the base commit "+pr.Base) {
		t.Fatalf("got %+v, %v; want an error naming the fetch", base, err)
	}
}

func TestReadBaseUnparseableFileIsAnError(t *testing.T) {
	pr := gittest.NewPR(t, map[string]string{Name: "[ratchet\nnot toml"}, func(work string) {
		gittest.Write(t, work, map[string]string{Name: enrolled100})
	})
	base, err := ReadBase(pr.Checkout)
	if err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("got %+v, %v; want a parse error", base, err)
	}
}

// The base is the merge commit's first parent, fetched into the shallow
// checkout, and not wherever the base branch has moved since.
func TestReadBaseUsesTheMergeCommitsFirstParent(t *testing.T) {
	pr := gittest.NewPR(t, map[string]string{Name: enrolled100}, func(work string) {
		gittest.Write(t, work, map[string]string{"README.md": "x\n"})
	})
	moved := pr.AdvanceBase(t, map[string]string{Name: strings.Replace(enrolled100, "= 100", "= 999", 1)})
	base, err := ReadBase(pr.Checkout)
	if err != nil {
		t.Fatal(err)
	}
	if base.Commit != pr.Base || base.Commit == moved {
		t.Fatalf("read base %s; want the first parent %s, not the moved branch tip %s", base.Commit, pr.Base, moved)
	}
	if got := base.Baseline.Ratchet[Coverage]; got != 100 {
		t.Fatalf("base %s = %d, want 100 from the first parent", Coverage, got)
	}
}

func TestReadBaseRefusesACommitThatIsNotAMerge(t *testing.T) {
	pr := gittest.NewPR(t, map[string]string{Name: enrolled100}, func(string) {})
	gittest.Git(t, pr.Checkout, "commit", "-q", "--allow-empty", "--no-verify", "-m", "not a merge")
	_, err := ReadBase(pr.Checkout)
	if err == nil || !strings.Contains(err.Error(), "has 1 parent(s), not 2") {
		t.Fatalf("got %v; want a refusal naming the parent count", err)
	}
}

func TestReadBaseOutsideAGitWorkTreeIsAnError(t *testing.T) {
	if _, err := ReadBase(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a git work tree") {
		t.Fatalf("got %v; want an error", err)
	}
}

func bl(ratchet map[string]int, reasons map[string]string) *Baseline {
	return &Baseline{Ratchet: ratchet, Reasons: reasons}
}

func TestCompareBase(t *testing.T) {
	const cov, other = Coverage, Coverage + "_widget"
	for name, tc := range map[string]struct {
		base, head *Baseline
		fail       []string // substrings, one per expected failure, in key order
		loosened   []Loosened
	}{
		"first enrollment passes": {
			base: nil, head: bl(map[string]int{cov: 100}, nil),
		},
		"head adds a key the base lacks": {
			base: bl(map[string]int{Suppressions: 0}, nil), head: bl(map[string]int{Suppressions: 0, cov: 100}, nil),
		},
		"tightening passes silently": {
			base: bl(map[string]int{cov: 100}, nil), head: bl(map[string]int{cov: 90}, nil),
		},
		"unchanged passes": {
			base: bl(map[string]int{cov: 100}, map[string]string{cov: "old"}), head: bl(map[string]int{cov: 100}, map[string]string{cov: "old"}),
		},
		"deleted file fails naming every key": {
			base: bl(map[string]int{cov: 100, other: 7}, nil), head: nil,
			fail: []string{cov + " is enrolled on the PR's base (100) and this PR deletes", other + " is enrolled on the PR's base (7) and this PR deletes"},
		},
		"another product's key removed fails": {
			base: bl(map[string]int{cov: 100, other: 7}, nil), head: bl(map[string]int{cov: 100}, nil),
			fail: []string{other + " is enrolled on the PR's base (7) and this PR removes it"},
		},
		"raise without a reason fails": {
			base: bl(map[string]int{cov: 100}, nil), head: bl(map[string]int{cov: 150}, nil),
			fail: []string{cov + " is raised 100 → 150 on this PR without a new reason"},
		},
		"raise with the base's unchanged reason fails": {
			base: bl(map[string]int{cov: 100}, map[string]string{cov: "legacy"}), head: bl(map[string]int{cov: 150}, map[string]string{cov: "legacy"}),
			fail: []string{cov + " is raised 100 → 150 on this PR without a new reason"},
		},
		"raise with a whitespace-only change to the reason fails": {
			base: bl(map[string]int{cov: 100}, map[string]string{cov: "legacy"}), head: bl(map[string]int{cov: 150}, map[string]string{cov: " legacy "}),
			fail: []string{cov + " is raised 100 → 150"},
		},
		"raise with a new reason is loosened": {
			base: bl(map[string]int{cov: 100}, map[string]string{cov: "legacy"}), head: bl(map[string]int{cov: 150}, map[string]string{cov: "vendored parser"}),
			loosened: []Loosened{{Key: cov, From: 100, To: 150, Reason: "vendored parser"}},
		},
		"a local metric raised without a reason fails too": {
			base: bl(map[string]int{Suppressions: 3}, nil), head: bl(map[string]int{Suppressions: 9}, nil),
			fail: []string{Suppressions + " is raised 3 → 9 on this PR without a new reason in [reasons]. Raise it with: lacquer ratchet --loosen " + Suppressions + " --reason"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := CompareBase(tc.base, tc.head)
			if len(v.Failures) != len(tc.fail) {
				t.Fatalf("failures %q, want %d matching %q", v.Failures, len(tc.fail), tc.fail)
			}
			for i, want := range tc.fail {
				if !strings.Contains(v.Failures[i], want) {
					t.Errorf("failure %d = %q, want it to contain %q", i, v.Failures[i], want)
				}
			}
			if !reflect.DeepEqual(v.Loosened, tc.loosened) {
				t.Errorf("loosened %+v, want %+v", v.Loosened, tc.loosened)
			}
		})
	}
}

func TestLoosenedLineRoundTrips(t *testing.T) {
	l := Loosened{Key: Coverage, From: 100, To: 150, Reason: "50% of <it> is \"generated\"\nsecond line"}
	line := l.MachineLine()
	if !strings.HasPrefix(line, LoosenedPrefix+"{") || strings.Contains(line, "\n") {
		t.Fatalf("not one prefixed line: %q", line)
	}
	got, ok, err := ParseLoosened(line)
	if !ok || err != nil || got != l {
		t.Fatalf("ParseLoosened(%q) = %+v, %v, %v; want %+v", line, got, ok, err, l)
	}
	ann := l.Annotation()
	if !strings.HasPrefix(ann, "::notice title=lacquer ratchet loosened::"+LoosenedPrefix) || strings.Contains(ann, "50% ") || !strings.Contains(ann, "50%25 ") {
		t.Errorf("the annotation does not escape %% for the workflow command: %q", ann)
	}
}

func TestParseLoosenedRejectsDamagedLines(t *testing.T) {
	if _, ok, err := ParseLoosened("ratchet: ios_uncovered_lines = 4"); ok || err != nil {
		t.Errorf("an unrelated line must be ignored, got ok=%v err=%v", ok, err)
	}
	for _, bad := range []string{
		LoosenedPrefix + "{",
		LoosenedPrefix + `{"key":"ios_uncovered_lines","from":150,"to":100,"reason":"x"}`,
		LoosenedPrefix + `{"key":"ios_uncovered_lines","from":1,"to":2,"reason":" "}`,
		LoosenedPrefix + `{"key":"nope","from":1,"to":2,"reason":"x"}`,
		LoosenedPrefix + `{"key":"ios_uncovered_lines","from":1,"to":2,"reason":"x","extra":1}`,
	} {
		if _, ok, err := ParseLoosened(bad); !ok || err == nil {
			t.Errorf("a damaged line must be reported, got ok=%v err=%v for %s", ok, err, bad)
		}
	}
}
