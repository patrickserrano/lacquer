package config

import (
	"strings"
	"testing"
)

// #522 U4: each Swift component is linted from inside its own directory. One
// nested in another would be linted twice, under two configs, and the outer
// config would decide what the inner one's files are held to. Refused at load,
// with both paths named.
func TestSwiftComponentInsideAnotherIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"package inside the app": `
[[component]]
path = "ios"
profiles = ["ios"]

[[component]]
path = "ios/Packages/Lib"
stack = "ios"
`,
		"package inside a root-layout app": `
[[component]]
path = "."
profiles = ["ios"]

[[component]]
path = "tools"
stack = "ios"
`,
		"package inside a package": `
[[component]]
path = "ios"
profiles = ["ios"]

[[component]]
path = "tools"
stack = "ios"

[[component]]
path = "tools/lib"
stack = "ios"
`,
		"app inside a package": `
[[component]]
path = "ios/App"
profiles = ["ios"]

[[component]]
path = "ios"
stack = "ios"
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(t, body)
			if err == nil {
				t.Fatal("a Swift component nested in another loaded without error")
			}
			if !strings.Contains(err.Error(), "inside Swift component") {
				t.Fatalf("error does not say which component holds which: %v", err)
			}
		})
	}
}

// Siblings, prefix-named siblings, and non-Swift components nested anywhere
// stay valid.
func TestSwiftComponentsSideBySideLoad(t *testing.T) {
	for name, body := range map[string]string{
		"app beside a package": `
[[component]]
path = "ios"
profiles = ["ios"]

[[component]]
path = "tools"
stack = "ios"
`,
		"a name that is a prefix of another is not nesting": `
[[component]]
path = "ios"
profiles = ["ios"]

[[component]]
path = "ios-tools"
stack = "ios"
`,
		"a web component under a root-layout app": `
[[component]]
path = "."
profiles = ["ios"]

[[component]]
path = "admin"
profiles = ["web"]
stack = "web"
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(t, body); err != nil {
				t.Fatalf("valid layout refused: %v", err)
			}
		})
	}
}
