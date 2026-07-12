package onboardcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T, dir string, extra ...[]string) {
	t.Helper()
	cmds := append([][]string{{"init", "-q"}}, extra...)
	for _, a := range cmds {
		cmd := exec.Command("git", a...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

func mk(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// lacquerIOS builds a temp lacquer checkout that ships the ios profile, so init
// (invoked by onboard when no manifest exists) records the detected ios component.
func lacquerIOS(t *testing.T) string {
	t.Helper()
	hr := t.TempDir()
	mk(t, filepath.Join(hr, "profiles", "ios", "CLAUDE.ios.md"))
	return hr
}

func TestOnboardCreatesRepoWhenNoRemote(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mk(t, filepath.Join(root, "Acme.xcodeproj", "project.pbxproj"))

	var gotOrg, gotName, gotDir string
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { gotDir, gotOrg, gotName = dir, org, name; return nil }
	defer func() { ghCreate = orig }()

	if _, err := Run(lacquerIOS(t), root, "AcmeOrg", true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotOrg != "AcmeOrg" || gotName != "Acme" || gotDir != root {
		t.Errorf("ghCreate called with dir=%q org=%q name=%q", gotDir, gotOrg, gotName)
	}
	if _, err := os.Stat(filepath.Join(root, ".lacquer.toml")); err != nil {
		t.Errorf(".lacquer.toml not written: %v", err)
	}
}

func TestOnboardSkipsRepoWhenRemoteExists(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root, []string{"remote", "add", "origin", "git@github.com:x/y.git"})
	mk(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"))

	called := false
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { called = true; return nil }
	defer func() { ghCreate = orig }()

	if _, err := Run(lacquerIOS(t), root, "AcmeOrg", true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("ghCreate must NOT be called when an origin remote already exists")
	}
}

func TestOnboardNoRepoFlag(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mk(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"))
	called := false
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { called = true; return nil }
	defer func() { ghCreate = orig }()
	if _, err := Run(lacquerIOS(t), root, "AcmeOrg", false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("ghCreate must NOT be called when createRepo is false")
	}
}

func TestOnboardRejectsUnsafeOrg(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mk(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"))
	called := false
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { called = true; return nil }
	defer func() { ghCreate = orig }()
	for _, org := range []string{"-evil", "a;b", "a/b", "a b"} {
		if _, err := Run(lacquerIOS(t), root, org, true); err == nil {
			t.Errorf("expected rejection for --org %q", org)
		}
	}
	if called {
		t.Error("ghCreate must not be called with an invalid org")
	}
}

func TestOnboardRequiresExplicitOrg(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mk(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"))
	called := false
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { called = true; return nil }
	defer func() { ghCreate = orig }()
	// Empty org with createRepo must fail closed — the lacquer has no default org.
	if _, err := Run(lacquerIOS(t), root, "", true); err == nil {
		t.Error("expected error when --org is empty and createRepo is true")
	}
	if called {
		t.Error("ghCreate must not be called with an empty org")
	}
	// Empty org is fine when not creating a repo.
	if _, err := Run(lacquerIOS(t), root, "", false); err != nil {
		t.Errorf("empty org with --no-repo should succeed, got %v", err)
	}
}

func TestOnboardFallsBackToManifestOrg(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	// Manifest declares github_org; no --org passed.
	if err := os.WriteFile(filepath.Join(root, ".lacquer.toml"),
		[]byte("[project]\nname=\"App\"\ngithub_org=\"AcmeOrg\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotOrg string
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { gotOrg = org; return nil }
	defer func() { ghCreate = orig }()

	if _, err := Run(lacquerIOS(t), root, "", true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotOrg != "AcmeOrg" {
		t.Errorf("org = %q, want AcmeOrg (from manifest github_org)", gotOrg)
	}
}

// With a manifest that declares no [project].name, the repo name falls back to
// the project directory's basename.
func TestOnboardRepoNameFallsBackToDirBasename(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "Widgetsmith")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, root)
	// Manifest present but nameless — repoName must use the dir basename.
	if err := os.WriteFile(filepath.Join(root, ".lacquer.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotName string
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { gotName = name; return nil }
	defer func() { ghCreate = orig }()

	if _, err := Run(lacquerIOS(t), root, "AcmeOrg", true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotName != "Widgetsmith" {
		t.Errorf("repo name = %q, want Widgetsmith (dir basename)", gotName)
	}
}

// A nameless manifest in a directory whose basename isn't a safe repo name must
// be rejected before reaching `gh`, rather than passing an unsafe name through.
func TestOnboardRejectsUnsafeDirBasename(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "-rf")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, ".lacquer.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { called = true; return nil }
	defer func() { ghCreate = orig }()

	if _, err := Run(lacquerIOS(t), root, "AcmeOrg", true); err == nil {
		t.Fatal("expected rejection of an unsafe dir-basename repo name")
	}
	if called {
		t.Error("ghCreate must not be called with an unsafe derived name")
	}
}

func TestOnboardSurfacesMalformedManifest(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	// pre-existing, malformed manifest (invalid project name) + no remote
	if err := os.WriteFile(filepath.Join(root, ".lacquer.toml"), []byte("[project]\nname=\"--bad\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { return nil }
	defer func() { ghCreate = orig }()
	if _, err := Run(lacquerIOS(t), root, "AcmeOrg", true); err == nil {
		t.Fatal("expected error surfacing the malformed manifest, got nil")
	}
}
