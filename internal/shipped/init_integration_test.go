package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/initcmd"
	"github.com/patrickserrano/lacquer/internal/sync"
	"gopkg.in/yaml.v3"
)

// A NEW PROJECT MUST ALWAYS WORK.
//
// `init` had nineteen tests and none of them ran `sync`. Every one asserted what
// init WRITES — manifest fields, symlink refusal, stack and profile handling —
// and nothing asserted that what it writes then works. They also all ran against
// a FAKE lacquer root: a temp directory holding one stub file, so no test ever
// touched the profiles that actually ship.
//
// The result was a tool whose most important path was the least covered. A
// broken init looked fully tested, and the first person to find out was whoever
// ran it on a real project.
//
// These tests use the REAL lacquer root — this repository — so they exercise the
// actual profiles, tokens and assets that a project receives.
func initProject(t *testing.T, stack string, marker string) (lacquerRoot, projectRoot string) {
	t.Helper()
	lacquerRoot = root(t)
	projectRoot = t.TempDir()

	// A marker file is what detection keys off; without one there is no
	// component and the test would prove nothing.
	full := filepath.Join(projectRoot, marker)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// sync refuses a non-repository on purpose — it will not overwrite work git
	// cannot recover. A real new project is a repository, so the test must be one.
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@e", "-c", "user.name=T", "commit", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = projectRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if _, err := initcmd.Run(lacquerRoot, projectRoot, stack); err != nil {
		t.Fatalf("init failed on a fresh %s project: %v", stack, err)
	}
	return lacquerRoot, projectRoot
}

// init -> sync -> audit, against the real profiles. This is the path a new
// project takes, and until now nothing walked it.
func TestInitThenSyncLeavesACleanProject(t *testing.T) {
	for _, tc := range []struct {
		stack, marker string
	}{
		{"ios", "Acme.xcodeproj/project.pbxproj"},
		{"web", "package.json"},
	} {
		t.Run(tc.stack, func(t *testing.T) {
			lacquerRoot, projectRoot := initProject(t, tc.stack, tc.marker)

			cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
			if err != nil {
				t.Fatalf("init wrote a manifest that will not load: %v", err)
			}

			// Every token the shipped content uses must have a value. A missing
			// one renders `{{SOMETHING}}` into a workflow, which is a syntax
			// error at best and a silently wrong value at worst.
			plan, err := assets.Plan(lacquerRoot, cfg)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if missing, err := assets.MissingTokens(plan, cfg); err != nil {
				t.Fatalf("MissingTokens: %v", err)
			} else if len(missing) > 0 {
				t.Errorf("a freshly initialised project cannot render its own assets; unsubstituted tokens: %v", missing)
			}

			if _, err := sync.Run(lacquerRoot, projectRoot, false); err != nil {
				t.Fatalf("sync failed immediately after init — a new project must work: %v", err)
			}

			// Audit is the tool's own definition of "this project is correct".
			// Straight after init and sync it must have nothing to report.
			rows, _, err := audit.Classify(lacquerRoot, projectRoot)
			if err != nil {
				t.Fatalf("audit failed on a freshly synced project: %v", err)
			}
			for _, r := range rows {
				if r.Status != audit.OK {
					t.Errorf("audit reports %q for %s straight after init+sync; a new project should be clean", r.Status, r.Dest)
				}
			}
		})
	}
}

// Everything sync writes into .github/workflows must be valid YAML.
//
// A token that renders empty at column 0, or one that renders a value where a
// block was meant, produces a file GitHub rejects — and nothing else in the test
// suite parses what a REAL project actually receives.
func TestInitThenSyncWritesValidWorkflows(t *testing.T) {
	_, projectRoot := initProject(t, "ios", "Acme.xcodeproj/project.pbxproj")
	if _, err := sync.Run(root(t), projectRoot, false); err != nil {
		t.Fatalf("sync: %v", err)
	}

	dir := filepath.Join(projectRoot, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no workflows written: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("sync wrote no workflows at all")
	}

	leftover := regexp.MustCompile(`\{\{[A-Z_]+\}\}`)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if m := leftover.Find(b); m != nil {
			t.Errorf("%s still contains %s after sync", e.Name(), m)
		}
		var doc any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Errorf("%s is not valid YAML after sync: %v", e.Name(), err)
		}
	}
}

// Syncing twice must be a no-op. If the second run reports changes, something
// renders non-deterministically — a map iterated without sorting, a timestamp —
// and every project would show permanent drift it can never resolve.
func TestSyncingTwiceChangesNothing(t *testing.T) {
	lacquerRoot, projectRoot := initProject(t, "ios", "Acme.xcodeproj/project.pbxproj")
	if _, err := sync.Run(lacquerRoot, projectRoot, false); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// sync refuses to overwrite uncommitted work, so the first sync's output has
	// to be committed before the second runs — which is what a real project does
	// anyway: sync, review, commit.
	commitAll(t, projectRoot)

	before := snapshot(t, projectRoot)
	if _, err := sync.Run(lacquerRoot, projectRoot, false); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	after := snapshot(t, projectRoot)

	for path, sum := range before {
		if after[path] != sum {
			t.Errorf("%s changed on a second sync with no edits in between", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s appeared only on the second sync", path)
		}
	}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// .git churns from the test's own commit, not from sync.
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// The lock records the version it was written by; comparing it would
		// test the clock rather than determinism.
		if strings.HasSuffix(p, ".lacquer.lock") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"},
		{"-c", "user.email=t@e", "-c", "user.name=T", "commit", "-qm", "sync"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
