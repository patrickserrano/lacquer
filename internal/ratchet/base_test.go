package ratchet

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
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
		// live is the head manifest's coverage keys; nil means both products
		// are still declared.
		live     map[string]bool
		fail     []string // substrings, one per expected failure, in key order
		loosened []Loosened
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
		"removed with a new reason while its product still exists fails": {
			base: bl(map[string]int{cov: 100, other: 7}, nil),
			head: bl(map[string]int{cov: 100}, map[string]string{other: "widget retired"}),
			fail: []string{other + " is enrolled on the PR's base (7) and this PR removes it from .lacquer.ratchet.toml, which removes the gate, and its product is still in .lacquer.toml"},
		},
		"removed with its product and a new reason is loosened to removed": {
			base:     bl(map[string]int{cov: 100, other: 7}, map[string]string{other: "legacy"}),
			head:     bl(map[string]int{cov: 100}, map[string]string{other: "widget retired"}),
			live:     map[string]bool{cov: true},
			loosened: []Loosened{{Key: other, From: 7, Removed: true, Reason: "widget retired"}},
		},
		"removed with its product and no reason fails": {
			base: bl(map[string]int{cov: 100, other: 7}, nil), head: bl(map[string]int{cov: 100}, nil),
			live: map[string]bool{cov: true},
			fail: []string{other + " is enrolled on the PR's base (7) and this PR removes it with its product, without a new reason in [reasons]"},
		},
		"removed with its product and the base's unchanged reason fails": {
			base: bl(map[string]int{cov: 100, other: 7}, map[string]string{other: "legacy"}),
			head: bl(map[string]int{cov: 100}, map[string]string{other: "legacy"}),
			live: map[string]bool{cov: true},
			fail: []string{other + " is enrolled on the PR's base (7) and this PR removes it with its product, without a new reason: [reasons] still holds the base's reason \"legacy\""},
		},
		"deleted file fails even with every product gone and new reasons": {
			base: bl(map[string]int{cov: 100}, nil), head: nil, live: map[string]bool{},
			fail: []string{cov + " is enrolled on the PR's base (100) and this PR deletes"},
		},
		"a removed local metric fails even with a new reason": {
			base: bl(map[string]int{Suppressions: 3}, nil), head: bl(map[string]int{}, map[string]string{Suppressions: "gone"}), live: map[string]bool{},
			fail: []string{Suppressions + " is enrolled on the PR's base (3) and this PR removes it from .lacquer.ratchet.toml, which removes the gate, and it is a local metric"},
		},
		"raise without a reason fails": {
			base: bl(map[string]int{cov: 100}, nil), head: bl(map[string]int{cov: 150}, nil),
			fail: []string{cov + " is raised 100 → 150 on this PR without a new reason"},
		},
		"raise with the base's unchanged reason fails": {
			base: bl(map[string]int{cov: 100}, map[string]string{cov: "legacy"}), head: bl(map[string]int{cov: 150}, map[string]string{cov: "legacy"}),
			fail: []string{cov + " is raised 100 → 150 on this PR without a new reason"},
		},
		"a reused reason says it must differ from the base's and quotes it": {
			base: bl(map[string]int{cov: 100}, map[string]string{cov: "legacy"}), head: bl(map[string]int{cov: 150}, map[string]string{cov: "legacy"}),
			fail: []string{`[reasons] still holds the base's reason "legacy", and a new reason must differ from the base's. Raise it with: lacquer ratchet --loosen ` + cov + ` --to 150 --reason "<a new reason, different from the base's>"`},
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
			live := tc.live
			if live == nil {
				live = map[string]bool{cov: true, other: true}
			}
			v := CompareBase(tc.base, tc.head, live)
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

func TestLiveCoverageKeys(t *testing.T) {
	ios := []config.Component{{Path: ".", Profiles: []string{"ios"}}}
	solo := &config.Config{Project: config.Project{ProjectName: "Demo", Scheme: "Demo"}, Components: ios}
	if got := LiveCoverageKeys(solo); !got[Coverage] {
		t.Errorf("lone product: %v lacks %s", got, Coverage)
	}
	two := &config.Config{Product: []config.Product{{Name: "Paid", Scheme: "Paid"}, {Name: "Free", Scheme: "Free"}}, Components: ios}
	got := LiveCoverageKeys(two)
	for _, p := range two.Product {
		if !got[CoverageKey(p.Slug())] {
			t.Errorf("two products: %v lacks %s", got, CoverageKey(p.Slug()))
		}
	}
	// 2 → 1: the surviving product's slug key is still present, so removing it
	// is not "its product is gone".
	one := &config.Config{Product: []config.Product{{Name: "Paid", Scheme: "Paid"}}, Components: ios}
	if got := LiveCoverageKeys(one); !got[CoverageKey(one.Product[0].Slug())] {
		t.Errorf("one declared product: %v lacks its slug key", got)
	}
	if got := LiveCoverageKeys(&config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"web"}}}}); len(got) != 0 {
		t.Errorf("no iOS component: %v, want none", got)
	}
}

func TestRemovedLooseningRoundTrips(t *testing.T) {
	l := Loosened{Key: Coverage + "_widget", From: 7, Removed: true, Reason: "widget retired"}
	if line := l.MachineLine(); line != `lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines_widget","from":7,"removed":true,"reason":"widget retired"}` {
		t.Fatalf("removed line = %s", line)
	}
	if got, ok, err := ParseLoosened(l.MachineLine()); !ok || err != nil || got != l {
		t.Fatalf("round trip = %+v, %v, %v", got, ok, err)
	}
	if s := l.Summary(); s != "loosened ios_uncovered_lines_widget 7→removed: widget retired" {
		t.Errorf("summary = %s", s)
	}
	for _, bad := range []string{
		LoosenedPrefix + `{"key":"ios_uncovered_lines","from":7,"to":9,"removed":true,"reason":"x"}`,
		LoosenedPrefix + `{"key":"unjustified_suppressions","from":7,"removed":true,"reason":"x"}`,
	} {
		if _, ok, err := ParseLoosened(bad); !ok || err == nil {
			t.Errorf("accepted %s", bad)
		}
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
