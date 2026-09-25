package ciwait

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Counts is how many of a PR's checks pass, fail, or have not finished.
type Counts struct{ Passing, Failing, Pending int }

// Tally counts the checks in a statusCheckRollup array, as `gh pr list --json
// statusCheckRollup` prints it, with the same classifier a Wait uses, so a PR
// listing and `lacquer wait pr` cannot disagree about one check. In particular a
// legacy commit status (Vercel's) is read by its `state`, and only the newest
// run of a workflow counts. Neutral and skipped checks pass. An absent or null
// rollup is a PR with no checks.
func Tally(rollup json.RawMessage) (Counts, error) {
	var c Counts
	if len(bytes.TrimSpace(rollup)) == 0 || bytes.Equal(bytes.TrimSpace(rollup), []byte("null")) {
		return c, nil
	}
	var nodes []node
	if err := json.Unmarshal(rollup, &nodes); err != nil {
		return c, fmt.Errorf("unreadable statusCheckRollup: %w", err)
	}
	now := time.Now()
	all := make([]Check, 0, len(nodes))
	for _, n := range nodes {
		all = append(all, classify(n, now))
	}
	kept, _ := dropSuperseded(all)
	for _, ch := range kept {
		switch ch.v {
		case vPass, vNeutral, vSkipped:
			c.Passing++
		case vFail:
			c.Failing++
		default:
			c.Pending++
		}
	}
	return c, nil
}
