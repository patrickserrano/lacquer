package pluginbootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, data string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plugins.toml")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := write(t, `
[[marketplace]]
name = "superpowers-marketplace"
source = "obra/superpowers-marketplace"

[[plugin]]
name = "superpowers@superpowers-marketplace"
`)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Marketplaces) != 1 || m.Marketplaces[0].Name != "superpowers-marketplace" || m.Marketplaces[0].Source != "obra/superpowers-marketplace" {
		t.Errorf("Marketplaces = %+v", m.Marketplaces)
	}
	if len(m.Plugins) != 1 || m.Plugins[0].Name != "superpowers@superpowers-marketplace" {
		t.Errorf("Plugins = %+v", m.Plugins)
	}
}

func TestLoadRejectsMalformedMarketplaceSource(t *testing.T) {
	cases := []string{
		"norepo",                    // missing "owner/repo" slash
		"-flag/repo",                // leading hyphen (flag injection)
		"owner/repo; rm -rf /",      // shell metacharacters
		"owner/repo\\n  evil: true", // newline injection
	}
	for _, c := range cases {
		path := write(t, fmt.Sprintf("[[marketplace]]\nname=\"x\"\nsource=%q\n", c))
		if _, err := Load(path); err == nil {
			t.Errorf("expected rejection for marketplace source %q", c)
		}
	}
}

func TestLoadRejectsMalformedPluginName(t *testing.T) {
	cases := []string{
		"noat",              // missing "@marketplace"
		"-flag@marketplace", // leading hyphen (flag injection)
		"plugin@; rm -rf /", // shell metacharacters
	}
	for _, c := range cases {
		path := write(t, fmt.Sprintf("[[plugin]]\nname=%q\n", c))
		if _, err := Load(path); err == nil {
			t.Errorf("expected rejection for plugin name %q", c)
		}
	}
}

func fakeClaude(t *testing.T, fail map[string]bool) func(args ...string) ([]byte, error) {
	t.Helper()
	return func(args ...string) ([]byte, error) {
		last := args[len(args)-1]
		if fail[last] {
			return []byte("boom"), fmt.Errorf("exit status 1")
		}
		return []byte("ok"), nil
	}
}

func TestApplyCallsMarketplaceAddThenInstall(t *testing.T) {
	orig := Runner
	var calls [][]string
	Runner = func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		return fakeClaude(t, nil)(args...)
	}
	defer func() { Runner = orig }()

	m := &Manifest{
		Marketplaces: []Marketplace{{Name: "mp", Source: "owner/repo"}},
		Plugins:      []Plugin{{Name: "plugin@mp"}},
	}
	res := Apply(m)
	if len(res.Marketplaces) != 1 || res.Marketplaces[0] != "mp" {
		t.Errorf("Marketplaces = %v", res.Marketplaces)
	}
	if len(res.Plugins) != 1 || res.Plugins[0] != "plugin@mp" {
		t.Errorf("Plugins = %v", res.Plugins)
	}
	if len(res.Failed) != 0 {
		t.Errorf("Failed = %v", res.Failed)
	}
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	wantFirst := []string{"plugin", "marketplace", "add", "owner/repo"}
	for i, w := range wantFirst {
		if calls[0][i] != w {
			t.Errorf("call[0][%d] = %q, want %q", i, calls[0][i], w)
		}
	}
	wantSecond := []string{"plugin", "install", "plugin@mp"}
	for i, w := range wantSecond {
		if calls[1][i] != w {
			t.Errorf("call[1][%d] = %q, want %q", i, calls[1][i], w)
		}
	}
}

func TestApplyContinuesAfterOneFailure(t *testing.T) {
	orig := Runner
	Runner = fakeClaude(t, map[string]bool{"bad-plugin@mp": true})
	defer func() { Runner = orig }()

	m := &Manifest{
		Plugins: []Plugin{{Name: "bad-plugin@mp"}, {Name: "good-plugin@mp"}},
	}
	res := Apply(m)
	if len(res.Plugins) != 1 || res.Plugins[0] != "good-plugin@mp" {
		t.Errorf("Plugins = %v", res.Plugins)
	}
	if _, ok := res.Failed["bad-plugin@mp"]; !ok {
		t.Errorf("Failed missing bad-plugin@mp: %v", res.Failed)
	}
}

func TestApplyWithEmptyManifest(t *testing.T) {
	orig := Runner
	called := false
	Runner = func(args ...string) ([]byte, error) {
		called = true
		return []byte("ok"), nil
	}
	defer func() { Runner = orig }()

	res := Apply(&Manifest{})
	if called {
		t.Error("Runner should not be called for an empty manifest")
	}
	if len(res.Marketplaces) != 0 || len(res.Plugins) != 0 {
		t.Errorf("res = %+v, want empty", res)
	}
}
