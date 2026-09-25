package inboxwatch

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The Stuck tab lists conditions that are computed, not typed: nothing needs an
// agent to have written an inbox entry for a row to appear. The thresholds are
// the operator's, set on 2026-09-20 (lacquer #424). They are deliberately
// short. Do not raise them here: one is raised at a time, with a recorded reason,
// never as a batch (#424 explains why).
const (
	// StuckPRFailingAfter is how long an open PR's checks may stay red.
	StuckPRFailingAfter = 3 * time.Hour
	// StuckLaterIdleAfter is how long a `later` issue may go without activity.
	StuckLaterIdleAfter = 14 * 24 * time.Hour
)

// StuckItem is one row of the Stuck tab: something that has been in a bad state
// for at least its source's threshold.
type StuckItem struct {
	// Key is the stable condition key a dismissal is stored under, such as
	// "pr-failing:owner/repo#12". It names the condition, so dismissing a PR's
	// failing checks does not hide the same PR stale in some other way.
	Key    string
	Source string // the StuckSource's Name
	Repo   string // owner/name
	Number int
	Title  string
	URL    string
	// Since is when the bad state began, and Why says what it is.
	Since time.Time
	Why   string
}

// Ref is "owner/name#number".
func (s StuckItem) Ref() string { return fmt.Sprintf("%s#%d", s.Repo, s.Number) }

// StuckReport is what one source found. Problems is everything the source could
// not check, and is never left empty when it could not: an empty Items with no
// Problems means "checked, and nothing is stuck", and nothing else may.
type StuckReport struct {
	Source   string
	Answered bool      // whether the data behind it has been read at all yet
	At       time.Time // when it was last read
	Items    []StuckItem
	Problems []string
}

// StuckInputs is the data the sources read. It is what the PRs and Later tabs
// already fetch, under their own throttle: computing the Stuck tab makes no gh
// call of its own. A source for a new family of conditions (424b's App Store
// states) adds its snapshot here.
type StuckInputs struct {
	PRs   PRsState
	Later LaterState
}

// StuckSource is one family of stuck conditions. Check is pure: it reads what is
// already in memory and asks nothing of the world, so it can run on every frame.
type StuckSource interface {
	Name() string
	Check(in StuckInputs, now time.Time) StuckReport
}

// stuckSources are the families the tab checks, in the order it shows them.
var stuckSources = []StuckSource{prFailingSource{}, laterStaleSource{}}

// stuckFor is how long a state has lasted, as "45m", "3h20m" or "14d3h".
func stuckFor(d time.Duration) string {
	mins := max(int(d/time.Minute), 0)
	switch {
	case mins < 60:
		return fmt.Sprintf("%dm", mins)
	case mins < 48*60:
		return fmt.Sprintf("%dh%02dm", mins/60, mins%60)
	}
	return fmt.Sprintf("%dd%dh", mins/1440, mins%1440/60)
}

// thresholdLabel is a threshold as the operator wrote it: "3h", "14d".
func thresholdLabel(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return fmt.Sprintf("%dh", d/time.Hour)
}

func stuckStamp(t time.Time) string { return t.Local().Format("01-02 15:04") }

// firstN is up to n of items, then how many more.
func firstN(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" and %d more", len(items)-n)
}

// prFailingSource: an open PR whose checks have been red for StuckPRFailingAfter,
// measured from when the failing check completed, never from the PR's creation:
// a PR open for a week that failed ten minutes ago is not stuck.
type prFailingSource struct{}

func (prFailingSource) Name() string {
	return "PR checks failing for " + thresholdLabel(StuckPRFailingAfter)
}

func prFailingKey(repo string, n int) string { return fmt.Sprintf("pr-failing:%s#%d", repo, n) }

func (s prFailingSource) Check(in StuckInputs, now time.Time) StuckReport {
	st := in.PRs
	r := StuckReport{Source: s.Name(), Answered: st.Answered, At: st.At}
	if !st.Answered {
		return r
	}
	if st.Err != "" {
		r.Problems = append(r.Problems, st.Err)
		if len(st.PRs) > 0 {
			r.Problems[0] += " (the rows below are from the last good check)"
		}
	}
	if len(st.Errors) > 0 {
		names := make([]string, len(st.Errors))
		for i, e := range st.Errors {
			names[i] = e.Repo
		}
		r.Problems = append(r.Problems, fmt.Sprintf("%d repos errored: %s (first: %s)", len(st.Errors), firstN(names, 3), st.Errors[0].Err))
	}
	if len(st.Full) > 0 {
		r.Problems = append(r.Problems, fmt.Sprintf("%s at the %d-PR limit, so may be cut", firstN(st.Full, 3), prLimit))
	}
	var untimed []string
	for _, p := range st.PRs {
		if p.Failing == 0 {
			continue
		}
		// The earliest known completion is a floor on how long it has been red:
		// an untimed failing check may have failed earlier, never later than the
		// PR being red at all. So a PR that clears the threshold on the known time
		// is stuck; one that does not, and has an untimed failure, is reported.
		if p.FailingSince.IsZero() || now.Sub(p.FailingSince) < StuckPRFailingAfter {
			if p.Untimed {
				untimed = append(untimed, p.Key())
			}
			continue
		}
		title := p.Title
		if p.Draft {
			title += " [draft]"
		}
		r.Items = append(r.Items, StuckItem{
			Key: prFailingKey(p.Repo, p.Number), Source: s.Name(), Repo: p.Repo, Number: p.Number, Title: title, URL: p.URL,
			Since: p.FailingSince,
			Why:   fmt.Sprintf("%d %s failing (%s) since %s", p.Failing, plural(p.Failing, "check", "checks"), firstN(p.FailedChecks, 3), stuckStamp(p.FailingSince)),
		})
	}
	if len(untimed) > 0 {
		r.Problems = append(r.Problems, fmt.Sprintf("%s failing with no check completion time, so not timed", firstN(untimed, 3)))
	}
	return r
}

// laterStaleSource: an open issue labelled `later` that has had no activity for
// StuckLaterIdleAfter. A parked issue is meant to sit, but not forever unseen.
type laterStaleSource struct{}

func (laterStaleSource) Name() string {
	return "Later issues idle for " + thresholdLabel(StuckLaterIdleAfter)
}

func laterStaleKey(repo string, n int) string { return fmt.Sprintf("later-stale:%s#%d", repo, n) }

func (s laterStaleSource) Check(in StuckInputs, now time.Time) StuckReport {
	st := in.Later
	r := StuckReport{Source: s.Name(), Answered: st.Answered, At: st.At}
	if !st.Answered {
		return r
	}
	if st.Err != "" {
		r.Problems = append(r.Problems, st.Err)
		if len(st.Issues) > 0 {
			r.Problems[0] += " (the rows below are from the last good check)"
		}
	}
	if len(st.Issues) >= laterLimit {
		r.Problems = append(r.Problems, fmt.Sprintf("the search hit its %d-issue limit, so may be cut", laterLimit))
	}
	var untimed []string
	for _, i := range st.Issues {
		if i.UpdatedAt.IsZero() {
			untimed = append(untimed, i.Ref())
			continue
		}
		if now.Sub(i.UpdatedAt) < StuckLaterIdleAfter {
			continue
		}
		r.Items = append(r.Items, StuckItem{
			Key: laterStaleKey(i.Repo, i.Number), Source: s.Name(), Repo: i.Repo, Number: i.Number, Title: i.Title, URL: i.URL,
			Since: i.UpdatedAt,
			Why:   "parked with no activity since " + stuckStamp(i.UpdatedAt),
		})
	}
	if len(untimed) > 0 {
		r.Problems = append(r.Problems, fmt.Sprintf("%s has no update time, so not checked", firstN(untimed, 3)))
	}
	return r
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// stuckReports runs every source over what the tabs have fetched.
func (m Model) stuckReports() []StuckReport {
	in := StuckInputs{PRs: m.PRs, Later: m.Later}
	out := make([]StuckReport, 0, len(stuckSources))
	for _, s := range stuckSources {
		r := s.Check(in, m.Now)
		// Longest stuck first, so the oldest problem is at the top of its section.
		sort.SliceStable(r.Items, func(a, b int) bool {
			if !r.Items[a].Since.Equal(r.Items[b].Since) {
				return r.Items[a].Since.Before(r.Items[b].Since)
			}
			return r.Items[a].Ref() < r.Items[b].Ref()
		})
		out = append(out, r)
	}
	return out
}

// dismissedNow is whether the condition has a dismissal that has not expired.
func (m Model) dismissedNow(key string) bool {
	until, ok := m.Stuck.Dismissed[key]
	return ok && until.After(m.Now)
}

// stuckShown is the items to list, and how many the operator has dismissed
// (only those still stuck: a dismissal for a condition that cleared is not a
// hidden row).
func (m Model) stuckShown(reports []StuckReport) (shown [][]StuckItem, hidden int) {
	shown = make([][]StuckItem, len(reports))
	for i, r := range reports {
		for _, it := range r.Items {
			if m.dismissedNow(it.Key) {
				hidden++
				continue
			}
			shown[i] = append(shown[i], it)
		}
	}
	return shown, hidden
}

// StuckState is the Stuck tab: its cursor, the dismissals in force, and the
// dismiss prompt. It has no fetch of its own; that is the point.
type StuckState struct {
	cursor
	// Dismissed is condition key -> when the dismissal ends, as last read from
	// stuck-dismissed.json. DismissErr is why it could not be read; the tab then
	// hides nothing and says so.
	Dismissed  map[string]time.Time
	DismissErr string
	// dismissedAt is when this process last wrote a dismissal. A read of the file
	// that started before then is older than what the model knows and is ignored.
	dismissedAt time.Time
	Prompt      dismissPrompt
}

// dismissPrompt is the period being typed for a dismissal.
type dismissPrompt struct {
	Active     bool
	Key, Label string
	Buf        string
	Err        string // why the last Enter was refused
}

// stuckRows is the tab's rows: per source a header, the stuck items (two screen
// rows each: what, then why and the link), and one red row for each thing the
// source could not check.
func (m Model) stuckRows() []row {
	reports := m.stuckReports()
	shown, _ := m.stuckShown(reports)
	var rows []row
	for i, r := range reports {
		switch {
		case !r.Answered:
			rows = append(rows, row{header: true, l: line{{"  " + r.Source + ": checking…", fg(dim)}}})
			continue
		case len(shown[i]) > 0:
			rows = append(rows, row{header: true, l: line{{fmt.Sprintf("%s  (%d)", r.Source, len(shown[i])), fgBold(cyan)}}})
		}
		for _, it := range shown[i] {
			age := stuckFor(m.Now.Sub(it.Since))
			rows = append(rows,
				row{key: it.Key, extra: 1, l: line{
					{"  " + clean(it.Ref()) + "  ", fg(magenta)},
					{clean(it.Title), fg(def)},
				}},
				row{header: true, l: line{
					{"      ↳ ", fg(dim)},
					{age + "  ", fgBold(red)},
					{clean(it.Why) + "  ", fg(dim)},
					{clean(it.URL), fg(blue)},
				}})
		}
		for _, p := range r.Problems {
			rows = append(rows, row{header: true, l: line{
				{"  " + r.Source + ": ", fgBold(red)},
				{"couldn't check: " + clean(p), fgBold(red)},
			}})
		}
	}
	return rows
}

func (m Model) selectedStuck() (StuckItem, bool) {
	rows := m.stuckRows()
	i, ok := m.Stuck.at(rows)
	if !ok {
		return StuckItem{}, false
	}
	for _, r := range m.stuckReports() {
		for _, it := range r.Items {
			if it.Key == rows[i].key {
				return it, true
			}
		}
	}
	return StuckItem{}, false
}

// stuckStatus is the tab's header text. It never reads the same for "nothing is
// stuck" and "could not tell": a source that could not be checked is named in red.
func (m Model) stuckStatus() seg {
	reports := m.stuckReports()
	shown, hidden := m.stuckShown(reports)
	n, broken, waiting := 0, 0, 0
	for i, r := range reports {
		n += len(shown[i])
		switch {
		case !r.Answered:
			waiting++
		case len(r.Problems) > 0:
			broken++
		}
	}
	parts := []string{fmt.Sprintf("%d stuck", n)}
	if hidden > 0 {
		parts = append(parts, fmt.Sprintf("%d dismissed", hidden))
	}
	st := fgBold(magenta)
	if n == 0 {
		st = fgBold(green)
		if waiting > 0 {
			st = fg(dim) // "0 stuck" is not yet good news while a source has not answered
		}
	}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d still checking", waiting))
	}
	if broken > 0 {
		parts = append(parts, fmt.Sprintf("couldn't check %d of %d sources", broken, len(reports)))
		st = fgBold(red)
	}
	if m.Stuck.DismissErr != "" {
		parts = append(parts, "dismissals unreadable, none applied")
		st = fgBold(red)
	}
	return seg{" " + strings.Join(parts, " · "), st}
}

// stuckEmpty is what the tab says with no rows. Rows exist whenever a source is
// still loading or could not be checked, so this is reached only when every
// source answered cleanly and found nothing.
func (m Model) stuckEmpty() string {
	reports := m.stuckReports()
	_, hidden := m.stuckShown(reports)
	checked := make([]string, len(reports))
	for i, r := range reports {
		checked[i] = fmt.Sprintf("%s at %s", r.Source, r.At.Local().Format("15:04"))
	}
	msg := "nothing stuck (checked " + strings.Join(checked, ", ") + ")"
	if hidden > 0 {
		msg += fmt.Sprintf(" · %d dismissed", hidden)
	}
	return msg
}

// startDismiss is x on a stuck row: ask for how long.
func (m *Model) startDismiss() {
	if m.kind() != KindStuck {
		m.Note = "x dismisses a row on the Stuck tab"
		return
	}
	it, ok := m.selectedStuck()
	if !ok {
		return
	}
	m.Stuck.Prompt = dismissPrompt{Active: true, Key: it.Key, Label: it.Ref()}
}

// promptKey types the period. Nothing else works while it is open, so no key
// meant for the list can act on a row the operator is not looking at.
func (m *Model) promptKey(k KeyEvent) []Cmd {
	p := &m.Stuck.Prompt
	switch k.Key {
	case KeyEsc, KeyCtrlC:
		*p = dismissPrompt{}
		m.Note = "not dismissed"
	case KeyBackspace:
		p.Err = ""
		if r := []rune(p.Buf); len(r) > 0 {
			p.Buf = string(r[:len(r)-1])
		}
	case KeyCtrlU:
		p.Buf = ""
	case KeyRune:
		if k.Rune >= ' ' && k.Rune != 0x7f && len(p.Buf) < 8 {
			p.Err = ""
			p.Buf += string(k.Rune)
		}
	case KeyEnter:
		d, err := ParsePeriod(p.Buf)
		if err != nil {
			p.Err = err.Error() // the prompt stays open, so it can be corrected
			return nil
		}
		key := p.Key
		*p = dismissPrompt{}
		return []Cmd{{Kind: CmdStuckDismiss, ID: key, Until: m.Now.Add(d)}}
	}
	return nil
}

// stuckDismissed applies the answer to a dismissal.
func (m *Model) stuckDismissed(ev StuckDismissedEvent) {
	if ev.Err != "" {
		m.Note = "not dismissed: " + ev.Err
		return
	}
	m.Stuck.Dismissed, m.Stuck.DismissErr, m.Stuck.dismissedAt = ev.Dismissed, "", ev.At
	m.Stuck.keep(m.stuckRows(), m.viewH(), 1)
	m.Note = "dismissed until " + stuckStamp(ev.Until)
}
