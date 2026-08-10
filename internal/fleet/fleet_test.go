package fleet

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// lacquerRoot builds a minimal but real lacquer: a VERSION, a core CLAUDE body,
// and one profile that ships a workflow (so exclusions have something to match).
func lacquerRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "VERSION"), "1.0.0\n")
	write(t, filepath.Join(dir, "core", "CLAUDE.core.md"), "core rules\n")
	write(t, filepath.Join(dir, "profiles", "web", "CLAUDE.web.md"), "web rules\n")
	write(t, filepath.Join(dir, "profiles", "web", "workflows", "ci.yml"), "name: Web CI\n")
	return dir
}

// project writes a project with the given [project] stanza body.
func project(t *testing.T, name, projectExtra string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	write(t, filepath.Join(dir, "package.json"), "{}")
	write(t, filepath.Join(dir, ".lacquer.toml"),
		"[project]\nname = \""+name+"\"\n"+projectExtra+"\n\n"+
			"[[component]]\npath = \".\"\nprofiles = [\"web\"]\n")
	return dir
}

func rosterFor(t *testing.T, paths map[string]string) Roster {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	for name, p := range paths {
		b.WriteString("[[project]]\nname = \"" + name + "\"\npath = \"" + p + "\"\n\n")
	}
	rp := filepath.Join(dir, "fleet.toml")
	write(t, rp, b.String())
	r, err := LoadRoster(rp)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func find(t *testing.T, reports []Report, name string) Report {
	t.Helper()
	for _, r := range reports {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no report for %q", name)
	return Report{}
}

// The whole point of a sweep: one unreadable project must not abort it, and must
// not be silently omitted either — an absent project reads as a healthy one.
func TestBrokenProjectIsReportedNotFatal(t *testing.T) {
	lq := lacquerRoot(t)
	good := project(t, "good", "")
	bad := filepath.Join(t.TempDir(), "bad")
	write(t, filepath.Join(bad, ".lacquer.toml"), "this is not valid toml {{{\n")

	reports := Run(lq, rosterFor(t, map[string]string{"good": good, "bad": bad}), day("2026-08-09"))
	if len(reports) != 2 {
		t.Fatalf("got %d reports, want 2 — a broken project must still appear", len(reports))
	}
	b := find(t, reports, "bad")
	if b.Error == "" {
		t.Error("the unreadable project must carry an error")
	}
	if !b.Blocking() {
		t.Error("a project that cannot be read must count as blocking, not as clean")
	}
	if g := find(t, reports, "good"); g.Error != "" {
		t.Errorf("the healthy project was affected by its neighbour: %s", g.Error)
	}
}

func TestExpiredExclusionBlocks(t *testing.T) {
	lq := lacquerRoot(t)
	p := project(t, "p", `exclude = [{ path = ".github/workflows/web-ci.yml", reason = "r", until = "2020-01-01" }]`)
	r := find(t, Run(lq, rosterFor(t, map[string]string{"p": p}), day("2026-08-09")), "p")
	if len(r.Exclusions) != 1 || r.Exclusions[0].Status != "expired" {
		t.Fatalf("exclusions = %+v", r.Exclusions)
	}
	if !r.Blocking() {
		t.Error("an expired exemption must block, exactly as `lacquer audit` gates on it")
	}
}

func TestHealthyProjectDoesNotBlock(t *testing.T) {
	lq := lacquerRoot(t)
	p := project(t, "p", `exclude = [{ path = ".github/workflows/web-ci.yml", reason = "deliberate, permanent" }]`)
	r := find(t, Run(lq, rosterFor(t, map[string]string{"p": p}), day("2026-08-09")), "p")
	if r.Blocking() {
		t.Errorf("a well-attributed, in-term project must not block: %+v", r)
	}
}

// The signal that justifies the sweep: several projects excluding one path means
// the shared asset is probably what is wrong.
func TestSharedExclusionIsSurfaced(t *testing.T) {
	lq := lacquerRoot(t)
	excl := `exclude = [{ path = ".github/workflows/web-ci.yml", reason = "local build env" }]`
	a := project(t, "alpha", excl)
	b := project(t, "bravo", excl)
	c := project(t, "charlie", "")

	reports := Run(lq, rosterFor(t, map[string]string{"alpha": a, "bravo": b, "charlie": c}), day("2026-08-09"))
	var out bytes.Buffer
	Text(&out, reports)
	s := out.String()
	if !strings.Contains(s, "excluded by more than one project") {
		t.Fatalf("the shared-exclusion section is missing:\n%s", s)
	}
	if !strings.Contains(s, "alpha") || !strings.Contains(s, "bravo") {
		t.Errorf("both projects sharing the exclusion must be named:\n%s", s)
	}
	if strings.Contains(s, "web-ci.yml  (3:") {
		t.Errorf("charlie excludes nothing and must not be counted:\n%s", s)
	}
}

// Dated exemptions are invisible until the morning they fire unless something
// lists them ahead of time.
func TestExpiryHorizonIsSortedSoonestFirst(t *testing.T) {
	lq := lacquerRoot(t)
	late := project(t, "late", `exclude = [{ path = ".github/workflows/web-ci.yml", reason = "r", until = "2027-01-01" }]`)
	soon := project(t, "soon", `exclude = [{ path = ".github/workflows/web-ci.yml", reason = "r", until = "2026-09-01" }]`)

	var out bytes.Buffer
	Text(&out, Run(lq, rosterFor(t, map[string]string{"late": late, "soon": soon}), day("2026-08-09")))
	s := out.String()
	i, j := strings.Index(s, "2026-09-01"), strings.Index(s, "2027-01-01")
	if i < 0 || j < 0 {
		t.Fatalf("both dates must be listed:\n%s", s)
	}
	if i > j {
		t.Errorf("the horizon must be soonest-first:\n%s", s)
	}
}

func TestJSONRoundTrips(t *testing.T) {
	lq := lacquerRoot(t)
	p := project(t, "p", `exclude = [{ path = ".github/workflows/web-ci.yml", reason = "r", until = "2026-12-01" }]`)
	var buf bytes.Buffer
	if err := JSON(&buf, Run(lq, rosterFor(t, map[string]string{"p": p}), day("2026-08-09"))); err != nil {
		t.Fatal(err)
	}
	var back []Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(back) != 1 || back[0].Name != "p" {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if len(back[0].Exclusions) != 1 || back[0].Exclusions[0].Until != "2026-12-01" {
		t.Errorf("exclusion detail lost: %+v", back[0].Exclusions)
	}
}

func TestLoadRosterRejectsDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "fleet.toml")
	write(t, rp, "[[project]]\nname=\"x\"\npath=\"/a\"\n\n[[project]]\nname=\"x\"\npath=\"/b\"\n")
	if _, err := LoadRoster(rp); err == nil {
		t.Error("two entries under one name would collapse in any diff between runs")
	}
}

func TestLoadRosterResolvesRelativePathsAgainstItself(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sub", "marker"), "x")
	rp := filepath.Join(dir, "fleet.toml")
	write(t, rp, "[[project]]\npath=\"sub\"\n")
	r, err := LoadRoster(rp)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "sub"); r.Project[0].Path != want {
		t.Errorf("path = %q, want %q — a roster must be usable beside the projects it lists", r.Project[0].Path, want)
	}
	if r.Project[0].Name != "sub" {
		t.Errorf("name = %q, want the path's base name", r.Project[0].Name)
	}
}

func TestLoadRosterRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "fleet.toml")
	write(t, rp, "# nothing here\n")
	if _, err := LoadRoster(rp); err == nil {
		t.Error("an empty roster sweeps nothing and reports success — reject it")
	}
}

// A local directory name is not a reliable key for the project it holds: in the
// fleet this was built for, three of seventeen checkouts live in a directory
// named differently from their repository, across three owners.
func TestRosterCarriesRepoSlug(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "fleet.toml")
	write(t, rp, "[[project]]\nname=\"local-name\"\nrepo=\"owner/actual-repo\"\npath=\"/tmp/x\"\n")
	r, err := LoadRoster(rp)
	if err != nil {
		t.Fatal(err)
	}
	if r.Project[0].Repo != "owner/actual-repo" {
		t.Errorf("repo = %q; the slug must survive, since it cannot be inferred from the path", r.Project[0].Repo)
	}
}

// A misspelled key dropped in silence is the same defect as an exclusion with no
// reason: it reads as configured while doing nothing.
func TestRosterRejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "fleet.toml")
	write(t, rp, "[[project]]\nname=\"x\"\npath=\"/tmp/x\"\nrepoo=\"typo/here\"\n")
	if _, err := LoadRoster(rp); err == nil {
		t.Error("expected rejection of an unknown roster key")
	}
}
