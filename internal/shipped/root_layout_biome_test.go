package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/sync"
)

// A web component at the repository root lints every file sync writes beside
// it, including the ones another profile ships to the root. The ios profile's
// .claude/scripts/allow_mcp.js failed the web profile's own biome.json there
// (noUnusedVariables, useOptionalChain twice, useTemplate), so a project with
// that layout could not pass `biome ci` on lacquer's files alone. A consumer's
// whole-file biome.json exclusion hid it.
//
// The fix is in the script, not in a wider ignore: ignoring .claude/scripts
// would also stop linting any script a project keeps there itself.
//
// Real biome, borrowed from the same pinned install as the probe tests.
func TestRootWebComponentPassesBiomeOverEveryShippedFile(t *testing.T) {
	nm := os.Getenv(biomeNodeModulesEnv)
	if nm == "" {
		if os.Getenv("LACQUER_TEST_REQUIRE_BIOME") != "" {
			t.Fatalf("LACQUER_TEST_REQUIRE_BIOME is set but %s is not", biomeNodeModulesEnv)
		}
		t.Skipf("%s is not set; CI sets it and requires this test to pass", biomeNodeModulesEnv)
	}
	project := t.TempDir()
	gittest.Init(t, project, "-q")
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".lacquer.toml", `[project]
name = "demo"
project_name = "Demo"
scheme = "Demo"
bundle_id = "com.x.demo"
asc_app_id = "1"
xcodeproj = "ios/Demo.xcodeproj"
swift_version = "6"
github_org = "acme"

[[component]]
path = "."
profiles = ["web"]

[[component]]
path = "ios"
profiles = ["ios"]
`)
	write("package.json", "{\n  \"name\": \"demo\",\n  \"private\": true\n}\n")
	write("ios/Demo.xcodeproj/project.pbxproj", "")
	if _, err := sync.Run(root(t), project, false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "scripts", "allow_mcp.js")); err != nil {
		t.Fatalf("sync did not write the ios profile's root script, so this proves nothing: %v", err)
	}
	if err := os.Symlink(nm, filepath.Join(project, "node_modules")); err != nil {
		t.Fatal(err)
	}
	git(t, project, "add", "-A")

	biome := filepath.Join(nm, ".bin", "biome")
	cmd := exec.Command(biome, "ci", "--error-on-warnings", "--colors=off", ".")
	cmd.Dir = project
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("biome ci failed over a freshly synced root-layout project: %v\n%s", err, out)
	}
	// Exit 0 over nothing would also pass: require the script itself to have
	// been checked by the same command.
	one := exec.Command(biome, "ci", "--error-on-warnings", "--colors=off", ".claude/scripts/allow_mcp.js")
	one.Dir = project
	oneOut, err := one.CombinedOutput()
	if err != nil || !strings.Contains(string(oneOut), "Checked 1 file") {
		t.Fatalf("biome did not check allow_mcp.js: %v\n%s", err, oneOut)
	}
}
