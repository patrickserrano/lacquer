package cirounds

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Kind is which event a ledger comment records.
type Kind string

const (
	// KindRound: an agent spent a CI round, on the commit named SHA.
	KindRound      Kind = "round"
	KindReview     Kind = "review"
	KindUnrecorded Kind = "unrecorded"
	// KindUpdate: a GitHub-created merge records a known head without spending.
	KindUpdate Kind = "update"
	// KindReset: an explicit operator decision starts a fresh budget.
	KindReset Kind = "reset"
	// KindExhausted: an agent asked for a round past the cap and was refused.
	// It carries no count; it is the stop notice, so a human sees it.
	KindExhausted Kind = "exhausted"
)

// markerPrefix opens every ledger comment. The comment must START with it (after
// whitespace): a marker quoted inside somebody's reply is not an entry, and a
// looser match would let a quotation move the count.
const (
	markerPrefix = "<!-- lacquer:ci-round "
	markerSuffix = " -->"
)

// Entry is the machine-readable half of a ledger comment: one JSON object in an
// HTML comment, which GitHub renders as nothing. The comment's visible text
// says the same thing for people (see render*), so the count is legible without
// opening anything and parseable without scraping prose.
type Entry struct {
	V     int  `json:"v"`
	Kind  Kind `json:"kind"`
	Epoch int  `json:"epoch"`
	// Round is 1-based within the epoch (round, review or unrecorded).
	Round int `json:"round,omitempty"`
	Cap   int `json:"cap,omitempty"`
	// SHA is the commit the entry is about: the commit pushed for a round, the
	// current head for a reset, the head that was still failing for a stop.
	SHA string `json:"sha"`
	At  string `json:"at,omitempty"` // UTC, RFC 3339
	// Addressing is the failing checks the previous round reported that this
	// round is about; Reason is the agent's own words for what it changed.
	Addressing []string `json:"addressing,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	// Failing (KindExhausted) is what was still failing when the agent was refused.
	Failing []string `json:"failing,omitempty"`
}

// Comment is one PR comment as far as the ledger cares.
type Comment struct {
	ID          string
	Association string // GitHub's authorAssociation: OWNER, MEMBER, COLLABORATOR, NONE, ...
	Body        string
	URL         string
}

// trusted is who may write the ledger: people with write access. Agents and
// humans push as the SAME account, so this cannot say which of them wrote an
// entry; what it does stop is a stranger on a public repo forging a stop notice
// (to block the agent) or a reset (to refill it).
func trusted(association string) bool {
	switch strings.ToUpper(association) {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return false
}

// Ledger is the PR's history as the comments record it.
type Ledger struct {
	// Epoch is the current budget's number, 1 until an explicit reset.
	Epoch int
	// Rounds are the current epoch's rounds, in order. When two sessions raced
	// for the same round number the earlier comment owns it and the other is in
	// Shadowed.
	Rounds   []Entry
	Shadowed []Entry
	// Known is every commit the tool has recorded, in any epoch. A head not in
	// it has not been recorded by this tool.
	Known map[string]bool
	// LastHead is the most recent accepted round, reset or neutral update SHA.
	LastHead string
	// Exhausted are the stop notices already on the PR for the current epoch.
	Exhausted []Entry
	// Any is whether the ledger has any entries at all.
	Any bool
}

func (l Ledger) roundFor(sha string) (Entry, bool) {
	for _, r := range l.Rounds {
		if r.SHA == sha {
			return r, true
		}
	}
	return Entry{}, false
}

// alreadyReported is whether this exact stop (this head, this failing set) is
// already on the PR, so a retrying agent does not bury it in its own notices.
func (l Ledger) alreadyReported(head string, failing []string) bool {
	for _, e := range l.Exhausted {
		if e.SHA == head && strings.Join(e.Failing, "\x00") == strings.Join(failing, "\x00") {
			return true
		}
	}
	return false
}

// ParseLedger reads the entries out of a PR's comments, in the order given.
//
// A trusted comment that opens with the marker and cannot be read is an ERROR,
// not something to skip: skipping it would refund a round, and a ledger that
// forgets when it is damaged is a state indistinguishable from an unspent one.
func ParseLedger(cs []Comment) (Ledger, error) {
	l := Ledger{Epoch: 1, Known: map[string]bool{}}
	for _, c := range cs {
		if !trusted(c.Association) {
			continue
		}
		body := strings.TrimSpace(c.Body)
		rest, ok := strings.CutPrefix(body, markerPrefix)
		if !ok {
			continue
		}
		js, _, ok := strings.Cut(rest, markerSuffix)
		if !ok {
			return Ledger{}, fmt.Errorf("ledger comment %s has an unterminated marker", where(c))
		}
		var e Entry
		if err := json.Unmarshal([]byte(js), &e); err != nil {
			return Ledger{}, fmt.Errorf("ledger comment %s is unreadable (%v); not guessing at what it recorded", where(c), err)
		}
		if e.V != 1 || e.SHA == "" || e.Epoch < 1 {
			return Ledger{}, fmt.Errorf("ledger comment %s is not a version-1 entry with a sha and epoch", where(c))
		}
		l.Any = true
		switch e.Kind {
		case KindUpdate:
			l.Known[e.SHA] = true
			if e.Epoch == l.Epoch {
				l.LastHead = e.SHA
			}
		case KindReset:
			l.Known[e.SHA] = true
			if e.Epoch >= l.Epoch {
				l.LastHead = e.SHA
			}
			if e.Epoch > l.Epoch {
				l.Epoch, l.Rounds, l.Exhausted = e.Epoch, nil, nil
			}
		case KindRound, KindReview, KindUnrecorded:
			if e.Epoch > l.Epoch {
				l.Epoch, l.Rounds, l.Exhausted = e.Epoch, nil, nil
			}
			if e.Epoch < l.Epoch {
				continue // a straggler from a budget a human already reset
			}
			if e.Kind == KindUnrecorded {
				duplicate := false
				for _, r := range l.Rounds {
					if r.SHA == e.SHA {
						duplicate = true
					}
				}
				if duplicate {
					continue
				}
				e.Round = len(l.Rounds) + 1
			}
			if l.hasRound(e.Round) {
				l.Shadowed = append(l.Shadowed, e)
				continue
			}
			l.Known[e.SHA] = true
			l.LastHead = e.SHA
			l.Rounds = append(l.Rounds, e)
		case KindExhausted:
			if e.Epoch == l.Epoch {
				l.Exhausted = append(l.Exhausted, e)
			}
		default:
			return Ledger{}, fmt.Errorf("ledger comment %s has unknown kind %q", where(c), e.Kind)
		}
	}
	return l, nil
}

func (l Ledger) hasRound(n int) bool {
	for _, r := range l.Rounds {
		if r.Round == n {
			return true
		}
	}
	return false
}

func where(c Comment) string {
	if c.URL != "" {
		return c.URL
	}
	return c.ID
}

func marker(e Entry) string {
	// json.Marshal escapes <, > and & (as < ...), so no reason text can
	// contain the "-->" that would end the HTML comment early.
	b, _ := json.Marshal(e)
	return markerPrefix + string(b) + markerSuffix
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func codeList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	return strings.Join(q, ", ")
}

// renderRound is the comment for a spent round: the marker, then the same facts
// as prose.
func renderRound(e Entry) string {
	var b strings.Builder
	b.WriteString(marker(e) + "\n")
	fmt.Fprintf(&b, "**Agent CI round %d of %d** · %s · `%s` · %s\n", e.Round, e.Cap, e.Kind, short(e.SHA), e.At)
	if len(e.Addressing) > 0 {
		fmt.Fprintf(&b, "\nAddressing what the previous round reported failing: %s\n", codeList(e.Addressing))
	}
	if e.Reason != "" {
		fmt.Fprintf(&b, "\n> %s\n", strings.ReplaceAll(e.Reason, "\n", "\n> "))
	}
	if e.Round >= e.Cap {
		b.WriteString("\nThis was the last round the agent gets on this PR. If checks still fail, it stops and hands the PR to a human; it will not push again.\n")
	}
	return b.String()
}

func renderUpdate(e Entry) string {
	return marker(e) + fmt.Sprintf("\n**GitHub branch update** · `%s` · %s\n\nNeutral update: no round spent; the budget is unchanged.\n", short(e.SHA), e.At)
}

func renderReset(e Entry) string {
	var b strings.Builder
	b.WriteString(marker(e) + "\n")
	fmt.Fprintf(&b, "**Agent CI round budget reset** · `%s` · %s\n", short(e.SHA), e.At)
	fmt.Fprintf(&b, "\nExplicit reset: %s. Budget starts over: 0 of %d rounds spent.\n", oneLine(e.Reason), e.Cap)
	return b.String()
}

func renderExhausted(e Entry, rounds []Entry) string {
	var b strings.Builder
	b.WriteString(marker(e) + "\n")
	fmt.Fprintf(&b, "**Agent CI budget exhausted: stopped, needs a human** · %s\n", e.At)
	fmt.Fprintf(&b, "\n%d of %d rounds spent. The agent asked for another and was refused; it will not push again.\n", len(rounds), e.Cap)
	if len(e.Failing) > 0 {
		fmt.Fprintf(&b, "\nStill failing on `%s`: %s\n", short(e.SHA), codeList(e.Failing))
	} else {
		fmt.Fprintf(&b, "\nNo check is failing on `%s` right now (checks may still be running).\n", short(e.SHA))
	}
	b.WriteString("\nRounds spent:\n")
	for _, r := range rounds {
		fmt.Fprintf(&b, "- round %d · `%s` · %s", r.Round, short(r.SHA), r.At)
		if r.Reason != "" {
			fmt.Fprintf(&b, " · %s", oneLine(r.Reason))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nThe PR is not failed or closed. To give the agent a fresh budget, explicitly run `lacquer ci-round reset <N> --reason \"<why>\"`. Unrecorded pushes spend rounds.\n")
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
