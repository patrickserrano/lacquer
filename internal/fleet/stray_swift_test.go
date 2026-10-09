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

// #522 U4: the sweep shows, per iOS project, how many Swift files sit under no
// declared Swift component, so each project's PM sees the finding the day the
// release lands rather than on a red PR.
func TestFleetShowsStraySwift(t *testing.T) {
	var b strings.Builder
	Text(&b, []Report{{Name: "alpha", StraySwift: 26}, {Name: "bravo"}})
	got := b.String()
	if !strings.Contains(got, "alpha  stray-swift 26") {
		t.Errorf("missing the stray count:\n%s", got)
	}
	if strings.Contains(got, "bravo  stray-swift") {
		t.Errorf("a project with no strays reported a count:\n%s", got)
	}
}

// Same gate as `lacquer audit`: strays block (exit 6) once the grace date has
// passed, and not before.
func TestStraySwiftBlocksTheFleetAfterTheGateDate(t *testing.T) {
	if (Report{StraySwift: 3}).ExitCode() != 0 {
		t.Error("strays before the gate date blocked the fleet")
	}
	if (Report{StraySwift: 3, StrayBlocking: true}).ExitCode() != 6 {
		t.Error("strays after the gate date did not block the fleet with exit 6")
	}
}

func TestStraySwiftStateReadsTheRepository(t *testing.T) {
	saved := testtargets.WatchGateFrom
	t.Cleanup(func() { testtargets.WatchGateFrom = saved })
	root := t.TempDir()
	for _, f := range []string{"ios/A.swift", "Lib/Sources/Lib/K.swift", "Top.swift"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Init(t, root, "-q")
	cfg := &config.Config{Components: []config.Component{{Path: "ios", Profiles: []string{"ios"}}}}

	testtargets.WatchGateFrom = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if n, blocks, err := strayState(root, cfg, time.Now()); err != nil || n != 2 || blocks {
		t.Errorf("before the gate: %d strays, blocks %v, err %v; want 2, false", n, blocks, err)
	}
	testtargets.WatchGateFrom = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if n, blocks, err := strayState(root, cfg, time.Now()); err != nil || n != 2 || !blocks {
		t.Errorf("after the gate: %d strays, blocks %v, err %v; want 2, true", n, blocks, err)
	}
}
