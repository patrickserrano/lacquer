package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".lacquer.toml")
	data := `
[project]
name = "journalcast"

[[component]]
path = "ios"
profiles = ["ios"]

[[component]]
path = "dashboard"
profiles = ["web"]
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Name != "journalcast" {
		t.Errorf("project name = %q, want journalcast", cfg.Project.Name)
	}
	if len(cfg.Components) != 2 {
		t.Fatalf("got %d components, want 2", len(cfg.Components))
	}
	if cfg.Components[0].Path != "ios" || cfg.Components[0].Profiles[0] != "ios" {
		t.Errorf("component[0] = %+v", cfg.Components[0])
	}
	if cfg.Components[1].Path != "dashboard" || cfg.Components[1].Profiles[0] != "web" {
		t.Errorf("component[1] = %+v", cfg.Components[1])
	}
}

// loadString writes data to a temp .lacquer.toml and loads it.
func loadString(t *testing.T, data string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".lacquer.toml")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadRejectsTraversalComponentPath(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"../escape\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"../../etc\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"/abs/path\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios/../../up\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"\"\nprofiles=[\"ios\"]\n",
	}
	for _, data := range cases {
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected error for component path in:\n%s", data)
		}
	}
}

func TestLoadRejectsInvalidProfileName(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"../evil\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"a/b\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"..\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"UPPER\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"\"]\n",
	}
	for _, data := range cases {
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected error for profile name in:\n%s", data)
		}
	}
}

func TestLoadAcceptsValidNames(t *testing.T) {
	data := "[project]\nname=\"x\"\n\n[[component]]\npath=\"apps/ios-app\"\nprofiles=[\"ios\",\"web-2\"]\n"
	if _, err := loadString(t, data); err != nil {
		t.Errorf("valid manifest rejected: %v", err)
	}
}

func TestLoadProjectValues(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"acme\"\nproject_name=\"Acme\"\nscheme=\"Acme\"\nbundle_id=\"com.me.acme\"\nasc_app_id=\"6451234567\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Project
	if p.ProjectName != "Acme" || p.Scheme != "Acme" || p.BundleID != "com.me.acme" || p.AscAppID != "6451234567" {
		t.Errorf("project = %+v", p)
	}
}

func TestLoadAllowsBlankProjectValues(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\nbundle_id=\"\"\n"); err != nil {
		t.Errorf("blank values must be allowed (init stubs them): %v", err)
	}
}

func TestEffectiveToolsDefaultsToClaude(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Project.EffectiveTools()
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("EffectiveTools() = %v, want [claude]", got)
	}
}

func TestLoadAcceptsKnownTools(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\ntools=[\"claude\",\"codex\",\"antigravity\"]\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Project.EffectiveTools()) != 3 {
		t.Errorf("tools = %v", cfg.Project.EffectiveTools())
	}
}

func TestLoadRejectsUnknownTool(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\ntools=[\"claude\",\"../evil\"]\n"); err == nil {
		t.Error("expected rejection of an unknown/unsafe tool name")
	}
}

func TestLoadGithubOrg(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\ngithub_org=\"AcmeOrg\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.GithubOrg != "AcmeOrg" {
		t.Errorf("github_org = %q", cfg.Project.GithubOrg)
	}
}

func TestLoadRejectsUnsafeGithubOrg(t *testing.T) {
	for _, bad := range []string{"a b", "a/b", "-evil", "a;b", "a--", "a-"} {
		if _, err := loadString(t, "[project]\nname=\"x\"\ngithub_org=\""+bad+"\"\n"); err == nil {
			t.Errorf("expected rejection of github_org %q", bad)
		}
	}
}

func TestExcludesMatching(t *testing.T) {
	p := Project{Exclude: []Exclusion{{Path: ".github/workflows/"}, {Path: "Brewfile"}}}
	cases := map[string]bool{
		".github/workflows/ios-ci.yml": true,
		".github/workflows":            true,
		"Brewfile":                     true,
		".github/workflowsX":           false, // prefix must be a path boundary
		".claude/skills/git.md":        false,
	}
	for dest, want := range cases {
		if got := p.Excludes(dest); got != want {
			t.Errorf("Excludes(%q) = %v, want %v", dest, got, want)
		}
	}
}

// Every manifest in the fleet spells exclude as a bare string array. Upgrading
// the lacquer must never be the thing that stops a project loading.
func TestLoadAcceptsBareStringExclude(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\nexclude=[\"typedoc.json\", \".github/workflows/\"]\n")
	if err != nil {
		t.Fatalf("bare-string exclude must keep loading: %v", err)
	}
	if len(cfg.Project.Exclude) != 2 || cfg.Project.Exclude[0].Path != "typedoc.json" {
		t.Fatalf("Exclude = %+v", cfg.Project.Exclude)
	}
	if cfg.Project.Exclude[0].Attributed() {
		t.Error("a bare string carries no reason, so it must not read as attributed")
	}
	if !cfg.Project.Excludes(".github/workflows/web-ci.yml") {
		t.Error("matching must behave exactly as it did before the type changed")
	}
}

func TestLoadAcceptsAttributedExclude(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\n"+
		"exclude=[{path=\"a.yml\", reason=\"macOS-only\"}, {path=\"b.yml\", reason=\"pending\", until=\"2026-12-01\"}]\n")
	if err != nil {
		t.Fatalf("attributed exclude must load: %v", err)
	}
	if len(cfg.Project.Exclude) != 2 {
		t.Fatalf("Exclude = %+v", cfg.Project.Exclude)
	}
	if !cfg.Project.Exclude[0].Attributed() || cfg.Project.Exclude[1].Until != "2026-12-01" {
		t.Errorf("Exclude = %+v", cfg.Project.Exclude)
	}
	if !cfg.Project.Excludes("a.yml") {
		t.Error("the table form must match like the string form")
	}
}

// A typo'd key would otherwise be dropped in silence, quietly demoting an
// attributed exclusion back to an unattributed one — the exact failure the
// structured form exists to prevent.
func TestLoadRejectsUnknownExcludeKey(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\nexclude=[{path=\"a.yml\", resaon=\"typo\"}]\n"); err == nil {
		t.Error("expected rejection of an unknown [project].exclude key")
	}
}

// An expiry nobody can interpret cannot be reviewed when it fires.
func TestLoadRejectsUntilWithoutReason(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\nexclude=[{path=\"a.yml\", until=\"2026-12-01\"}]\n"); err == nil {
		t.Error("expected rejection of an until with no reason")
	}
}

func TestLoadRejectsMalformedUntil(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\nexclude=[{path=\"a.yml\", reason=\"r\", until=\"soon\"}]\n"); err == nil {
		t.Error("expected rejection of a non-YYYY-MM-DD until")
	}
}

// Expiry is evaluated at audit time, never at load: load runs inside `sync` and
// `fix`, and a manifest that will not load is one whose own repair tooling is
// unavailable. An expired exemption must block CI, not lock the project out of
// the commands that fix it.
func TestLoadAcceptsExpiredExclude(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\nexclude=[{path=\"a.yml\", reason=\"r\", until=\"2020-01-01\"}]\n"); err != nil {
		t.Errorf("an expired exclusion must still load (audit gates it, not load): %v", err)
	}
}

func TestLoadRejectsUnsafeExclude(t *testing.T) {
	for _, bad := range []string{"../etc", "/abs", "a/../../b"} {
		data := "[project]\nname=\"x\"\nexclude=[\"" + bad + "\"]\n"
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected rejection of unsafe exclude %q", bad)
		}
	}
}

func TestLoadRejectsInjectionInProjectValues(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\nscheme=\"Acme\\n  evil: true\"\n",
		"[project]\nname=\"x\"\nbundle_id=\"com.me.$(whoami)\"\n",
		"[project]\nname=\"x\"\nproject_name=\"Acme`id`\"\n",
		"[project]\nname=\"x\"\nasc_app_id=\"12a34\"\n",
		"[project]\nname=\"x\"\nscheme=\"a\\\"b\"\n",
	}
	for _, data := range cases {
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected rejection for project value in:\n%s", data)
		}
	}
}

func TestLoadRejectsDuplicateProfile(t *testing.T) {
	data := "[project]\nname=\"x\"\n\n[[component]]\npath=\"a\"\nprofiles=[\"ios\"]\n\n[[component]]\npath=\"b\"\nprofiles=[\"ios\"]\n"
	if _, err := loadString(t, data); err == nil {
		t.Fatal("expected error: two components declare profile ios")
	}
}

func TestLoadRejectsUnsafeComponentPath(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios;rm -rf\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios app\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios$(x)\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"-rf\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"apps/-evil\"\nprofiles=[\"ios\"]\n",
	}
	for _, d := range cases {
		if _, err := loadString(t, d); err == nil {
			t.Errorf("expected rejection for unsafe component path in:\n%s", d)
		}
	}
}

func TestLoadAllowsNestedAndRootComponentPaths(t *testing.T) {
	for _, p := range []string{".", "ios", "apps/ios-app"} {
		data := "[project]\nname=\"x\"\n\n[[component]]\npath=\"" + p + "\"\nprofiles=[\"ios\"]\n"
		if _, err := loadString(t, data); err != nil {
			t.Errorf("path %q should be valid: %v", p, err)
		}
	}
}

func TestLoadXcodeproj(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"q\"\nxcodeproj=\"ios/Queueify/Queueify.xcodeproj\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Xcodeproj != "ios/Queueify/Queueify.xcodeproj" {
		t.Errorf("xcodeproj = %q", cfg.Project.Xcodeproj)
	}
}

func TestLoadRejectsUnsafeXcodeproj(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\nxcodeproj=\"/abs/App.xcodeproj\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"../escape/App.xcodeproj\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"ios/$(x).xcodeproj\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"ios/App.xcodeproj; rm -rf\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"ios/App\"\n",
	}
	for _, d := range cases {
		if _, err := loadString(t, d); err == nil {
			t.Errorf("expected rejection for xcodeproj in:\n%s", d)
		}
	}
}

func TestLoadAllowsBlankXcodeproj(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\n"); err != nil {
		t.Errorf("blank xcodeproj must be allowed: %v", err)
	}
}

func TestLoadRejectsUnsafeProjectName(t *testing.T) {
	for _, n := range []string{`--public`, `a;rm -rf`, `a$(x)`, "a\nb", `-x`} {
		data := "[project]\nname=\"" + n + "\"\n"
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected rejection for project name %q", n)
		}
	}
	if _, err := loadString(t, "[project]\nname=\"Acme\"\n"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
}

func TestLoadSwiftVersion(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\nswift_version=\"6.2\"\n")
	if err != nil || cfg.Project.SwiftVersion != "6.2" {
		t.Fatalf("swift_version=%q err=%v", cfg.Project.SwiftVersion, err)
	}
	if _, err := loadString(t, "[project]\nname=\"x\"\nswift_version=\"6\"\n"); err != nil {
		t.Errorf("single-component version should be allowed: %v", err)
	}
	for _, v := range []string{"6.2; rm", "6.x", "$(id)", "6.2 "} {
		if _, err := loadString(t, "[project]\nname=\"x\"\nswift_version=\""+v+"\"\n"); err == nil {
			t.Errorf("expected rejection for swift_version %q", v)
		}
	}
}

func TestLoadSkills(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\nskills=[\"dpearson2699/swift-ios-skills@healthkit\", \"patrickserrano/lacquer@security-review\"]\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entries, err := cfg.Project.ParsedSkills()
	if err != nil {
		t.Fatalf("ParsedSkills: %v", err)
	}
	want := []SkillEntry{
		{Source: "dpearson2699/swift-ios-skills", Name: "healthkit"},
		{Source: "patrickserrano/lacquer", Name: "security-review"},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for i, w := range want {
		if entries[i] != w {
			t.Errorf("entry[%d] = %+v, want %+v", i, entries[i], w)
		}
	}
	if got := entries[0].String(); got != "dpearson2699/swift-ios-skills@healthkit" {
		t.Errorf("String() = %q", got)
	}
}

func TestLoadRejectsMalformedSkillEntries(t *testing.T) {
	cases := []string{
		"norepo@skill",                    // owner/repo missing a slash
		"owner/repo",                      // missing "@skill"
		"owner/repo@Skill-Name",           // skill name must be lowercase
		"owner/repo@-leading-hyphen",      // skill name can't start with a hyphen
		"-flag/repo@skill",                // owner can't start with a hyphen (flag injection)
		"owner/repo@skill; rm -rf /",      // shell metacharacters
		"owner/repo@skill\\n  evil: true", // newline injection
	}
	for _, c := range cases {
		if _, err := loadString(t, "[project]\nname=\"x\"\nskills=[\""+c+"\"]\n"); err == nil {
			t.Errorf("expected rejection for skills entry %q", c)
		}
	}
}

func loadWith(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".lacquer.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadBaselineRelax(t *testing.T) {
	cfg, err := loadWith(t, `
[project]
name = "throughline"

[baseline.relax]
swift_version = { until = "2026-09-01", reason = "pre-Swift-6 audio engine, #142" }
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r, ok := cfg.Baseline.Relax["swift_version"]
	if !ok {
		t.Fatalf("no relaxation parsed, got %+v", cfg.Baseline.Relax)
	}
	if r.Until != "2026-09-01" || r.Reason != "pre-Swift-6 audio engine, #142" {
		t.Errorf("relaxation = %+v", r)
	}
}

// An unknown relax key must be rejected. Silently ignoring a typo like
// "swift_verison" would create a permanent invisible exemption — the exact
// failure mode the baseline exists to prevent, in a new costume.
func TestLoadBaselineRelaxRejectsUnknownKey(t *testing.T) {
	_, err := loadWith(t, `
[project]
name = "throughline"

[baseline.relax]
swift_verison = { until = "2026-09-01", reason = "typo" }
`)
	if err == nil {
		t.Fatal("want error for an unknown relax key, got nil")
	}
	if !strings.Contains(err.Error(), "swift_verison") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
}

func TestLoadBaselineRelaxRequiresReason(t *testing.T) {
	if _, err := loadWith(t, `
[project]
name = "throughline"

[baseline.relax]
swift_version = { until = "2026-09-01" }
`); err == nil {
		t.Fatal("want error for a relaxation with no reason, got nil")
	}
}

func TestLoadBaselineRelaxRequiresUntil(t *testing.T) {
	if _, err := loadWith(t, `
[project]
name = "throughline"

[baseline.relax]
swift_version = { reason = "no expiry" }
`); err == nil {
		t.Fatal("want error for a relaxation with no expiry, got nil")
	}
}

func TestLoadBaselineRelaxRejectsMalformedDate(t *testing.T) {
	if _, err := loadWith(t, `
[project]
name = "throughline"

[baseline.relax]
swift_version = { until = "Sept 1st", reason = "r" }
`); err == nil {
		t.Fatal("want error for an unparseable until date, got nil")
	}
}

// No [baseline.relax] block at all is the normal case: the standard applies.
func TestLoadNoBaselineBlock(t *testing.T) {
	cfg, err := loadWith(t, "[project]\nname = \"throughline\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Baseline.Relax) != 0 {
		t.Errorf("Relax = %+v, want empty", cfg.Baseline.Relax)
	}
}

func TestBaselineTargets(t *testing.T) {
	cfg, err := loadWith(t, `
[project]
name = "throughline"
xcodeproj = "ios/Throughline.xcodeproj"

[[component]]
path = "ios"
profiles = ["ios"]

[[component]]
path = "server"
profiles = ["supabase"]
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.BaselineTargets()
	if len(got) != 2 {
		t.Fatalf("got %d targets, want 2: %+v", len(got), got)
	}
	// The xcodeproj belongs to the component that contains it, not to every
	// component — a server component must not be handed the iOS project.
	for _, tgt := range got {
		switch tgt.Component {
		case "ios":
			if tgt.Xcodeproj != "ios/Throughline.xcodeproj" {
				t.Errorf("ios target xcodeproj = %q", tgt.Xcodeproj)
			}
		case "server":
			if tgt.Xcodeproj != "" {
				t.Errorf("server target should carry no xcodeproj, got %q", tgt.Xcodeproj)
			}
		}
	}
}

// A root-layout project (component ".") still owns its xcodeproj.
func TestBaselineTargetsRootLayout(t *testing.T) {
	cfg, err := loadWith(t, `
[project]
name = "app"
xcodeproj = "App.xcodeproj"

[[component]]
path = "."
profiles = ["ios"]
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.BaselineTargets()
	if len(got) != 1 || got[0].Xcodeproj != "App.xcodeproj" {
		t.Errorf("targets = %+v", got)
	}
}

// A component with no profiles produces no target.
func TestBaselineTargetsSkipsProfilelessComponent(t *testing.T) {
	cfg, err := loadWith(t, `
[project]
name = "app"

[[component]]
path = "server"
profiles = []
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.BaselineTargets(); len(got) != 0 {
		t.Errorf("targets = %+v, want none", got)
	}
}

// An Xcode project named after a human-readable app title is ordinary, and
// rejecting it locked the oldest app in the fleet out of lacquer entirely.
// Spaces are allowed ONLY because every {{XCODEPROJ}} substitution site is
// quoted; everything a shell would act on is still refused.
func TestXcodeprojAllowsSpacesButNotMetacharacters(t *testing.T) {
	for _, tc := range []struct {
		val string
		ok  bool
	}{
		{"A Bible Verse Each Day.xcodeproj", true},
		{"App.xcodeproj", true},
		{"ios/My App.xcodeproj", true},
		{"", true}, // blank is allowed; sync fails closed if the token is used

		{"App\".xcodeproj", false},
		{"App'.xcodeproj", false},
		{"App$(whoami).xcodeproj", false},
		{"App`id`.xcodeproj", false},
		{"App;rm -rf /.xcodeproj", false},
		{"App|tee.xcodeproj", false},
		{"App&.xcodeproj", false},
		{"App\\x.xcodeproj", false},
		{"App\n.xcodeproj", false},
		{"-rf.xcodeproj", false},      // must not read as a flag
		{"ios/-rf.xcodeproj", false},  // ...in any segment
		{"/abs/App.xcodeproj", false}, // absolute
		{"../App.xcodeproj", false},   // escapes the root
		{"App.xcworkspace", false},    // wrong extension
	} {
		err := validateXcodeproj(tc.val)
		if (err == nil) != tc.ok {
			t.Errorf("validateXcodeproj(%q) error=%v, want ok=%v", tc.val, err, tc.ok)
		}
	}
}
