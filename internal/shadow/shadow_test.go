package shadow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, ".github", "workflows", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const managed = `
jobs:
  verify-ci-provenance:
    steps: [{run: gh api}]
  release:
    steps:
      - run: pipx install codemagic-cli-tools==0.68.0
      - run: security unlock-keychain -p x login.keychain-db
      - run: security set-keychain-settings -lut 3600 login.keychain-db
      - run: xcodebuild archive -project X
      - run: xcodebuild -exportArchive
      - run: app-store-connect publish --path x.ipa
      - run: security set-keychain-settings login.keychain-db
`

// The live case. dailybread carried the managed ios-release.yml AND a
// project-owned testflight.yml, and the project-owned one cut builds 313-317 —
// so every gate on the managed path was bypassed on the path that shipped.
func TestASecondReleasePathIsReported(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios-release.yml": managed,
		"testflight.yml": `
jobs:
  build:
    steps:
      - run: pipx install codemagic-cli-tools --force
      - run: security unlock-keychain -p x login.keychain-db
      - run: xcodebuild archive -project X
      - run: app-store-connect publish --path x.ipa
`,
	})
	fs := Check(dir)
	if len(fs) != 1 {
		t.Fatalf("a second release path was not reported: %+v", fs)
	}
	got := Format(fs)
	for _, want := range []string{
		"testflight.yml",
		"xcodebuild archive",
		"no provenance gate",
		"never restores its settings",
		"unpinned",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
}

// windsock is the reason this check has a second condition. It ships a
// project-owned macos-release.yml and EXCLUDES ios-release.yml with a recorded
// reason — a macOS-only app the generic iOS archive workflow does not fit.
// That is a declared replacement, not a shadow. Flagging it would teach people
// the check cries wolf, on the one project that documented its decision.
func TestADeclaredReplacementIsNotReported(t *testing.T) {
	dir := repo(t, map[string]string{
		// No ios-release.yml: excluded in .lacquer.toml, so sync never wrote it.
		"macos-release.yml": `
jobs:
  release:
    steps:
      - run: xcodebuild archive -project X
      - run: app-store-connect publish --path x.pkg
`,
	})
	if fs := Check(dir); len(fs) != 0 {
		t.Fatalf("a project that excluded the managed workflow was flagged anyway: %+v\n%s", fs, Format(fs))
	}
}

// A workflow that builds and tests but never ships is not a release path.
func TestANonReleasingWorkflowIsNotReported(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios-release.yml": managed,
		"ios-ci.yml": `
jobs:
  test:
    steps:
      - run: xcodebuild test -project X
      - run: xcodebuild build-for-testing
`,
	})
	if fs := Check(dir); len(fs) != 0 {
		t.Fatalf("a build/test workflow was mistaken for a release path: %+v", fs)
	}
}

// A shadow that DOES harden itself should not be told it is missing things.
// Otherwise the fix produces the same finding as the bug, which is how a check
// earns being ignored.
func TestAHardenedSecondPathReportsNoMissingRisks(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios-release.yml": managed,
		"testflight.yml": `
jobs:
  verify-ci-provenance:
    steps: [{run: gh api}]
  build:
    steps:
      - run: pipx install codemagic-cli-tools==0.68.0
      - run: security unlock-keychain -p x login.keychain-db
      - run: security set-keychain-settings login.keychain-db
      - run: xcodebuild archive -project X
`,
	})
	fs := Check(dir)
	if len(fs) != 1 {
		t.Fatalf("expected the duplication to still be reported: %+v", fs)
	}
	if len(fs[0].Risks) != 0 {
		t.Errorf("a hardened second path was told it is missing %v", fs[0].Risks)
	}
	if strings.Contains(Format(fs), "MISSING:") {
		t.Errorf("report lists missing hardening for a path that has it:\n%s", Format(fs))
	}
}
