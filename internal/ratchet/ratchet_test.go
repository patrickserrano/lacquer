package ratchet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gittest"
)

func put(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()
	gittest.Init(t, root, "-q")
	put(t, root, "CLAUDE.md", "one\ntwo\n")
	put(t, root, "source.ts", "// eslint-disable no-console\n")
	cmd := exec.Command("git", "add", "source.ts")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	return root, &config.Config{}
}

func TestTightenAndLoosen(t *testing.T) {
	root, cfg := fixture(t)
	if _, err := Tighten(root, cfg, true); err != nil {
		t.Fatal(err)
	}
	put(t, root, "CLAUDE.md", "one\n")
	put(t, root, "source.ts", "// eslint-disable no-console -- test logging\n")
	findings, err := Check(root, cfg)
	if err != nil || len(findings) != 2 || Blocking(findings) != 0 {
		t.Fatalf("improved: %v %v", findings, err)
	}
	before, _ := Read(root)
	if before.Ratchet[ClaudeLines] != 2 {
		t.Fatal("audit wrote baseline")
	}
	if _, err := Tighten(root, cfg, false); err != nil {
		t.Fatal(err)
	}
	b, _ := Read(root)
	if b.Ratchet[ClaudeLines] != 1 || b.Ratchet[Suppressions] != 0 {
		t.Fatalf("not tightened: %+v", b)
	}
	put(t, root, "source.ts", "// eslint-disable no-console\n")
	findings, err = Tighten(root, cfg, true)
	if err != nil || Blocking(findings) != 1 {
		t.Fatalf("regression: %v %v", findings, err)
	}
	b, _ = Read(root)
	if b.Ratchet[Suppressions] != 0 {
		t.Fatal("--write loosened baseline")
	}
	if err := Loosen(root, cfg, Suppressions, " "); err == nil {
		t.Fatal("accepted blank reason")
	}
	if err := Loosen(root, cfg, "typo", "reason"); err == nil {
		t.Fatal("accepted unknown metric")
	}
	if err := Loosen(root, cfg, Suppressions, "legacy fixture"); err != nil {
		t.Fatal(err)
	}
	b, _ = Read(root)
	if b.Ratchet[Suppressions] != 1 || b.Reasons[Suppressions] != "legacy fixture" {
		t.Fatalf("reason not saved: %+v", b)
	}
	put(t, root, "source.ts", "// eslint-disable no-console\n// eslint-disable no-alert\n")
	findings, err = Check(root, cfg)
	if err != nil || Blocking(findings) != 1 {
		t.Fatalf("old reason exempted new regression: %v %v", findings, err)
	}
}

func TestMeasureTrackedSourcesAndUniqueDestinations(t *testing.T) {
	root, cfg := fixture(t)
	cfg.Components = []config.Component{{Path: ".", Profiles: []string{"web", "ios"}}, {Path: "nested", Profiles: []string{"web"}}}
	put(t, root, "nested/CLAUDE.md", "no final newline")
	put(t, root, "untracked.ts", "// eslint-disable\n")
	put(t, root, "AGENTS.md", "not counted\n")
	values, err := Measure(root, cfg)
	if err != nil || values[ClaudeLines] != 3 || values[Suppressions] != 1 {
		t.Fatalf("values=%v err=%v", values, err)
	}
	if err := os.Remove(filepath.Join(root, "source.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := Measure(root, cfg); err == nil {
		t.Fatal("missing tracked source counted as improvement")
	}
}

func TestRejectInvalidBaselines(t *testing.T) {
	for _, body := range []string{
		"[ratchet]\nclaude_lines = -1\nunjustified_suppressions = 0\n",
		"[ratchet]\nclaude_lines = 1\n",
		"[ratchet]\nclaude_lines = 1\nunjustified_suppressions = 0\ntypo = 0\n",
		"[ratchet]\nclaude_lines = 1\nunjustified_suppressions = 0\n[unknown]\na = 1\n",
		"bad toml [[",
	} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, Name, body)
			if _, err := Read(root); err == nil {
				t.Fatal("accepted invalid baseline")
			}
		})
	}
}

func TestRejectSymlinks(t *testing.T) {
	root, cfg := fixture(t)
	outside := t.TempDir()
	put(t, outside, "target", "preserve\n")
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(root, Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := Tighten(root, cfg, true); err == nil {
		t.Fatal("accepted symlink baseline")
	}
	if err := os.Remove(filepath.Join(root, Name)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "source.ts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(root, "source.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := Measure(root, cfg); err == nil {
		t.Fatal("followed source symlink")
	}
	data, _ := os.ReadFile(filepath.Join(outside, "target"))
	if string(data) != "preserve\n" {
		t.Fatal("modified symlink target")
	}
}

func TestSuppressionComments(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   int
	}{
		{"// swiftlint:disable force_cast", 1},
		{"// swiftlint:disable force_cast --", 1},
		{"// swiftlint:disable force_cast - framework contract", 0},
		{"// swiftlint:disable force_cast: framework contract", 0},
		{"// swiftlint:disable:next force_cast // framework contract", 0},
		{"// swiftlint:disable force_cast // ", 1},
		{"// swiftlint:disable force_cast -- framework contract", 0},
		{"// swiftlint:enable force_cast", 0},
		{"// biome-ignore lint/suspicious/noExplicitAny: upstream API", 0},
		{"// biome-ignore lint/suspicious/noExplicitAny:", 1},
		{"/* eslint-disable no-console, no-alert */", 1},
		{"// eslint-disable-next-line -- generated bridge", 0},
		{"/* eslint-disable-line no-console -- */", 1},
		{"let s = \"// eslint-disable no-console\"", 0},
		{"const s = `// biome-ignore lint/foo`;", 0},
		{"// Explain eslint-disable directives here", 0},
		{"call(); // eslint-disable-line no-console", 1},
		{"/*\n * eslint-disable no-console\n */", 1},
		{"// biome-ignore-end lint/suspicious/noExplicitAny", 0},
	} {
		t.Run(tc.source, func(t *testing.T) {
			if n := countSuppressions(tc.source); n != tc.want {
				t.Fatalf("got %d want %d", n, tc.want)
			}
		})
	}
}

func TestNoBaselineDoesNotSilentlyEnroll(t *testing.T) {
	root := t.TempDir()
	if findings, err := Check(root, &config.Config{}); err != nil || len(findings) != 0 {
		t.Fatalf("%v %v", findings, err)
	}
	if _, err := Tighten(root, &config.Config{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, Name)); !os.IsNotExist(err) {
		t.Fatal("sync enrolled without measurement")
	}
	if !strings.Contains(Format([]Finding{{ClaudeLines, 2, 1}}), "improved 2 → 1") {
		t.Fatal("missing diagnostic")
	}
}

func TestTrackedSymlinkIsNotAnotherSourceBlob(t *testing.T) {
	root, cfg := fixture(t)
	if err := os.Symlink("source.ts", filepath.Join(root, "alias.ts")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "alias.ts")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, b)
	}
	values, err := Measure(root, cfg)
	if err != nil || values[Suppressions] != 1 {
		t.Fatalf("alias counted twice: %v %v", values, err)
	}
}
