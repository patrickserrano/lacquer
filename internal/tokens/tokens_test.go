package tokens

import (
	"testing"

	"github.com/patrickserrano/harness/internal/config"
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

func TestSubstituteValues(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Rail", Scheme: "Rail", BundleID: "com.me.rail", AscAppID: "9"}, "ios/")
	in := "p: {{COMPONENT_PREFIX}}{{PROJECT_NAME}}.xcodeproj\nf: '{{COMPONENT_PREFIX}}**'\nga: ${{ github.ref }}\n"
	out, missing := Substitute(in, vals)
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}
	want := "p: ios/Rail.xcodeproj\nf: 'ios/**'\nga: ${{ github.ref }}\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestSubstituteEmptyPrefixIsValid(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Rail", Scheme: "Rail", BundleID: "b", AscAppID: "9"}, "")
	out, missing := Substitute("f: '{{COMPONENT_PREFIX}}**'\nd: {{COMPONENT_PREFIX}}DerivedData\n", vals)
	if len(missing) != 0 {
		t.Fatalf("empty prefix must not be 'missing': %v", missing)
	}
	if out != "f: '**'\nd: DerivedData\n" {
		t.Fatalf("got: %q", out)
	}
}

func TestSubstituteReportsMissingProjectValue(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Rail"}, "ios/") // scheme blank
	_, missing := Substitute("{{SCHEME}} {{PROJECT_NAME}}", vals)
	if len(missing) != 1 || missing[0] != "{{SCHEME}}" {
		t.Fatalf("missing = %v", missing)
	}
}

func TestSubstituteXcodeproj(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Q", Scheme: "Q", BundleID: "b", AscAppID: "9", Xcodeproj: "ios/Queueify/Queueify.xcodeproj"}, "ios/")
	out, missing := Substitute("-project {{XCODEPROJ}}\nlint: {{COMPONENT_PREFIX}}.swiftlint.yml", vals)
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}
	if out != "-project ios/Queueify/Queueify.xcodeproj\nlint: ios/.swiftlint.yml" {
		t.Fatalf("out: %q", out)
	}
}

func TestSubstituteReportsMissingXcodeproj(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Q", Scheme: "Q", BundleID: "b", AscAppID: "9"}, "ios/")
	_, missing := Substitute("-project {{XCODEPROJ}}", vals)
	if len(missing) != 1 || missing[0] != "{{XCODEPROJ}}" {
		t.Fatalf("missing = %v", missing)
	}
}

func TestSubstituteSwiftVersion(t *testing.T) {
	vals := Values(config.Project{ProjectName: "A", Scheme: "A", BundleID: "b", AscAppID: "9", Xcodeproj: "A.xcodeproj", SwiftVersion: "6.2"}, "")
	out, missing := Substitute("--swiftversion {{SWIFT_VERSION}}", vals)
	if len(missing) != 0 || out != "--swiftversion 6.2" {
		t.Fatalf("out=%q missing=%v", out, missing)
	}
	// blank swift_version with the token present must fail closed
	v2 := Values(config.Project{ProjectName: "A", Scheme: "A", BundleID: "b", AscAppID: "9", Xcodeproj: "A.xcodeproj"}, "")
	if _, m := Substitute("--swiftversion {{SWIFT_VERSION}}", v2); len(m) != 1 || m[0] != "{{SWIFT_VERSION}}" {
		t.Fatalf("expected {{SWIFT_VERSION}} missing, got %v", m)
	}
}

func TestSubstituteGithubOrg(t *testing.T) {
	vals := Values(config.Project{GithubOrg: "PixelFoxStudio"}, "")
	out, missing := Substitute("gh secret set X --org {{GITHUB_ORG}}", vals)
	if len(missing) != 0 || out != "gh secret set X --org PixelFoxStudio" {
		t.Fatalf("out=%q missing=%v", out, missing)
	}
	// github_org is NOT required: a blank value renders empty, never fails closed.
	out2, m2 := Substitute("--org {{GITHUB_ORG}}", Values(config.Project{}, ""))
	if len(m2) != 0 || out2 != "--org " {
		t.Fatalf("blank org should render empty, got out=%q missing=%v", out2, m2)
	}
}
