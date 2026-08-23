package rootcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// upstreamAndClone builds a real origin plus a clone of it, so staleness is
// measured the way it is in the field rather than simulated.
func upstreamAndClone(t *testing.T) (origin, clone string) {
	t.Helper()
	base := t.TempDir()
	origin = filepath.Join(base, "origin")
	clone = filepath.Join(base, "clone")

	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, origin, "git", "init", "--quiet", "--initial-branch=main")
	write(t, filepath.Join(origin, "VERSION"), "0.1.0\n")
	run(t, origin, "git", "add", "-A")
	run(t, origin, "git", "commit", "--quiet", "-m", "first")

	run(t, base, "git", "clone", "--quiet", origin, clone)
	return origin, clone
}

func TestInspectReportsCurrentCheckout(t *testing.T) {
	_, clone := upstreamAndClone(t)

	s := Inspect(clone, false)

	if s.NotGit {
		t.Fatal("NotGit = true for a real checkout")
	}
	if s.Version != "0.1.0" {
		t.Errorf("Version = %q, want 0.1.0", s.Version)
	}
	if s.Branch != "main" {
		t.Errorf("Branch = %q, want main", s.Branch)
	}
	if s.Behind != 0 {
		t.Errorf("Behind = %d, want 0 for an up-to-date clone", s.Behind)
	}
	if w := s.Warning(); w != "" {
		t.Errorf("Warning() = %q, want empty when current", w)
	}
	if d := s.Describe(); !strings.Contains(d, "0.1.0") || !strings.Contains(d, "main") {
		t.Errorf("Describe() = %q, want version and branch", d)
	}
}

// The failure this package exists to catch: the root is behind, so a sync from
// it renders old content while reporting success.
func TestInspectDetectsStaleRoot(t *testing.T) {
	origin, clone := upstreamAndClone(t)

	write(t, filepath.Join(origin, "VERSION"), "0.2.0\n")
	run(t, origin, "git", "add", "-A")
	run(t, origin, "git", "commit", "--quiet", "-m", "second")

	// fetch=true is the point. The clone's remote-tracking ref still says the
	// old commit until something refreshes it, so a check that skipped the
	// fetch would report "up to date" here and be wrong — which is exactly how
	// the real incident stayed invisible.
	s := Inspect(clone, true)

	if s.Behind != 1 {
		t.Fatalf("Behind = %d, want 1 after one upstream commit", s.Behind)
	}
	w := s.Warning()
	if w == "" {
		t.Fatal("Warning() was empty for a root that is behind")
	}
	for _, want := range []string{"behind", "OLD content", "pull"} {
		if !strings.Contains(w, want) {
			t.Errorf("Warning() = %q, missing %q", w, want)
		}
	}
	// Still 0.1.0 on disk: the version alone cannot reveal this, which is why
	// the commit comparison is what warns.
	if s.Version != "0.1.0" {
		t.Errorf("Version = %q, want the stale 0.1.0 still on disk", s.Version)
	}
}

func TestInspectWithoutFetchUsesLocalRef(t *testing.T) {
	origin, clone := upstreamAndClone(t)

	write(t, filepath.Join(origin, "VERSION"), "0.2.0\n")
	run(t, origin, "git", "add", "-A")
	run(t, origin, "git", "commit", "--quiet", "-m", "second")

	// LACQUER_NO_FETCH=1 trades detection for not touching the network. The
	// stale remote-tracking ref then reports 0, and no warning is emitted —
	// documented here so the trade is a decision rather than a surprise.
	s := Inspect(clone, false)

	if s.Behind != 0 {
		t.Errorf("Behind = %d, want 0 without a fetch (stale remote-tracking ref)", s.Behind)
	}
	if w := s.Warning(); w != "" {
		t.Errorf("Warning() = %q, want empty without a fetch", w)
	}
}

func TestInspectHandlesNonGitRoot(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "VERSION"), "9.9.9\n")

	s := Inspect(dir, true)

	if !s.NotGit {
		t.Error("NotGit = false for a plain directory")
	}
	if s.Behind != -1 {
		t.Errorf("Behind = %d, want -1 (unknown) outside git", s.Behind)
	}
	if w := s.Warning(); w != "" {
		t.Errorf("Warning() = %q, want empty when staleness is unknowable", w)
	}
	if d := s.Describe(); !strings.Contains(d, "not a git checkout") {
		t.Errorf("Describe() = %q, want it to say the root is not a checkout", d)
	}
}

// A checkout with no upstream (a fresh `git init`, or a detached tarball turned
// into a repo) cannot be behind anything, and must not warn as if it were.
func TestInspectNoUpstreamDoesNotWarn(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--quiet", "--initial-branch=main")
	write(t, filepath.Join(dir, "VERSION"), "1.0.0\n")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "--quiet", "-m", "only")

	s := Inspect(dir, true)

	if s.Behind != -1 {
		t.Errorf("Behind = %d, want -1 with no upstream", s.Behind)
	}
	if w := s.Warning(); w != "" {
		t.Errorf("Warning() = %q, want empty with no upstream", w)
	}
}

func TestDescribeFlagsDirtyRoot(t *testing.T) {
	_, clone := upstreamAndClone(t)
	write(t, filepath.Join(clone, "VERSION"), "0.1.0-local\n")

	s := Inspect(clone, false)

	if !s.Dirty {
		t.Fatal("Dirty = false with an uncommitted tracked change")
	}
	if d := s.Describe(); !strings.Contains(d, "dirty") {
		t.Errorf("Describe() = %q, want it to flag the dirty tree", d)
	}
}

// The banner reports the version of the CONTENT under LACQUER_ROOT. A binary
// built from an older source tree reads today's content and renders it with that
// day's logic, so the banner calls a stale binary current — which is exactly how
// a 1.3.0 binary rewrote a project's lefthook.yml from 135 lines to 60 while
// announcing "lacquer: 1.5.4". The banner must stop being able to say that.
func TestDescribeNamesTheBinaryWhenItIsStale(t *testing.T) {
	s := State{Version: "1.5.4", Branch: "main", Commit: "ed16e99", BuiltVersion: "1.3.0"}
	got := s.Describe()

	if !strings.Contains(got, "1.3.0") {
		t.Errorf("Describe() = %q — it does not name the version the BINARY was built from, so a stale binary still reads as current", got)
	}
	if !s.StaleBinary() {
		t.Error("StaleBinary() = false for a 1.3.0 binary against 1.5.4 content")
	}
}

func TestDescribeIsUnchangedWhenBinaryMatches(t *testing.T) {
	s := State{Version: "1.5.4", Branch: "main", Commit: "ed16e99", BuiltVersion: "1.5.4"}
	got := s.Describe()

	if strings.Contains(got, "built from") {
		t.Errorf("Describe() = %q — the stale note leaked into the matching case, which would make it noise on every ordinary run", got)
	}
	if s.StaleBinary() {
		t.Error("StaleBinary() = true when the versions match")
	}
}

func TestStaleBinaryIsSilentWhenUnknown(t *testing.T) {
	// An empty BuiltVersion means the binary predates this check. It must not be
	// reported as stale on no evidence.
	s := State{Version: "1.5.4", BuiltVersion: ""}
	if s.StaleBinary() {
		t.Error("StaleBinary() = true with no embedded version — that is an unknown, not a mismatch")
	}
}
