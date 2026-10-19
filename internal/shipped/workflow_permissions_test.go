package shipped

import (
	"maps"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"gopkg.in/yaml.v3"
)

// Two permission widenings ship with tree reuse and release provenance, and
// both were accepted scoped to ONE job (2026-10-19):
//
//   - iOS CI's `changes` gains `actions: read` and `pull-requests: read`, to
//     find the merged PR's run and read its record. Read-only.
//   - iOS Release's `verify-ci-provenance` gains `actions: write`, to dispatch a
//     full iOS CI run when the release SHA has none. Write includes read, which
//     the gate also needs for runs, jobs and artifacts; a permissions map has
//     one value per scope, so `actions: write` is how "read + write" is spelled.
//
// Every other job's permissions are pinned here to exactly what they were
// before, and the workflow-level defaults too, so a permission that drifts onto
// a second job (the easy mistake: copy a block, or hoist it to the workflow
// level) fails a named test instead of shipping to every iOS repository.

type permsDoc struct {
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		Permissions map[string]string `yaml:"permissions"`
	} `yaml:"jobs"`
}

func assertPermissions(t *testing.T, label, rendered string, workflow map[string]string, jobs map[string]map[string]string) {
	t.Helper()
	var doc permsDoc
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if !maps.Equal(doc.Permissions, workflow) {
		t.Errorf("%s: workflow-level permissions are %v, want exactly %v", label, doc.Permissions, workflow)
	}
	for name, job := range doc.Jobs {
		want := jobs[name] // nil: the job inherits the workflow default
		if !maps.Equal(job.Permissions, want) {
			t.Errorf("%s: job %s has permissions %v, want exactly %v", label, name, job.Permissions, want)
		}
	}
	for name := range jobs {
		if _, ok := doc.Jobs[name]; !ok {
			t.Errorf("%s: expected job %s is missing", label, name)
		}
	}
}

func TestOnlyTheReuseAndProvenanceJobsGainedPermissions(t *testing.T) {
	read := map[string]string{"contents": "read"}
	for name, cfg := range map[string]*config.Config{"solo": soloConfig(), "watch": watchProject()} {
		assertPermissions(t, "ios ci.yml ("+name+")", renderIOSCI(t, cfg), read, map[string]map[string]string{
			"changes": {"contents": "read", "actions": "read", "pull-requests": "read"},
		})
	}
	assertPermissions(t, "ios release.yml", renderRelease(t, soloProject()), read, map[string]map[string]string{
		"verify-ci-provenance": {"contents": "read", "checks": "read", "actions": "write"},
		"build-and-deploy":     {"contents": "write"},
	})
}
