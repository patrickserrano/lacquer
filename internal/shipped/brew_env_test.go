package shipped

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Self-hosted macOS runners share one Homebrew prefix but run as different
// macOS users. `brew install` auto-updates and then cleans up, and cleanup fails
// on files another user owns ("Permission denied @ apply2files"), killing the
// job before it reaches any real work. Both switches have to be on the JOB, not
// on one step, so a step added later inherits them.
//
// The jobs are found by what their steps run, not by name: a list of job names
// would pass for ever once a new job that calls brew was added outside it.

// brewCommand matches `brew <subcommand>` as a command, at the start of a line
// or after a shell operator. Comment lines are dropped before matching.
var brewCommand = regexp.MustCompile(`(^|[;&|(]|\s)brew\s+(install|list|update|upgrade|tap|reinstall|bundle|uninstall)\b`)

type brewJob struct {
	Env   map[string]any `yaml:"env"`
	Steps []struct {
		Run string `yaml:"run"`
	} `yaml:"steps"`
}

func runsBrew(j brewJob) bool {
	for _, s := range j.Steps {
		for _, line := range strings.Split(s.Run, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if brewCommand.MatchString(line) {
				return true
			}
		}
	}
	return false
}

func TestEveryJobThatRunsBrewTurnsOffAutoUpdateAndCleanup(t *testing.T) {
	rendered, _ := renderedWorkflows(t)
	// The watch job only renders for a project that declares watch tests, which
	// no ledger config does; render it explicitly so that path is covered.
	rendered["watch-config ci.yml"] = renderIOSCI(t, watchProject())
	// The matrix form emits its own env entries into the same block.
	rendered["two-product ci.yml"] = renderIOSCI(t, twoIOSProducts())

	var checked []string
	for name, body := range rendered {
		var doc struct {
			Jobs map[string]brewJob `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("%s: rendered workflow is not valid YAML: %v", name, err)
		}
		for id, job := range doc.Jobs {
			if !runsBrew(job) {
				continue
			}
			checked = append(checked, name+" :: "+id)
			for _, v := range []string{"HOMEBREW_NO_AUTO_UPDATE", "HOMEBREW_NO_INSTALL_CLEANUP"} {
				if got, ok := job.Env[v]; !ok || got != "1" {
					t.Errorf("%s: job %q runs brew but job-level env %s is %v, want \"1\"", name, id, v, got)
				}
			}
		}
	}
	sort.Strings(checked)

	if len(checked) == 0 {
		t.Fatal("found no job that runs brew; the detector is broken and this test would pass vacuously")
	}
	// The watch job is the one only an explicit config renders: prove it was seen.
	var sawWatch bool
	for _, c := range checked {
		if strings.HasPrefix(c, "watch-config ci.yml :: watch-test") {
			sawWatch = true
		}
	}
	if !sawWatch {
		t.Errorf("the watch-test job was not among the brew jobs checked: %v", checked)
	}
}
