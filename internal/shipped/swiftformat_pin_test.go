package shipped

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/swiftformatpin"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// SwiftFormat is pinned, not inherited from the runner's Homebrew.
//
// A routine `brew upgrade` moved the shared runner to a release with new default
// rules, and Lint failed on unchanged main branches in every iOS project, with
// Build and Test skipped behind it. These tests hold the rendered files to the
// pin: CI installs the checksummed binary and lints with exactly that path, and
// every local entry point goes through the same resolver or does not run.

const pinnedResolver = "scripts/swiftformat-pinned.sh"

type lintStep struct {
	Run, ID, If string
	Env         map[string]string
}

func lintSteps(t *testing.T, cfg *config.Config) (install, check lintStep, order []string) {
	t.Helper()
	doc := parseIOSCI(t, cfg)
	for _, st := range doc.Jobs["lint"].Steps {
		order = append(order, st.Name)
		switch st.Name {
		case "Install pinned SwiftFormat":
			install = lintStep{st.Run, st.ID, st.If, st.Env}
		case "Check SwiftFormat":
			check = lintStep{st.Run, st.ID, st.If, st.Env}
		}
	}
	return install, check, order
}

func TestIOSCILintsWithThePinnedSwiftFormat(t *testing.T) {
	cfg := soloConfig()
	install, check, order := lintSteps(t, cfg)
	if install.Run == "" {
		t.Fatalf(`the Lint job has no "Install pinned SwiftFormat" step; steps: %v`, order)
	}
	if install.ID == "" {
		t.Fatal("the install step needs an id so the lint step can read the binary path it resolved")
	}

	iInstall, iCheck := -1, -1
	for i, n := range order {
		switch n {
		case "Install pinned SwiftFormat":
			iInstall = i
		case "Check SwiftFormat":
			iCheck = i
		}
	}
	if iInstall < 0 || iCheck < 0 || iInstall >= iCheck {
		t.Errorf("the install step must run before Check SwiftFormat; order = %v", order)
	}

	sum, err := swiftformatpin.Checksum(swiftformatpin.Default)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{pinnedResolver, "runner binary reports", "repo pins"} {
		if !strings.Contains(install.Run, want) {
			t.Errorf("install step does not carry %q:\n%s", want, install.Run)
		}
	}
	if got := install.Env["SWIFTFORMAT_PIN_VERSION"]; got != swiftformatpin.Default {
		t.Errorf("install step pins version %q, want %q", got, swiftformatpin.Default)
	}
	if got := install.Env["SWIFTFORMAT_PIN_SHA256"]; got != sum {
		t.Errorf("install step pins sha256 %q, want %q", got, sum)
	}

	// The lint must run the binary the install step resolved, never whatever is
	// on PATH.
	// Through an env var, not interpolated into the script text.
	wantBin := "steps." + install.ID + ".outputs.bin"
	var binVar string
	for k, v := range check.Env {
		if strings.Contains(v, wantBin) {
			binVar = k
		}
	}
	if binVar == "" {
		t.Errorf("Check SwiftFormat reads no env var from %s: %v", wantBin, check.Env)
	} else if !strings.Contains(check.Run, `"$`+binVar+`" --lint`) {
		t.Errorf("Check SwiftFormat does not lint with $%s:\n%s", binVar, check.Run)
	}
	if bareSwiftFormat.MatchString(check.Run) {
		t.Errorf("Check SwiftFormat still invokes a bare swiftformat:\n%s", check.Run)
	}
	// A pre-code component has no Swift to format; both steps skip together.
	if install.If != check.If || install.If == "" {
		t.Errorf("install if=%q, check if=%q; they must gate on the same condition", install.If, check.If)
	}
}

func TestIOSCISwiftFormatPinFollowsTheProjectOverride(t *testing.T) {
	cfg := soloConfig()
	cfg.Project.SwiftFormatVersion = "0.62.1"
	install, _, _ := lintSteps(t, cfg)
	sum, err := swiftformatpin.Checksum("0.62.1")
	if err != nil {
		t.Fatal(err)
	}
	if got := install.Env["SWIFTFORMAT_PIN_VERSION"]; got != "0.62.1" {
		t.Errorf("override not rendered: install step pins version %q, want 0.62.1", got)
	}
	if got := install.Env["SWIFTFORMAT_PIN_SHA256"]; got != sum {
		t.Errorf("override not rendered: install step pins sha256 %q, want %q", got, sum)
	}
}

// A required token left blank must stop the sync, so a config that skipped
// Load's check (built in memory) cannot render an install step with no sum.
func TestRenderRefusesAPinWithNoChecksum(t *testing.T) {
	cfg := soloConfig()
	cfg.Project.SwiftFormatVersion = "9.9.9"
	raw, err := os.ReadFile(iosCIPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, missing := substituteForTest(string(raw), cfg); len(missing) == 0 {
		t.Fatal("an unlisted swiftformat_version rendered without a missing-token failure")
	}
}

// bareSwiftFormat is an invocation of `swiftformat` resolved through PATH: the
// word standing alone as a command, followed by an argument, a comma (an argv
// or requires list) or a glob. A path to the resolver (…/swiftformat-pinned.sh),
// a config (.swiftformat) and a hook id do not match because the character
// either side is part of a name.
var bareSwiftFormat = regexp.MustCompile(`(^|[^A-Za-z0-9_./-])swiftformat( +[-.{*"$]|",)`)

func TestBareSwiftFormatDetectorSeesWhatItShould(t *testing.T) {
	for _, bad := range []string{
		`run: swiftformat --lint --config .swiftformat .`,
		`argv = ["swiftformat", "--lint"]`,
		`requires = ["swiftformat", "swiftlint"]`,
		`allowed-tools: Bash(swiftformat *), Bash(swiftlint *)`,
		`if x; then swiftformat --config "$C" "$FP"; fi`,
		"swiftformat {dir} --config c --quiet",
	} {
		if !bareSwiftFormat.MatchString(bad) {
			t.Errorf("the detector missed a bare invocation: %s", bad)
		}
	}
	for _, ok := range []string{
		`run: scripts/swiftformat-pinned.sh --lint .`,
		`argv = ["{root}/scripts/swiftformat-pinned.sh", "--lint"]`,
		`entry: scripts/precommit-swift.sh swiftformat`,
		`- id: swiftformat`,
		`name = "swiftformat"`,
		`config: .swiftformat`,
	} {
		if bareSwiftFormat.MatchString(ok) {
			t.Errorf("the detector flagged a line that is not a bare invocation: %s", ok)
		}
	}
}

// Every file the lacquer renders into a project, scanned for a bare
// `swiftformat`. The detector test above is what stops this passing vacuously.
func TestNoRenderedFileInvokesABareSwiftFormat(t *testing.T) {
	r := root(t)
	var hits []string
	scanned := 0
	for _, top := range []string{"core", "profiles"} {
		err := filepath.WalkDir(filepath.Join(r, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(r, p)
			rel = filepath.ToSlash(rel)
			// Config files are read by the tool, not run by it.
			if strings.HasSuffix(rel, "/config/.swiftformat") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			scanned++
			for i, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				if bareSwiftFormat.MatchString(line) {
					hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d files; the walk is not seeing the shipped tree", scanned)
	}
	if len(hits) > 0 {
		t.Errorf("%d rendered site(s) run swiftformat through PATH instead of %s:\n  %s",
			len(hits), pinnedResolver, strings.Join(hits, "\n  "))
	}
}

// substituteForTest renders a template the way sync does for the root component.
func substituteForTest(body string, cfg *config.Config) (string, []string) {
	return tokens.Substitute(body, tokens.Values(cfg, ""))
}

// parseDependabot reads a rendered dependabot.yml.
func parseDependabot(t *testing.T, body []byte) dependabotFile {
	t.Helper()
	var doc dependabotFile
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("rendered dependabot.yml is not valid YAML: %v\n%s", err, body)
	}
	return doc
}

// The resolver itself, run for real against a stand-in release served from a
// local file: no network, and the stand-in prints whatever version a test says.
func resolver(t *testing.T) string {
	t.Helper()
	cfg := soloConfig()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "root", "scripts", "swiftformat-pinned.sh"))
	if err != nil {
		t.Fatalf("the resolver is not shipped: %v", err)
	}
	body, missing := substituteForTest(string(raw), cfg)
	if len(missing) > 0 {
		t.Fatalf("resolver has unsubstituted tokens: %v", missing)
	}
	p := filepath.Join(t.TempDir(), "swiftformat-pinned.sh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeRelease zips a shell script that reports `version`, and returns the zip's
// path and sha256.
func fakeRelease(t *testing.T, version string) (zipPath, sum string) {
	t.Helper()
	if _, err := exec.LookPath("zip"); err != nil {
		t.Skip("zip not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "swiftformat")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	zipPath = filepath.Join(dir, "swiftformat.zip")
	cmd := exec.Command("zip", "-q", "-j", zipPath, bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zip: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(zipPath)
	h := sha256.Sum256(b)
	return zipPath, hex.EncodeToString(h[:])
}

func runResolver(t *testing.T, script, cache, zipPath, version, sum string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(),
		"SWIFTFORMAT_PIN_CACHE="+cache,
		"SWIFTFORMAT_PIN_URL=file://"+zipPath,
		"SWIFTFORMAT_PIN_VERSION="+version,
		"SWIFTFORMAT_PIN_SHA256="+sum,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestResolverInstallsVerifiesAndReuses(t *testing.T) {
	script := resolver(t)
	zipPath, sum := fakeRelease(t, "1.2.3")
	cache := t.TempDir()

	out, err := runResolver(t, script, cache, zipPath, "1.2.3", sum, "--path")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	bin := strings.TrimSpace(out)
	if !strings.HasPrefix(bin, cache) || !strings.Contains(bin, "/1.2.3/") {
		t.Errorf("binary %q is not in a per-version directory under the cache %q", bin, cache)
	}
	if v, _ := exec.Command(bin, "--version").Output(); strings.TrimSpace(string(v)) != "1.2.3" {
		t.Errorf("installed binary reports %q", v)
	}

	// Reuse: with the download gone, a verified cache still resolves.
	if err := os.Remove(zipPath); err != nil {
		t.Fatal(err)
	}
	if out, err := runResolver(t, script, cache, zipPath, "1.2.3", sum, "--path"); err != nil {
		t.Fatalf("a verified cache entry was not reused: %v\n%s", err, out)
	}

	// Corrupt the cached binary: it must not be trusted, and with no download
	// to repair it from the run fails rather than using it.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 1.2.3\n# tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := runResolver(t, script, cache, zipPath, "1.2.3", sum, "--path"); err == nil {
		t.Fatalf("a cached binary that no longer matches its checksum was used:\n%s", out)
	}
}

func TestResolverRejectsATamperedDownload(t *testing.T) {
	script := resolver(t)
	zipPath, sum := fakeRelease(t, "1.2.3")
	wrong := strings.Repeat("0", 64)
	out, err := runResolver(t, script, t.TempDir(), zipPath, "1.2.3", wrong, "--path")
	if err == nil {
		t.Fatalf("a download whose sha256 (%s) is not the pin (%s) was installed:\n%s", sum, wrong, out)
	}
	if !strings.Contains(out, "sha256") {
		t.Errorf("the failure does not say it was a checksum mismatch:\n%s", out)
	}
}

func TestResolverFailsWhenTheBinaryReportsAnotherVersion(t *testing.T) {
	script := resolver(t)
	zipPath, sum := fakeRelease(t, "1.2.4") // bytes verify, but it is not 1.2.3
	out, err := runResolver(t, script, t.TempDir(), zipPath, "1.2.3", sum, "--path")
	if err == nil {
		t.Fatalf("a binary reporting 1.2.4 satisfied a pin of 1.2.3:\n%s", out)
	}
	if !strings.Contains(out, "SwiftFormat: runner binary reports 1.2.4, repo pins 1.2.3") {
		t.Errorf("missing the named message:\n%s", out)
	}
}

func TestResolverPassesArgumentsThroughToThePinnedBinary(t *testing.T) {
	script := resolver(t)
	zipPath, sum := fakeRelease(t, "1.2.3")
	out, err := runResolver(t, script, t.TempDir(), zipPath, "1.2.3", sum, "--version")
	if err != nil || strings.TrimSpace(out) != "1.2.3" {
		t.Errorf("--version should reach the pinned binary: err=%v out=%q", err, out)
	}
}

// dependabotExcludes renders the shipped dependabot.yml for a planned config and
// returns the github-actions entry's exclude-paths plus every other entry's.
func plannedDependabot(t *testing.T, cfg *config.Config) dependabotFile {
	t.Helper()
	plan, err := assets.Plan(root(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range plan {
		if a.Dest != ".github/dependabot.yml" {
			continue
		}
		body, missing, err := assets.Render(a, cfg)
		if err != nil || len(missing) > 0 {
			t.Fatalf("render: %v %v", err, missing)
		}
		return parseDependabot(t, body)
	}
	t.Fatal("the plan does not ship .github/dependabot.yml")
	return dependabotFile{}
}

// expectedManagedWorkflows is derived from the profile directories, not from
// the code under test: every <profile>/workflows/*.yml lands at
// .github/workflows/<profile>-<name>.
func expectedManagedWorkflows(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	var out []string
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			ents, err := os.ReadDir(filepath.Join(root(t), "profiles", p, "workflows"))
			if err != nil {
				continue
			}
			for _, e := range ents {
				dest := ".github/workflows/" + p + "-" + e.Name()
				if !cfg.Project.Excludes(dest) {
					out = append(out, dest)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestDependabotExcludesExactlyTheManagedWorkflows(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	// Project-owned: no profile ships it, so Dependabot must keep updating it.
	if err := os.WriteFile(filepath.Join(wf, "nightly.yml"), []byte("name: nightly\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := soloConfig()
	cfg.Root = dir
	cfg.Components = []config.Component{
		{Path: ".", Profiles: []string{"ios"}},
		{Path: "web", Profiles: []string{"web"}},
	}
	// Excluded by the project: its content is the project's, so it is not managed.
	cfg.Project.Exclude = []config.Exclusion{{Path: ".github/workflows/ios-cleanup-ci.yml"}}

	want := expectedManagedWorkflows(t, cfg)
	if len(want) < 3 {
		t.Fatalf("the fixture manages only %d workflows; it cannot tell a list from a single entry: %v", len(want), want)
	}

	doc := plannedDependabot(t, cfg)
	var got []string
	seenActions := false
	for _, u := range doc.Updates {
		if u.Ecosystem != "github-actions" {
			if len(u.ExcludePaths) > 0 {
				t.Errorf("%s entry at %s carries exclude-paths %v; only github-actions manages workflows", u.Ecosystem, u.Directory, u.ExcludePaths)
			}
			continue
		}
		seenActions = true
		got = u.ExcludePaths
	}
	if !seenActions {
		t.Fatal("no github-actions entry")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("exclude-paths\n got: %v\nwant: %v", got, want)
	}
	for _, g := range got {
		if strings.Contains(g, "nightly") {
			t.Errorf("a project-owned workflow is excluded: %s", g)
		}
		if strings.Contains(g, "cleanup-ci") {
			t.Errorf("a workflow the project excluded from lacquer is still listed as managed: %s", g)
		}
	}
}

func TestDependabotHeaderExplainsTheExclusion(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root(t), "core", "root", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatal(err)
	}
	head := string(b)
	if i := strings.Index(head, "\nversion: 2"); i > 0 {
		head = head[:i]
	}
	for _, want := range []string{"exclude-paths", "drift"} {
		if !strings.Contains(head, want) {
			t.Errorf("the header comment never mentions %q, so it does not explain why managed workflows are excluded", want)
		}
	}
}

// A cached install is trusted only while the zip it came from still has the
// pinned checksum too, not just the unpacked binary: either changing means the
// entry is not what was verified.
func TestResolverDistrustsACacheWhoseZipChanged(t *testing.T) {
	script := resolver(t)
	zipPath, sum := fakeRelease(t, "1.2.3")
	cache := t.TempDir()
	out, err := runResolver(t, script, cache, zipPath, "1.2.3", sum, "--path")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	cachedZip := filepath.Join(filepath.Dir(strings.TrimSpace(out)), "swiftformat.zip")
	if err := os.WriteFile(cachedZip, []byte("not the release"), 0o644); err != nil {
		t.Fatal(err)
	}
	// With the download gone the entry cannot be repaired, so a run that still
	// succeeds trusted a cache whose zip no longer matched the pin.
	if err := os.Remove(zipPath); err != nil {
		t.Fatal(err)
	}
	if out, err := runResolver(t, script, cache, zipPath, "1.2.3", sum, "--path"); err == nil {
		t.Fatalf("a cache entry whose zip no longer matches the pin was trusted:\n%s", out)
	}
}

// A workflow the project seeds once is the project's from the next sync, so
// Dependabot must keep updating it; listing it would flip the moment it landed.
func TestDependabotLeavesSeedOnceWorkflowsToTheProject(t *testing.T) {
	cfg := soloConfig()
	cfg.Root = t.TempDir()
	cfg.Components = []config.Component{{Path: ".", Profiles: []string{"ios"}}}
	const seeded = ".github/workflows/ios-cleanup-ci.yml"
	cfg.Project.SeedOnce = []string{seeded}

	var got []string
	for _, u := range plannedDependabot(t, cfg).Updates {
		if u.Ecosystem == "github-actions" {
			got = u.ExcludePaths
		}
	}
	if len(got) < 2 {
		t.Fatalf("the fixture manages only %d workflows; it cannot tell a missing entry from an empty list: %v", len(got), got)
	}
	for _, g := range got {
		if g == seeded {
			t.Errorf("a seed-once workflow is listed as managed: %s", g)
		}
	}
}
