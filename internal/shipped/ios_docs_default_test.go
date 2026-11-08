package shipped

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/doctor"
	"github.com/patrickserrano/lacquer/internal/lock"
	"gopkg.in/yaml.v3"
)

func assertDocumentationHook(t *testing.T, p *project, enabled bool) {
	t.Helper()
	var rendered preCommitConfig
	if err := yaml.Unmarshal([]byte(p.read(".pre-commit-config.yaml")), &rendered); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]bool{}
	for _, repo := range rendered.Repos {
		for _, hook := range repo.Hooks {
			hooks[hook.ID] = true
		}
	}
	hookFound := hooks["swiftlint-docs"]
	for _, id := range []string{"swiftlint", "swiftformat", "no-secrets", "model-defaults"} {
		if !hooks[id] {
			t.Errorf("unrelated hook lost: %s", id)
		}
	}
	if hookFound != enabled {
		t.Errorf("documentation hook present=%t, want %t", hookFound, enabled)
	}
	for _, rel := range []string{"scripts/docs-hook.sh", ".swiftlint-docs.yml"} {
		_, err := os.Stat(filepath.Join(p.root, rel))
		if enabled && err != nil || !enabled && !os.IsNotExist(err) {
			t.Errorf("%s enabled=%t: %v", rel, enabled, err)
		}
	}
}

// Removing the opt-in condition would reinstall a commit gate on existing debt.
func TestIOSDocumentationHookDefaultsOff(t *testing.T) {
	p := fromFixture(t, "rootapp")
	p.sync()
	assertDocumentationHook(t, p, false)
}

func TestIOSDocumentationHookOptIn(t *testing.T) {
	p := fromFixture(t, "rootapp")
	appendManifest(t, p, "\n[ios]\ndocumentation_hook = true\n")
	p.sync()
	assertDocumentationHook(t, p, true)
	// Base-release render hashes pin the opt-in's existing bytes.
	for rel, want := range map[string]string{
		".pre-commit-config.yaml": "5f705f31089138c9bd9f8f73a41bf302dabda39dc7ece48bc325eb7685a485c4",
		"scripts/docs-hook.sh":    "8c47f75c0fd05131bbfb370d773ed646e794b70a7e6a1fb6d43a09e4ef39d4ea",
		".swiftlint-docs.yml":     "635a0c8619e7483e78554f91d3c6cc288dbf4c007a35d2ee270322a06bf01930",
	} {
		if got := fmt.Sprintf("%x", sha256.Sum256([]byte(p.read(rel)))); got != want {
			t.Errorf("opt-in changed %s: %s", rel, got)
		}
	}
}

// The retained documentation relaxation must load and sync even after expiry.
func TestIOSDocumentationRelaxationWithoutHook(t *testing.T) {
	for _, until := range []string{"2000-01-01", "2999-12-31"} {
		t.Run(until, func(t *testing.T) {
			p := fromFixture(t, "rootapp")
			appendManifest(t, p, "\n[baseline.relax]\ndocumentation = { until = \""+until+"\", reason = \"Existing documentation debt\" }\n")
			p.sync()
			assertDocumentationHook(t, p, false)
			reports, err := baseline.Run(p.lacquerRoot, p.root, p.config().BaselineTargets(), p.config().Baseline.Relax, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			for _, report := range reports {
				if bad := baseline.Violations(report.Findings); len(bad) != 0 {
					t.Fatalf("documentation relaxation became a violation: %+v", bad)
				}
			}
		})
	}
}

// Real git and a linked checkout exercise the lock's no-longer-shipped path.
func TestIOSDocumentationHookRetirement(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "linked"}[linked], func(t *testing.T) {
			p := fromFixture(t, "rootapp")
			appendManifest(t, p, "\n[ios]\ndocumentation_hook = true\n")
			p.sync()
			assertDocumentationHook(t, p, true)
			p.commit("sync documentation hook")
			if linked {
				work := filepath.Join(t.TempDir(), "linked")
				gitIn(t, p.root, "worktree", "add", "-q", "-b", "topic", work)
				p = &project{root: work, lacquerRoot: p.lacquerRoot, t: t}
			}
			manifest := strings.Replace(p.read(".lacquer.toml"), "documentation_hook = true", "documentation_hook = false", 1)
			if err := os.WriteFile(filepath.Join(p.root, ".lacquer.toml"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			// Even an edited retired file is reported, never clobbered as drift.
			if err := os.WriteFile(filepath.Join(p.root, "scripts/docs-hook.sh"), []byte("# project note\n"), 0755); err != nil {
				t.Fatal(err)
			}
			p.commit("disable documentation hook")
			p.sync()
			orphans, err := audit.Orphans(p.lacquerRoot, p.root)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{"scripts/docs-hook.sh": true}
			for _, orphan := range orphans {
				delete(want, orphan.Dest)
			}
			if len(want) != 0 || len(orphans) != 1 {
				t.Fatalf("edited retired file lost from audit: %v; got %+v", want, orphans)
			}
			if _, err := os.Stat(filepath.Join(p.root, ".swiftlint-docs.yml")); !os.IsNotExist(err) {
				t.Fatalf("unchanged managed config not removed: %v", err)
			}
			if strings.Contains(p.read(".pre-commit-config.yaml"), "swiftlint-docs") {
				t.Fatal("sync retained the documentation hook entry")
			}
			p.commit("sync without documentation hook")
			p.sync() // The retained lock must keep reporting the files.
			orphans, err = audit.Orphans(p.lacquerRoot, p.root)
			if err != nil || len(orphans) != 1 {
				t.Fatalf("retirement report lost after second sync: %+v, %v", orphans, err)
			}
			if p.read("scripts/docs-hook.sh") != "# project note\n" {
				t.Fatal("sync clobbered a retired file")
			}
			for _, rel := range []string{"scripts/docs-hook.sh"} {
				if err := os.Remove(filepath.Join(p.root, rel)); err != nil {
					t.Fatal(err)
				}
			}
			p.commit("remove reported retired files")
			p.sync()
			assertDocumentationHook(t, p, false)
			lk, _, err := lock.Read(p.root)
			if err != nil {
				t.Fatal(err)
			}
			for _, rel := range []string{"scripts/docs-hook.sh", ".swiftlint-docs.yml"} {
				if _, present := lk.Files[rel]; present {
					t.Fatalf("removed retired file retained in lock: %s", rel)
				}
			}
			orphans, err = audit.Orphans(p.lacquerRoot, p.root)
			if err != nil || len(orphans) != 0 {
				t.Fatalf("removed files remain orphaned: %+v, %v", orphans, err)
			}
		})
	}
}

// A probe for a file no longer installed is inapplicable, not a failed check.
func TestIOSDocumentationDoctorScope(t *testing.T) {
	probes, err := doctor.LoadProbes(root(t), "ios")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"missing_docs rejects an undocumented declaration": true, "missing_docs does not apply to test targets": true}
	for _, probe := range probes {
		if probe.IOSDocumentationHook != want[probe.Name] {
			t.Fatalf("incorrect documentation-only scope: %s", probe.Name)
		}
		delete(want, probe.Name)
	}
	if len(want) != 0 {
		t.Fatalf("documentation checks lost: %v", want)
	}
}

// The shared reader stays available for independent runtime consumers.
func TestIOSSharedRelaxationReaderCallers(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		unused         bool
	}{
		{"default", "", true},
		{"documentation opt-in", "\n[ios]\ndocumentation_hook = true\n", false},
		{"lint consumer", "\n[baseline.relax]\nlint = {until='2999-12-31', reason='Existing lint debt'}\n", false},
		{"format consumer", "\n[baseline.relax]\nformat = {until='2999-12-31', reason='Existing formatting debt'}\n", false},
		{"documentation debt without hook", "\n[baseline.relax]\ndocumentation = {until='2999-12-31', reason='Existing documentation debt'}\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fromFixture(t, "rootapp")
			if tc.manifest != "" {
				appendManifest(t, p, tc.manifest)
			}
			found, err := audit.UncalledScripts(p.lacquerRoot, p.root)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.unused {
				want = 1
			}
			if len(found) != want {
				t.Fatalf("uncalled scripts: %+v, want count %d", found, want)
			}
			for _, f := range found {
				if !f.Confirmed() || f.Dest != "scripts/docs-relaxation.sh" {
					t.Fatalf("unexpected unused script: %+v", f)
				}
			}
		})
	}
}
