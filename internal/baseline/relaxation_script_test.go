package baseline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/docs-relaxation.sh is what the CI workflows actually consult — Check
// never sees the `documentation` or `pgtap` keys — so its parsing is the real
// enforcement point for those two baselines and had no test at all until the
// `pgtap` gate started sharing it.
//
// The awk match is the part worth pinning. It interpolates the key into a
// regex and it has to stop at the next table header, so the two ways it can go
// wrong are silent and symmetrical: matching a same-named key in an unrelated
// table (reporting a relaxation that does not exist) or failing to isolate one
// key from another (reporting the wrong one's expiry). Both would read as a
// valid answer.
const relaxScript = "../../core/root/scripts/docs-relaxation.sh"

func runRelax(t *testing.T, manifest, today, key string) string {
	t.Helper()

	cmd := exec.Command("bash", relaxScript, manifest, today)
	cmd.Env = os.Environ()
	if key != "" {
		cmd.Env = append(cmd.Env, "RELAX_KEY="+key)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docs-relaxation.sh %s %s (RELAX_KEY=%q) failed: %v\n%s", manifest, today, key, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "lacquer.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	return path
}

func TestRelaxationScriptReadsEitherKey(t *testing.T) {
	both := writeManifest(t, `[project]
name = "X"

[baseline.relax]
documentation = { until = "2026-11-01", reason = "docs debt" }
pgtap = { until = "2026-09-01", reason = "no RLS tests yet, #300" }

[other]
pgtap = { until = "2099-01-01", reason = "decoy outside [baseline.relax]" }
`)

	docsOnly := writeManifest(t, `[baseline.relax]
documentation = { until = "2026-11-01", reason = "docs debt" }
`)

	noReason := writeManifest(t, `[baseline.relax]
pgtap = { until = "2026-09-01" }
`)

	for _, tc := range []struct {
		name     string
		manifest string
		today    string
		key      string
		want     string
	}{
		// An empty key is the default caller: every pre-existing workflow
		// invokes the script with no RELAX_KEY and must keep reading
		// `documentation`.
		{"default key stays documentation", both, "2026-08-04", "", "relaxed"},
		{"explicit documentation", both, "2026-08-04", "documentation", "relaxed"},

		{"pgtap before its date", both, "2026-08-04", "pgtap", "relaxed"},
		{"pgtap on its date", both, "2026-09-01", "pgtap", "relaxed"},
		{"pgtap after its date", both, "2026-09-02", "pgtap", "expired"},

		// The two keys expire on different dates, so reading one must never
		// return the other's state.
		{"keys do not bleed into each other", both, "2026-09-02", "documentation", "relaxed"},

		// A `pgtap` key in some other table is not a relaxation. If the awk
		// block stopped scoping to [baseline.relax] this would report the
		// decoy's far-future date as `relaxed`.
		{"decoy in another table is ignored", both, "2026-09-02", "pgtap", "expired"},

		{"absent key is none, not malformed", docsOnly, "2026-08-04", "pgtap", "none"},
		{"missing manifest is none", filepath.Join(t.TempDir(), "absent.toml"), "2026-08-04", "pgtap", "none"},

		// A relaxation with no reason is undocumented debt; `malformed` is a
		// hard failure in the workflows, deliberately not the same as `none`.
		{"missing reason is malformed", noReason, "2026-08-04", "pgtap", "malformed"},

		// The key reaches an awk regex, so a caller passing a pattern must be
		// rejected rather than matching whichever relaxation comes first.
		{"regex metacharacters rejected", both, "2026-08-04", ".*", "malformed"},
		{"uppercase rejected", both, "2026-08-04", "PGTAP", "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runRelax(t, tc.manifest, tc.today, tc.key); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}
