package shipped

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/actionlint"
	"gopkg.in/yaml.v3"
)

// Compare actual runs-on values in synced workflows with the production list,
// not another handwritten test config. A new workflow label must fail here.
func TestActionlintLabelsEqualRenderedWorkflowLabels(t *testing.T) {
	p := fromFixture(t, "multistack")
	p.sync()
	files, err := filepath.Glob(filepath.Join(p.root, ".github/workflows/*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no workflows checked")
	}
	builtin := map[string]bool{"self-hosted": true, "linux": true, "macos": true, "arm64": true, "x64": true, "windows": true}
	have := map[string]bool{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]struct {
				RunsOn any `yaml:"runs-on"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			t.Fatal(err)
		}
		for _, job := range workflow.Jobs {
			var labels []string
			switch runs := job.RunsOn.(type) {
			case string:
				labels = []string{runs}
			case []any:
				for _, v := range runs {
					s, ok := v.(string)
					if !ok {
						t.Fatalf("non-string runs-on in %s", file)
					}
					labels = append(labels, s)
				}
			default:
				t.Fatalf("unhandled runs-on in %s: %#v", file, runs)
			}
			for _, label := range labels {
				if !builtin[strings.ToLower(label)] {
					have[label] = true
				}
			}
		}
	}
	var got []string
	for label := range have {
		got = append(got, label)
	}
	sort.Strings(got)
	want := strings.Split(actionlint.Labels, "\n")
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow custom labels=%q; managed labels=%q", got, want)
	}
	if err := actionlint.Check([]byte(p.read(actionlint.Name))); err != nil {
		t.Fatal(err)
	}
}
