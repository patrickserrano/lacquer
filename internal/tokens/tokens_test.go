package tokens

import (
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func TestPrefix(t *testing.T) {
	if Prefix(".") != "" {
		t.Errorf("Prefix(\".\") = %q, want empty", Prefix("."))
	}
	if Prefix("ios") != "ios/" {
		t.Errorf("Prefix(\"ios\") = %q, want ios/", Prefix("ios"))
	}
	if Prefix("apps/ios-app") != "apps/ios-app/" {
		t.Errorf("Prefix nested = %q", Prefix("apps/ios-app"))
	}
}

// ToRoot is the inverse of Prefix, and the root case is the one that matters:
// biome aborts against a vcs.root pointing outside the repository exactly as it
// aborts against a missing ignore file, so "" or ".." for a root-layout
// component would trade one broken config for another.
func TestToRoot(t *testing.T) {
	for _, tc := range []struct{ prefix, want string }{
		{"", "."},
		{".", "."},
		{"ios/", ".."},
		{"apps/ios-app/", "../.."},
		{"a/b/c/", "../../.."},
		// Prefix always ends in "/", but nothing in the type system says so.
		{"ios", ".."},
	} {
		if got := ToRoot(tc.prefix); got != tc.want {
			t.Errorf("ToRoot(%q) = %q, want %q", tc.prefix, got, tc.want)
		}
	}
}

// Prefix and ToRoot must describe the same layout: joining them has to land back
// where it started. Two independent derivations of one fact drift; this is the
// check that they have not.
func TestToRootInvertsPrefix(t *testing.T) {
	for _, path := range []string{".", "ios", "admin", "apps/ios-app", "a/b/c"} {
		prefix := Prefix(path)
		toRoot := ToRoot(prefix)
		depth := 0
		if toRoot != "." {
			depth = len(strings.Split(toRoot, "/"))
		}
		want := 0
		if path != "." {
			want = len(strings.Split(path, "/"))
		}
		if depth != want {
			t.Errorf("path %q: Prefix=%q descends %d level(s) but ToRoot=%q climbs %d", path, prefix, want, toRoot, depth)
		}
		// Climbing the right NUMBER of levels is not the same as climbing. Every
		// segment must actually be "..".
		if toRoot != "." {
			for _, seg := range strings.Split(toRoot, "/") {
				if seg != ".." {
					t.Errorf("path %q: ToRoot=%q contains the segment %q, which does not climb", path, toRoot, seg)
				}
			}
		}
	}
}

// A root-layout component must still get a value: ToRoot never returns "", so a
// blank one can only mean the derivation broke — and Substitute is registered to
// fail closed on it rather than render `"root": ""`, which sends biome's lookup
// straight back to the folder it could not find an ignore file in.
func TestComponentToRootIsRequired(t *testing.T) {
	vals := Values(&config.Config{Project: config.Project{ProjectName: "Acme", Scheme: "Acme", BundleID: "com.me.acme", AscAppID: "9"}}, "")
	out, missing := Substitute(`"root": "{{COMPONENT_TO_ROOT}}"`, vals)
	if len(missing) != 0 {
		t.Fatalf("a root-layout component must render, got missing: %v", missing)
	}
	if out != `"root": "."` {
		t.Fatalf("got %q, want %q", out, `"root": "."`)
	}

	vals[ComponentToRoot] = ""
	if _, missing := Substitute(`"root": "{{COMPONENT_TO_ROOT}}"`, vals); len(missing) == 0 {
		t.Error("an empty COMPONENT_TO_ROOT was substituted silently; it must be reported missing so sync fails closed")
	}
}

func TestSubstituteValues(t *testing.T) {
	vals := Values(&config.Config{Project: config.Project{ProjectName: "Acme", Scheme: "Acme", BundleID: "com.me.acme", AscAppID: "9"}}, "ios/")
	in := "p: {{COMPONENT_PREFIX}}{{PROJECT_NAME}}.xcodeproj\nf: '{{COMPONENT_PREFIX}}**'\nga: ${{ github.ref }}\n"
	out, missing := Substitute(in, vals)
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}
	want := "p: ios/Acme.xcodeproj\nf: 'ios/**'\nga: ${{ github.ref }}\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestSubstituteEmptyPrefixIsValid(t *testing.T) {
	vals := Values(&config.Config{Project: config.Project{ProjectName: "Acme", Scheme: "Acme", BundleID: "b", AscAppID: "9"}}, "")
	out, missing := Substitute("f: '{{COMPONENT_PREFIX}}**'\nd: {{COMPONENT_PREFIX}}DerivedData\n", vals)
	if len(missing) != 0 {
		t.Fatalf("empty prefix must not be 'missing': %v", missing)
	}
	if out != "f: '**'\nd: DerivedData\n" {
		t.Fatalf("got: %q", out)
	}
}

func TestSubstituteReportsMissingProjectValue(t *testing.T) {
	vals := Values(&config.Config{Project: config.Project{ProjectName: "Acme"}}, "ios/") // scheme blank
	_, missing := Substitute("{{SCHEME}} {{PROJECT_NAME}}", vals)
	if len(missing) != 1 || missing[0] != "{{SCHEME}}" {
		t.Fatalf("missing = %v", missing)
	}
}

func TestSubstituteXcodeproj(t *testing.T) {
	vals := Values(&config.Config{Project: config.Project{ProjectName: "Q", Scheme: "Q", BundleID: "b", AscAppID: "9", Xcodeproj: "ios/Queueify/Queueify.xcodeproj"}}, "ios/")
	out, missing := Substitute("-project {{XCODEPROJ}}\nlint: {{COMPONENT_PREFIX}}.swiftlint.yml", vals)
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}
	if out != "-project ios/Queueify/Queueify.xcodeproj\nlint: ios/.swiftlint.yml" {
		t.Fatalf("out: %q", out)
	}
}

func TestSubstituteReportsMissingXcodeproj(t *testing.T) {
	vals := Values(&config.Config{Project: config.Project{ProjectName: "Q", Scheme: "Q", BundleID: "b", AscAppID: "9"}}, "ios/")
	_, missing := Substitute("-project {{XCODEPROJ}}", vals)
	if len(missing) != 1 || missing[0] != "{{XCODEPROJ}}" {
		t.Fatalf("missing = %v", missing)
	}
}

func TestSubstituteSwiftVersion(t *testing.T) {
	vals := Values(&config.Config{Project: config.Project{ProjectName: "A", Scheme: "A", BundleID: "b", AscAppID: "9", Xcodeproj: "A.xcodeproj", SwiftVersion: "6.2"}}, "")
	out, missing := Substitute("--swiftversion {{SWIFT_VERSION}}", vals)
	if len(missing) != 0 || out != "--swiftversion 6.2" {
		t.Fatalf("out=%q missing=%v", out, missing)
	}
	// blank swift_version with the token present must fail closed
	v2 := Values(&config.Config{Project: config.Project{ProjectName: "A", Scheme: "A", BundleID: "b", AscAppID: "9", Xcodeproj: "A.xcodeproj"}}, "")
	if _, m := Substitute("--swiftversion {{SWIFT_VERSION}}", v2); len(m) != 1 || m[0] != "{{SWIFT_VERSION}}" {
		t.Fatalf("expected {{SWIFT_VERSION}} missing, got %v", m)
	}
}

func TestSubstituteGithubOrg(t *testing.T) {
	vals := Values(&config.Config{Project: config.Project{GithubOrg: "AcmeOrg"}}, "")
	out, missing := Substitute("gh secret set X --org {{GITHUB_ORG}}", vals)
	if len(missing) != 0 || out != "gh secret set X --org AcmeOrg" {
		t.Fatalf("out=%q missing=%v", out, missing)
	}
	// github_org is NOT required: a blank value renders empty, never fails closed.
	out2, m2 := Substitute("--org {{GITHUB_ORG}}", Values(&config.Config{Project: config.Project{}}, ""))
	if len(m2) != 0 || out2 != "--org " {
		t.Fatalf("blank org should render empty, got out=%q missing=%v", out2, m2)
	}
}
