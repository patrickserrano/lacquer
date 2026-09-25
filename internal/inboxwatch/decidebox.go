package inboxwatch

import (
	"strings"

	"github.com/patrickserrano/lacquer/internal/decisions"
)

// The record-as-decision flow in the entry popup (#427). D opens it, and it is
// three prompts, each of which Esc abandons with nothing posted:
//
//	words  the operator's own words, typed and kept exactly as typed
//	basis  what they were decided against, e.g. a measurement (optional)
//	scope  r this repository, f fleet-wide; the target of each is on the screen
//
// A reply is different: an ordinary reply stays a reply, goes to the overseer
// and is never recorded. A decision goes to GitHub only, and nothing is sent to
// the overseer, so the popup confirms it with the comment's URL instead.

type decStep int

const (
	decOff decStep = iota
	decWords
	decBasis
	decScope
	decSending
)

type decision struct {
	step         decStep
	box          replyBox // the buffer and its wrapping; its Enter and Esc are not used
	words, basis string
	targets      DecisionTargets
	result       []string // what the last attempt did, shown until the next key
	resultFailed bool
	unsureOfLast bool
}

const (
	wordsPrompt = "record as a decision, in your own words (kept exactly as typed):"
	basisPrompt = "basis, optional: the measurement or reason this was decided against, and when. ⏎ skips it"
)

// startDecision opens the words prompt, or says why it cannot.
func (d *Detail) startDecision() {
	if !d.Loaded || !d.Found {
		d.Note = "no entry to record a decision from"
		return
	}
	if d.Targets == nil {
		d.Note = "decisions are not configured in this popup"
		return
	}
	t := d.Targets(d.Entry.Ref, d.Entry.Project)
	if t.Repo == "" && t.Fleet == "" {
		d.Note = clean("cannot record a decision: this repo: " + t.RepoWhy + "; fleet-wide: " + t.FleetWhy)
		return
	}
	d.dec = decision{step: decWords, targets: t}
}

func (d *Detail) cancelDecision() {
	d.dec = decision{}
	d.Note = "decision cancelled; nothing was posted"
}

// decKey applies a key while a decision prompt is open.
func (d *Detail) decKey(k KeyEvent) []Cmd {
	dec := &d.dec
	dec.result = nil
	if k.Key == KeyEsc || k.Key == KeyCtrlC {
		d.cancelDecision()
		return nil
	}
	switch dec.step {
	case decWords:
		if k.Key == KeyEnter {
			// Kept as typed: not trimmed. Only a box with nothing in it is refused.
			if strings.TrimSpace(dec.box.Buf) == "" {
				d.cancelDecision()
				return nil
			}
			dec.words, dec.box = dec.box.Buf, replyBox{}
			dec.step = decBasis
			return nil
		}
		dec.box.key(k)
	case decBasis:
		if k.Key == KeyEnter {
			if strings.TrimSpace(dec.box.Buf) != "" {
				dec.basis = dec.box.Buf
			}
			dec.box = replyBox{}
			dec.step = decScope
			return nil
		}
		dec.box.key(k)
	case decScope:
		if k.Key != KeyRune {
			return nil
		}
		var repo, why, scope string
		switch k.Rune {
		case 'r':
			repo, why, scope = dec.targets.Repo, dec.targets.RepoWhy, "this repo"
		case 'f':
			repo, why, scope = dec.targets.Fleet, dec.targets.FleetWhy, "fleet-wide"
		default:
			return nil
		}
		if repo == "" {
			d.Note = clean(scope + " is unavailable: " + why)
			return nil
		}
		dec.step = decSending
		return []Cmd{{Kind: CmdDecide, ID: d.ID, Text: dec.words, Basis: dec.basis, Repo: repo, Ref: d.Entry.Ref}}
	}
	d.clampTop()
	return nil
}

// decided applies the answer to a CmdDecide.
func (d *Detail) decided(ev DecidedEvent) []Cmd {
	dec := &d.dec
	dec.result = wrap(ev.Note, max(d.W-3, 20))
	dec.resultFailed = !ev.OK
	dec.unsureOfLast = ev.Unsure
	if ev.OK || ev.Unsure {
		// Done, or possibly done: retrying could record it twice, so the words are
		// let go, and the note stays up until the next key so it is read.
		*dec = decision{result: dec.result, resultFailed: dec.resultFailed, unsureOfLast: dec.unsureOfLast}
	} else {
		// Nothing was recorded: the words are kept, and the operator is back at the
		// scope prompt to try again or to Esc.
		dec.step = decScope
	}
	d.clampTop()
	return nil
}

func (dec decision) hint() string {
	switch dec.step {
	case decWords:
		return "⏎ next, basis is asked after · Esc cancel, nothing is posted · ctrl-u clear"
	case decBasis:
		return "⏎ next (empty skips the basis) · Esc cancel, nothing is posted · ctrl-u clear"
	case decScope:
		return "r this repo · f fleet-wide · Esc cancel, nothing is posted"
	}
	return "working…"
}

// rows is the footer while a decision is being recorded, or its result is up.
func (dec decision) rows(w, h int) []frow {
	var out []frow
	cw := max(w-1, 10)
	if len(dec.result) > 0 {
		st := fgBold(green)
		switch {
		case dec.unsureOfLast:
			st = fgBold(yellow)
		case dec.resultFailed:
			st = fgBold(red)
		}
		res := dec.result
		if n := max(h/2, 1); len(res) > n {
			res = res[:n]
		}
		for _, t := range res {
			out = append(out, frow{" " + t, st})
		}
	}
	switch dec.step {
	case decWords:
		out = append(out, frow{wordsPrompt, fg(dim)})
		out = append(out, boxRows(dec.box, w, h)...)
	case decBasis:
		out = append(out, frow{basisPrompt, fg(dim)})
		out = append(out, boxRows(dec.box, w, h)...)
	case decScope, decSending:
		out = append(out, dec.scopeRows(cw)...)
	}
	return out
}

func boxRows(b replyBox, w, h int) []frow {
	b.Replying = true
	var out []frow
	for _, t := range b.box(w, h) {
		out = append(out, frow{t, fgBold(yellow)})
	}
	return out
}

func (dec decision) scopeRows(w int) []frow {
	var out []frow
	row := func(key, what, target, why string) {
		if target != "" {
			out = append(out, frow{" " + key + "  " + what + ": " + target, fgBold(yellow)})
			return
		}
		for i, t := range wrap(key+"  "+what+" is unavailable: "+why, max(w-2, 10)) {
			if i > 0 {
				t = "   " + t
			}
			out = append(out, frow{" " + t, fg(dim)})
		}
	}
	out = append(out, frow{"record in which repository's `" + decisions.Label + "` issue? (made first if there is none)", fg(dim)})
	row("r", "this repo", dec.targets.Repo, dec.targets.RepoWhy)
	row("f", "fleet-wide", dec.targets.Fleet, dec.targets.FleetWhy)
	if dec.step == decSending {
		out = append(out, frow{" recording…", fgBold(yellow)})
	}
	return out
}
