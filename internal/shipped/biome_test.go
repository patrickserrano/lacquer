package shipped

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/sync"
)

// The shipped biome.json sets `vcs.useIgnoreFile: true`, and biome resolves that
// against the config file's OWN folder. The lacquer ships biome.json INTO the
// component directory, so on any project whose web component is not the repo
// root, biome looked for an ignore file in e.g. `admin/` and found none.
//
// It does not degrade to "no ignore rules applied". It refuses to start:
//
//	✖ Biome couldn't find an ignore file in the following folder: …/admin
//	✖ Biome exited because the configuration resulted in errors. Please fix them.
//
// That is a configuration abort, so the CI lint step, the lefthook pre-commit
// hook and every `lacquer doctor` biome probe were all failing on a fresh web
// component before a single line of the project's own code was read. A repo-root
// .gitignore does NOT satisfy it — measured; biome looks only in the config's
// folder, which is also why the managed .gitignore region added for credentials
// did not fix this.
//
// `vcs.root` moves the lookup, and its value is the relative path from the
// component directory up to the repo root — which is what {{COMPONENT_TO_ROOT}}
// renders. The root layout is the case that makes a hardcoded ".." wrong: it
// would point one level ABOVE the repository and abort there instead.

// biomeProject syncs the real lacquer into a fresh git repo whose single web
// component sits at componentPath, and returns the project root.
//
// A real sync, not a hand-rolled render: the thing under test is what a project
// actually receives on disk, and the prefix that drives the token is chosen deep
// inside assets.Plan. Rendering the file directly would assert the substitution
// and skip the part that picks what to substitute.
func biomeProject(t *testing.T, componentPath string) string {
	t.Helper()
	r := root(t)
	project := t.TempDir()

	git(t, project, "init", "-q")
	git(t, project, "config", "user.email", "test@example.com")
	git(t, project, "config", "user.name", "Test")

	manifest := `[project]
name = "demo"
project_name = "Demo"
scheme = "Demo"
bundle_id = "com.x.demo"
asc_app_id = "1"
xcodeproj = "Demo.xcodeproj"
swift_version = "6"
github_org = "acme"

[[component]]
path = "` + componentPath + `"
profiles = ["web"]
`
	if err := os.WriteFile(filepath.Join(project, ".lacquer.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// The marker detection keys off, so the declared component is a component
	// git and `lacquer detect` both agree exists.
	dir := project
	if componentPath != "." {
		dir = filepath.Join(project, filepath.FromSlash(componentPath))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := sync.Run(r, project, false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	return project
}

// biomeVCS is the slice of the rendered config this bug lives in.
type biomeVCS struct {
	Enabled       *bool  `json:"enabled"`
	ClientKind    string `json:"clientKind"`
	UseIgnoreFile *bool  `json:"useIgnoreFile"`
	Root          string `json:"root"`
}

// readBiomeVCS reads the biome.json a synced project received for the component
// at componentPath and returns its parsed `vcs` block.
func readBiomeVCS(t *testing.T, project, componentPath string) biomeVCS {
	t.Helper()
	dir := project
	if componentPath != "." {
		dir = filepath.Join(project, filepath.FromSlash(componentPath))
	}
	path := filepath.Join(dir, "biome.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("sync wrote no biome.json for the component at %q: %v", componentPath, err)
	}
	// An unrendered token would parse fine as a JSON string and then be a
	// literal `{{COMPONENT_TO_ROOT}}` directory name to biome, so check the text
	// before trusting the parse.
	if m := regexp.MustCompile(`\{\{[A-Z_]+\}\}`).Find(data); m != nil {
		t.Fatalf("%s still contains the unrendered token %s", path, m)
	}
	var doc struct {
		VCS biomeVCS `json:"vcs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	return doc.VCS
}

// TestBiomeVCSRootPointsAtTheRepoRoot is the assertion that would have caught
// the bug: for every layout, the rendered vcs.root must be the path from the
// component directory back up to the repo root.
func TestBiomeVCSRootPointsAtTheRepoRoot(t *testing.T) {
	for _, tc := range []struct {
		name, componentPath, want string
	}{
		// The root layout. A hardcoded ".." aborts here, against the directory
		// ABOVE the repository — so this case is not a formality, it is the one
		// that rules out the obvious wrong fix.
		{"component at the repo root", ".", "."},
		{"component one level down", "admin", ".."},
		{"component two levels down", "apps/admin", "../.."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := biomeProject(t, tc.componentPath)
			vcs := readBiomeVCS(t, project, tc.componentPath)

			if vcs.Root != tc.want {
				t.Errorf("vcs.root = %q, want %q — biome resolves the ignore file relative to biome.json's own folder, so a component at %q needs %q to reach the repo root",
					vcs.Root, tc.want, tc.componentPath, tc.want)
			}
			// vcs.root only matters BECAUSE useIgnoreFile is on. If someone ever
			// turns the ignore file off, this whole token is dead weight and the
			// next reader should be told so rather than left maintaining it.
			if vcs.UseIgnoreFile == nil || !*vcs.UseIgnoreFile {
				t.Error("vcs.useIgnoreFile is no longer true — vcs.root exists only to make the ignore-file lookup land on the repo root; if the ignore file is genuinely no longer wanted, delete vcs.root and {{COMPONENT_TO_ROOT}} with it")
			}
			if vcs.Enabled == nil || !*vcs.Enabled {
				t.Error("vcs.enabled is no longer true — vcs.root is inert without it")
			}
			if vcs.ClientKind != "git" {
				t.Errorf("vcs.clientKind = %q, want \"git\"", vcs.ClientKind)
			}
		})
	}
}

// TestBiomeStartsInASyncedComponent is the end-to-end half: the rendered config
// is fed to the REAL biome, from the directory CI runs it in
// (`working-directory: {{COMPONENT_PREFIX}}.`), and must not abort.
//
// It asserts on the absence of the ignore-file error rather than on exit status
// on purpose. `biome ci` exits non-zero for ordinary lint and format findings
// and for a $schema/CLI version skew, none of which are this bug; keying on the
// abort message keeps the test measuring the one thing it is for across biome
// versions.
func TestBiomeStartsInASyncedComponent(t *testing.T) {
	bin, err := exec.LookPath("biome")
	if err != nil {
		t.Skip("biome not installed")
	}
	for _, componentPath := range []string{".", "admin", "apps/admin"} {
		t.Run(componentPath, func(t *testing.T) {
			project := biomeProject(t, componentPath)
			dir := project
			if componentPath != "." {
				dir = filepath.Join(project, filepath.FromSlash(componentPath))
			}
			// Nothing creates a .gitignore beside biome.json, and nothing should:
			// the managed region writes ONE at the repo root, which is the whole
			// reason vcs.root has to point there.
			if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err == nil && componentPath != "." {
				t.Fatalf("the component dir has its own .gitignore, so this test could pass without vcs.root doing anything")
			}
			if _, err := os.Stat(filepath.Join(project, ".gitignore")); err != nil {
				t.Fatalf("sync wrote no repo-root .gitignore, so there is nothing for vcs.root to point at: %v", err)
			}

			cmd := exec.Command(bin, "ci", ".")
			cmd.Dir = dir
			out, _ := cmd.CombinedOutput()
			if strings.Contains(string(out), "couldn't find an ignore file") {
				t.Errorf("biome refused to start in the synced component at %q:\n%s", componentPath, out)
			}
			if strings.Contains(string(out), "configuration resulted in errors") {
				t.Errorf("biome rejected the synced configuration at %q:\n%s", componentPath, out)
			}
		})
	}
}
