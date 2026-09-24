package shipped

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/region"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// Count what an agent loads: root guidance plus the component's guidance, not
// just the profile in isolation. AGENTS.md has its own contract test.
func TestRenderedClaudeContextBudget(t *testing.T) {
	for _, tc := range []struct{ fixture, component string }{
		{"rootapp", "."}, {"multistack", "ios"},
		{"multistack", "admin"}, {"multistack", "server"}, {"spmpackage", "."},
	} {
		t.Run(tc.fixture+"/"+tc.component, func(t *testing.T) {
			p := fromFixture(t, tc.fixture)
			p.sync()
			paths := []string{"CLAUDE.md"}
			if tc.component != "." {
				paths = append(paths, filepath.Join(tc.component, "CLAUDE.md"))
			}
			lines := 0
			for _, path := range paths {
				body := p.read(path)
				lines += strings.Count(body, "\n")
			}
			t.Logf("loaded CLAUDE.md lines (core + component): %d", lines)
			if lines > 300 {
				t.Errorf("loaded context has %d lines; budget 300", lines)
			}
			core, ok := region.ExtractBody(p.read("CLAUDE.md"), "core")
			if !ok {
				t.Fatal("missing core region")
			}
			for _, want := range []string{"Never force-push", "rebase a pushed branch", "lacquer wait pr <N>", "lacquer ci-round begin <N>", "exit 10", "prove the check can fail", "compaction", "PR number", "branch", "CI-round", "worktree"} {
				if !strings.Contains(core, want) {
					t.Errorf("always-loaded core lost %q", want)
				}
			}
		})
	}
}

// These are the sections migrated by #453. Keep them discoverable from the
// skill entrypoint and present in each tool's rendered skill tree.
func TestMovedClaudeGuidanceIsDelivered(t *testing.T) {
	p := fromFixture(t, "multistack")
	p.sync()
	for _, tc := range []struct {
		skill    string
		headings []string
	}{
		{"engineering-workflow", []string{"## Fundamental Rules", "## Response Style", "## Agent Delegation", "## Context Management", "## Papercuts Log", "## Critical Review Pattern"}},
		{"project-documentation", []string{"## Docs Taxonomy", "## Documentation"}},
		{"working-with-lacquer", []string{"## Local Checks Match CI", "## Warnings as Errors"}},
		{"github-ci-fix", []string{"## CI Hygiene", "## CI round budget"}},
		{"ios-project-development", []string{"## Xcode-Specific Prohibitions", "## Architecture", "## SwiftData + CloudKit", "## Testing", "## Documentation (DocC)", "## Premium / Subscription Gating (if monetized)"}},
		{"ios-release-guide", []string{"## App Store Requirements", "## Release archives go to the archive volume, not the repository", "## App Store Connect accepts a binary before it lists it"}},
		{"ios-ci-configuration", []string{"## Shipping more than one app from one repository", "## CI Runners", "## Editor hooks (.claude/settings.json)", "## Local Checks vs CI"}},
		{"ios-secrets-setup", []string{"## Secrets & Service Keys"}},
		{"ios-build-verification", []string{"## Build data stays in the worktree", "## Build & Test Tooling (flowdeck)", "## Test Timeout Rule"}},
		{"ios-ui-verification", []string{"## Battery & Performance Patterns", "## Swift 6 Concurrency & Default Actor Isolation", "## iOS 26 API Gotchas", "## URL Validation Security Posture", "## Verifying UI in the Simulator", "## Accessibility & Design-Token Contrast (WCAG 1.4.11)"}},
		{"web-development-guide", []string{"## Read the vendored framework docs first", "## Required package.json scripts", "## TypeScript — extend the strict base", "## Code quality — Biome", "## Testing — Vitest", "## Documentation (TSDoc + TypeDoc)", "## Environment & secrets", "## Security", "## Accessibility", "## Local Checks vs CI", "## Git hooks & commits", "## CI", "## Monorepos — Turborepo"}},
		{"supabase-development-guide", []string{"## Tooling — Deno, not npm", "## Migrations (`supabase/migrations/`)", "## Row-Level Security (the security boundary)", "## Edge Functions (`supabase/functions/`, Deno)", "## Documentation", "## Secrets", "## Local Checks vs CI", "## Deploying migrations", "## Git hooks & commits", "## Testing & CI"}},
	} {
		t.Run(tc.skill, func(t *testing.T) {
			var canonical string
			for _, dir := range []string{".claude/skills", ".codex/skills", ".agents/skills"} {
				base := filepath.Join(dir, tc.skill)
				entry := p.read(filepath.Join(base, "SKILL.md"))
				if !strings.Contains(entry, "[references/project-rules.md](references/project-rules.md)") {
					t.Errorf("%s does not route to its moved guidance", base)
				}
				front := strings.SplitN(entry, "---", 3)
				if len(front) != 3 {
					t.Fatalf("%s has no frontmatter", base)
				}
				var meta struct{ Name, Description string }
				if err := yaml.Unmarshal([]byte(front[1]), &meta); err != nil {
					t.Fatal(err)
				}
				if meta.Name != tc.skill || strings.TrimSpace(meta.Description) == "" {
					t.Errorf("%s is not discoverable by name and description", base)
				}
				body := p.read(filepath.Join(base, "references/project-rules.md"))
				if canonical == "" {
					canonical = body
				} else if body != canonical {
					t.Errorf("%s has different guidance from the Claude copy", base)
				}
				if left := tokens.Surviving(body); len(left) != 0 {
					t.Errorf("%s has unresolved tokens: %v", base, left)
				}
				for _, heading := range tc.headings {
					if !strings.Contains(body, heading+"\n") {
						t.Errorf("%s lost %s", base, heading)
					}
				}
			}
		})
	}
}

func TestRenderedIOSRetainsSafetyRules(t *testing.T) {
	for _, tc := range []struct{ fixture, path string }{
		{"rootapp", "CLAUDE.md"}, {"multistack", "ios/CLAUDE.md"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			p := fromFixture(t, tc.fixture)
			p.sync()
			body, ok := region.ExtractBody(p.read(tc.path), "ios")
			if !ok {
				t.Fatal("missing iOS rules")
			}
			for _, want := range []string{"Never hand-edit", "MARKETING_VERSION", "scripts/bump-marketing-version.sh", "--show-toplevel)/DerivedData", "-derivedDataPath", ".metadata_never_index", "Secrets.xcconfig", "never enter the app"} {
				if !strings.Contains(body, want) {
					t.Errorf("always-loaded iOS rules lost %q", want)
				}
			}
		})
	}
}
