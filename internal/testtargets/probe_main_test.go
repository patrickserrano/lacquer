package testtargets

import (
	"os"
	"testing"
)

// A harness for eyeballing the parser against real projects during development:
//
//	go test ./internal/testtargets/ -run TestParseRealProject -v -args <pbxproj>
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
		kind := "unit"
		if x.UI {
			kind = "UI"
		}
		t.Logf("  %-42s %s", x.Name, kind)
	}
}
