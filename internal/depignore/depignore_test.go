package depignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
)

func at(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func comp(path string, igs ...config.DependabotIgnore) config.Component {
	return config.Component{Path: path, Stack: "web", DependabotIgnore: igs}
}

func ig(dep, until string) config.DependabotIgnore {
	return config.DependabotIgnore{Dependency: dep, Versions: []string{"7.x"}, Reason: "known incompatibility", Until: until}
}

// An ignore past its date is EXPIRED and blocks, exactly as an expired exclusion
// does. That is the whole safety argument for allowing ignores at all: the
// update it withholds may now be perfectly mergeable, and nothing else in this
// tool would ever ask.
func TestExpiredIgnoreIsReportedAndBlocks(t *testing.T) {
	fs := Review([]config.Component{comp("admin", ig("typedoc", "2026-11-30"))}, "", at("2026-12-01"))
	if len(fs) != 1 {
		t.Fatalf("got %d findings, want 1", len(fs))
	}
	if fs[0].Status != StatusExpired {
		t.Errorf("status = %q, want %q", fs[0].Status, StatusExpired)
	}
	if !fs[0].Blocks() {
		t.Error("an expired ignore does not block; it would persist past its own term with nothing objecting")
	}
	if Blocking(fs) != 1 {
		t.Errorf("Blocking = %d, want 1", Blocking(fs))
	}
	out := Format(fs)
	if !strings.Contains(out, "EXPIRED") {
		t.Errorf("Format did not say EXPIRED:\n%s", out)
	}
	if !strings.Contains(out, "typedoc") || !strings.Contains(out, "2026-11-30") {
		t.Errorf("Format named neither the dependency nor the date:\n%s", out)
	}
	// The reason is the point of recording one. A report that says something
	// expired without saying what it was for sends the reader to the manifest.
	if !strings.Contains(out, "known incompatibility") {
		t.Errorf("Format dropped the reason:\n%s", out)
	}
}

// An in-term ignore that is doing its job blocks nothing and prints nothing.
// A report that nags about healthy config is a report people learn to skip.
func TestInTermIgnoreIsSilent(t *testing.T) {
	fs := Review([]config.Component{comp("admin", ig("typedoc", "2026-11-30"))}, "", at("2026-08-18"))
	if len(fs) != 1 || fs[0].Status != StatusDated {
		t.Fatalf("findings = %+v, want one dated", fs)
	}
	if Blocking(fs) != 0 {
		t.Errorf("Blocking = %d, want 0", Blocking(fs))
	}
	if out := Format(fs); out != "" {
		t.Errorf("Format printed for a healthy ignore:\n%s", out)
	}
}

// The whole `until` DAY is in term. "until = 2026-11-30" reads as "through the
// 30th" to everyone who writes one, and expiring at 00:00 on the 30th would
// blame a project for a day it was promised. Same boundary as an exclusion's.
func TestExpiryBoundaryIsTheEndOfTheUntilDay(t *testing.T) {
	for _, tc := range []struct {
		now  string
		want Status
	}{
		{"2026-11-29", StatusDated},
		{"2026-11-30", StatusDated},
		{"2026-12-01", StatusExpired},
	} {
		fs := Review([]config.Component{comp("admin", ig("typedoc", "2026-11-30"))}, "", at(tc.now))
		if fs[0].Status != tc.want {
			t.Errorf("at %s: status = %q, want %q", tc.now, fs[0].Status, tc.want)
		}
	}
	// And the last instant of the day itself, which is the boundary the
	// comparison is actually written against.
	last := at("2026-11-30").Add(24*time.Hour - time.Nanosecond)
	if fs := Review([]config.Component{comp("admin", ig("typedoc", "2026-11-30"))}, "", last); fs[0].Status != StatusDated {
		t.Error("the final instant of the until day is already expired; the term is short by a day")
	}
}

// A project with no ignores produces no findings and no output at all.
func TestNoIgnoresIsNoReport(t *testing.T) {
	fs := Review([]config.Component{{Path: "admin", Stack: "web"}}, "", at("2030-01-01"))
	if len(fs) != 0 {
		t.Errorf("findings = %+v, want none", fs)
	}
	if out := Format(fs); out != "" {
		t.Errorf("Format printed something for a project with no ignores:\n%s", out)
	}
}

// project writes files under a temp root and returns the root.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// An ignore naming a dependency the component does not declare is STALE.
//
// This is the analogue of an exclusion that suppresses nothing, and it is the
// harder kind to notice: the entry is attributed, in term, and withholding
// nothing at all. Dead config reads exactly like a live decision.
func TestStaleIgnoreNamesADependencyTheComponentDoesNotHave(t *testing.T) {
	root := project(t, map[string]string{
		"admin/package.json": `{"dependencies":{"react":"^19.0.0"},"devDependencies":{"typedoc":"0.28.20"}}`,
	})
	fs := Review([]config.Component{comp("admin",
		ig("typedoc", "2026-11-30"),
		ig("webpack", "2026-11-30"),
	)}, root, at("2026-08-18"))
	if len(fs) != 2 {
		t.Fatalf("got %d findings, want 2", len(fs))
	}
	byDep := map[string]Finding{}
	for _, f := range fs {
		byDep[f.Dependency] = f
	}
	if byDep["typedoc"].Stale {
		t.Error("typedoc is in devDependencies and was called stale; an ignore for a real dependency must not be flagged")
	}
	if !byDep["webpack"].Stale {
		t.Error("webpack is in no dependency map and was not called stale")
	}
	out := Format(fs)
	if !strings.Contains(out, "webpack") {
		t.Errorf("Format did not report the stale ignore:\n%s", out)
	}
	if strings.Contains(out, "typedoc") {
		t.Errorf("Format reported the healthy ignore:\n%s", out)
	}
	// Stale is a documentation defect, not a hazard. Gating on it would fail
	// repos for something that endangers nothing, which is how a gate gets
	// worked around.
	if Blocking(fs) != 0 {
		t.Errorf("Blocking = %d; a stale ignore must not block", Blocking(fs))
	}
}

// Every npm dependency map counts. The measured case lives entirely in
// devDependencies, and a reader that only looked at `dependencies` would have
// called it stale — advice to delete a live ignore.
func TestStalenessReadsEveryNpmDependencyMap(t *testing.T) {
	root := project(t, map[string]string{
		"admin/package.json": `{
      "dependencies": {"a": "1"},
      "devDependencies": {"b": "1"},
      "peerDependencies": {"c": "1"},
      "optionalDependencies": {"d": "1"}
    }`,
	})
	fs := Review([]config.Component{comp("admin", ig("a", "2030-01-01"), ig("b", "2030-01-01"), ig("c", "2030-01-01"), ig("d", "2030-01-01"))}, root, at("2026-08-18"))
	for _, f := range fs {
		if f.Stale {
			t.Errorf("%q is declared and was called stale", f.Dependency)
		}
	}
}

// Go and Cargo components get the same treatment; both ecosystems render
// Dependabot entries, so both can carry ignores.
func TestStalenessReadsGoModAndCargoToml(t *testing.T) {
	root := project(t, map[string]string{
		"svc/go.mod":     "module x\n\ngo 1.24\n\nrequire (\n\tgithub.com/owner/mod v1.2.3 // indirect\n)\n\nrequire github.com/other/one v0.1.0\n",
		"cli/Cargo.toml": "[package]\nname = \"x\"\n\n[dependencies]\nserde = \"1.0\"\n\n[dev-dependencies]\ncriterion = { version = \"0.5\" }\n",
	})
	fs := Review([]config.Component{
		{Path: "svc", Stack: "go", DependabotIgnore: []config.DependabotIgnore{
			ig("github.com/owner/mod", "2030-01-01"),
			ig("github.com/other/one", "2030-01-01"),
			ig("github.com/absent/pkg", "2030-01-01"),
		}},
		{Path: "cli", Stack: "rust", DependabotIgnore: []config.DependabotIgnore{
			ig("serde", "2030-01-01"),
			ig("criterion", "2030-01-01"),
			ig("tokio", "2030-01-01"),
		}},
	}, root, at("2026-08-18"))
	want := map[string]bool{
		"github.com/owner/mod":  false,
		"github.com/other/one":  false,
		"github.com/absent/pkg": true,
		"serde":                 false,
		"criterion":             false,
		"tokio":                 true,
	}
	for _, f := range fs {
		if got, ok := want[f.Dependency]; ok && f.Stale != got {
			t.Errorf("%s: stale = %v, want %v", f.Dependency, f.Stale, got)
		}
	}
}

// When the manifest cannot be read, the answer is "unknown" and NOTHING is
// reported stale.
//
// The direction to fail in is silence. A false stale report is advice to delete
// a load-bearing ignore, which is much worse than saying nothing — and every
// swift component lands here permanently, because its dependency names live in
// a Package.resolved keyed by repository URL rather than by the name Dependabot
// reports.
func TestUnknownManifestIsNeverStale(t *testing.T) {
	cases := map[string]string{
		"no root":            "",
		"no manifest at all": project(t, map[string]string{"admin/README.md": "x"}),
		"malformed json":     project(t, map[string]string{"admin/package.json": "{not json"}),
	}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			fs := Review([]config.Component{comp("admin", ig("anything", "2030-01-01"))}, root, at("2026-08-18"))
			if len(fs) != 1 {
				t.Fatalf("got %d findings, want 1", len(fs))
			}
			if fs[0].Stale {
				t.Error("reported stale from a manifest it could not read; that is advice to delete live config")
			}
			if out := Format(fs); out != "" {
				t.Errorf("Format printed:\n%s", out)
			}
		})
	}
}

// A root-layout component reads the manifest at the project root, not at a
// directory literally named ".".
func TestStalenessHandlesTheRootComponent(t *testing.T) {
	root := project(t, map[string]string{"package.json": `{"dependencies":{"react":"^19"}}`})
	for _, path := range []string{".", ""} {
		fs := Review([]config.Component{comp(path, ig("react", "2030-01-01"), ig("vue", "2030-01-01"))}, root, at("2026-08-18"))
		byDep := map[string]bool{}
		for _, f := range fs {
			byDep[f.Dependency] = f.Stale
		}
		if byDep["react"] {
			t.Errorf("path %q: react is declared at the root and was called stale", path)
		}
		if !byDep["vue"] {
			t.Errorf("path %q: vue is declared nowhere and was not called stale", path)
		}
	}
}

// Findings are ordered so two runs of the same project produce the same output.
// The fleet report is diffed between runs; unstable ordering makes every diff
// noise.
func TestFindingsAreSorted(t *testing.T) {
	fs := Review([]config.Component{
		comp("web", ig("zzz", "2030-01-01"), ig("aaa", "2030-01-01")),
		comp("admin", ig("mmm", "2030-01-01")),
	}, "", at("2026-08-18"))
	var got []string
	for _, f := range fs {
		got = append(got, f.Component+"/"+f.Dependency)
	}
	want := []string{"admin/mmm", "web/aaa", "web/zzz"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
