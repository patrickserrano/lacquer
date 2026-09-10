package shipped

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Five shipped agent definitions had a multi-line UNQUOTED description, which
// the agent loader silently refuses — no warning, the agent simply does not
// appear in the registry. `ios-swift-engineer`, `ios-elite-craftsman`,
// `test-automation-engineer`, `ios-debugging-expert` and `ios-ux-designer` were
// therefore absent from every project the lacquer syncs, while the iOS profile's
// rule #10 mandates the first two for ALL Swift work and names the third for
// tests. The rule was not being ignored; it was impossible to follow, and the
// only symptom was "Agent type not found" at the moment someone tried to comply.
//
// THE DISCRIMINATOR IS NOT YAML VALIDITY. Six agent files that load perfectly
// well also fail a strict YAML parse, because their single-line descriptions
// contain `user: "..."`, which trips the same scanner rule. Keying this check on
// "does it parse" would condemn six working files and teach whoever hits it that
// the check lies. What actually breaks the loader is a description whose PLAIN
// (unindicated) scalar spans more than one line. That is what this asserts.
//
// The fix is `description: |-` with the body indented two spaces: a literal
// block scalar preserves the text verbatim and terminates on dedent rather than
// on the first blank line.
func TestShippedAgentsHaveLoadableDescriptions(t *testing.T) {
	r := root(t)

	// Only these begin a new frontmatter key. Anything else at column 0 inside a
	// description — `user:`, `assistant:`, `Context:` — is prose, and mistaking
	// it for a key is precisely how the naive version of this check reported a
	// 27-line description as one line long.
	frontmatterKey := regexp.MustCompile(`^(name|description|tools|model|memory|color):`)

	var files []string
	for _, pat := range []string{
		filepath.Join(r, "core", "agents", "*.md"),
		filepath.Join(r, "profiles", "*", "agents", "*.md"),
	} {
		found, err := filepath.Glob(pat)
		if err != nil {
			t.Fatalf("glob %s: %v", pat, err)
		}
		files = append(files, found...)
	}
	if len(files) == 0 {
		t.Fatal("no agent definitions found — this check would pass by finding nothing")
	}

	for _, f := range files {
		rel, _ := filepath.Rel(r, f)
		b, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		lines := strings.Split(string(b), "\n")
		if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
			t.Errorf("%s: no YAML frontmatter", rel)
			continue
		}

		start, end := -1, -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
		if end < 0 {
			t.Errorf("%s: frontmatter is never closed", rel)
			continue
		}
		for i := 1; i < end; i++ {
			if strings.HasPrefix(lines[i], "description:") {
				start = i
				break
			}
		}
		if start < 0 {
			t.Errorf("%s: no description", rel)
			continue
		}

		stop := end
		for i := start + 1; i < end; i++ {
			if frontmatterKey.MatchString(lines[i]) {
				stop = i
				break
			}
		}

		value := strings.TrimSpace(strings.TrimPrefix(lines[start], "description:"))
		block := strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">")
		if stop-start > 1 && !block {
			t.Errorf("%s: description is a multi-line PLAIN scalar (%d lines). "+
				"The agent loader will silently skip this file and the agent will "+
				"not exist. Use `description: |-` with the body indented two spaces.",
				rel, stop-start)
		}
	}
}
