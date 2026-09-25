package assets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// mergeSettings composes several profiles' `.claude/settings.json` into one.
//
// The file is Claude Code's hook configuration, shaped
//
//	{"hooks": {"<Event>": [{"matcher": "...", "hooks": [{...}, ...]}, ...]}}
//
// and the merge works at the level of the matcher group, because that is the
// unit Claude Code itself selects on:
//
//  1. For each event, the groups of every profile are unioned, keyed by their
//     `matcher`. A group with NO `matcher` key is its own key: it is not the same
//     as `"matcher": ""`, and the merge does not decide they mean the same thing.
//  2. Within one matcher, the hook entries are unioned and an entry two profiles
//     ship identically (full JSON equality, key order and whitespace aside)
//     appears once. That is what makes the Stop hook, which every profile ships,
//     land once in an ios+web repository rather than firing twice per stop.
//  3. Any top-level key other than `hooks` (`permissions`, `env`, `model`, ...)
//     that two profiles both set must agree. There is no meaningful union of two
//     different `model`s, and picking one silently is the bug merge.go exists to
//     end, so a disagreement fails the sync naming both profiles and the key.
//     The same holds for a matcher group's own keys other than `hooks`.
//
// Output order is fixed, so repeated syncs do not churn: top-level keys, events,
// matcher groups and entries appear in first-seen order across the fragments,
// and fragments arrive in sorted profile-name order (see plan()). Nothing here
// iterates a map to emit. Entries are copied byte-for-byte, so their inner key
// order and string escaping survive; only the whitespace is normalised, to the
// two-space indent the shipped iOS file uses.
func mergeSettings(dest string, frags []Fragment) ([]byte, error) {
	if len(frags) < 2 {
		return nil, fmt.Errorf("merge %s: need at least two fragments, got %d", dest, len(frags))
	}

	var (
		topOrder  []string
		topClaims = map[string][]settingsClaim{}
		events    []string // first-seen order
		byEvent   = map[string]*settingsEvent{}
	)
	for _, f := range frags {
		top, err := orderedObject(f.Content)
		if err != nil {
			return nil, fmt.Errorf("merge %s: %s (%s profile) is not a JSON object: %w", dest, f.Src, f.Profile, err)
		}
		for _, kv := range top {
			if kv.key != "hooks" {
				if _, seen := topClaims[kv.key]; !seen {
					topOrder = append(topOrder, kv.key)
				}
				topClaims[kv.key] = append(topClaims[kv.key], settingsClaim{f.Profile, kv.val})
				continue
			}
			if _, seen := topClaims["hooks"]; !seen {
				topOrder = append(topOrder, "hooks")
			}
			topClaims["hooks"] = append(topClaims["hooks"], settingsClaim{f.Profile, kv.val})

			evs, err := orderedObject(string(kv.val))
			if err != nil {
				return nil, fmt.Errorf("merge %s: the %s profile's \"hooks\" is not an object: %w", dest, f.Profile, err)
			}
			for _, ev := range evs {
				e, ok := byEvent[ev.key]
				if !ok {
					e = &settingsEvent{groups: map[string]*settingsGroup{}}
					byEvent[ev.key] = e
					events = append(events, ev.key)
				}
				if err := e.add(dest, f.Profile, ev.key, ev.val); err != nil {
					return nil, err
				}
			}
		}
	}

	var out bytes.Buffer
	out.WriteByte('{')
	for i, name := range topOrder {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(quoteKey(name))
		out.WriteByte(':')
		if name != "hooks" {
			val, err := settingsAgree(dest, name, topClaims[name])
			if err != nil {
				return nil, err
			}
			out.Write(val)
			continue
		}
		out.WriteByte('{')
		for j, ev := range events {
			if j > 0 {
				out.WriteByte(',')
			}
			out.WriteString(quoteKey(ev))
			out.WriteByte(':')
			out.Write(byEvent[ev].render())
		}
		out.WriteByte('}')
	}
	out.WriteByte('}')

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out.Bytes(), "", "  "); err != nil {
		return nil, fmt.Errorf("merge %s: format: %w", dest, err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

// settingsClaim is one profile's raw value for a key.
type settingsClaim struct {
	profile string
	val     json.RawMessage
}

// settingsEvent is one hook event's merged matcher groups, in first-seen order.
type settingsEvent struct {
	order  []string // group keys, first-seen
	groups map[string]*settingsGroup
}

// settingsGroup is one matcher group: the keys it was written with (first
// claimant's order) and the union of its hook entries.
type settingsGroup struct {
	profile string // the first claimant, for error messages
	kvs     []rawKV
	entries []json.RawMessage
	seen    map[string]bool // canonical form of each entry already in `entries`
}

// add merges one profile's array of matcher groups for this event.
func (e *settingsEvent) add(dest, profile, event string, raw json.RawMessage) error {
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return fmt.Errorf("merge %s: the %s profile's hooks.%s is not an array: %w", dest, profile, event, err)
	}
	for _, gr := range groups {
		kvs, err := orderedObject(string(gr))
		if err != nil {
			return fmt.Errorf("merge %s: a hooks.%s group in the %s profile is not an object: %w", dest, event, profile, err)
		}
		// "matcher absent" and "matcher": "" are different keys. \x00 cannot be
		// the canonical form of any JSON value, so the absent key cannot collide.
		key := "\x00absent"
		var entries []json.RawMessage
		for _, kv := range kvs {
			switch kv.key {
			case "matcher":
				c, err := canonicalJSON(kv.val)
				if err != nil {
					return fmt.Errorf("merge %s: hooks.%s matcher in the %s profile: %w", dest, event, profile, err)
				}
				key = c
			case "hooks":
				if err := json.Unmarshal(kv.val, &entries); err != nil {
					return fmt.Errorf("merge %s: hooks.%s[].hooks in the %s profile is not an array: %w", dest, event, profile, err)
				}
			}
		}
		g, ok := e.groups[key]
		if !ok {
			g = &settingsGroup{profile: profile, kvs: kvs, seen: map[string]bool{}}
			e.groups[key] = g
			e.order = append(e.order, key)
		} else if err := g.agreeOnOtherKeys(dest, event, profile, kvs); err != nil {
			return err
		}
		for _, ent := range entries {
			c, err := canonicalJSON(ent)
			if err != nil {
				return fmt.Errorf("merge %s: hooks.%s entry in the %s profile: %w", dest, event, profile, err)
			}
			if g.seen[c] {
				continue
			}
			g.seen[c] = true
			g.entries = append(g.entries, ent)
		}
	}
	return nil
}

// agreeOnOtherKeys enforces rule 3 for a group's own keys: two profiles writing
// the same matcher may not disagree about anything but the `hooks` list.
func (g *settingsGroup) agreeOnOtherKeys(dest, event, profile string, kvs []rawKV) error {
	have := map[string]json.RawMessage{}
	for _, kv := range g.kvs {
		if kv.key != "hooks" {
			have[kv.key] = kv.val
		}
	}
	for _, kv := range kvs {
		if kv.key == "hooks" {
			continue
		}
		prev, ok := have[kv.key]
		if !ok {
			g.kvs = append(g.kvs, kv)
			continue
		}
		a, err := canonicalJSON(prev)
		if err != nil {
			return err
		}
		b, err := canonicalJSON(kv.val)
		if err != nil {
			return err
		}
		if a != b {
			return fmt.Errorf("merge %s: the %s and %s profiles set %q on the same hooks.%s matcher group to "+
				"different values (%s vs %s) and nothing here can choose between them",
				dest, g.profile, profile, kv.key, event, a, b)
		}
	}
	return nil
}

// render emits the event's array of groups, compact; the caller indents.
func (e *settingsEvent) render() []byte {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, key := range e.order {
		if i > 0 {
			b.WriteByte(',')
		}
		g := e.groups[key]
		b.WriteByte('{')
		wroteHooks := false
		for j, kv := range g.kvs {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(quoteKey(kv.key))
			b.WriteByte(':')
			if kv.key == "hooks" {
				b.Write(joinRaw(g.entries))
				wroteHooks = true
				continue
			}
			b.Write(kv.val)
		}
		if !wroteHooks && len(g.entries) > 0 {
			if len(g.kvs) > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`"hooks":`)
			b.Write(joinRaw(g.entries))
		}
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.Bytes()
}

func joinRaw(items []json.RawMessage) []byte {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(it)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// settingsAgree returns the one value every claimant wrote for a non-`hooks`
// key, or an error naming the first two profiles that differ.
func settingsAgree(dest, key string, cs []settingsClaim) (json.RawMessage, error) {
	want, err := canonicalJSON(cs[0].val)
	if err != nil {
		return nil, fmt.Errorf("merge %s: %q in the %s profile: %w", dest, key, cs[0].profile, err)
	}
	for _, c := range cs[1:] {
		got, err := canonicalJSON(c.val)
		if err != nil {
			return nil, fmt.Errorf("merge %s: %q in the %s profile: %w", dest, key, c.profile, err)
		}
		if got != want {
			return nil, fmt.Errorf(
				"merge %s: the %s and %s profiles set %q to different values (%s vs %s) and nothing here can "+
					"choose between them. Make the two profiles agree, or move the setting into one profile",
				dest, cs[0].profile, c.profile, key, want, got)
		}
	}
	return cs[0].val, nil
}

// rawKV is one member of a JSON object, its value kept as the bytes written.
type rawKV struct {
	key string
	val json.RawMessage
}

// orderedObject parses a JSON object preserving member order, which a
// map[string]any would discard. A repeated key is an error: encoding/json would
// keep the last silently, and this merge exists to stop silent choices.
func orderedObject(s string) ([]rawKV, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected an object, found %v", tok)
	}
	var out []rawKV
	seen := map[string]bool{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		if seen[key] {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		seen[key] = true
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		out = append(out, rawKV{key, val})
	}
	if _, err := dec.Token(); err != nil { // the closing '}'
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after the object")
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("trailing data after the object")
	}
	return out, nil
}

// canonicalJSON renders a value so two values compare equal exactly when they
// are equal as JSON: member order and whitespace are erased, numbers are kept as
// written-digits rather than round-tripped through float64.
func canonicalJSON(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	out, err := json.Marshal(v) // map keys are emitted sorted
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// quoteKey JSON-encodes an object key without HTML escaping, matching how the
// shipped file is written.
func quoteKey(k string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(k)
	return strings.TrimSuffix(b.String(), "\n")
}
