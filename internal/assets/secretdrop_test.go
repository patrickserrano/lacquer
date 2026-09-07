package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// secretDropFixture builds a lacquer home shipping one workflow and a committed
// project already holding a LOCAL version of it, whose body the caller chooses.
//
// The project is committed clean so the dirty guard cannot be what refuses —
// that guard would refuse the interesting cases for the wrong reason and this
// test would prove nothing about secrets.
func secretDropFixture(t *testing.T, shipped, local string) (string, []Asset) {
	t.Helper()
	h := t.TempDir()
	project := t.TempDir()
	dest := filepath.Join(".github", "workflows", "ios-release.yml")
	write(t, filepath.Join(h, "profiles", "ios", "workflows", "release.yml"), shipped)
	write(t, filepath.Join(project, dest), local)
	gitInit(t, project)
	gitRun(t, project, "commit", "-qm", "init")

	cfg := &config.Config{
		Project: config.Project{
			ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj",
			SwiftVersion: "6",
		},
		Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
	}
	plan, err := Plan(h, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out []Asset
	for _, a := range plan {
		if a.Dest == filepath.ToSlash(dest) || a.Dest == dest {
			out = append(out, a)
		}
	}
	if len(out) != 1 {
		t.Fatalf("fixture planned %d release workflows, want 1 (dests: %v)", len(out), plan)
	}
	return project, out
}

func cfgForDrop() *config.Config {
	return &config.Config{Project: config.Project{
		ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj", SwiftVersion: "6",
	}}
}

// The whole point. Rail's release read six app-runtime secrets; the sync that
// replaced it with the shared workflow read none of them, said nothing, and the
// next archive reached App Review with every key unset.
func TestPreflightRefusesToDropASecretAWorkflowReads(t *testing.T) {
	local := "jobs:\n  b:\n    steps:\n      - env:\n" +
		"          REVENUECAT_API_KEY: ${{ secrets.REVENUECAT_API_KEY }}\n" +
		"          SUPABASE_URL: ${{ secrets.SUPABASE_URL }}\n"
	shipped := "jobs:\n  b:\n    steps:\n      - env:\n          ASC_KEY_ID: ${{ secrets.ASC_KEY_ID }}\n"

	project, plan := secretDropFixture(t, shipped, local)
	_, err := Preflight(project, plan, cfgForDrop())
	if err == nil {
		t.Fatal("preflight allowed a sync that stops the release reading two provisioned secrets")
	}
	for _, want := range []string{"REVENUECAT_API_KEY", "SUPABASE_URL", "ios-release.yml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s:\n%v", want, err)
		}
	}
	// The way out has to be in the message, or the next person's only option is
	// to delete the guard.
	if !strings.Contains(err.Error(), "[[product]]") || !strings.Contains(err.Error(), "exclude") {
		t.Errorf("the refusal does not say how to proceed deliberately:\n%v", err)
	}
}

// The permissive half. A guard that refused everything would satisfy the test
// above on its own.
func TestPreflightAllowsAWorkflowThatKeepsItsSecrets(t *testing.T) {
	local := "jobs:\n  b:\n    steps:\n      - env:\n          ASC_KEY_ID: ${{ secrets.ASC_KEY_ID }}\n"
	shipped := "jobs:\n  b:\n    steps:\n      - env:\n" +
		"          ASC_KEY_ID: ${{ secrets.ASC_KEY_ID }}\n" +
		"          ASC_ISSUER_ID: ${{ secrets.ASC_ISSUER_ID }}\n"

	project, plan := secretDropFixture(t, shipped, local)
	if _, err := Preflight(project, plan, cfgForDrop()); err != nil {
		t.Fatalf("preflight refused a sync that ADDS a secret and drops none: %v", err)
	}
}

// GITHUB_TOKEN is minted per run. Refusing a sync because a workflow stopped
// mentioning it would fire on ordinary edits and teach people to ignore this.
func TestPreflightIgnoresTheAutoProvisionedToken(t *testing.T) {
	local := "jobs:\n  b:\n    steps:\n      - env:\n          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}\n"
	shipped := "jobs:\n  b:\n    steps:\n      - run: true\n"

	project, plan := secretDropFixture(t, shipped, local)
	if _, err := Preflight(project, plan, cfgForDrop()); err != nil {
		t.Fatalf("preflight refused a sync over GITHUB_TOKEN alone: %v", err)
	}
}

// A workflow the project does not have yet drops nothing — this is the ordinary
// first sync, and it must not be refused.
func TestPreflightAllowsAWorkflowThatDoesNotExistYet(t *testing.T) {
	shipped := "jobs:\n  b:\n    steps:\n      - env:\n          ASC_KEY_ID: ${{ secrets.ASC_KEY_ID }}\n"
	project, plan := secretDropFixture(t, shipped, "placeholder\n")
	if err := os.Remove(filepath.Join(project, ".github", "workflows", "ios-release.yml")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, project, "commit", "-qam", "remove")
	if _, err := Preflight(project, plan, cfgForDrop()); err != nil {
		t.Fatalf("preflight refused a first sync: %v", err)
	}
}

// The names have to come from the RENDERED lacquer version, not the template.
// The release workflow's secret step is generated from [[product]].secrets, so
// a template read raw carries none of them — and every project declaring
// product secrets would be refused forever.
func TestPreflightComparesAgainstTheRenderedVersion(t *testing.T) {
	local := "jobs:\n  b:\n    steps:\n      - env:\n          ADMOB_APPLICATION_ID: ${{ secrets.ABV_ADMOB_APP_ID }}\n"
	shipped := "jobs:\n  b:\n    steps:\n{{IOS_PRODUCT_SECRETS}}\n"

	project, plan := secretDropFixture(t, shipped, local)
	cfg := cfgForDrop()
	cfg.Product = []config.Product{{
		Name: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1",
		Secrets: map[string]string{"ADMOB_APPLICATION_ID": "ABV_ADMOB_APP_ID"},
	}}
	if _, err := Preflight(project, plan, cfg); err != nil {
		t.Fatalf("preflight refused a sync whose rendered workflow DOES read the secret: %v", err)
	}
}
