package shipped

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A shipped pre-commit hook once invoked
// `.github/scripts/test_fetch_testflight_feedback.py` — a script from the
// TestFlight feedback feature this lacquer stopped shipping entirely. The hook
// itself was left behind in profiles/ios/root/.pre-commit-config.yaml, so any
// iOS project synced to current content got a `feedback-fetcher-tests` hook
// whose `entry:` names a file that does not exist anywhere the lacquer ships.
// It was latent rather than loud: pre-commit only runs a hook whose `files:`
// pattern matches something in the commit, so nothing broke until a project
// happened to touch `.github/scripts/*.py` — at which point `python3` failed to
// open a file that was never there, on every commit, with no clue in the
// config about why.
//
// internal/audit.UncalledScripts (see internal/shipped/uncalled_scripts_test.go)
// checks the opposite direction — a script the lacquer ships with no caller.
// It could not have caught this: the missing file is not a shipped script at
// all, so it never entered that sweep's universe. This test exists for the
// direction UncalledScripts cannot see: a hook command naming a script path
// that nothing in the lacquer's own shipped tree provides.
//
// It parses every shipped .pre-commit-config.yaml and lefthook.yml — the ONLY
// two hook-manager formats this lacquer ships — and for every `entry:` or
// `run:` value, extracts anything that reads as a repo-relative script
// invocation (a path containing a "/" and ending in a script extension). Each
// one must resolve to a file shipped either by core/root or by the owning
// profile's own root/ tree — the same two trees internal/assets.Plan merges
// into every project (see assets.go's walkInto calls over "core", "root" and
// "profiles/<p>/root") — or be named in hookScriptAllowlist with a reason.
//
// Keyed on parsed YAML values, never on prose: a comment naming a script is not
// a command, and gopkg.in/yaml.v3 never hands comment text back as a mapping
// value, so changing a comment cannot change what this test checks.

// hookScriptAllowlist explicitly permits a hook command's script path even
// though the lacquer does not ship it — for a script the RECEIVING project is
// expected to provide itself. Nothing needs an entry today: every
// script-invoking hook this lacquer ships resolves to a script the lacquer
// itself ships (core/root or the owning profile's root/). Add an entry only
// alongside a comment explaining why the path is intentionally project-owned
// rather than shipped — the same discipline .lacquer.toml's [project].exclude
// documentation applies to an opted-out managed file.
//
// Keyed "<profile>:<path>", e.g. "ios:.github/scripts/my-project-script.sh".
var hookScriptAllowlist = map[string]string{
	// (empty — see comment above)
}

// scriptInvocation matches a repo-relative script path inside a hook command:
// at least one path segment, then a filename ending in a script extension.
// Deliberately excludes anything with no such extension (a bare binary name
// like `./node_modules/.bin/biome`, `pre-commit`, `deno`) — those are
// resolved at runtime from PATH or an npm/deno install, never a file this
// lacquer ships, so they are out of scope rather than needing an allowlist
// entry apiece.
var scriptInvocation = regexp.MustCompile(`[\w./-]+/[\w.-]+\.(?:sh|py|rb|pl|js|mjs|cjs)\b`)

// hookConfigFile is one shipped file this test parses, and which profile owns
// it (the profile whose root/ tree — plus core/root — is the universe of
// scripts its commands may call).
type hookConfigFile struct {
	path    string // absolute
	rel     string // relative to the lacquer root, for error messages
	profile string
}

// findHookConfigs walks profiles/*/root (and core/root, for completeness —
// core ships no hook manager config today, but a future one should be caught
// by this sweep automatically rather than needing this test updated) for
// every shipped .pre-commit-config.yaml or lefthook.yml.
func findHookConfigs(t *testing.T, lacquerRoot string) []hookConfigFile {
	t.Helper()
	var out []hookConfigFile

	walk := func(base, profile string) {
		err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if info.IsDir() {
				return nil
			}
			name := filepath.Base(p)
			if name == ".pre-commit-config.yaml" || name == "lefthook.yml" {
				rel, relErr := filepath.Rel(lacquerRoot, p)
				if relErr != nil {
					rel = p
				}
				out = append(out, hookConfigFile{path: p, rel: filepath.ToSlash(rel), profile: profile})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}

	walk(filepath.Join(lacquerRoot, "core", "root"), "")

	entries, err := os.ReadDir(filepath.Join(lacquerRoot, "profiles"))
	if err != nil {
		t.Fatalf("read profiles/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		walk(filepath.Join(lacquerRoot, "profiles", e.Name(), "root"), e.Name())
	}

	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out
}

// shippedRootFiles returns every file path (relative, forward-slashed) shipped
// verbatim by a root/ tree — the same tree walked by internal/assets.Plan into
// a project's own root. Returns an empty (non-nil) set when the directory does
// not exist, matching "a profile need not ship a root tree" elsewhere in this
// package (see TestEveryMergeableDestinationIsGenuinelyContested).
func shippedRootFiles(t *testing.T, base string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", base, err)
	}
	return out
}

// collectCommandValues recursively walks a decoded YAML node tree and returns
// every scalar string found under a key literally named "entry" or "run" — the
// two forms pre-commit and lefthook use for a hook's command. Any other shape
// (name, id, files, matcher, glob, …) is not a command and is ignored, so a
// files: regex or a hook's name can never be mistaken for an invocation.
//
// Walked as a *yaml.Node tree rather than decoded into map[string]interface{}:
// profiles/web/root/lefthook.yml's `run: {{WEB_AUDIT}}` template token is
// syntactically a flow mapping nested inside a flow mapping ("{{" opens one,
// "{" opens another), which yaml.v3 parses fine at the Node level but refuses
// to decode into a Go map, since the inner mapping cannot be a Go map key.
// This never has to know that {{WEB_AUDIT}} is a lacquer template token; it
// just never asks a non-scalar node to become a Go map key in the first place.
func collectCommandValues(n *yaml.Node) []string {
	var out []string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, val := n.Content[i], n.Content[i+1]
				if key.Kind == yaml.ScalarNode && (key.Value == "entry" || key.Value == "run") &&
					val.Kind == yaml.ScalarNode {
					out = append(out, val.Value)
					continue
				}
				walk(val)
			}
		}
	}
	walk(n)
	return out
}

// TestShippedHookCommandsOnlyInvokeShippedScripts is the guard: a hook this
// lacquer ships must never name a script the lacquer does not also ship (or
// explicitly disclaim via hookScriptAllowlist).
func TestShippedHookCommandsOnlyInvokeShippedScripts(t *testing.T) {
	lacquerRoot := root(t)
	configs := findHookConfigs(t, lacquerRoot)
	if len(configs) == 0 {
		t.Fatal("found zero shipped .pre-commit-config.yaml / lefthook.yml files under core/root or " +
			"profiles/*/root — this test would silently assert nothing, which is worse than not " +
			"having it: see CLAUDE.md's 'a detector that finds nothing must not pass'")
	}

	coreFiles := shippedRootFiles(t, filepath.Join(lacquerRoot, "core", "root"))

	invocationsChecked := 0
	for _, cfg := range configs {
		raw, err := os.ReadFile(cfg.path)
		if err != nil {
			t.Fatalf("%s: %v", cfg.rel, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: invalid YAML: %v", cfg.rel, err)
		}

		var profileFiles map[string]bool
		if cfg.profile != "" {
			profileFiles = shippedRootFiles(t, filepath.Join(lacquerRoot, "profiles", cfg.profile, "root"))
		} else {
			profileFiles = map[string]bool{}
		}

		for _, cmd := range collectCommandValues(&doc) {
			for _, match := range scriptInvocation.FindAllString(cmd, -1) {
				scriptPath := strings.TrimPrefix(match, "./")
				invocationsChecked++

				if coreFiles[scriptPath] || profileFiles[scriptPath] {
					continue
				}
				if reason, ok := hookScriptAllowlist[cfg.profile+":"+scriptPath]; ok {
					t.Logf("%s: %s allow-listed as project-owned: %s", cfg.rel, scriptPath, reason)
					continue
				}
				t.Errorf("%s: hook command %q invokes %q, which is not shipped by core/root or "+
					"profiles/%s/root, and is not in hookScriptAllowlist. Either the script was dropped "+
					"and this hook is dead (the feedback-fetcher-tests shape — delete the hook), or the "+
					"script moved and the hook needs updating, or the project genuinely owns this script "+
					"and the path belongs in hookScriptAllowlist with a reason",
					cfg.rel, cmd, scriptPath, cfg.profile)
			}
		}
	}

	if invocationsChecked == 0 {
		t.Fatal("parsed every shipped hook config but found zero script-invoking entry:/run: values — " +
			"this test would silently assert nothing")
	}
	t.Logf("checked %d script invocation(s) across %d shipped hook config(s)", invocationsChecked, len(configs))
}

// TestHookScriptAllowlistEntriesAreExplained pins the "with a comment giving
// the reason" half of the contract: an allowlist entry with an empty reason is
// indistinguishable from someone silencing a real defect to unblock a commit,
// which is exactly the failure this whole check exists to prevent one layer
// up.
func TestHookScriptAllowlistEntriesAreExplained(t *testing.T) {
	for key, reason := range hookScriptAllowlist {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("hookScriptAllowlist[%q] has no reason", key)
		}
	}
}
