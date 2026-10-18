package shipped

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// dryRunGate is the condition the TestFlight steps carry: a tag push always
// runs, and only a manual dispatch may decline.
const dryRunGate = "${{ github.event_name != 'workflow_dispatch' || !inputs.skip_testflight }}"

// tagAndRealRunGate is the release-asset gate: only a tag ref publishes a
// GitHub Release, and a dry run dispatched on a tag ref must not.
const tagAndRealRunGate = "${{ startsWith(github.ref, 'refs/tags/v') && (github.event_name != 'workflow_dispatch' || !inputs.skip_testflight) }}"

// A release dispatched with skip_testflight is a dry run. Uploading its signed
// IPA as a workflow artifact leaves a build carrying the app's runtime keys
// readable by anyone with repo access, for a run that was meant to publish
// nothing. The IPA and dSYM upload steps must carry the same gate as the
// TestFlight steps, so a tag push and a real dispatch still upload. The same
// holds for the GitHub Release and the dSYM zip that exists only to feed it:
// a dry run dispatched on a tag ref must publish nothing.
func TestReleaseDryRunUploadsNoIPAOrDSYMArtifact(t *testing.T) {
	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				If   string `yaml:"if"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(renderRelease(t, soloProject())), &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Upload IPA artifact":           dryRunGate,
		"Upload dSYM artifact":          dryRunGate,
		"Package dSYMs for the release": tagAndRealRunGate,
		"Create GitHub Release":         tagAndRealRunGate,
	}
	seen := map[string]bool{}
	for _, st := range doc.Jobs["build-and-deploy"].Steps {
		gate, ok := want[st.Name]
		if !ok {
			continue
		}
		seen[st.Name] = true
		if st.If != gate {
			t.Errorf("%q has if: %q, want exactly %q", st.Name, st.If, gate)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("step %q not found in build-and-deploy; this test would pass vacuously", name)
		}
	}
}
