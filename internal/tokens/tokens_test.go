package tokens

import (
	"testing"

	"github.com/patrickserrano/harness/internal/config"
)

func TestSubstitute(t *testing.T) {
	p := config.Project{ProjectName: "Rail", Scheme: "Rail", BundleID: "com.me.rail", AscAppID: "999"}
	in := "scheme: {{SCHEME}}\nid: {{BUNDLE_ID}}\nasc: {{ASC_APP_ID}}\nname: {{PROJECT_NAME}}\nga: ${{ github.ref }}\n"
	out, missing := Substitute(in, p)
	if len(missing) != 0 {
		t.Fatalf("unexpected missing: %v", missing)
	}
	want := "scheme: Rail\nid: com.me.rail\nasc: 999\nname: Rail\nga: ${{ github.ref }}\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestSubstituteReportsMissing(t *testing.T) {
	p := config.Project{ProjectName: "Rail"} // scheme blank
	out, missing := Substitute("a {{SCHEME}} b {{PROJECT_NAME}}", p)
	if len(missing) != 1 || missing[0] != "{{SCHEME}}" {
		t.Fatalf("missing = %v, want [{{SCHEME}}]", missing)
	}
	if out != "a {{SCHEME}} b Rail" {
		t.Fatalf("out = %q", out)
	}
}
