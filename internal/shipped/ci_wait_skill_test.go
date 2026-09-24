package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The verification instruction reaches every synced project. A status snapshot
// must not substitute for waiting, and an empty check list must never be green.
func TestCIFixSkillWaitContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(root(t), "core/skills/github-ci-fix/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{
		"**Verify:** `lacquer wait pr <N>`",
		"| `0` | passed |", "| `1` | failed |", "| `2` | timed out |",
		"| `3` | no checks | Never green", "| `4` | wait failed |",
		"`gh pr checks --watch` exits 0 even when checks fail",
		"Older binaries without `lacquer wait`:",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("CI fix skill missing %q", want)
		}
	}
}
