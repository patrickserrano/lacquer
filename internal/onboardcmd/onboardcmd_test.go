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

func TestOnboardCreatesRepoWhenNoRemote(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mk(t, filepath.Join(root, "ShelfLife.xcodeproj", "project.pbxproj"))

	var gotOrg, gotName, gotDir string
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { gotDir, gotOrg, gotName = dir, org, name; return nil }
	defer func() { ghCreate = orig }()

	if _, err := Run(root, "PixelFoxStudio", true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotOrg != "PixelFoxStudio" || gotName != "ShelfLife" || gotDir != root {
		t.Errorf("ghCreate called with dir=%q org=%q name=%q", gotDir, gotOrg, gotName)
	}
	if _, err := os.Stat(filepath.Join(root, ".harness.toml")); err != nil {
		t.Errorf(".harness.toml not written: %v", err)
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

	if _, err := Run(root, "PixelFoxStudio", true); err != nil {
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
	if _, err := Run(root, "PixelFoxStudio", false); err != nil {
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
		if _, err := Run(root, org, true); err == nil {
			t.Errorf("expected rejection for --org %q", org)
		}
	}
	if called {
		t.Error("ghCreate must not be called with an invalid org")
	}
}

func TestOnboardSurfacesMalformedManifest(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	// pre-existing, malformed manifest (invalid project name) + no remote
	if err := os.WriteFile(filepath.Join(root, ".harness.toml"), []byte("[project]\nname=\"--bad\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { return nil }
	defer func() { ghCreate = orig }()
	if _, err := Run(root, "PixelFoxStudio", true); err == nil {
		t.Fatal("expected error surfacing the malformed manifest, got nil")
	}
}
