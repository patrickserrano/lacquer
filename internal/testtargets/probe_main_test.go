package testtargets

import (
	"os"
	"testing"
)

// A harness for eyeballing the parser against real projects during development:
//
//	PBXPROJ=<path/to/project.pbxproj> go test ./internal/testtargets/ -run TestParseRealProject -v
func TestParseRealProject(t *testing.T) {
	path := os.Getenv("PBXPROJ")
	if path == "" {
		t.Skip("no pbxproj given")
	}
	ts, _, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ts {
		if x.Unread != "" {
			t.Logf("  UNREAD %s", x.Unread)
			continue
		}
		kind := "unit"
		if x.UI {
			kind = "UI"
		}
		where := "native"
		if x.Package != "" {
			where = "package " + x.Package
		}
		t.Logf("  %-42s %-4s %s", x.Name, kind, where)
	}
}
