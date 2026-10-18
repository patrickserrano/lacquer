package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/testtargets"
)

// The sweep shows, per iOS project with a watch app, whether its watch suite is
// run by the managed job, run elsewhere, run by nothing, or has no suite at all.
func TestFleetShowsWatchState(t *testing.T) {
	var b strings.Builder
	Text(&b, []Report{
		{Name: "alpha", Watch: WatchDeclared},
		{Name: "bravo", Watch: WatchUnrun},
		{Name: "web"},
	})
	got := b.String()
	for _, want := range []string{"alpha  watch declared", "bravo  watch unrun"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "web  watch") {
		t.Errorf("a project with no watch app reported a watch state:\n%s", got)
	}
}

// Same gate as `lacquer audit`: unrun blocks once the grace date has passed.
func TestUnrunWatchBlocksTheFleetAfterTheGateDate(t *testing.T) {
	if (Report{Watch: WatchUnrun}).ExitCode() != 0 {
		t.Error("unrun before the gate date blocked the fleet")
	}
	if (Report{Watch: WatchUnrun, WatchBlocking: true}).ExitCode() != 4 {
		t.Error("unrun after the gate date did not block the fleet")
	}
}

func watchSpecProject(t *testing.T, spec string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yml"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const fleetWatchSpec = `targets:
  App:
    type: application
    platform: iOS
  AppTests:
    type: bundle.unit-test
    platform: iOS
  App Watch App:
    type: application
    platform: watchOS
  App Watch AppTests:
    type: bundle.unit-test
    platform: watchOS
schemes:
  App Watch App:
    test:
      targets: [App Watch AppTests]
`

func TestWatchStateReadsTheProject(t *testing.T) {
	saved := testtargets.WatchGateFrom
	t.Cleanup(func() { testtargets.WatchGateFrom = saved })
	testtargets.WatchGateFrom = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now()

	cfg := &config.Config{Project: config.Project{Scheme: "App", Xcodeproj: "App.xcodeproj"}}
	root := watchSpecProject(t, fleetWatchSpec)
	if got, blocks := watchState(root, cfg, now); got != WatchUnrun || !blocks {
		t.Errorf("undeclared watch bundle: state %q blocks %v, want unrun and blocking", got, blocks)
	}

	cfg.Project.WatchTests = &config.WatchTests{Scheme: "App Watch App", TestTarget: "App Watch AppTests"}
	if got, blocks := watchState(root, cfg, now); got != WatchDeclared || blocks {
		t.Errorf("declared: state %q blocks %v", got, blocks)
	}

	cfg.Project.WatchTests = nil
	noTests := watchSpecProject(t, strings.Replace(fleetWatchSpec, "  App Watch AppTests:\n    type: bundle.unit-test\n    platform: watchOS\n", "", 1))
	if got, blocks := watchState(noTests, cfg, now); got != WatchNoTests || blocks {
		t.Errorf("no watch bundle: state %q blocks %v", got, blocks)
	}

	widget := watchSpecProject(t, "targets:\n  App:\n    type: application\n    platform: iOS\n  W:\n    type: app-extension\n    platform: iOS\n")
	if got, _ := watchState(widget, cfg, now); got != "" {
		t.Errorf("no watch app: state %q, want none", got)
	}
}

// A git that never answers inside ReadForAudit must not read as "no watch app,
// not blocking": the sweep would then pass a project it never looked at.
func TestWatchStateFailsClosedWhenGitTimesOut(t *testing.T) {
	root := watchSpecProject(t, fleetWatchSpec)
	gittest.Init(t, root, "-q")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	saved := testtargets.GitLimit
	t.Cleanup(func() { testtargets.GitLimit = saved })
	testtargets.GitLimit = 500 * time.Millisecond

	cfg := &config.Config{Project: config.Project{Scheme: "App", Xcodeproj: "App.xcodeproj"}}
	var state string
	var blocks bool
	done := make(chan struct{})
	go func() { state, blocks = watchState(root, cfg, time.Now()); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("watchState still running after 15s: the git call is not bounded")
	}
	if !strings.HasPrefix(state, WatchUnchecked) || !blocks {
		t.Fatalf("state %q blocks %v, want %q and blocking", state, blocks, WatchUnchecked)
	}
	for _, want := range []string{"git", "500ms"} {
		if !strings.Contains(state, want) {
			t.Errorf("state %q does not name %q", state, want)
		}
	}
	// It reaches the sweep's per-project line, and fails the sweep.
	var b strings.Builder
	Text(&b, []Report{{Name: "alpha", Watch: state, WatchBlocking: blocks}})
	if !strings.Contains(b.String(), "500ms") {
		t.Errorf("the timeout text is not in the sweep output:\n%s", b.String())
	}
	if (Report{Watch: state, WatchBlocking: blocks}).ExitCode() != 4 {
		t.Error("a timed-out watch check did not fail the sweep")
	}
}
