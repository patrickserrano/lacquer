package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadTree writes manifest into a scratch project with dirs created, and loads
// it. The prefix check reads the tree, so the tree is part of the fixture.
func loadTree(t *testing.T, manifest string, dirs ...string) (*Config, error) {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, ".lacquer.toml")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func iosComponent(projectKeys string) string {
	return "[project]\nname = \"x\"\n" + projectKeys + "\n\n[[component]]\npath = \"ios\"\nprofiles = [\"ios\"]\n"
}

// secrets_file is relative to the COMPONENT. A value that repeats the
// component's own path renders ios/ios/…: the writer creates that directory,
// writes the real keys into it, and the archive reads the file it was always
// going to read — which still holds placeholders.
func TestSecretsFileRepeatingTheComponentPrefixIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, key, value, want string }{
		{"secrets_file", "secrets_file", "ios/App/Secrets.xcconfig", `secrets_file = "App/Secrets.xcconfig"`},
		{"secrets_file at the component root", "secrets_file", "ios/Secrets.xcconfig", `secrets_file = "Secrets.xcconfig"`},
		{"secrets_example", "secrets_example", "ios/Secrets.xcconfig.example", `secrets_example = "Secrets.xcconfig.example"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys := "secrets = { K = \"K\" }\n" + tc.key + " = \"" + tc.value + "\""
			_, err := loadTree(t, iosComponent(keys), "ios/App")
			if err == nil {
				t.Fatalf("loaded %s = %q under component ios/, which names ios/%s", tc.key, tc.value, tc.value)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not show the corrected value %s:\n%v", tc.want, err)
			}
		})
	}
}

// Xcode's default layout puts the app's sources in a folder named like the
// project: App/App/. Under component App/, secrets_file = "App/Secrets.xcconfig"
// is CORRECT and starts with the component's name. The tree decides it.
func TestSecretsFileInAFolderNamedLikeTheComponentLoads(t *testing.T) {
	m := "[project]\nname = \"x\"\nsecrets = { K = \"K\" }\nsecrets_file = \"App/Secrets.xcconfig\"\n\n[[component]]\npath = \"App\"\nprofiles = [\"ios\"]\n"
	if _, err := loadTree(t, m, "App/App"); err != nil {
		t.Fatalf("a correct secrets_file in Xcode's default App/App layout was rejected: %v", err)
	}
}

// A project with no code yet has neither directory. Nothing in the tree says
// the value is wrong, so it loads: refusing a pre-code project for a file it
// cannot have yet is the defect this repository's CLAUDE.md records.
func TestSecretsFileWithNoTreeToJudgeByLoads(t *testing.T) {
	if _, err := loadTree(t, iosComponent("secrets = { K = \"K\" }\nsecrets_file = \"ios/App/Secrets.xcconfig\"")); err != nil {
		t.Fatalf("a pre-code project was refused: %v", err)
	}
}

// The [[product]] spelling gets the same check.
func TestProductSecretsFileRepeatingThePrefixIsRejected(t *testing.T) {
	m := "[project]\nname = \"x\"\n\n[[product]]\nname = \"Free\"\nscheme = \"Free\"\nbundle_id = \"com.x.free\"\nasc_app_id = \"1\"\nsecrets = { K = \"K\" }\nsecrets_file = \"ios/App/Secrets.xcconfig\"\n\n[[component]]\npath = \"ios\"\nprofiles = [\"ios\"]\n"
	if _, err := loadTree(t, m, "ios/App"); err == nil || !strings.Contains(err.Error(), `secrets_file = "App/Secrets.xcconfig"`) {
		t.Fatalf("err = %v, want the corrected value", err)
	}
}

func TestSecretsExampleValidation(t *testing.T) {
	for _, tc := range []struct{ name, keys, want string }{
		{"without secrets", `secrets_example = "Secrets.xcconfig.example"`, "no secrets declared"},
		{"absolute", "secrets = { K = \"K\" }\nsecrets_example = \"/etc/x\"", "relative path"},
		{"traversal", "secrets = { K = \"K\" }\nsecrets_example = \"../x\"", "relative path"},
		{"the destination itself", "secrets = { K = \"K\" }\nsecrets_example = \"Secrets.xcconfig\"", "secrets_file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, "[project]\nname = \"x\"\n"+tc.keys+"\n")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
	cfg, err := loadString(t, "[project]\nname = \"x\"\nsecrets = { K = \"K\" }\nsecrets_file = \"App/Secrets.xcconfig\"\nsecrets_example = \"Secrets.xcconfig.example\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Products()[0].SecretsTemplate(); got != "Secrets.xcconfig.example" {
		t.Errorf("SecretsTemplate() = %q — [project].secrets_example did not reach the synthesised product", got)
	}
	cfg, err = loadString(t, "[project]\nname = \"x\"\nsecrets = { K = \"K\" }\nsecrets_file = \"App/Secrets.xcconfig\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Products()[0].SecretsTemplate(); got != "App/Secrets.xcconfig.example" {
		t.Errorf("SecretsTemplate() = %q, want the file beside the destination", got)
	}
}

// Two products sharing one destination with different templates would make the
// rendered workflow pick one by order.
func TestSharedSecretsFileWithDifferentTemplatesIsRejected(t *testing.T) {
	m := `[project]
name = "x"

[[product]]
name = "A"
scheme = "A"
bundle_id = "com.x.a"
asc_app_id = "1"
tag_prefix = "a"
secrets = { K = "K" }
secrets_example = "One.example"

[[product]]
name = "B"
scheme = "B"
bundle_id = "com.x.b"
asc_app_id = "2"
tag_prefix = "b"
secrets = { K = "K2" }
secrets_example = "Two.example"
`
	if _, err := loadString(t, m); err == nil || !strings.Contains(err.Error(), "Secrets.xcconfig") {
		t.Fatalf("err = %v, want a refusal naming the shared destination", err)
	}
}
