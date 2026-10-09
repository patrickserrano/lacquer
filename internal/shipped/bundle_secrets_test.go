package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// scripts/verify-bundle-secrets.sh fails when a bundle other than the app carries
// a key from the secrets template. The leak it catches is silent everywhere
// else: a watch app, an extension or a test bundle that inherits the app's
// Info.plist wiring ships a second copy of every service key, and it builds,
// signs, uploads and passes review.
//
// The tests below lay bundles out on disk with real Info.plists and run the
// shipped script, and then the RENDERED workflow steps, against them. plutil is
// the real one on a Mac; on Linux CI it is the plistlib stand-in from
// release_embedded_build_test.go, which implements the one form the script
// uses (`plutil -extract KEY raw -o - FILE`) and is held to the real plutil by
// TestFakePlutilAgreesWithPlutil and TestFakePlutilAgreesOnBundleSecretsShapes.

// leakedValue stands in for a real secret. It is planted in every bundle that
// carries a key, and no output may ever contain it.
const leakedValue = "VALUE-NEVER-PRINTED-7f3a"

// bundleSecretsTemplate defines two keys, one of them conditional, and the
// build wiring line that is not a key. The commented line must not count.
const bundleSecretsTemplate = `// Service keys. Copy to Secrets.xcconfig.
// COMMENTED_KEY = not a key
REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx
PROXY_SECRET[sdk=iphoneos*] = your-proxy-secret
INFOPLIST_FILE = App/Info.plist
`

// plistWith is an Info.plist with a bundle id and the named keys, each holding
// leakedValue.
func plistWith(id string, keys ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>` + id + `</string>
`)
	for _, k := range keys {
		b.WriteString("\t<key>" + k + "</key>\n\t<string>" + leakedValue + "</string>\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// plistWithDict carries key as a DICTIONARY. A check reading values as strings
// would miss it.
func plistWithDict(id, key string) string {
	return strings.Replace(plistWith(id),
		"</dict>\n</plist>",
		"\t<key>"+key+"</key>\n\t<dict>\n\t\t<key>v</key>\n\t\t<string>"+leakedValue+"</string>\n\t</dict>\n</dict>\n</plist>", 1)
}

// binaryPlist converts an XML plist to binary, the format a device build
// actually writes. plistlib is on both a Mac and Linux CI.
func binaryPlist(t *testing.T, xml string) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("python3 is needed to write a binary plist fixture")
	}
	cmd := exec.Command(py, "-I", "-c",
		"import plistlib,sys; sys.stdout.buffer.write(plistlib.dumps(plistlib.loads(sys.stdin.buffer.read()), fmt=plistlib.FMT_BINARY))")
	cmd.Stdin = strings.NewReader(xml)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("binary plist: %v", err)
	}
	return string(out)
}

// shippedApp is a built products directory: the app with both keys, a watch app
// and its complication, a widget, and a hosted test bundle in the app's PlugIns
// plus a standalone one beside it, as the Test job's products are laid out.
func shippedApp(over map[string]string) map[string]string {
	b := map[string]string{
		"Demo.app":                     plistWith("com.x.demo", "PROXY_SECRET", "REVENUECAT_API_KEY"),
		"Demo.app/Watch/DemoWatch.app": plistWith("com.x.demo.watchkitapp"),
		"Demo.app/Watch/DemoWatch.app/PlugIns/C.appex":      plistWith("com.x.demo.watchkitapp.complication"),
		"Demo.app/PlugIns/DemoWidget.appex":                 plistWith("com.x.demo.widget"),
		"Demo.app/PlugIns/DemoTests.xctest":                 plistWith("com.x.demo.tests"),
		"DemoUITests-Runner.app/PlugIns/DemoUITests.xctest": plistWith("com.x.demo.uitests"),
		"DemoUITests-Runner.app":                            plistWith("com.apple.test.DemoUITests-Runner"),
	}
	for k, v := range over {
		if v == "-" {
			delete(b, k)
			continue
		}
		b[k] = v
	}
	return b
}

// layOut writes bundles (path -> Info.plist; "" for a bundle with none) under
// dir.
func layOut(t *testing.T, dir string, bundles map[string]string) {
	t.Helper()
	for rel, plist := range bundles {
		b := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(b, 0o755); err != nil {
			t.Fatal(err)
		}
		if plist != "" {
			writeAt(t, filepath.Join(b, "Info.plist"), plist)
		}
	}
}

func bundleSecretsScript(t *testing.T) string {
	return filepath.Join(root(t), "profiles", "ios", "root", "scripts", "verify-bundle-secrets.sh")
}

// runBundleSecrets runs the shipped script. template "" writes the default
// fixture template; "-" writes none.
func runBundleSecrets(t *testing.T, template, app string, bundles map[string]string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "Secrets.xcconfig.example")
	switch template {
	case "":
		writeAt(t, tmpl, bundleSecretsTemplate)
	case "-":
	default:
		writeAt(t, tmpl, template)
	}
	products := filepath.Join(dir, "Products")
	if bundles != nil {
		layOut(t, products, bundles)
	}
	cmd := exec.Command("bash", bundleSecretsScript(t), tmpl, app, products)
	cmd.Env = append(os.Environ(), "PATH="+plutilPath(t))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("could not run the script: %v", err)
		}
		code = ee.ExitCode()
	}
	if strings.Contains(string(out), leakedValue) {
		t.Fatalf("the check printed a secret VALUE:\n%s", out)
	}
	return string(out), code
}

// The fail path first: a key planted in any non-app bundle, at any depth and in
// any plist format, is exit 1 and names the bundle and the key.
func TestBundleSecretsFailsOnAPlantedKey(t *testing.T) {
	for _, c := range []struct {
		name    string
		bundles map[string]string
		want    []string
	}{
		{"in the embedded watch app",
			shippedApp(map[string]string{"Demo.app/Watch/DemoWatch.app": plistWith("com.x.demo.watchkitapp", "PROXY_SECRET")}),
			[]string{"::error::Demo.app/Watch/DemoWatch.app carries secrets keys in its Info.plist: PROXY_SECRET (only Demo.app may)"}},
		{"in the watch app's own extension",
			shippedApp(map[string]string{"Demo.app/Watch/DemoWatch.app/PlugIns/C.appex": plistWith("c", "REVENUECAT_API_KEY")}),
			[]string{"::error::Demo.app/Watch/DemoWatch.app/PlugIns/C.appex carries secrets keys in its Info.plist: REVENUECAT_API_KEY"}},
		{"in a widget, as a binary plist",
			shippedApp(map[string]string{"Demo.app/PlugIns/DemoWidget.appex": binaryPlist(t, plistWith("w", "PROXY_SECRET"))}),
			[]string{"::error::Demo.app/PlugIns/DemoWidget.appex carries secrets keys in its Info.plist: PROXY_SECRET"}},
		{"in a hosted test bundle",
			shippedApp(map[string]string{"Demo.app/PlugIns/DemoTests.xctest": plistWith("t", "REVENUECAT_API_KEY")}),
			[]string{"::error::Demo.app/PlugIns/DemoTests.xctest carries secrets keys"}},
		{"in a standalone test runner",
			shippedApp(map[string]string{"DemoUITests-Runner.app": plistWith("r", "PROXY_SECRET")}),
			[]string{"::error::DemoUITests-Runner.app carries secrets keys"}},
		{"as a dictionary rather than a string",
			shippedApp(map[string]string{"Demo.app/PlugIns/DemoWidget.appex": plistWithDict("w", "PROXY_SECRET")}),
			[]string{"::error::Demo.app/PlugIns/DemoWidget.appex carries secrets keys in its Info.plist: PROXY_SECRET"}},
		{"every key, named once each",
			shippedApp(map[string]string{"Demo.app/Watch/DemoWatch.app": plistWith("w", "PROXY_SECRET", "REVENUECAT_API_KEY")}),
			[]string{"Info.plist: PROXY_SECRET REVENUECAT_API_KEY (only"}},
		// The app's name does not make a bundle the app: one embedded in another
		// bundle is checked like any other.
		{"in a bundle named like the app but embedded in it",
			shippedApp(map[string]string{"Demo.app/Watch/Demo.app": plistWith("w", "PROXY_SECRET")}),
			[]string{"::error::Demo.app/Watch/Demo.app carries secrets keys"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, code := runBundleSecrets(t, "", "Demo.app", c.bundles)
			if code != 1 {
				t.Fatalf("exit %d, want 1 (a leak):\n%s", code, out)
			}
			mustContain(t, "the output", out, c.want...)
			mustContain(t, "the output", out, "verify-bundle-secrets: FAIL")
		})
	}
}

// Each positive control, failing, is exit 2 and says which. A pass must never
// mean "found nothing to check".
func TestBundleSecretsControlsFailClosed(t *testing.T) {
	for _, c := range []struct {
		name     string
		template string
		app      string
		bundles  map[string]string
		want     string
	}{
		{name: "an empty key list", template: "// only comments\nINFOPLIST_FILE = App/Info.plist\n",
			bundles: shippedApp(nil), want: "no keys parsed from"},
		{name: "no template", template: "-", bundles: shippedApp(nil), want: "no secrets template at"},
		{name: "the app is not there", bundles: map[string]string{
			"DemoUITests-Runner.app":                            plistWith("r"),
			"DemoUITests-Runner.app/PlugIns/DemoUITests.xctest": plistWith("u"),
		}, want: "no Demo.app under"},
		{name: "the app carries none of the keys",
			bundles: shippedApp(map[string]string{"Demo.app": plistWith("com.x.demo")}),
			want:    "Demo.app carries none of the keys; the key list or the lookup is broken"},
		{name: "a bundle's plist is not a dictionary",
			bundles: shippedApp(map[string]string{"Demo.app/PlugIns/DemoWidget.appex": "junk"}),
			want:    "cannot read a CFBundleIdentifier from Demo.app/PlugIns/DemoWidget.appex's Info.plist"},
		{name: "a bundle's plist is garbage",
			bundles: shippedApp(map[string]string{"Demo.app/Watch/DemoWatch.app": "not a property list\x00\x01"}),
			want:    "cannot read a CFBundleIdentifier from Demo.app/Watch/DemoWatch.app's Info.plist"},
		{name: "a bundle with no Info.plist",
			bundles: shippedApp(map[string]string{"Demo.app/PlugIns/DemoWidget.appex": ""}),
			want:    "cannot read a CFBundleIdentifier from Demo.app/PlugIns/DemoWidget.appex's Info.plist"},
		// The app's Watch folder holds something the scan did not recognise as a
		// bundle, and nothing else was checked: the scan is what is broken.
		{name: "the app embeds something no bundle was checked for",
			bundles: map[string]string{"Demo.app": plistWith("com.x.demo", "PROXY_SECRET"), "Demo.app/Watch/DemoWatch": ""},
			want:    "Demo.app/Watch holds DemoWatch, but no bundle besides the app was checked"},
		{name: "no products directory", bundles: nil, want: "no directory at"},
		{name: "an app name that is not a bundle", app: "Demo", bundles: shippedApp(nil),
			want: "'Demo' is not an app bundle's file name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			app := c.app
			if app == "" {
				app = "Demo.app"
			}
			out, code := runBundleSecrets(t, c.template, app, c.bundles)
			if code != 2 {
				t.Fatalf("exit %d, want 2 (a control failed):\n%s", code, out)
			}
			mustContain(t, "the output", out, c.want)
		})
	}
}

func TestBundleSecretsPassesACleanProduct(t *testing.T) {
	out, code := runBundleSecrets(t, "", "Demo.app", shippedApp(nil))
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	mustContain(t, "the output", out,
		"secret keys (from ", ": PROXY_SECRET REVENUECAT_API_KEY\n",
		"app (allowed): Demo.app\n",
		"clean: Demo.app/Watch/DemoWatch.app\n",
		"clean: Demo.app/Watch/DemoWatch.app/PlugIns/C.appex\n",
		"clean: Demo.app/PlugIns/DemoWidget.appex\n",
		"clean: Demo.app/PlugIns/DemoTests.xctest\n",
		"clean: DemoUITests-Runner.app\n",
		"checked 6 non-app bundles, none carry secrets keys (1 Demo.app allowed)")
	if strings.Contains(out, "COMMENTED_KEY") || strings.Contains(out, "INFOPLIST_FILE") {
		t.Errorf("the key list took a comment or the build wiring line as a key:\n%s", out)
	}

	// A plain app embeds nothing, so nothing else can carry a copy. That is a
	// pass, and it says so rather than reporting zero bundles checked.
	out, code = runBundleSecrets(t, "", "Demo.app", map[string]string{"Demo.app": plistWith("com.x.demo", "PROXY_SECRET")})
	if code != 0 {
		t.Fatalf("a plain app: exit %d, want 0:\n%s", code, out)
	}
	mustContain(t, "the output", out, "Demo.app embeds no other bundle and none was built beside it")
}

// On a Mac, the stand-in must answer as plutil does for the shapes these tests
// rely on beyond TestFakePlutilAgreesWithPlutil's: a dictionary-valued key, a
// binary plist, and a plist whose root is not a dictionary.
func TestFakePlutilAgreesOnBundleSecretsShapes(t *testing.T) {
	real, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("no plutil to compare against; this runs on a Mac")
	}
	fake := filepath.Join(fakePlutil(t), "plutil")
	dir := t.TempDir()
	files := map[string]string{
		"dict":   plistWithDict("w", "PROXY_SECRET"),
		"binary": binaryPlist(t, plistWith("w", "PROXY_SECRET")),
		"junk":   "junk",
	}
	for name, body := range files {
		writeAt(t, filepath.Join(dir, name), body)
	}
	for _, c := range []struct{ file, key string }{
		{"dict", "PROXY_SECRET"}, {"dict", "REVENUECAT_API_KEY"}, {"dict", "CFBundleIdentifier"},
		{"binary", "PROXY_SECRET"}, {"binary", "REVENUECAT_API_KEY"},
		{"junk", "CFBundleIdentifier"},
	} {
		ok := func(bin string) bool {
			return exec.Command(bin, "-extract", c.key, "raw", "-o", "-", filepath.Join(dir, c.file)).Run() == nil
		}
		if r, f := ok(real), ok(fake); r != f {
			t.Errorf("%s %s: plutil ok=%v, stand-in ok=%v", c.file, c.key, r, f)
		}
	}
}

// bundleSecretsConfig is a lone app that declares secrets, with its template
// named explicitly, as U7 resolves it.
func bundleSecretsConfig() *config.Config {
	cfg := soloConfig()
	cfg.Project.Secrets = map[string]string{"REVENUECAT_API_KEY": "RC_KEY"}
	cfg.Project.SecretsFile = "Demo/Secrets.xcconfig"
	cfg.Project.SecretsExample = "Secrets.xcconfig.example"
	return cfg
}

// bundleSecretsSteps returns every rendered step that runs the script, keyed by
// job, in order.
func bundleSecretsSteps(t *testing.T, rendered string) map[string][]map[string]string {
	t.Helper()
	var doc struct {
		Jobs map[string]struct {
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered workflow is not valid YAML: %v", err)
	}
	out := map[string][]map[string]string{}
	for job, j := range doc.Jobs {
		for _, s := range j.Steps {
			step := map[string]string{}
			for k, v := range s {
				if str, ok := v.(string); ok {
					step[k] = str
				}
			}
			if strings.Contains(step["run"], "verify-bundle-secrets.sh") {
				out[job] = append(out[job], step)
			}
		}
	}
	return out
}

// A project declaring no secrets renders no step anywhere, and its release is
// byte-identical to the template without the token (its ci.yml is pinned by
// TestIOSCISingleProductRenderIsUnchanged).
func TestNoSecretsRendersNoBundleSecretsStep(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "{{IOS_RELEASE_BUNDLE_SECRETS}}") {
		t.Fatal("release.yml no longer carries the token; this test pins nothing")
	}
	for name, cfg := range map[string]*config.Config{"solo": soloConfig(), "two products": twoIOSProducts()} {
		for _, wf := range []string{"ci.yml", "release.yml"} {
			got := renderIOSWorkflow(t, wf, cfg, "")
			if strings.Contains(got, "verify-bundle-secrets") {
				t.Errorf("%s: %s renders a bundle-secrets step for a project declaring no secrets", name, wf)
			}
		}
		want := renderIOSWorkflowFrom(t, strings.ReplaceAll(string(raw), "{{IOS_RELEASE_BUNDLE_SECRETS}}", ""), cfg)
		if got := renderIOSWorkflow(t, "release.yml", cfg, ""); got != want {
			t.Errorf("%s: release.yml changed for a project declaring no secrets.\n%s", name, firstDiff(want, got))
		}
	}
}

func renderIOSWorkflowFrom(t *testing.T, template string, cfg *config.Config) string {
	t.Helper()
	out, missing := tokens.Substitute(template, tokens.Values(cfg, ""))
	if len(missing) > 0 {
		t.Fatalf("unsubstituted tokens: %v", missing)
	}
	return out
}

func TestBundleSecretsStepsRenderWhereProductsAreBuilt(t *testing.T) {
	cfg := bundleSecretsConfig()
	ci := bundleSecretsSteps(t, renderIOSWorkflow(t, "ci.yml", cfg, "ios/"))
	rel := bundleSecretsSteps(t, renderIOSWorkflow(t, "release.yml", cfg, "ios/"))

	want := map[string]string{
		"build-release": `scripts/verify-bundle-secrets.sh "ios/Secrets.xcconfig.example" "Demo.app" "ios/DerivedData/Build/Products/Release-iphoneos"`,
		"test":          `scripts/verify-bundle-secrets.sh "ios/Secrets.xcconfig.example" "Demo.app" "ios/DerivedData/Build/Products"`,
	}
	for job, run := range want {
		if len(ci[job]) != 1 {
			t.Fatalf("ci.yml %s: %d bundle-secrets steps, want 1", job, len(ci[job]))
		}
		if got := ci[job][0]["run"]; got != run {
			t.Errorf("ci.yml %s runs\n  %s\nwant\n  %s", job, got, run)
		}
		if ci[job][0]["if"] != "" {
			t.Errorf("ci.yml %s: a lone product's step is gated: %q", job, ci[job][0]["if"])
		}
	}
	if len(ci) != 2 {
		t.Errorf("bundle-secrets steps in ci.yml jobs %v, want build-release and test only", jobNames(ci))
	}
	if len(rel["build-and-deploy"]) != 1 {
		t.Fatalf("release: %d bundle-secrets steps, want 1", len(rel["build-and-deploy"]))
	}
	s := rel["build-and-deploy"][0]
	if got, w := s["run"], `scripts/verify-bundle-secrets.sh "ios/Secrets.xcconfig.example" "Demo.app" "$ARCHIVE_DIR/$PRODUCT_NAME.xcarchive/Products/Applications"`; got != w {
		t.Errorf("release runs\n  %s\nwant\n  %s", got, w)
	}
	if s["if"] != "matrix.product.name == 'Demo'" {
		t.Errorf("release step if = %q, want it gated on its matrix leg", s["if"])
	}

	// After the build it reads, in each job: a step before its build has nothing
	// to scan, and would fail its own control.
	for wf, pair := range map[string][2]string{
		"ci.yml":      {"Build Release", "Check non-app bundles for secrets keys"},
		"ci.yml#test": {"Run Tests", "Check test and embedded bundles for secrets keys"},
		"release.yml": {"Build and archive", "Check archived bundles for secrets keys (Demo)"},
	} {
		file, _, _ := strings.Cut(wf, "#")
		body := renderIOSWorkflow(t, file, cfg, "ios/")
		before, after := strings.Index(body, "- name: "+pair[0]+"\n"), strings.Index(body, "- name: "+pair[1]+"\n")
		if before < 0 || after < 0 || after < before {
			t.Errorf("%s: %q must follow %q (at %d, %d)", file, pair[1], pair[0], after, before)
		}
	}
}

// With two products, each leg checks its own app against its own template, and
// a product declaring no secrets gets no step.
func TestBundleSecretsStepsArePerProduct(t *testing.T) {
	cfg := twoIOSProducts()
	cfg.Product[1].Secrets = map[string]string{"ADMOB_APPLICATION_ID": "ADMOB_ID"}
	cfg.Product[1].SecretsFile = "Config/Monetization.xcconfig"
	for _, wf := range []string{"ci.yml", "release.yml"} {
		for job, st := range bundleSecretsSteps(t, renderIOSWorkflow(t, wf, cfg, "")) {
			if len(st) != 1 {
				t.Fatalf("%s %s: %d steps, want one, for the one product declaring secrets", wf, job, len(st))
			}
			if st[0]["if"] != "matrix.product.name == 'Free'" {
				t.Errorf("%s %s: if = %q, want the Free leg", wf, job, st[0]["if"])
			}
			mustContain(t, wf+" "+job, st[0]["run"], `"Config/Monetization.xcconfig.example" "Sample Reader Daily Free.app"`)
		}
	}
}

// The rendered steps, run as Actions runs them, against products laid out on
// disk: this proves the wiring (paths, quoting, the archive variables), not just
// the script.
func TestRenderedBundleSecretsStepsCatchAPlantedKey(t *testing.T) {
	cfg := bundleSecretsConfig()
	ci := bundleSecretsSteps(t, renderIOSWorkflow(t, "ci.yml", cfg, "ios/"))
	rel := bundleSecretsSteps(t, renderIOSWorkflow(t, "release.yml", cfg, "ios/"))
	for _, c := range []struct {
		name, run, products string
		env                 []string
	}{
		{"Build (Release)", ci["build-release"][0]["run"], "ios/DerivedData/Build/Products/Release-iphoneos", nil},
		{"Test", ci["test"][0]["run"], "ios/DerivedData/Build/Products/Debug-iphonesimulator", nil},
		{"release", rel["build-and-deploy"][0]["run"], "archives/Demo.xcarchive/Products/Applications",
			[]string{"PRODUCT_NAME=Demo"}},
	} {
		for _, planted := range []bool{false, true} {
			t.Run(c.name, func(t *testing.T) {
				dir := t.TempDir()
				script, err := os.ReadFile(bundleSecretsScript(t))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "scripts", "verify-bundle-secrets.sh"), script, 0o755); err != nil {
					t.Fatal(err)
				}
				writeAt(t, filepath.Join(dir, "ios", "Secrets.xcconfig.example"), bundleSecretsTemplate)
				over := map[string]string{}
				if planted {
					over["Demo.app/Watch/DemoWatch.app"] = plistWith("com.x.demo.watchkitapp", "PROXY_SECRET")
				}
				layOut(t, filepath.Join(dir, filepath.FromSlash(c.products)), shippedApp(over))
				env := append([]string{"PATH=" + plutilPath(t), "ARCHIVE_DIR=" + filepath.Join(dir, "archives")}, c.env...)
				out, err := runIn(t, dir, c.run, env...)
				if strings.Contains(out, leakedValue) {
					t.Fatalf("printed a secret value:\n%s", out)
				}
				if !planted {
					if err != nil {
						t.Fatalf("a clean product failed the rendered step: %v\n%s", err, out)
					}
					mustContain(t, "the output", out, "none carry secrets keys")
					return
				}
				if err == nil {
					t.Fatalf("a key planted in the watch app passed the rendered step:\n%s", out)
				}
				// The Test job scans all of Products, so its paths start one level up.
				mustContain(t, "the output", out, "::error::", "Demo.app/Watch/DemoWatch.app carries secrets keys in its Info.plist: PROXY_SECRET")
			})
		}
	}
}

// The script is executable, so the rendered `run: scripts/verify-bundle-secrets.sh`
// can exec it.
func TestBundleSecretsScriptIsExecutable(t *testing.T) {
	fi, err := os.Stat(bundleSecretsScript(t))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable (%v)", bundleSecretsScript(t), fi.Mode())
	}
	if b, _ := os.ReadFile(bundleSecretsScript(t)); !regexp.MustCompile(`\A#!/usr/bin/env bash\n`).Match(b) {
		t.Fatal("the script does not start with a bash shebang")
	}
}

func jobNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
