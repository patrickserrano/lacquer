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
