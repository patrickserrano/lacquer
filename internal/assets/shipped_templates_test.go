package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIOSPreCommitQuotesSpacedProjectValues(t *testing.T) {
	path := filepath.Join("..", "..", "profiles", "ios", "root", ".pre-commit-config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read iOS pre-commit template: %v", err)
	}

	template := string(data)
	for _, quoted := range []string{
		`flowdeck test`,
		`-w "{{XCODEPROJ}}"`,
		`-s "{{SCHEME}}"`,
		`--test-targets "{{PROJECT_NAME}}Tests"`,
	} {
		if !strings.Contains(template, quoted) {
			t.Errorf("iOS pre-commit template must contain %q", quoted)
		}
	}
	if strings.Contains(template, "xcodebuild test") {
		t.Error("iOS pre-commit template must route tests through FlowDeck")
	}
}
