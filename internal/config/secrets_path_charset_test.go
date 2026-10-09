package config

import (
	"strings"
	"testing"
)

// secrets_file and secrets_example are spliced, double-quoted, into rendered
// shell: the release writer's arguments and the bundle-secrets check. Double
// quotes stop a space and nothing else, so a $ or a backtick in either would
// expand on the runner. A space is ordinary in a folder name and stays allowed.
func TestSecretsPathsRefuseShellMetacharacters(t *testing.T) {
	for _, field := range []string{"secrets_file", "secrets_example"} {
		for _, bad := range []string{`App/$(id).xcconfig`, "App/`id`.xcconfig", `App/$HOME.xcconfig`, `App/a;b.xcconfig`, `App/a"b.xcconfig`} {
			t.Run(field+" "+bad, func(t *testing.T) {
				m := "[project]\nname = \"x\"\nsecrets = { K = \"K\" }\n" + field + " = '" + bad + "'\n"
				_, err := loadString(t, m)
				if err == nil || !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), "unsafe in a shell command") {
					t.Fatalf("%s = %q: err = %v, want a refusal naming the field", field, bad, err)
				}
			})
		}
	}
	for _, field := range []string{"secrets_file", "secrets_example"} {
		m := "[project]\nname = \"x\"\nsecrets = { K = \"K\" }\n" + field + " = \"My App/Config.xcconfig\"\n"
		if _, err := loadString(t, m); err != nil {
			t.Errorf("%s with a space in the folder name was refused: %v", field, err)
		}
	}
}
