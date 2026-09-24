package shipped

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

// Shell-level tests of profiles/ios/root/scripts/bump-marketing-version.sh,
// executed the way a synced repo runs it, against a real fixture repository
// built by internal/gittest. Every refusal is asserted twice: it exits non-zero
// AND the file on disk is byte-for-byte what it was — a refusal that leaves the
// pbxproj half-written is worse than no script.

// pbxprojFixture is a minimal project.pbxproj with the shapes that matter:
// several build configurations sharing one MARKETING_VERSION, a line that is
// not one (CURRENT_PROJECT_VERSION), and a line that merely MENTIONS the setting
// (a comment), which a careless pattern would rewrite.
const pbxprojFixture = `// !$*UTF8*$!
{
	archiveVersion = 1;
	objectVersion = 56;
	objects = {
		AA /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				CURRENT_PROJECT_VERSION = 7;
				MARKETING_VERSION = 1.0;
				PRODUCT_NAME = App;
			};
		};
		BB /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				CURRENT_PROJECT_VERSION = 7;
				MARKETING_VERSION = 1.0;
				PRODUCT_NAME = App;
			};
		};
		CC /* Tests */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				MARKETING_VERSION = 1.0;
				/* MARKETING_VERSION = 1.0; is bumped by scripts/bump-marketing-version.sh */
			};
		};
	};
}
`

type bumpRepo struct {
	dir  string
	pbx  string // repo-relative path of the project file
	orig string
}

// newBumpRepo commits pbxprojFixture (or body, when given) at pbx in a fresh
// repository, plus a README so an "other file changed" case has something to touch.
func newBumpRepo(t *testing.T, pbx, body string) *bumpRepo {
	t.Helper()
	dir := t.TempDir()
	gittest.Init(t, dir)
	if body == "" {
		body = pbxprojFixture
	}
	full := filepath.Join(dir, filepath.FromSlash(pbx))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "initial")
	return &bumpRepo{dir: dir, pbx: pbx, orig: body}
}

func (r *bumpRepo) read(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(r.pbx)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bumpScriptPath is the script in the profile tree; the test above proves the
// sync delivers the same file.
func bumpScriptPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(root(t), "profiles", "ios", "root", filepath.FromSlash(bumpScript))
}

// runBump executes the script with dir as the working directory. extraPath, when
// set, is prepended to PATH so a test can stand a tool in front of the real one.
func runBump(t *testing.T, dir, extraPath string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bumpScriptPath(t), args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if extraPath != "" {
		cmd.Env = append(cmd.Env, "PATH="+extraPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	default:
		t.Fatalf("could not run %s: %v", bumpScript, err)
		return "", -1
	}
}

// wantUntouched fails unless the project file is exactly what was committed.
func (r *bumpRepo) wantUntouched(t *testing.T) {
	t.Helper()
	if got := r.read(t); got != r.orig {
		t.Errorf("the project file was not restored:\n%s", firstLineDiff(r.orig, got))
	}
}

func firstLineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return "line " + strconv.Itoa(i+1) + ":\n  want " + a + "\n  got  " + b
		}
	}
	return "(identical)"
}

// The success case: every MARKETING_VERSION line moves, nothing else does, and
// the report says old → new and how many.
func TestBumpMarketingVersionCleanBump(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")

	out, code := runBump(t, r.dir, "", "1.0.1")
	if code != 0 {
		t.Fatalf("a clean bump failed (exit %d):\n%s", code, out)
	}
	mustContain(t, "the report", out, "1.0", "1.0.1", "→", "3")

	want := strings.Replace(r.orig, "\t\t\t\tMARKETING_VERSION = 1.0;", "\t\t\t\tMARKETING_VERSION = 1.0.1;", -1)
	if got := r.read(t); got != want {
		t.Errorf("the file is not the original with only MARKETING_VERSION lines changed:\n%s", firstLineDiff(want, got))
	}
	if !strings.Contains(r.read(t), "/* MARKETING_VERSION = 1.0; is bumped by") {
		t.Errorf("a comment that merely mentions MARKETING_VERSION was rewritten")
	}
	// The verification the script claims is real: git sees one file, and only
	// MARKETING_VERSION lines in it.
	if files := strings.Fields(gitIn(t, r.dir, "diff", "HEAD", "--name-only")); len(files) != 1 || files[0] != r.pbx {
		t.Errorf("the diff touches %v, want only %s", files, r.pbx)
	}
	for _, line := range strings.Split(gitIn(t, r.dir, "diff", "HEAD", "-U0", "--", r.pbx), "\n") {
		if (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")) &&
			!strings.HasPrefix(line, "+++") && !strings.HasPrefix(line, "---") &&
			!strings.Contains(line, "MARKETING_VERSION = ") {
			t.Errorf("a changed line is not a MARKETING_VERSION line: %q", line)
		}
	}
}

// Found from the component root, however deep the project sits; and an explicit
// path works from anywhere.
func TestBumpMarketingVersionFindsTheProjectFile(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "ios/App.xcodeproj/project.pbxproj", "")

	if out, code := runBump(t, r.dir, "", "2.0"); code != 0 {
		t.Fatalf("could not find a nested project from the repository root (exit %d):\n%s", code, out)
	}
	if !strings.Contains(r.read(t), "MARKETING_VERSION = 2.0;") {
		t.Errorf("the nested project was not bumped")
	}

	// Explicit path, from a directory that is not the component root. The first
	// bump has to be committed: the script refuses a dirty project file.
	gitIn(t, r.dir, "commit", "-qam", "bump")
	elsewhere := filepath.Join(r.dir, "ios")
	if out, code := runBump(t, elsewhere, "", "2.1", filepath.Join(r.dir, r.pbx)); code != 0 {
		t.Fatalf("an explicit path argument failed (exit %d):\n%s", code, out)
	}
	if !strings.Contains(r.read(t), "MARKETING_VERSION = 2.1;") {
		t.Errorf("the explicit-path bump did not apply")
	}
}

// Two projects and no path is ambiguous. Guessing would bump the wrong app.
func TestBumpMarketingVersionRefusesAnAmbiguousProject(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "A.xcodeproj/project.pbxproj", "")
	other := filepath.Join(r.dir, "B.xcodeproj", "project.pbxproj")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(pbxprojFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.dir, "add", "-A")
	gitIn(t, r.dir, "commit", "-qm", "second project")

	out, code := runBump(t, r.dir, "", "1.1")
	if code == 0 {
		t.Fatalf("two projects and no path argument was accepted:\n%s", out)
	}
	mustContain(t, "the refusal", out, "A.xcodeproj/project.pbxproj", "B.xcodeproj/project.pbxproj")
	r.wantUntouched(t)
}

// An unrelated STAGED edit in the same file. The script cannot tell its own
// change from that one afterwards, so it must refuse before touching anything —
// and must not disturb the index.
func TestBumpMarketingVersionRefusesUnrelatedStagedEdit(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")
	edited := strings.Replace(r.orig, "objectVersion = 56;", "objectVersion = 77;", 1)
	if err := os.WriteFile(filepath.Join(r.dir, r.pbx), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.dir, "add", r.pbx)

	out, code := runBump(t, r.dir, "", "1.0.1")
	if code == 0 {
		t.Fatalf("a pbxproj with a staged unrelated edit was bumped:\n%s", out)
	}
	mustContain(t, "the refusal", out, "uncommitted")
	if got := r.read(t); got != edited {
		t.Errorf("the refusal changed the file:\n%s", firstLineDiff(edited, got))
	}
	if staged := strings.TrimSpace(gitIn(t, r.dir, "diff", "--cached", "--name-only")); staged != r.pbx {
		t.Errorf("the staged edit was disturbed: index now differs in %q", staged)
	}
}

// The same, unstaged.
func TestBumpMarketingVersionRefusesUnrelatedUnstagedEdit(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")
	edited := strings.Replace(r.orig, "objectVersion = 56;", "objectVersion = 77;", 1)
	if err := os.WriteFile(filepath.Join(r.dir, r.pbx), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, code := runBump(t, r.dir, "", "1.0.1"); code == 0 {
		t.Fatalf("a pbxproj with an unstaged unrelated edit was bumped:\n%s", out)
	}
	if got := r.read(t); got != edited {
		t.Errorf("the refusal changed the file:\n%s", firstLineDiff(edited, got))
	}
}

// Zero lines replaced is a failure, not a success: a project with no
// MARKETING_VERSION (or a wrong file) would otherwise "succeed" having done
// nothing, which reads exactly like a bump that worked.
func TestBumpMarketingVersionFailsWhenNothingMatches(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", strings.Replace(pbxprojFixture, "MARKETING_VERSION", "OTHER_SETTING", -1))

	out, code := runBump(t, r.dir, "", "1.0.1")
	if code == 0 {
		t.Fatalf("a project with no MARKETING_VERSION line reported success:\n%s", out)
	}
	// The message that names THIS failure, not the downstream count mismatch a
	// zero-match run would also trip.
	mustContain(t, "the failure", out, "no MARKETING_VERSION")
	r.wantUntouched(t)
}

// A version that is not a plain version never reaches sed.
func TestBumpMarketingVersionRejectsAMalformedVersion(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")
	for _, bad := range []string{"", "abc", "1.0;rm", "1.0/2", "v1.0", "1..0"} {
		args := []string{bad}
		if bad == "" {
			args = nil
		}
		if out, code := runBump(t, r.dir, "", args...); code == 0 {
			t.Errorf("version %q was accepted:\n%s", bad, out)
		}
		r.wantUntouched(t)
	}
}

// Running it with the version the project already has is a no-op that says so.
func TestBumpMarketingVersionIsIdempotent(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")

	out, code := runBump(t, r.dir, "", "1.0")
	if code != 0 {
		t.Fatalf("bumping to the current version failed (exit %d):\n%s", code, out)
	}
	mustContain(t, "the no-op report", out, "already", "1.0")
	r.wantUntouched(t)
	if status := strings.TrimSpace(gitIn(t, r.dir, "status", "--porcelain")); status != "" {
		t.Errorf("the no-op left the worktree dirty:\n%s", status)
	}

	// And running a real bump twice: the second is the no-op — before the
	// first is committed (the dirty-file refusal must not pre-empt it) and after.
	if _, code := runBump(t, r.dir, "", "1.0.1"); code != 0 {
		t.Fatal("first bump failed")
	}
	if out, code := runBump(t, r.dir, "", "1.0.1"); code != 0 || !strings.Contains(out, "already") {
		t.Errorf("the repeat bump of an uncommitted bump (exit %d) is not a reported no-op:\n%s", code, out)
	}
	gitIn(t, r.dir, "commit", "-qam", "bump")
	if out, code := runBump(t, r.dir, "", "1.0.1"); code != 0 || !strings.Contains(out, "already") {
		t.Errorf("the second identical bump (exit %d) is not a reported no-op:\n%s", code, out)
	}
}

// Targets may carry different versions (an app and its extension). All move to
// the new value, and the report names every old value it replaced.
func TestBumpMarketingVersionReportsEveryOldValue(t *testing.T) {
	t.Parallel()
	body := strings.Replace(pbxprojFixture, "\t\t\t\tMARKETING_VERSION = 1.0;\n\t\t\t\t/*", "\t\t\t\tMARKETING_VERSION = 0.9;\n\t\t\t\t/*", 1)
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", body)

	out, code := runBump(t, r.dir, "", "1.2")
	if code != 0 {
		t.Fatalf("a mixed-version bump failed (exit %d):\n%s", code, out)
	}
	mustContain(t, "the report", out, "1.0", "0.9", "1.2")
	if n := strings.Count(r.read(t), "MARKETING_VERSION = 1.2;"); n != 3 {
		t.Errorf("%d lines carry 1.2, want 3", n)
	}
}

// The diff must touch only the project file. A change anywhere else in the
// worktree fails the run and the project file goes back.
func TestBumpMarketingVersionFailsWhenTheDiffTouchesAnotherFile(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")
	if err := os.WriteFile(filepath.Join(r.dir, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runBump(t, r.dir, "", "1.0.1")
	if code == 0 {
		t.Fatalf("the diff touched README.md as well and the run still passed:\n%s", out)
	}
	mustContain(t, "the failure", out, "README.md")
	r.wantUntouched(t)
}

// The guard against a changed line that is not a MARKETING_VERSION line. sed
// cannot produce one from the anchored pattern, so this stands a sed in front of
// the real one that ALSO rewrites objectVersion — a tool that misbehaves — and
// requires the script to notice by looking at the result, not by trusting its
// own command. Without this test the check could be deleted and nothing would fail.
func TestBumpMarketingVersionCatchesAnUnrelatedLineChange(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")

	realSed, err := exec.LookPath("sed")
	if err != nil {
		t.Fatal(err)
	}
	stubs := t.TempDir()
	writeExe(t, filepath.Join(stubs, "sed"),
		"#!/bin/sh\n\""+realSed+"\" \"$@\" | \""+realSed+"\" 's/objectVersion = 56;/objectVersion = 77;/'\n")

	out, code := runBump(t, r.dir, stubs, "1.0.1")
	if code == 0 {
		t.Fatalf("a change to a line that is not MARKETING_VERSION was not caught:\n%s", out)
	}
	mustContain(t, "the failure", out, "objectVersion")
	r.wantUntouched(t)
}

// And the count check: a rewrite that drops a MARKETING_VERSION line (sed
// deleting one) is not "replaced N lines" and must not pass.
func TestBumpMarketingVersionCatchesAMissingReplacement(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")

	realSed, err := exec.LookPath("sed")
	if err != nil {
		t.Fatal(err)
	}
	stubs := t.TempDir()
	// Removes the first MARKETING_VERSION line instead of rewriting it.
	writeExe(t, filepath.Join(stubs, "sed"),
		"#!/bin/sh\n\""+realSed+"\" \"$@\" | awk '/MARKETING_VERSION = 1.0.1;/ && !d {d=1; next} {print}'\n")

	if out, code := runBump(t, r.dir, stubs, "1.0.1"); code == 0 {
		t.Fatalf("a dropped MARKETING_VERSION line was not caught:\n%s", out)
	}
	r.wantUntouched(t)
}

// stubSed puts a sed in front of the real one that pipes the real output through
// an awk program — a stand-in for a tool that misbehaves in a specific way.
func stubSed(t *testing.T, awkProgram string) string {
	t.Helper()
	realSed, err := exec.LookPath("sed")
	if err != nil {
		t.Fatal(err)
	}
	stubs := t.TempDir()
	writeExe(t, filepath.Join(stubs, "sed"),
		"#!/bin/sh\n\""+realSed+"\" \"$@\" | awk '"+awkProgram+"'\n")
	return stubs
}

// Two more ways the result can be wrong while every changed line still IS a
// MARKETING_VERSION line and only the project file changed. Each is caught by
// exactly one check, so removing either check fails exactly one test here.

// A line rewritten to the wrong value: counts of removed and added agree, but
// fewer lines carry the requested version than the file has settings.
func TestBumpMarketingVersionCatchesAWrongValue(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")
	stubs := stubSed(t, `/MARKETING_VERSION = 1.0.1;/ && !d {d=1; sub(/1.0.1/, "9.9")} {print}`)

	out, code := runBump(t, r.dir, stubs, "1.0.1")
	if code == 0 {
		t.Fatalf("a line left at the wrong version passed:\n%s", out)
	}
	mustContain(t, "the failure", out, "afterwards")
	r.wantUntouched(t)
}

// An extra MARKETING_VERSION line appearing: the lines at the requested version
// still number what they should, but the diff has one more added line than removed.
func TestBumpMarketingVersionCatchesAnExtraLine(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", "")
	stubs := stubSed(t, `{print} /MARKETING_VERSION = 1.0.1;/ && !d {d=1; print "\t\t\t\tMARKETING_VERSION = 5.5;"}`)

	out, code := runBump(t, r.dir, stubs, "1.0.1")
	if code == 0 {
		t.Fatalf("an extra MARKETING_VERSION line passed:\n%s", out)
	}
	mustContain(t, "the failure", out, "added")
	r.wantUntouched(t)
}

// Generated project edits disappear on regeneration (#445). Refuse before
// writing, including the otherwise-successful already-at-this-version path.
func TestBumpMarketingVersionRefusesGeneratedProject(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, pbx, spec, version     string
		explicit, ignored, untracked bool
	}{
		{name: "root spec", pbx: "App.xcodeproj/project.pbxproj", spec: "project.yml", version: "1.1"},
		{name: "already current", pbx: "App.xcodeproj/project.pbxproj", spec: "project.yml", version: "1.0"},
		{name: "untracked with spec", pbx: "App.xcodeproj/project.pbxproj", spec: "project.yml", version: "1.1", ignored: true, untracked: true},
		{name: "nested automatic", pbx: "ios/App.xcodeproj/project.pbxproj", spec: "ios/project.yml", version: "1.1"},
		{name: "nested explicit", pbx: "ios/App.xcodeproj/project.pbxproj", spec: "ios/project.yml", version: "1.1", explicit: true},
		{name: "ancestor spec", pbx: "ios/Generated/App.xcodeproj/project.pbxproj", spec: "ios/project.yml", version: "1.1", explicit: true},
		{name: "yaml spec", pbx: "App.xcodeproj/project.pbxproj", spec: "project.yaml", version: "1.1"},
		{name: "ignored tracked", pbx: "App.xcodeproj/project.pbxproj", version: "1.1", ignored: true},
		{name: "ignored untracked", pbx: "App.xcodeproj/project.pbxproj", version: "1.1", explicit: true, ignored: true, untracked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newBumpRepo(t, tc.pbx, "")
			if tc.spec != "" {
				if err := os.WriteFile(filepath.Join(r.dir, tc.spec), []byte("name: App\nsettings:\n  MARKETING_VERSION: 1.0\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.ignored {
				if err := os.WriteFile(filepath.Join(r.dir, ".gitignore"), []byte("*.xcodeproj/\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.untracked {
				gitIn(t, r.dir, "rm", "--cached", r.pbx)
			}
			gitIn(t, r.dir, "add", "-A")
			gitIn(t, r.dir, "commit", "-qm", "generator inputs")
			args := []string{tc.version}
			if tc.explicit {
				args = append(args, filepath.Join(r.dir, r.pbx))
			}
			out, code := runBump(t, r.dir, "", args...)
			if code == 0 {
				t.Errorf("generated project reported success (exit 0):\n%s", out)
			}
			mustContain(t, "the refusal", out, "refusing", "MARKETING_VERSION", "xcconfig")
			if tc.spec != "" {
				mustContain(t, "the source path", out, tc.spec, "xcodegen generate")
			}
			if tc.ignored && tc.spec == "" {
				mustContain(t, "the ignored project", out, "gitignored")
			}
			r.wantUntouched(t)
			if diff := gitIn(t, r.dir, "status", "--porcelain"); strings.TrimSpace(diff) != "" {
				t.Errorf("refusal changed repository state: %s", diff)
			}
		})
	}
}

// A spec for another component must not disable a hand-maintained project.
func TestBumpMarketingVersionIgnoresSiblingGenerator(t *testing.T) {
	t.Parallel()
	r := newBumpRepo(t, "manual/App.xcodeproj/project.pbxproj", "")
	if err := os.Mkdir(filepath.Join(r.dir, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "generated/project.yml"), []byte("name: Other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.dir, "add", "-A")
	gitIn(t, r.dir, "commit", "-qm", "sibling generator")
	out, code := runBump(t, r.dir, "", "1.1")
	if code != 0 {
		t.Fatalf("unrelated generator blocked manual project (exit %d):\n%s", code, out)
	}
	want := strings.ReplaceAll(r.orig, "\t\t\t\tMARKETING_VERSION = 1.0;", "\t\t\t\tMARKETING_VERSION = 1.1;")
	if got := r.read(t); got != want {
		t.Errorf("manual project was not bumped: %s", firstLineDiff(want, got))
	}
}

// The abved failure: project settings are stale, while target configurations
// inherit the shipped version from Config/Paid.xcconfig.
const xcconfigPbxprojFixture = `// !$*UTF8*$!
{
	objects = {
		ROOT = { isa = PBXGroup; children = (CONFIG,); sourceTree = "<group>"; };
		CONFIG = { isa = PBXGroup; children = (PAID,); path = Config; sourceTree = "<group>"; };
		PAID = { isa = PBXFileReference; path = Paid.xcconfig; sourceTree = "<group>"; };
		PROJECT = { isa = PBXProject; mainGroup = ROOT; buildConfigurationList = PROJECTLIST; targets = (APP,); };
		APP = { isa = PBXNativeTarget; buildConfigurationList = TARGETLIST; };
		PROJECTLIST = { isa = XCConfigurationList; buildConfigurations = (PD, PR,); };
		TARGETLIST = { isa = XCConfigurationList; buildConfigurations = (TD, TR,); };
		PD = {
			isa = XCBuildConfiguration;
			buildSettings = {
				MARKETING_VERSION = 3.0;
			};
		};
		PR = {
			isa = XCBuildConfiguration;
			buildSettings = {
				MARKETING_VERSION = 3.0;
			};
		};
		TD = { isa = XCBuildConfiguration; baseConfigurationReference = PAID; buildSettings = {}; };
		TR = { isa = XCBuildConfiguration; baseConfigurationReference = PAID; buildSettings = {}; };
	};
	rootObject = PROJECT;
}
`

func TestBumpMarketingVersionRefusesXCConfig(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, config, included, version, want string
	}{
		{"abved", "MARKETING_VERSION = 3.0.2\n", "", "3.1", "Config/Paid.xcconfig"},
		{"already stale", "MARKETING_VERSION = 3.0.2\n", "", "3.0", "Config/Paid.xcconfig"},
		{"nested include", "#include \"Shared/Version.xcconfig\"\n", "#include? \"../Actual.xcconfig\"\n", "3.1", "Config/Actual.xcconfig"},
		{"conditional", "MARKETING_VERSION[sdk=iphoneos*] = 3.0.2\n", "", "3.1", "Config/Paid.xcconfig"},
		{"missing include", "#include \"Missing.xcconfig\"\n", "", "3.1", "Missing.xcconfig"},
		{"variable include", "#include \"$(VERSIONS)/Version.xcconfig\"\n", "", "3.1", "cannot determine"},
		{"cycle", "#include \"Paid.xcconfig\"\n", "", "3.1", "cannot determine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newBumpRepo(t, "ios/App.xcodeproj/project.pbxproj", xcconfigPbxprojFixture)
			files := map[string]string{"Config/Paid.xcconfig": tc.config}
			if tc.included != "" {
				files["Config/Shared/Version.xcconfig"] = tc.included
				files["Config/Actual.xcconfig"] = "MARKETING_VERSION = 3.0.2\n"
			}
			for path, body := range files {
				full := filepath.Join(r.dir, "ios", path)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			gitIn(t, r.dir, "add", "-A")
			gitIn(t, r.dir, "commit", "-qm", "xcconfig source")
			out, code := runBump(t, r.dir, "", tc.version)
			if code == 0 {
				t.Errorf("xcconfig-sourced version reported success:\n%s", out)
			}
			mustContain(t, "the refusal", out, "refusing", tc.want, "resolved", "-showBuildSettings", "-derivedDataPath DerivedData")
			r.wantUntouched(t)
		})
	}
}

func TestBumpMarketingVersionXCConfigResolution(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, old, replacement, config string
		refuse                         bool
	}{
		{"unrelated settings", "", "", "// MARKETING_VERSION = 9.9\n/* MARKETING_VERSION = 8.8 */\nSWIFT_VERSION = 6.0\n#include? \"Absent.xcconfig\"\n", false},
		{"source root", "path = Paid.xcconfig; sourceTree = \"<group>\"", "path = Config/Paid.xcconfig; sourceTree = SOURCE_ROOT", "MARKETING_VERSION = 3.0.2\n", true},
		{"missing reference", "baseConfigurationReference = PAID", "baseConfigurationReference = MISSING", "SWIFT_VERSION = 6.0\n", true},
		{"unknown source tree", "path = Paid.xcconfig; sourceTree = \"<group>\"", "path = Paid.xcconfig; sourceTree = SDKROOT", "SWIFT_VERSION = 6.0\n", true},
		{"missing group", "children = (PAID,)", "children = ()", "SWIFT_VERSION = 6.0\n", true},
		{"group cycle", "children = (PAID,)", "children = (PAID, ROOT,)", "SWIFT_VERSION = 6.0\n", true},
		{"unsupported include", "", "", "#include <Versions.xcconfig>\n", true},
		{"unterminated comment", "", "", "/* MARKETING_VERSION = 3.0.2\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := xcconfigPbxprojFixture
			if tc.old != "" {
				body = strings.ReplaceAll(body, tc.old, tc.replacement)
			}
			r := newBumpRepo(t, "App.xcodeproj/project.pbxproj", body)
			if err := os.Mkdir(filepath.Join(r.dir, "Config"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(r.dir, "Config/Paid.xcconfig"), []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			gitIn(t, r.dir, "add", "-A")
			gitIn(t, r.dir, "commit", "-qm", "xcconfig inputs")
			out, code := runBump(t, r.dir, "", "3.1")
			if tc.refuse {
				if code == 0 {
					t.Errorf("unresolved/xcconfig source accepted:\n%s", out)
				}
				mustContain(t, "the refusal", out, "refusing", "resolved", "-showBuildSettings")
				r.wantUntouched(t)
			} else {
				if code != 0 {
					t.Fatalf("unrelated xcconfig blocked a safe bump:\n%s", out)
				}
				want := strings.ReplaceAll(r.orig, "MARKETING_VERSION = 3.0;", "MARKETING_VERSION = 3.1;")
				if got := r.read(t); got != want {
					t.Errorf("safe bump differs: %s", firstLineDiff(want, got))
				}
			}
		})
	}
}
