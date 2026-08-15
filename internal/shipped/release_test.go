package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// selectScript pulls the product-selection shell out of the rendered release
// workflow so it can be run directly.
//
// Testing the extracted script rather than asserting on YAML matters here: the
// thing that can be wrong is the jq filter and the fail-closed branch, and no
// amount of structural assertion exercises those. It is the same reason this
// repo extracts `run:` blocks and puts them through `bash -n`.
func selectScript(t *testing.T, cfg *config.Config) string {
	t.Helper()
	r := root(t)
	raw, err := os.ReadFile(filepath.Join(r, "profiles", "ios", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, ""))
	if len(missing) > 0 {
		t.Fatalf("unsubstituted tokens: %v", missing)
	}
	if m := lacquerToken.FindString(out); m != "" {
		t.Fatalf("rendered workflow still contains %s", m)
	}

	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				ID  string `yaml:"id"`
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered release workflow is not valid YAML: %v", err)
	}
	for _, st := range doc.Jobs["select-products"].Steps {
		if st.ID == "pick" {
			return st.Run
		}
	}
	t.Fatal("no `pick` step in select-products")
	return ""
}

// runSelect executes the selection script with a given trigger, returning the
// emitted matrix and whether it failed. product is the dispatch input; the
// workflow declares it in `env:`, so it is always set on a real run — including
// a tag push, where it defaults to `all`.
func runSelect(t *testing.T, script, event, ref, product string) (string, bool) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	out := filepath.Join(t.TempDir(), "gh_output")
	if err := os.WriteFile(out, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "EVENT_NAME="+event, "REF_NAME="+ref,
		"PRODUCT_INPUT="+product, "GITHUB_OUTPUT="+out)
	combined, err := cmd.CombinedOutput()
	data, _ := os.ReadFile(out)
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "matrix="); ok {
			return rest, err != nil
		}
	}
	return string(combined), err != nil
}

func twoProducts() *config.Config {
	return &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Product: []config.Product{
			{Name: "Paid", Scheme: "Paid", BundleID: "com.x.paid", AscAppID: "111", TagPrefix: "paid"},
			{Name: "Lite", Scheme: "Lite", BundleID: "com.x.lite", AscAppID: "222", TagPrefix: "lite"},
		},
	}
}

func namesIn(t *testing.T, matrix string) []string {
	t.Helper()
	re := regexp.MustCompile(`"name":"([^"]*)"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(matrix, -1) {
		out = append(out, m[1])
	}
	return out
}

// A tag releases exactly ONE product. An App Store version train closes
// permanently at READY_FOR_SALE, so a tag fanning out to both apps drags the
// already-shipped one through a closed train and fails with error 90186.
func TestTagSelectsOneProduct(t *testing.T) {
	script := selectScript(t, twoProducts())
	for tag, want := range map[string]string{
		"paid-v1.2.3": "Paid",
		"lite-v1.2.3": "Lite",
	} {
		matrix, failed := runSelect(t, script, "push", tag, "all")
		if failed {
			t.Errorf("%s: selection failed: %s", tag, matrix)
			continue
		}
		got := namesIn(t, matrix)
		if len(got) != 1 || got[0] != want {
			t.Errorf("tag %q selected %v, want exactly [%s]", tag, got, want)
		}
	}
}

// Fail closed. Guessing either signs a product with another app's credentials or
// pushes at a version train that has already closed.
func TestUnknownTagFailsClosed(t *testing.T) {
	script := selectScript(t, twoProducts())
	out, failed := runSelect(t, script, "push", "v1.2.3", "all")
	if !failed {
		t.Errorf("a tag matching no product must fail, got matrix %q", out)
	}
	// Assert the workflow's own marker, not just any failure: a crashing jq
	// filter would otherwise read as a correct fail-closed. Both negative tests
	// here did exactly that until the filter was fixed.
	if !strings.Contains(out, "::error::") {
		t.Errorf("want the workflow's own error, got: %s", out)
	}
}

// An operator watching the run may legitimately want every product.
func TestDispatchSelectsEveryProduct(t *testing.T) {
	script := selectScript(t, twoProducts())
	matrix, failed := runSelect(t, script, "workflow_dispatch", "main", "all")
	if failed {
		t.Fatalf("dispatch must not fail: %s", matrix)
	}
	if got := namesIn(t, matrix); len(got) != 2 {
		t.Errorf("dispatch selected %v, want both products", got)
	}
}

// The case that must not regress: twelve projects ship one app and declare
// nothing. Any tag must release it, exactly as before products existed.
func TestSingleProductMatchesAnyTag(t *testing.T) {
	cfg := &config.Config{Project: config.Project{
		ProjectName: "Solo", Scheme: "Solo", BundleID: "com.x.solo", AscAppID: "111", Xcodeproj: "Solo.xcodeproj",
	}}
	script := selectScript(t, cfg)
	for _, tag := range []string{"v1.0.0", "v2.3.4-beta", "release-2026"} {
		matrix, failed := runSelect(t, script, "push", tag, "all")
		if failed {
			t.Errorf("tag %q failed for a single-product project: %s", tag, matrix)
			continue
		}
		if got := namesIn(t, matrix); len(got) != 1 || got[0] != "Solo" {
			t.Errorf("tag %q selected %v, want [Solo]", tag, got)
		}
	}
}

// A prefix must match at the START of the tag, or `lite-v1` would also select a
// product prefixed `v1`, and two products would ship on one tag.
func TestPrefixMatchIsAnchored(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Product: []config.Product{
			{Name: "Alpha", Scheme: "Alpha", BundleID: "com.x.a", AscAppID: "1", TagPrefix: "alpha"},
		},
	}
	out, failed := runSelect(t, selectScript(t, cfg), "push", "release-alpha-v1", "all")
	if !failed {
		t.Errorf("a prefix appearing mid-tag must not match, got %q", out)
	}
	if !strings.Contains(out, "::error::") {
		t.Errorf("want the workflow's own error, got: %s", out)
	}
}

// A dispatch names the product it means. Inferring from the ref cannot work: a
// dispatch runs from a branch, whose name carries no product.
//
// This is the failure that was observed for real — a dispatch fanned out to both
// products, and the leg whose version had already reached READY_FOR_SALE failed
// with "Invalid Pre-Release Train. The train version '3.0' is closed for new
// build submissions".
func TestDispatchCanScopeToOneProduct(t *testing.T) {
	script := selectScript(t, twoProducts())
	matrix, failed := runSelect(t, script, "workflow_dispatch", "main", "Lite")
	if failed {
		t.Fatalf("scoped dispatch must not fail: %s", matrix)
	}
	got := namesIn(t, matrix)
	if len(got) != 1 || got[0] != "Lite" {
		t.Errorf("scoped dispatch selected %v, want [Lite]", got)
	}
}

// A named product that does not exist must not silently release everything.
func TestDispatchUnknownProductFailsClosed(t *testing.T) {
	out, failed := runSelect(t, selectScript(t, twoProducts()), "workflow_dispatch", "main", "Typo")
	if !failed {
		t.Errorf("an unknown product name must fail, got %q", out)
	}
	if !strings.Contains(out, "::error::") {
		t.Errorf("want the workflow's own error, got: %s", out)
	}
}
