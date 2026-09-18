package audit

import (
	"strings"
	"testing"
)

// The comparator is the whole check: a wrong answer here either reports a
// correct lockfile as lying (and teaches people to ignore the section) or
// passes the exact case this exists for. Each row is one edge, satisfied and
// violated, for every requirement kind Xcode writes.
func TestRequirementSatisfiedBy(t *testing.T) {
	exact := func(v string) PackageRequirement { return PackageRequirement{Kind: ReqExact, Version: v} }
	major := func(v string) PackageRequirement { return PackageRequirement{Kind: ReqUpToNextMajor, Version: v} }
	minor := func(v string) PackageRequirement { return PackageRequirement{Kind: ReqUpToNextMinor, Version: v} }
	rng := func(lo, hi string) PackageRequirement {
		return PackageRequirement{Kind: ReqRange, Version: lo, Max: hi}
	}
	ver := func(v string) pinState {
		return pinState{Version: v, Revision: "61e8cb02434b26fb34c58126d883e5663cfde238"}
	}

	cases := []struct {
		name string
		req  PackageRequirement
		pin  pinState
		want bool
	}{
		// momfriend: exactVersion 9.26.0 in the pbxproj, Dependabot moved the
		// lockfile to 9.27.0 and then 9.28.0.
		{"exact equal", exact("9.26.0"), ver("9.26.0"), true},
		{"exact, momfriend's 9.28.0", exact("9.26.0"), ver("9.28.0"), false},
		{"exact, momfriend's 9.27.0", exact("9.26.0"), ver("9.27.0"), false},
		{"exact, patch above", exact("9.26.0"), ver("9.26.1"), false},
		{"exact, patch below", exact("9.26.1"), ver("9.26.0"), false},
		{"exact, prerelease of it", exact("9.26.0"), ver("9.26.0-beta.1"), false},
		{"exact ignores build metadata", exact("9.26.0"), ver("9.26.0+ci.7"), true},

		// upToNextMajor 9.26.0 is >= 9.26.0 < 10.0.0.
		{"major, lower bound", major("9.26.0"), ver("9.26.0"), true},
		{"major, inside", major("9.26.0"), ver("9.28.3"), true},
		{"major, numeric not lexical minor", major("9.26.0"), ver("9.100.0"), true},
		{"major, top of range", major("9.26.0"), ver("9.999.999"), true},
		{"major, upper bound excluded", major("9.26.0"), ver("10.0.0"), false},
		{"major, below lower bound", major("9.26.0"), ver("9.25.9"), false},
		{"major, below by patch", major("9.26.1"), ver("9.26.0"), false},
		{"major, prerelease inside range", major("9.26.0"), ver("9.27.0-rc.1"), false},
		{"major, prerelease of upper bound", major("9.26.0"), ver("10.0.0-beta.1"), false},
		// SPM does not special-case 0.x: upToNextMajor 0.3.11 is < 1.0.0.
		{"major 0.x, next minor", major("0.3.11"), ver("0.9.0"), true},
		{"major 0.x, 1.0.0", major("0.3.11"), ver("1.0.0"), false},
		{"major 0.x, below", major("0.3.11"), ver("0.3.10"), false},

		// upToNextMinor 0.3.11 is >= 0.3.11 < 0.4.0.
		{"minor, lower bound", minor("0.3.11"), ver("0.3.11"), true},
		{"minor, inside", minor("0.3.11"), ver("0.3.99"), true},
		{"minor, upper bound excluded", minor("0.3.11"), ver("0.4.0"), false},
		{"minor, below", minor("0.3.11"), ver("0.3.10"), false},
		{"minor, next major", minor("1.2.3"), ver("2.2.3"), false},
		{"minor 1.x, inside", minor("1.2.3"), ver("1.2.9"), true},
		{"minor 1.x, next minor", minor("1.2.3"), ver("1.3.0"), false},

		// versionRange is [min, max).
		{"range, lower bound", rng("5.85.0", "6.0.0"), ver("5.85.0"), true},
		{"range, inside", rng("5.85.0", "6.0.0"), ver("5.99.0"), true},
		{"range, upper bound excluded", rng("5.85.0", "6.0.0"), ver("6.0.0"), false},
		{"range, below", rng("5.85.0", "6.0.0"), ver("5.84.9"), false},
		{"range, narrow", rng("1.2.3", "1.2.5"), ver("1.2.4"), true},
		{"range, narrow excluded", rng("1.2.3", "1.2.5"), ver("1.2.5"), false},
		// A prerelease is admitted only when a bound names one (SPM's rule).
		{"range, prerelease lower bound admits later prerelease", rng("2.0.0-beta.1", "3.0.0"), ver("2.0.0-beta.2"), true},
		{"range, prerelease lower bound rejects earlier prerelease", rng("2.0.0-beta.1", "3.0.0"), ver("2.0.0-alpha.9"), false},
		{"range, prerelease lower bound admits release", rng("2.0.0-beta.1", "3.0.0"), ver("2.0.0"), true},
		{"range, prerelease upper bound admits prerelease below it", rng("1.0.0", "2.0.0-beta.1"), ver("2.0.0-alpha"), true},
		// A prerelease bound admits prereleases, but never one of a release upper
		// bound: 3.0.0-alpha sorts below 3.0.0 and is still not "before 3.0.0".
		{"range, prerelease lower bound still rejects prerelease of release upper bound", rng("2.0.0-beta.1", "3.0.0"), ver("3.0.0-alpha"), false},
		{"major from a prerelease, prerelease of next major", major("2.0.0-beta.1"), ver("3.0.0-rc.1"), false},
		{"major from a prerelease, later prerelease inside", major("2.0.0-beta.1"), ver("2.1.0-rc.1"), true},

		// Branch and revision requirements.
		{"branch, same", PackageRequirement{Kind: ReqBranch, Branch: "main"}, pinState{Branch: "main", Revision: "abc"}, true},
		{"branch, other", PackageRequirement{Kind: ReqBranch, Branch: "main"}, pinState{Branch: "develop", Revision: "abc"}, false},
		{"branch, pinned to a version instead", PackageRequirement{Kind: ReqBranch, Branch: "main"}, ver("1.0.0"), false},
		{"revision, same", PackageRequirement{Kind: ReqRevision, Revision: "61e8cb02434b26fb34c58126d883e5663cfde238"}, ver("9.28.0"), true},
		{"revision, other", PackageRequirement{Kind: ReqRevision, Revision: "1b65c3baa951ad5ef4ab46f3b96a6e1dcc5cf015"}, ver("9.28.0"), false},
		// A version requirement is not met by a branch pin, whatever it resolved to.
		{"version requirement, branch pin", major("1.0.0"), pinState{Branch: "main", Revision: "abc"}, false},
		{"exact requirement, branch pin", exact("1.0.0"), pinState{Branch: "main", Revision: "abc"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, checked := c.req.satisfiedBy(c.pin)
			if !checked {
				t.Fatalf("satisfiedBy(%+v) was not checked; want a verdict", c.pin)
			}
			if got != c.want {
				t.Errorf("%s %q/%q satisfiedBy %+v = %v, want %v", c.req.Kind, c.req.Version, c.req.Max, c.pin, got, c.want)
			}
		})
	}
}

// What the comparator cannot read, it must say it did not check — never pass
// and never fail.
func TestRequirementNotCheckedWhenUnreadable(t *testing.T) {
	cases := []struct {
		name string
		req  PackageRequirement
		pin  pinState
	}{
		{"requirement version not semver", PackageRequirement{Kind: ReqUpToNextMajor, Version: "latest"}, pinState{Version: "1.0.0"}},
		{"two-component requirement", PackageRequirement{Kind: ReqExact, Version: "5.0"}, pinState{Version: "5.0.0"}},
		{"pinned version not semver", PackageRequirement{Kind: ReqUpToNextMajor, Version: "1.0.0"}, pinState{Version: "v1"}},
		{"range without an upper bound", PackageRequirement{Kind: ReqRange, Version: "1.0.0"}, pinState{Version: "1.0.0"}},
		{"unknown kind", PackageRequirement{Kind: "somethingNew", Version: "1.0.0"}, pinState{Version: "1.0.0"}},
		{"pin with no state at all", PackageRequirement{Kind: ReqExact, Version: "1.0.0"}, pinState{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if ok, checked := c.req.satisfiedBy(c.pin); checked {
				t.Errorf("satisfiedBy = (%v, checked); want not checked", ok)
			}
		})
	}
}

// Semver precedence, from semver.org §11, plus the case a lexical comparison
// gets wrong.
func TestSemverPrecedence(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
		"1.0.1", "1.9.0", "1.10.0", "2.0.0", "10.0.0",
	}
	for i := 0; i+1 < len(ordered); i++ {
		a, okA := parseSemver(ordered[i])
		b, okB := parseSemver(ordered[i+1])
		if !okA || !okB {
			t.Fatalf("parseSemver(%q)=%v parseSemver(%q)=%v", ordered[i], okA, ordered[i+1], okB)
		}
		if compareSemver(a, b) >= 0 || compareSemver(b, a) <= 0 {
			t.Errorf("want %s < %s", ordered[i], ordered[i+1])
		}
	}
	a, _ := parseSemver("1.2.3+build.1")
	b, _ := parseSemver("1.2.3+build.2")
	if compareSemver(a, b) != 0 {
		t.Errorf("build metadata must not affect precedence")
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "v1.2.3", "01.2.3", "1.2.x", "1.2.3-", "1.2.3-a..b", "-1.2.3"} {
		if _, ok := parseSemver(bad); ok {
			t.Errorf("parseSemver(%q) accepted; want rejected", bad)
		}
	}
}

// Every spelling of one repository must match its pin, and a different
// repository must not — including one whose last path segment is the same
// (the supply-chain swap: evil/sentry-cocoa is not getsentry/sentry-cocoa).
func TestNormalizePackageURL(t *testing.T) {
	same := []string{
		"https://github.com/getsentry/sentry-cocoa",
		"https://github.com/getsentry/sentry-cocoa.git",
		"https://github.com/getsentry/sentry-cocoa/",
		"https://github.com/getsentry/sentry-cocoa.git/",
		"HTTPS://GitHub.com/GetSentry/Sentry-Cocoa",
		"http://github.com/getsentry/sentry-cocoa",
		"git@github.com:getsentry/sentry-cocoa.git",
		"git@github.com:getsentry/sentry-cocoa",
		"ssh://git@github.com/getsentry/sentry-cocoa.git",
		"https://someone@github.com/getsentry/sentry-cocoa",
		"  https://github.com/getsentry/sentry-cocoa  ",
	}
	want := normalizePackageURL(same[0])
	if want == "" {
		t.Fatal("normalizePackageURL returned empty")
	}
	for _, u := range same {
		if got := normalizePackageURL(u); got != want {
			t.Errorf("normalizePackageURL(%q) = %q, want %q", u, got, want)
		}
	}
	different := []string{
		"https://github.com/evil/sentry-cocoa",
		"https://github.com/getsentry/sentry-cocoa-extra",
		"https://gitlab.com/getsentry/sentry-cocoa",
		"https://github.com/getsentry/sentry-cocoa/tree/main",
		"https://github.com/getsentry/sentry",
	}
	for _, u := range different {
		if got := normalizePackageURL(u); got == want {
			t.Errorf("normalizePackageURL(%q) = %q, equal to getsentry/sentry-cocoa's; want different", u, got)
		}
	}
}

const resolvedV1 = `{
  "object": {
    "pins": [
      {
        "package": "Sentry",
        "repositoryURL": "https://github.com/getsentry/sentry-cocoa.git",
        "state": {
          "branch": null,
          "revision": "61e8cb02434b26fb34c58126d883e5663cfde238",
          "version": "9.28.0"
        }
      },
      {
        "package": "Nuke",
        "repositoryURL": "https:\/\/github.com\/kean\/Nuke",
        "state": {
          "branch": "main",
          "revision": "0123456789abcdef0123456789abcdef01234567",
          "version": null
        }
      }
    ]
  },
  "version": 1
}
`

const resolvedV2 = `{
  "pins" : [
    {
      "identity" : "nuke",
      "kind" : "remoteSourceControl",
      "location" : "https://github.com/kean/Nuke",
      "state" : {
        "branch" : "main",
        "revision" : "0123456789abcdef0123456789abcdef01234567"
      }
    },
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
  "version" : 2
}
`

// momfriend's, verbatim.
const resolvedMomfriend = `{
  "originHash" : "33a2d2f2b66914b638d2a720148e9c1957c892efa4f25c445bdc227dd9a471bf",
  "pins" : [
    {
      "identity" : "aptabase-swift",
      "kind" : "remoteSourceControl",
      "location" : "https://github.com/aptabase/aptabase-swift",
      "state" : {
        "revision" : "cfd67fac2a228d448d9d2ac92ffc71589cc3ef00",
        "version" : "0.3.11"
      }
    },
    {
      "identity" : "purchases-ios-spm",
      "kind" : "remoteSourceControl",
      "location" : "https://github.com/RevenueCat/purchases-ios-spm.git",
      "state" : {
        "revision" : "1b65c3baa951ad5ef4ab46f3b96a6e1dcc5cf015",
        "version" : "5.89.0"
      }
    },
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

// lineOf is the 1-based line of the first line of body containing sub.
func lineOf(t *testing.T, body, sub string, nth int) int {
	t.Helper()
	seen := 0
	for i, l := range strings.Split(body, "\n") {
		if strings.Contains(l, sub) {
			seen++
			if seen == nth {
				return i + 1
			}
		}
	}
	t.Fatalf("%q (occurrence %d) not in body", sub, nth)
	return 0
}

func TestParseResolvedFormats(t *testing.T) {
	type want struct {
		identity, location string
		state              pinState
		line               int
	}
	sentryState := pinState{Version: "9.28.0", Revision: "61e8cb02434b26fb34c58126d883e5663cfde238"}
	nukeState := pinState{Branch: "main", Revision: "0123456789abcdef0123456789abcdef01234567"}
	cases := []struct {
		name string
		body string
		want []want
	}{
		{"v1", resolvedV1, []want{
			{"sentry-cocoa", "https://github.com/getsentry/sentry-cocoa.git", sentryState, lineOf(t, resolvedV1, `"version": "9.28.0"`, 1)},
			// v1 escapes slashes; the line still has to be found.
			{"nuke", "https://github.com/kean/Nuke", nukeState, lineOf(t, resolvedV1, `"branch": "main"`, 1)},
		}},
		{"v2", resolvedV2, []want{
			{"nuke", "https://github.com/kean/Nuke", nukeState, lineOf(t, resolvedV2, `"branch" : "main"`, 1)},
			{"sentry-cocoa", "https://github.com/getsentry/sentry-cocoa", sentryState, lineOf(t, resolvedV2, `"version" : "9.28.0"`, 1)},
		}},
		{"v3", resolvedMomfriend, []want{
			{"aptabase-swift", "https://github.com/aptabase/aptabase-swift", pinState{Version: "0.3.11", Revision: "cfd67fac2a228d448d9d2ac92ffc71589cc3ef00"}, lineOf(t, resolvedMomfriend, `"version" : "0.3.11"`, 1)},
			{"purchases-ios-spm", "https://github.com/RevenueCat/purchases-ios-spm.git", pinState{Version: "5.89.0", Revision: "1b65c3baa951ad5ef4ab46f3b96a6e1dcc5cf015"}, lineOf(t, resolvedMomfriend, `"version" : "5.89.0"`, 1)},
			{"sentry-cocoa", "https://github.com/getsentry/sentry-cocoa", sentryState, lineOf(t, resolvedMomfriend, `"version" : "9.28.0"`, 1)},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pins, err := parseResolved([]byte(c.body))
			if err != nil {
				t.Fatal(err)
			}
			if len(pins) != len(c.want) {
				t.Fatalf("got %d pins, want %d: %+v", len(pins), len(c.want), pins)
			}
			for i, w := range c.want {
				p := pins[i]
				if p.Identity != w.identity || p.Location != w.location || p.State != w.state || p.Line != w.line {
					t.Errorf("pin %d = %+v, want %+v", i, p, w)
				}
			}
		})
	}
	for _, bad := range []string{"", "{", `{"version": 4, "pins": []}`, `{"pins": []}`} {
		if _, err := parseResolved([]byte(bad)); err == nil {
			t.Errorf("parseResolved(%q) = nil error; want one", bad)
		}
	}
}

// A trimmed pbxproj in Xcode's own spelling: every requirement kind, a local
// package, quoted and bare values, and comments that mention package syntax.
const pbxprojAllKinds = `// !$*UTF8*$!
{
	archiveVersion = 1;
	classes = {
	};
	objectVersion = 77;
	objects = {

/* Begin PBXBuildFile section */
		F34E09393030F9F500A9D293 /* MomFriendCore in Frameworks */ = {isa = PBXBuildFile; productRef = F34E09383030F9F500A9D293 /* MomFriendCore */; };
/* End PBXBuildFile section */

/* Begin XCLocalSwiftPackageReference section */
		F34E09373030F66F00A9D293 /* XCLocalSwiftPackageReference "MomFriendCore" */ = {
			isa = XCLocalSwiftPackageReference;
			relativePath = MomFriendCore;
		};
		F34E09373030F66F00A9D294 /* XCLocalSwiftPackageReference "Shared Kit" */ = {
			isa = XCLocalSwiftPackageReference;
			relativePath = "../Shared Kit";
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
				kind = upToNextMinorVersion;
				minimumVersion = 0.3.11;
			};
		};
		F3B9157B303A6D8600C1ADE7 /* XCRemoteSwiftPackageReference "purchases-ios-spm" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://github.com/RevenueCat/purchases-ios-spm.git";
			requirement = {
				kind = versionRange;
				maximumVersion = 6.0.0;
				minimumVersion = 5.85.0;
			};
		};
		A00000000000000000000001 /* XCRemoteSwiftPackageReference "swift-dependencies" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://github.com/pointfreeco/swift-dependencies";
			requirement = {
				kind = upToNextMajorVersion;
				minimumVersion = "1.9.0";
			};
		};
		A00000000000000000000002 /* XCRemoteSwiftPackageReference "Nuke" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://github.com/kean/Nuke";
			requirement = {
				branch = main;
				kind = branch;
			};
		};
		A00000000000000000000003 /* XCRemoteSwiftPackageReference "GRDB" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "git@github.com:groue/GRDB.swift.git";
			requirement = {
				kind = revision;
				revision = 0123456789abcdef0123456789abcdef01234567;
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

func TestParsePbxprojPackages(t *testing.T) {
	remote, local, err := parsePbxprojPackages(pbxprojAllKinds)
	if err != nil {
		t.Fatal(err)
	}
	reqLine := func(nth int) int { return lineOf(t, pbxprojAllKinds, "requirement = {", nth) }
	want := []PackageRequirement{
		{URL: "https://github.com/getsentry/sentry-cocoa", Kind: ReqExact, Version: "9.26.0", Line: reqLine(1)},
		{URL: "https://github.com/aptabase/aptabase-swift", Kind: ReqUpToNextMinor, Version: "0.3.11", Line: reqLine(2)},
		{URL: "https://github.com/RevenueCat/purchases-ios-spm.git", Kind: ReqRange, Version: "5.85.0", Max: "6.0.0", Line: reqLine(3)},
		{URL: "https://github.com/pointfreeco/swift-dependencies", Kind: ReqUpToNextMajor, Version: "1.9.0", Line: reqLine(4)},
		{URL: "https://github.com/kean/Nuke", Kind: ReqBranch, Branch: "main", Line: reqLine(5)},
		{URL: "git@github.com:groue/GRDB.swift.git", Kind: ReqRevision, Revision: "0123456789abcdef0123456789abcdef01234567", Line: reqLine(6)},
	}
	if len(remote) != len(want) {
		t.Fatalf("got %d requirements, want %d: %+v", len(remote), len(want), remote)
	}
	for i := range want {
		if remote[i] != want[i] {
			t.Errorf("requirement %d = %+v\nwant %+v", i, remote[i], want[i])
		}
	}
	if strings.Join(local, "|") != "MomFriendCore|../Shared Kit" {
		t.Errorf("local packages = %q", local)
	}
	if _, _, err := parsePbxprojPackages("{ objects = { A = { isa = XCRemoteSwiftPackageReference; "); err == nil {
		t.Error("truncated pbxproj parsed without error")
	}
}

const xcodegenPackages = `name: Rail

options:
  bundleIdPrefix: com.example

packages:
  # a comment mentioning from: 1.0.0
  RailCore:
    path: RailCore
  RevenueCat:
    url: https://github.com/RevenueCat/purchases-ios-spm
    from: 5.0.0
  Aptabase:
    url: https://github.com/aptabase/aptabase-swift
    minorVersion: "0.3.11"
  Sentry:
    url: https://github.com/getsentry/sentry-cocoa
    exactVersion: 9.26.0
  Legacy:
    url: https://github.com/example/legacy
    version: 1.2.3
  Major:
    url: https://github.com/example/major
    majorVersion: 2.0.0
  Ranged:
    url: https://github.com/example/ranged
    minVersion: 1.0.0
    maxVersion: 1.5.0
  Branchy:
    github: example/branchy
    branch: develop
  Pinned:
    url: https://github.com/example/pinned
    revision: abcdef0
  Floaty:
    url: https://github.com/example/floaty
    from: 5.0
  Unconstrained:
    url: https://github.com/example/unconstrained

targets:
  Rail:
    type: application
`

func TestParseXcodeGenPackages(t *testing.T) {
	spec, err := parseXcodeGenPackages([]byte(xcodegenPackages))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "Rail" {
		t.Errorf("name = %q", spec.Name)
	}
	at := func(key string) int { return lineOf(t, xcodegenPackages, "  "+key+":", 1) }
	want := []PackageRequirement{
		{URL: "https://github.com/RevenueCat/purchases-ios-spm", Kind: ReqUpToNextMajor, Version: "5.0.0", Line: at("RevenueCat")},
		{URL: "https://github.com/aptabase/aptabase-swift", Kind: ReqUpToNextMinor, Version: "0.3.11", Line: at("Aptabase")},
		{URL: "https://github.com/getsentry/sentry-cocoa", Kind: ReqExact, Version: "9.26.0", Line: at("Sentry")},
		{URL: "https://github.com/example/legacy", Kind: ReqExact, Version: "1.2.3", Line: at("Legacy")},
		{URL: "https://github.com/example/major", Kind: ReqUpToNextMajor, Version: "2.0.0", Line: at("Major")},
		{URL: "https://github.com/example/ranged", Kind: ReqRange, Version: "1.0.0", Max: "1.5.0", Line: at("Ranged")},
		{URL: "https://github.com/example/branchy", Kind: ReqBranch, Branch: "develop", Line: at("Branchy")},
		{URL: "https://github.com/example/pinned", Kind: ReqRevision, Revision: "abcdef0", Line: at("Pinned")},
		// `from: 5.0` is a YAML float; it reaches the comparator as "5.0", which
		// is not a version, so it is reported not-checked there rather than here.
		{URL: "https://github.com/example/floaty", Kind: ReqUpToNextMajor, Version: "5.0", Line: at("Floaty")},
	}
	if len(spec.Remote) != len(want) {
		t.Fatalf("got %d requirements, want %d: %+v", len(spec.Remote), len(want), spec.Remote)
	}
	for i := range want {
		if spec.Remote[i] != want[i] {
			t.Errorf("requirement %d = %+v\nwant %+v", i, spec.Remote[i], want[i])
		}
	}
	if strings.Join(spec.Local, "|") != "RailCore" {
		t.Errorf("local = %q", spec.Local)
	}
	if len(spec.Unchecked) != 1 || spec.Unchecked[0].Line != at("Unconstrained") {
		t.Errorf("unchecked = %+v; want Unconstrained at line %d", spec.Unchecked, at("Unconstrained"))
	}
}

const packageSwift = `// swift-tools-version: 5.9
import PackageDescription

// .package(url: "https://github.com/commented/out", exact: "0.0.1"),
/* .package(url: "https://github.com/block/commented", from: "0.0.1"),
   /* nested */ still a comment .package(url: "https://github.com/nested/comment", from: "1.0.0") */
let version = "1.0.0"
let package = Package(
    name: "WindsockKit",
    dependencies: [
        .package(url: "https://github.com/groue/GRDB.swift", .upToNextMajor(from: "7.11.0")),
        .package(url: "https://github.com/pointfreeco/swift-dependencies", from: "1.9.0"),
        .package(url: "https://github.com/example/minor", .upToNextMinor(from: "0.3.11")),
        .package(url: "https://github.com/example/exact", exact: "2.0.0"),
        .package(url: "https://github.com/example/exact-old", .exact("2.1.0")),
        .package(url: "https://github.com/example/half-open", "1.0.0"..<"1.5.0"),
        .package(url: "https://github.com/example/closed", "1.0.0"..."1.4.9"),
        .package(url: "https://github.com/example/branch", branch: "main"),
        .package(url: "https://github.com/example/branch-old", .branch("develop")),
        .package(url: "https://github.com/example/revision", revision: "abcdef0"),
        .package(url: "https://github.com/example/revision-old", .revision("1234567")),
        .package(
            name: "Named",
            url: "https://github.com/example/named.git",
            from: "3.0.0",
        ),
        .package(path: "../RailCore"),
        .package(name: "Local", path: "Packages/Local"),
        .package(url: "https://github.com/example/variable", from: version),
        .package(url: "https://github.com/example/\(version)", from: "1.0.0"),
        .package(id: "scope.registry", from: "1.0.0"),
        .package(url: "https://github.com/example/traits", from: "1.0.0", traits: ["A"]),
    ],
    targets: [.target(name: "WindsockKit", dependencies: [.product(name: "GRDB", package: "GRDB.swift")])]
)
`

func TestParsePackageSwift(t *testing.T) {
	m := parsePackageSwift(packageSwift)
	at := func(sub string) int { return lineOf(t, packageSwift, sub, 1) }
	want := []PackageRequirement{
		{URL: "https://github.com/groue/GRDB.swift", Kind: ReqUpToNextMajor, Version: "7.11.0", Line: at("GRDB.swift\", .upTo")},
		{URL: "https://github.com/pointfreeco/swift-dependencies", Kind: ReqUpToNextMajor, Version: "1.9.0", Line: at("swift-dependencies")},
		{URL: "https://github.com/example/minor", Kind: ReqUpToNextMinor, Version: "0.3.11", Line: at("example/minor")},
		{URL: "https://github.com/example/exact", Kind: ReqExact, Version: "2.0.0", Line: at("example/exact\"")},
		{URL: "https://github.com/example/exact-old", Kind: ReqExact, Version: "2.1.0", Line: at("example/exact-old")},
		{URL: "https://github.com/example/half-open", Kind: ReqRange, Version: "1.0.0", Max: "1.5.0", Line: at("half-open")},
		// A closed range ends one patch above its upper bound, as SwiftPM does it.
		{URL: "https://github.com/example/closed", Kind: ReqRange, Version: "1.0.0", Max: "1.4.10", Line: at("example/closed")},
		{URL: "https://github.com/example/branch", Kind: ReqBranch, Branch: "main", Line: at("example/branch\"")},
		{URL: "https://github.com/example/branch-old", Kind: ReqBranch, Branch: "develop", Line: at("branch-old")},
		{URL: "https://github.com/example/revision", Kind: ReqRevision, Revision: "abcdef0", Line: at("example/revision\"")},
		{URL: "https://github.com/example/revision-old", Kind: ReqRevision, Revision: "1234567", Line: at("revision-old")},
		{URL: "https://github.com/example/named.git", Kind: ReqUpToNextMajor, Version: "3.0.0", Line: lineOf(t, packageSwift, ".package(", 15)},
	}
	if len(m.Remote) != len(want) {
		t.Fatalf("got %d requirements, want %d:\n%+v", len(m.Remote), len(want), m.Remote)
	}
	for i := range want {
		if m.Remote[i] != want[i] {
			t.Errorf("requirement %d = %+v\nwant %+v", i, m.Remote[i], want[i])
		}
	}
	if strings.Join(m.Local, "|") != "../RailCore|Packages/Local" {
		t.Errorf("local = %q", m.Local)
	}
	var lines []int
	for _, u := range m.Unchecked {
		lines = append(lines, u.Line)
	}
	wantUnchecked := []int{at("example/variable"), at(`example/\(`), at("scope.registry"), at("example/traits")}
	if len(lines) != len(wantUnchecked) {
		t.Fatalf("unchecked lines = %v, want %v (%+v)", lines, wantUnchecked, m.Unchecked)
	}
	for i := range lines {
		if lines[i] != wantUnchecked[i] {
			t.Errorf("unchecked lines = %v, want %v", lines, wantUnchecked)
			break
		}
	}
}
