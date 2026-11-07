package shipped

import (
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

// Bare inputs is event-dependent. The event payload path is safe to read when
// the workflow is first introduced on a pull request as well as on dispatch.
func TestRenderedRunNamesAvoidBareInputs(t *testing.T) {
	files, err := shippedWorkflowFiles(root(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no shipped workflows found")
	}
	// A dot before inputs belongs to a qualified path such as github.event.inputs.
	bareInputs := regexp.MustCompile(`(^|[^[:alnum:]_.])inputs\.`)
	names := 0
	for _, file := range files {
		rendered, err := renderShippedWorkflow(t, file)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			RunName string `yaml:"run-name"`
		}
		if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if doc.RunName == "" {
			continue
		}
		names++
		if bareInputs.MatchString(doc.RunName) {
			t.Errorf("%s: rendered run-name uses bare inputs context: %s", file, doc.RunName)
		}
	}
	if names == 0 {
		t.Fatal("no rendered run-name checked; the input-context regression must not pass vacuously")
	}
}
