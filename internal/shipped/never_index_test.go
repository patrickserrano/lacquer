package shipped

import (
	"strings"
	"testing"
)

// Spotlight indexing build output is a recurring, expensive failure that does
// not name its cause. Measured on the dedicated runner 2026-09-11: ~215GB of
// regenerable output indexed — 133G of simulator devices, 59G of shared
// DerivedData, 22G across ten per-project trees — mdbulkimport running a full
// reindex, load average 99. Excluding it took mds* from 34% CPU across nine
// processes to 12%.
//
// It recurs because the output is REGENERABLE: deleted and recreated
// constantly, so a one-time exclusion does not survive. The marker has to be
// written at the same moment the directory is, which means every job that
// builds needs its own write — not one write somewhere in the file.

// markerWrites returns the line numbers of lines that actually CREATE the
// marker, excluding comments.
//
// The distinction is the whole test. The first version of this matched the
// bare string `metadata_never_index`, which also appears in the comment block
// explaining why the step exists. Deleting an entire marker step left that
// comment behind, and the test stayed green through a mutation that removed
// the thing it guards — a detector keyed on human prose rather than on the
// command, which is the exact defect `lacquer audit uncalled` exists to catch.
func markerWrites(body string) []int {
	var out []int
	for i, l := range strings.Split(body, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			continue
		}
		// The write is a redirection into the marker path; `mkdir -p` on the
		// directory alone does not exclude anything.
		if strings.Contains(t, ".metadata_never_index") && strings.Contains(t, ">") {
			out = append(out, i)
		}
	}
	return out
}

// jobSpans splits a rendered workflow into [start, end) line ranges keyed by
// job name, so coverage can be asserted per job rather than per file.
func jobSpans(body string) map[string][2]int {
	lines := strings.Split(body, "\n")
	spans := map[string][2]int{}

	inJobs := false
	var name string
	var start int
	flush := func(end int) {
		if name != "" {
			spans[name] = [2]int{start, end}
		}
	}
	for i, l := range lines {
		if !inJobs {
			if strings.HasPrefix(l, "jobs:") {
				inJobs = true
			}
			continue
		}
		// A job key is indented exactly two spaces and ends in a colon.
		trimmed := strings.TrimRight(l, " ")
		if !strings.HasPrefix(trimmed, "  ") || strings.HasPrefix(trimmed, "   ") ||
			!strings.HasSuffix(trimmed, ":") {
			continue
		}
		// Check the KEY for spaces, not the indented line — every indented
		// line contains a space, which is how the first version of this
		// matched nothing at all.
		key := strings.TrimSuffix(strings.TrimSpace(trimmed), ":")
		if key == "" || strings.ContainsAny(key, " #") {
			continue
		}
		flush(i)
		name = key
		start = i
	}
	flush(len(lines))
	return spans
}

func TestEveryBuildingJobExcludesItsDerivedDataFromSpotlight(t *testing.T) {
	body := readFile(t, iosCIPath(t))

	writes := markerWrites(body)
	if len(writes) == 0 {
		t.Fatal("no line creates .metadata_never_index; Spotlight will index every build this workflow produces")
	}

	spans := jobSpans(body)
	if len(spans) == 0 {
		t.Fatal("parsed no jobs out of the workflow — this test is keyed on a layout that has changed, so it is now checking nothing")
	}

	lines := strings.Split(body, "\n")
	building := 0
	for name, span := range spans {
		firstBuild := -1
		for i := span[0]; i < span[1] && i < len(lines); i++ {
			if strings.Contains(lines[i], "-derivedDataPath") {
				firstBuild = i
				break
			}
		}
		if firstBuild < 0 {
			continue // this job does not build
		}
		building++

		// The write must live in THIS job and run before the build. A write in
		// a different job is a different runner step and excludes nothing here.
		covered := false
		for _, w := range writes {
			if w >= span[0] && w < span[1] && w < firstBuild {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("job %q builds into DerivedData at line %d with no preceding .metadata_never_index write inside the job; that tree gets indexed",
				name, firstBuild+1)
		}
	}

	if building == 0 {
		t.Fatal("no job builds into a -derivedDataPath — the flag this test keys on has moved, so the check is vacuous")
	}
}

// The watch job gets its own DerivedData, so the capability added for watch
// tests must not quietly reintroduce the problem this rule exists for.
func TestWatchDerivedDataIsExcludedToo(t *testing.T) {
	body := readFile(t, iosCIPath(t))
	if !strings.Contains(body, "WatchDerivedData") {
		t.Skip("no watch DerivedData in the rendered workflow")
	}
	for _, w := range markerWrites(body) {
		// The write loops over both directory names; find one that names the
		// watch tree, either directly or through the loop variable.
		line := strings.Split(body, "\n")[w]
		if strings.Contains(line, "$dd") || strings.Contains(line, "WatchDerivedData") {
			return
		}
	}
	t.Error("WatchDerivedData is created but no marker write covers it")
}
