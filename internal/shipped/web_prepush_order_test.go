package shipped

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// `next build` rewrites .next/types while it runs, and TypeDoc reads that
// directory, so a pre-push that runs `docs` and `build` at the same moment fails
// intermittently on a push with nothing wrong in it.
//
// lefthook runs every command of a hook at once when the hook says `parallel:
// true`, and offers no way to order a subset of them while the merged
// lefthook.yml is built from per-profile `commands:` maps (a `jobs:` group would
// not survive that merge). So the guarantee is structural: the rendered pre-push
// hook is not parallel and not left to ordering by anything else. This asserts
// the rendered file, not the fragment, because it is the rendered file lefthook
// reads, and the merge refuses to pick between profiles that disagree.
func TestWebPrePushDoesNotRunDocsAndBuildTogether(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T) *project{
		"single component": func(t *testing.T) *project {
			return fromInit(t, "web", map[string]string{
				"package.json": "{ \"name\": \"acme\", \"packageManager\": \"pnpm@10.20.0\" }\n",
			})
		},
		"nested component": func(t *testing.T) *project {
			return fromInit(t, "web", map[string]string{
				"admin/package.json": "{ \"name\": \"acme\", \"packageManager\": \"pnpm@10.20.0\" }\n",
			})
		},
		"merged with other stacks": func(t *testing.T) *project { return fromFixture(t, "multistack") },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := mk(t)
			p.sync()
			var doc map[string]struct {
				Parallel bool  `yaml:"parallel"`
				Piped    bool  `yaml:"piped"`
				Jobs     []any `yaml:"jobs"`
				Commands map[string]struct {
					Priority int `yaml:"priority"`
				} `yaml:"commands"`
			}
			if err := yaml.Unmarshal([]byte(p.read("lefthook.yml")), &doc); err != nil {
				t.Fatalf("lefthook.yml is not valid YAML: %v", err)
			}
			push := doc["pre-push"]
			for _, c := range []string{"docs", "build"} {
				if _, ok := push.Commands[c]; !ok {
					t.Fatalf("pre-push has no %q command (has %v); the ordering below would prove nothing", c, push.Commands)
				}
			}
			if len(push.Jobs) > 0 {
				t.Fatalf("pre-push mixes `jobs:` with `commands:`; this test cannot say how lefthook orders them")
			}
			// Not parallel, lefthook runs commands sorted by name unless a priority
			// says otherwise, so the order is only as good as these two facts.
			if push.Commands["build"].Priority != 0 || push.Commands["docs"].Priority != 0 {
				t.Errorf("a priority on build or docs changes the order lefthook runs them in; this test assumes name order, build before docs")
			}
			if push.Parallel {
				t.Errorf("pre-push is `parallel: true`, so lefthook starts docs (TypeDoc, which reads .next/types) " +
					"at the same moment as build (next build, which rewrites it)")
			}
		})
	}
}
