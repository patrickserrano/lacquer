package audit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

const bundleResolved = "project.xcworkspace/xcshareddata/swiftpm/Package.resolved"

// pinRepo builds a git repository holding committed files, plus files written
// to disk but never added.
func pinRepo(t *testing.T, committed, untracked map[string]string) string {
	t.Helper()
	root := t.TempDir()
	gittest.Init(t, root, "-q")
	write := func(files map[string]string) {
		for p, body := range files {
			full := filepath.Join(root, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(committed)
	pinGit(t, root, "add", "-A")
	pinGit(t, root, "commit", "-q", "-m", "init")
	write(untracked)
	return root
}

func pinGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// kinds counts findings by Kind.
func kinds(fs []PinFinding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.Kind]++
	}
	return m
}

func only(t *testing.T, fs []PinFinding, kind string) []PinFinding {
	t.Helper()
	var out []PinFinding
	for _, f := range fs {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

// momfriend's exact case: exactVersion 9.26.0 in the pbxproj (for privacy
// verification), Dependabot bumped only Package.resolved to 9.28.0, and SPM
// quietly resolved back to 9.26.0 at build time, so nothing ever went red.
func TestPackagePinsMomfriendExactVersion(t *testing.T) {
	resolved := "ios/MomFriend.xcodeproj/" + bundleResolved
	pbx := "ios/MomFriend.xcodeproj/project.pbxproj"
	root := pinRepo(t, map[string]string{
		pbx:                               pbxprojMomfriend,
		resolved:                          resolvedMomfriend,
		"ios/MomFriendCore/Package.swift": "// swift-tools-version: 6.2\nimport PackageDescription\nlet package = Package(name: \"MomFriendCore\")\n",
	}, nil)

	fs := PackagePinFindings(root)
	v := only(t, fs, PinViolates)
	if len(v) != 1 {
		t.Fatalf("violations = %+v; want exactly sentry-cocoa", fs)
	}
	f := v[0]
	if f.Package != "sentry-cocoa" || f.Resolved != resolved || f.Pinned != "9.28.0" {
		t.Errorf("finding = %+v", f)
	}
	if want := lineOf(t, resolvedMomfriend, `"version" : "9.28.0"`, 1); f.Line != want {
		t.Errorf("resolved line = %d, want %d", f.Line, want)
	}
	if f.Req == nil || f.Req.File != pbx || f.Req.Line != lineOf(t, pbxprojMomfriend, "requirement = {", 1) {
		t.Errorf("requirement = %+v", f.Req)
	}
	// aptabase and purchases-ios-spm satisfy their requirements; MomFriendCore
	// declares nothing. Nothing else to say beyond the summary.
	if k := kinds(fs); len(fs) != 2 || k[PinChecked] != 1 {
		t.Errorf("findings = %+v; want the summary and the violation", fs)
	}
	if s := only(t, fs, PinChecked)[0]; s.Pins != 3 || s.Reqs != 3 || s.Violations != 1 {
		t.Errorf("summary = %+v", s)
	}

	out := FormatPackagePins(fs)
	for _, want := range []string{
		"sentry-cocoa",
		"exactly 9.26.0",
		"9.28.0",
		resolved + ":" + itoa(f.Line),
		pbx + ":" + itoa(f.Req.Line),
		"build time",
		"Dependabot",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

// Only COMMITTED lockfiles are the subject. One on disk but not in git is not
// what Dependabot bumps or what a checkout builds.
func TestPackagePinsIgnoresUntrackedResolved(t *testing.T) {
	root := pinRepo(t,
		map[string]string{"App.xcodeproj/project.pbxproj": pbxprojMomfriend},
		map[string]string{"App.xcodeproj/" + bundleResolved: resolvedMomfriend})
	if fs := PackagePinFindings(root); len(fs) != 0 {
		t.Errorf("findings = %+v; want none for an untracked Package.resolved", fs)
	}
	if out := FormatPackagePins(nil); out != "" {
		t.Errorf("FormatPackagePins(nil) = %q; want empty", out)
	}
}

// A project whose resolved file satisfies everything says so, in one line —
// "checked and fine" must not look like "never looked".
func TestPackagePinsCleanProjectSaysWhatItChecked(t *testing.T) {
	fixed := strings.Replace(resolvedMomfriend, `"version" : "9.28.0"`, `"version" : "9.26.0"`, 1)
	root := pinRepo(t, map[string]string{
		"App.xcodeproj/project.pbxproj":   pbxprojMomfriend,
		"App.xcodeproj/" + bundleResolved: fixed,
		"MomFriendCore/Package.swift":     "// swift-tools-version: 6.2\nimport PackageDescription\nlet package = Package(name: \"MomFriendCore\")\n",
	}, nil)
	fs := PackagePinFindings(root)
	if k := kinds(fs); k[PinViolates] != 0 || k[PinUnpinned] != 0 || k[PinUnrequired] != 0 {
		t.Fatalf("findings = %+v; want none", fs)
	}
	if len(fs) != 1 || fs[0].Kind != PinChecked || fs[0].Violations != 0 {
		t.Fatalf("findings = %+v; want one clean summary", fs)
	}
	out := FormatPackagePins(fs)
	if !strings.Contains(out, "App.xcodeproj/"+bundleResolved) || !strings.Contains(out, "3 pins") || !strings.Contains(out, "3 requirements") || !strings.Contains(out, "all satisfied") {
		t.Errorf("clean summary does not say what it checked:\n%s", out)
	}
}

// A requirement missing from the resolved file, and a pin nothing requires, are
// notes: the first is a stale lockfile, the second is usually a transitive
// dependency. Neither is the lockfile contradicting a requirement.
func TestPackagePinsUnpinnedAndUnrequiredAreNotes(t *testing.T) {
	pbx := strings.Replace(pbxprojMomfriend, "/* End XCRemoteSwiftPackageReference section */",
		`		B00000000000000000000001 /* XCRemoteSwiftPackageReference "swift-collections" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://github.com/apple/swift-collections";
			requirement = {
				kind = upToNextMajorVersion;
				minimumVersion = 1.1.0;
			};
		};
/* End XCRemoteSwiftPackageReference section */`, 1)
	resolved := strings.Replace(resolvedMomfriend, `  "pins" : [`, `  "pins" : [
    {
      "identity" : "swift-log",
      "kind" : "remoteSourceControl",
      "location" : "https://github.com/apple/swift-log.git",
      "state" : {
        "revision" : "96a2f8a0fa41e9e09af4585e2724c4e825410b91",
        "version" : "1.6.2"
      }
    },`, 1)
	resolved = strings.Replace(resolved, `"version" : "9.28.0"`, `"version" : "9.26.0"`, 1)
	root := pinRepo(t, map[string]string{
		"App.xcodeproj/project.pbxproj":   pbx,
		"App.xcodeproj/" + bundleResolved: resolved,
	}, nil)
	fs := PackagePinFindings(root)
	k := kinds(fs)
	if k[PinViolates] != 0 || k[PinUnpinned] != 1 || k[PinUnrequired] != 1 {
		t.Fatalf("kinds = %v; findings = %+v", k, fs)
	}
	if u := only(t, fs, PinUnpinned)[0]; u.Package != "swift-collections" || u.Req == nil || u.Req.Line != lineOf(t, pbx, "requirement = {", 4) {
		t.Errorf("unpinned = %+v", u)
	}
	if u := only(t, fs, PinUnrequired)[0]; u.Package != "swift-log" || u.Line != lineOf(t, resolved, `"version" : "1.6.2"`, 1) {
		t.Errorf("unrequired = %+v", u)
	}
	out := FormatPackagePins(fs)
	if strings.Contains(out, "do not match the declared") {
		t.Errorf("notes alone were reported as a mismatch:\n%s", out)
	}
	for _, want := range []string{"swift-collections", "swift-log"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

// XcodeGen: with no committed pbxproj, project.yml's packages are what the next
// generate will write, so they are the requirements.
func TestPackagePinsXcodeGenWithoutPbxproj(t *testing.T) {
	yml := "name: App\npackages:\n  Sentry:\n    url: https://github.com/getsentry/sentry-cocoa.git\n    exactVersion: 9.26.0\n  Aptabase:\n    url: https://github.com/aptabase/aptabase-swift\n    minorVersion: 0.3.11\n  RevenueCat:\n    url: https://github.com/RevenueCat/purchases-ios-spm\n    from: 5.0.0\n"
	root := pinRepo(t, map[string]string{
		"ios/project.yml":                     yml,
		"ios/App.xcodeproj/" + bundleResolved: resolvedMomfriend,
	}, nil)
	fs := PackagePinFindings(root)
	v := only(t, fs, PinViolates)
	if len(v) != 1 || v[0].Package != "sentry-cocoa" {
		t.Fatalf("findings = %+v", fs)
	}
	if v[0].Req.File != "ios/project.yml" || v[0].Req.Line != lineOf(t, yml, "  Sentry:", 1) {
		t.Errorf("requirement = %+v", v[0].Req)
	}
	if len(fs) != 2 {
		t.Errorf("findings = %+v; want only the summary and the violation", fs)
	}
}

// When both are committed the pbxproj wins: it is what xcodebuild reads. A
// project.yml that disagrees is said, as a note, because the next xcodegen
// generate will change the requirement.
func TestPackagePinsPbxprojWinsOverProjectYml(t *testing.T) {
	yml := "name: App\npackages:\n  Sentry:\n    url: https://github.com/getsentry/sentry-cocoa\n    from: 9.27.0\n  Aptabase:\n    url: https://github.com/aptabase/aptabase-swift\n    from: 0.3.11\n"
	fixed := strings.Replace(resolvedMomfriend, `"version" : "9.28.0"`, `"version" : "9.26.0"`, 1)
	root := pinRepo(t, map[string]string{
		"project.yml":                     yml,
		"App.xcodeproj/project.pbxproj":   pbxprojMomfriend,
		"App.xcodeproj/" + bundleResolved: fixed,
	}, nil)
	fs := PackagePinFindings(root)
	if k := kinds(fs); k[PinViolates] != 0 {
		t.Fatalf("pbxproj's exact 9.26.0 is satisfied, but got violations: %+v", fs)
	}
	d := only(t, fs, PinDisagree)
	if len(d) != 1 || d[0].Package != "sentry-cocoa" || d[0].Req == nil || d[0].Req.File != "project.yml" {
		t.Fatalf("disagreements = %+v; findings = %+v", d, fs)
	}
	out := FormatPackagePins(fs)
	for _, want := range []string{"project.yml:" + itoa(lineOf(t, yml, "  Sentry:", 1)), "from 9.27.0", "exactly 9.26.0", "pbxproj"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

// A requirement the comparator cannot read — XcodeGen's `from: 9.26` is a YAML
// float, not a version — is neither a pass nor a violation, and the summary
// must not call the lockfile satisfied.
func TestPackagePinsIncomparableRequirementIsNotAPass(t *testing.T) {
	yml := "name: App\npackages:\n  Sentry:\n    url: https://github.com/getsentry/sentry-cocoa\n    from: 9.26\n"
	resolved := `{
  "pins" : [
    {
      "identity" : "sentry-cocoa",
      "kind" : "remoteSourceControl",
      "location" : "https://github.com/getsentry/sentry-cocoa",
      "state" : {
        "revision" : "61e8cb02434b26fb34c58126d883e5663cfde238",
        "version" : "9.28.0"
      }
    }
  ],
  "version" : 3
}
`
	root := pinRepo(t, map[string]string{
		"project.yml":                     yml,
		"App.xcodeproj/" + bundleResolved: resolved,
	}, nil)
	fs := PackagePinFindings(root)
	if k := kinds(fs); k[PinViolates] != 0 || k[PinUnchecked] != 1 {
		t.Fatalf("kinds = %v; findings = %+v", k, fs)
	}
	if u := only(t, fs, PinUnchecked)[0]; u.File != "project.yml" || u.Line != lineOf(t, yml, "  Sentry:", 1) {
		t.Errorf("not-checked = %+v", u)
	}
	if s := only(t, fs, PinChecked)[0]; s.Incomparable != 1 {
		t.Errorf("summary = %+v", s)
	}
	out := FormatPackagePins(fs)
	if strings.Contains(out, "all satisfied") || !strings.Contains(out, "1 could not be compared") {
		t.Errorf("summary misstates an uncompared pair:\n%s", out)
	}
}

// A local package whose requirements were not read leaves its pins looking
// transitive; the summary says so rather than "all satisfied" alone.
func TestPackagePinsSummaryCountsUnreadDeclarations(t *testing.T) {
	root := pinRepo(t, map[string]string{
		"App.xcodeproj/project.pbxproj":   pbxprojMomfriend,
		"App.xcodeproj/" + bundleResolved: strings.Replace(resolvedMomfriend, `"version" : "9.28.0"`, `"version" : "9.26.0"`, 1),
	}, nil)
	out := FormatPackagePins(PackagePinFindings(root))
	if !strings.Contains(out, "all satisfied; 1 declaration not read (see notes)") || !strings.Contains(out, "MomFriendCore  not checked") {
		t.Errorf("unread local package not surfaced:\n%s", out)
	}
}

// A bare SwiftPM package: Package.resolved next to Package.swift, and a local
// path dependency whose own remote requirement lands in the same lockfile.
func TestPackagePinsSwiftPackageFollowsLocalDependencies(t *testing.T) {
	resolved := `{
  "pins" : [
    {
      "identity" : "swift-argument-parser",
      "kind" : "remoteSourceControl",
      "location" : "https://github.com/apple/swift-argument-parser",
      "state" : {
        "revision" : "41982a3656a71c768319979febd796c6fd111d5c",
        "version" : "1.5.0"
      }
    }
  ],
  "version" : 3
}
`
	root := pinRepo(t, map[string]string{
		"tools/harness/Package.swift":    "// swift-tools-version: 6.0\nimport PackageDescription\nlet package = Package(name: \"harness\", dependencies: [\n    .package(path: \"../arguments\"),\n])\n",
		"tools/harness/Package.resolved": resolved,
		"tools/arguments/Package.swift":  "// swift-tools-version: 6.0\nimport PackageDescription\nlet package = Package(name: \"arguments\", dependencies: [\n    .package(url: \"https://github.com/apple/swift-argument-parser\", .upToNextMajor(from: \"1.8.2\")),\n])\n",
	}, nil)
	fs := PackagePinFindings(root)
	v := only(t, fs, PinViolates)
	if len(v) != 1 || v[0].Package != "swift-argument-parser" || v[0].Req.File != "tools/arguments/Package.swift" || v[0].Req.Line != 4 {
		t.Fatalf("findings = %+v", fs)
	}
	if len(fs) != 2 {
		t.Errorf("findings = %+v; want only the summary and the violation", fs)
	}
}

// A pbxproj's local package (XCLocalSwiftPackageReference) declares
// requirements too, and they meet in the app's lockfile. A requirement the
// audit could not read is reported as not checked, never guessed.
func TestPackagePinsPbxprojLocalPackageAndUnchecked(t *testing.T) {
	root := pinRepo(t, map[string]string{
		"App.xcodeproj/project.pbxproj":   pbxprojMomfriend,
		"App.xcodeproj/" + bundleResolved: strings.Replace(resolvedMomfriend, `"version" : "9.28.0"`, `"version" : "9.26.0"`, 1),
		"MomFriendCore/Package.swift": "// swift-tools-version: 6.2\nimport PackageDescription\nlet v = \"1.0.0\"\nlet package = Package(name: \"MomFriendCore\", dependencies: [\n" +
			"    .package(url: \"https://github.com/aptabase/aptabase-swift.git\", exact: \"0.3.10\"),\n" +
			"    .package(url: \"https://github.com/example/computed\", from: v),\n])\n",
	}, nil)
	fs := PackagePinFindings(root)
	v := only(t, fs, PinViolates)
	if len(v) != 1 || v[0].Package != "aptabase-swift" || v[0].Req.File != "MomFriendCore/Package.swift" || v[0].Req.Line != 5 {
		t.Fatalf("violations = %+v; findings = %+v", v, fs)
	}
	u := only(t, fs, PinUnchecked)
	if len(u) != 1 || u[0].File != "MomFriendCore/Package.swift" || u[0].Line != 6 {
		t.Fatalf("unchecked = %+v; findings = %+v", u, fs)
	}
	if !strings.Contains(FormatPackagePins(fs), "not checked") {
		t.Errorf("report does not say what it did not check:\n%s", FormatPackagePins(fs))
	}
}

// A standalone workspace's lockfile covers every project the workspace names.
func TestPackagePinsStandaloneWorkspace(t *testing.T) {
	root := pinRepo(t, map[string]string{
		"App.xcworkspace/contents.xcworkspacedata": `<?xml version="1.0" encoding="UTF-8"?>
<Workspace
   version = "1.0">
   <FileRef
      location = "group:ios/MomFriend.xcodeproj">
   </FileRef>
</Workspace>
`,
		"App.xcworkspace/xcshareddata/swiftpm/Package.resolved": resolvedMomfriend,
		"ios/MomFriend.xcodeproj/project.pbxproj":               pbxprojMomfriend,
	}, nil)
	fs := PackagePinFindings(root)
	v := only(t, fs, PinViolates)
	if len(v) != 1 || v[0].Package != "sentry-cocoa" || v[0].Req.File != "ios/MomFriend.xcodeproj/project.pbxproj" {
		t.Fatalf("findings = %+v", fs)
	}
}

// A lockfile the audit cannot read, or one whose requirements it cannot find,
// is said — never a clean result.
func TestPackagePinsUnreadableIsReported(t *testing.T) {
	root := pinRepo(t, map[string]string{
		"A.xcodeproj/project.pbxproj":   pbxprojMomfriend,
		"A.xcodeproj/" + bundleResolved: "{ not json",
		"B.xcodeproj/project.pbxproj":   "{ objects = { X = { isa = XCRemoteSwiftPackageReference; ",
		"B.xcodeproj/" + bundleResolved: resolvedMomfriend,
		"C.xcodeproj/" + bundleResolved: resolvedMomfriend,
	}, nil)
	fs := PackagePinFindings(root)
	u := only(t, fs, PinUnchecked)
	files := map[string]bool{}
	for _, f := range u {
		files[f.File] = true
	}
	for _, want := range []string{"A.xcodeproj/" + bundleResolved, "B.xcodeproj/project.pbxproj", "C.xcodeproj"} {
		if !files[want] {
			t.Errorf("no not-checked note for %s; findings = %+v", want, fs)
		}
	}
	if k := kinds(fs); k[PinChecked] != 0 || k[PinViolates] != 0 || k[PinUnrequired] != 0 {
		t.Errorf("an unreadable input produced a verdict: %+v", fs)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// momfriend's pbxproj, trimmed to the package sections.
const pbxprojMomfriend = `// !$*UTF8*$!
{
	archiveVersion = 1;
	classes = {
	};
	objectVersion = 77;
	objects = {

/* Begin XCLocalSwiftPackageReference section */
		F34E09373030F66F00A9D293 /* XCLocalSwiftPackageReference "MomFriendCore" */ = {
			isa = XCLocalSwiftPackageReference;
			relativePath = MomFriendCore;
		};
/* End XCLocalSwiftPackageReference section */

/* Begin XCRemoteSwiftPackageReference section */
		F3B91579303A6C3500C1ADE7 /* XCRemoteSwiftPackageReference "sentry-cocoa" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://github.com/getsentry/sentry-cocoa";
			requirement = {
				kind = exactVersion;
				version = 9.26.0;
			};
		};
		F3B9157A303A6C4200C1ADE7 /* XCRemoteSwiftPackageReference "aptabase-swift" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://github.com/aptabase/aptabase-swift";
			requirement = {
				kind = upToNextMajorVersion;
				minimumVersion = 0.3.11;
			};
		};
		F3B9157B303A6D8600C1ADE7 /* XCRemoteSwiftPackageReference "purchases-ios-spm" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://github.com/RevenueCat/purchases-ios-spm.git";
			requirement = {
				kind = upToNextMajorVersion;
				minimumVersion = 5.89.0;
			};
		};
/* End XCRemoteSwiftPackageReference section */

/* Begin XCSwiftPackageProductDependency section */
		F34E09383030F9F500A9D293 /* MomFriendCore */ = {
			isa = XCSwiftPackageProductDependency;
			package = F34E09373030F66F00A9D293 /* XCLocalSwiftPackageReference "MomFriendCore" */;
			productName = MomFriendCore;
		};
/* End XCSwiftPackageProductDependency section */
	};
	rootObject = D62340D02EA7AEEF009BAB4F /* Project object */;
}
`
