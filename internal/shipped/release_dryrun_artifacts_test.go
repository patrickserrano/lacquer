package shipped

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// dryRunGate is the condition the TestFlight steps carry: a tag push always
// runs, and only a manual dispatch may decline.
const dryRunGate = "${{ github.event_name != 'workflow_dispatch' || !inputs.skip_testflight }}"

// A release dispatched with skip_testflight is a dry run. Uploading its signed
// IPA as a workflow artifact leaves a build carrying the app's runtime keys
// readable by anyone with repo access, for a run that was meant to publish
// nothing. The IPA and dSYM upload steps must carry the same gate as the
// TestFlight steps, so a tag push and a real dispatch still upload.
func TestReleaseDryRunUploadsNoIPAOrDSYMArtifact(t *testing.T) {
	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				If   string `yaml:"if"`
				Uses string `yaml:"uses"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(renderRelease(t, soloProject())), &doc); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, st := range doc.Jobs["build-and-deploy"].Steps {
		if st.Name != "Upload IPA artifact" && st.Name != "Upload dSYM artifact" {
			continue
		}
		seen[st.Name] = true
		if !strings.HasPrefix(st.Uses, "actions/upload-artifact@") {
			t.Errorf("%q uses %q, want actions/upload-artifact", st.Name, st.Uses)
		}
		if st.If != dryRunGate {
			t.Errorf("%q has if: %q, want exactly %q", st.Name, st.If, dryRunGate)
		}
	}
	for _, name := range []string{"Upload IPA artifact", "Upload dSYM artifact"} {
		if !seen[name] {
			t.Errorf("step %q not found in build-and-deploy; this test would pass vacuously", name)
		}
	}
}
