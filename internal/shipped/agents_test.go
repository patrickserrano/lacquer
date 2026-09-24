package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
)

func TestRenderedAgentsContract(t *testing.T) {
	for _, profile := range []string{"core", "ios", "web", "supabase", "marketing"} {
		t.Run(profile, func(t *testing.T) {
			dir := t.TempDir()
			manifest, err := os.ReadFile(filepath.Join(root(t), "internal/shipped/testdata/projects/rootapp/.lacquer.toml"))
			if err != nil {
				t.Fatal(err)
			}
			content := strings.Replace(string(manifest), `profiles = ["ios"]`, `profiles = ["`+profile+`"]`, 1)
			if profile == "core" {
				content = strings.Replace(content, `profiles = ["core"]`, `profiles = []`, 1)
			}
			if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			initRepo(t, dir)
			p := &project{root: dir, lacquerRoot: root(t), t: t}
			p.sync()
			body := p.read("AGENTS.md")
			t.Logf("%s rendered AGENTS.md: %d bytes", profile, len(body))
			if len(body) > 10000 {
				t.Errorf("AGENTS.md is %d bytes; maximum 10000", len(body))
			}
			for _, forbidden := range []string{"claude", "skill", "subagent", "/commands"} {
				if strings.Contains(strings.ToLower(body), forbidden) {
					t.Errorf("AGENTS.md contains %q", forbidden)
				}
			}
			required := []string{"Never force-push", "--force-with-lease", "--no-verify", "--admin", "Never rebase a pushed branch", "git merge origin/main", "lacquer wait pr <N>", "never `gh pr checks --watch`", "Never commit or log secrets", "Local checks", ".github/workflows/", "pull request", "Never merge", "Never kill a simulator or process"}
			if profile == "ios" {
				required = append(required, "pbxproj", "xcconfig", "project.yml", "scripts/bump-marketing-version.sh", "MARKETING_VERSION lines only", "Never hand-edit `.pbxproj`", "Never modify `.entitlements` without explicit permission", "through XcodeGen or the operator", ".xcworkspace", ".xib", ".storyboard", ".entitlements", "<worktree>/DerivedData", "cloudKitDatabase: .none", "flowdeck build", "flowdeck test", "pre-commit run --all-files")
			}
			if profile == "web" {
				required = append(required, "biome ci --error-on-warnings", "typecheck", "test:coverage", "build")
			}
			if profile == "supabase" {
				required = append(required, "deno lint", "deno check", "deno test", "supabase test db", "RLS")
			}
			for _, want := range required {
				if !strings.Contains(body, want) {
					t.Errorf("AGENTS.md missing %q", want)
				}
			}
			for _, row := range rowsOf(t, p) {
				if row.Status != audit.OK {
					t.Errorf("fresh sync drift: %+v", row)
				}
			}
			orphans, err := audit.Orphans(p.lacquerRoot, p.root)
			if err != nil || len(orphans) != 0 {
				t.Fatalf("orphans=%v err=%v", orphans, err)
			}
		})
	}
}

func TestNestedAgentsContract(t *testing.T) {
	p := fromFixture(t, "multistack")
	p.sync()
	for _, tc := range []struct{ component, command string }{
		{"ios", "flowdeck build -w ios/Multistack.xcodeproj"},
		{"admin", "biome ci --error-on-warnings"},
		{"server", "supabase test db"},
	} {
		body := p.read(filepath.Join(tc.component, "AGENTS.md"))
		if !strings.Contains(body, tc.command) {
			t.Errorf("%s missing %q", tc.component, tc.command)
		}
		if strings.Contains(body, "{{") {
			t.Errorf("unsubstituted instruction token: %s", body)
		}
		total := len(p.read("AGENTS.md")) + len(body)
		if total > 10000 {
			t.Errorf("%s inherited context %d bytes exceeds 10000", tc.component, total)
		}
		for _, term := range []string{"claude", "skill", "subagent", "/commands"} {
			if strings.Contains(strings.ToLower(body), term) {
				t.Errorf("%s contains %q", tc.component, term)
			}
		}
	}
}
