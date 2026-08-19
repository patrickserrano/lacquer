package assets

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Two profiles claiming one destination is a silent data loss, and this file is
// what stops it being silent.
//
// plan() dedupes by destination and keeps the first writer. For core-vs-profile
// that is a deliberate precedence rule. For profile-vs-profile it is a bug with
// no symptom: profiles/web/root/lefthook.yml and profiles/supabase/root/
// lefthook.yml both target `lefthook.yml` at the repository root, so in an
// ios+web+supabase repository `supabase` sorted first and every web hook — the
// biome check, the typecheck, the pre-push test, build and audit — was never
// installed. `lacquer audit` reported OK throughout, because the lacquer really
// would write that exact file; the collision is invisible from the destination
// alone. The observed cost was a biome violation reaching CI that a pre-commit
// hook catches locally in under a second.
//
// So a profile-vs-profile collision is now one of exactly two things:
//
//   - a destination with a merger registered below, which composes every
//     claimant into one file; or
//   - an error from plan(), which fails the sync rather than dropping a profile.
//
// There is no third case, and that is the point: adding profiles/x/root/foo.yml
// beside an existing profiles/y/root/foo.yml can no longer half-work.

// merger composes several profiles' fragments, already token-substituted for
// their own components, into the single file their shared destination receives.
type merger func(dest string, frags []Fragment) ([]byte, error)

// mergers maps a destination to the strategy that combines the profiles
// claiming it. A destination absent from this map may be claimed by at most one
// profile — see mergerFor's caller in plan().
//
// Keys are slash-separated, destination-relative paths.
var mergers = map[string]merger{
	"lefthook.yml": mergeLefthook,
}

// MergeableDests returns the destinations a merger is registered for. Exported
// so a test can assert the registry still describes reality — an entry naming a
// path no two profiles ship is dead text that reads as an active decision, the
// same failure mode internal/exclusion exists to catch.
func MergeableDests() []string {
	out := make([]string, 0, len(mergers))
	for d := range mergers {
		out = append(out, d)
	}
	return out
}

// mergerFor returns the strategy registered for a destination, if any.
func mergerFor(dest string) (merger, bool) {
	m, ok := mergers[filepath.ToSlash(dest)]
	return m, ok
}

// Fragment is one profile's contribution to a merged destination: the bytes that
// profile alone would have written, with its own component's tokens already
// substituted.
//
// Substitution happens per fragment and BEFORE the merge, which is the whole
// reason the merge can be correct: each profile's commands carry
// `root: "{{COMPONENT_PREFIX}}"`, and that renders "admin/" for the web
// component and "server/" for the supabase one. Merging first and substituting
// after would give every command one prefix — a file where the web hooks run
// pnpm against the supabase directory, which is worse than the bug being fixed.
type Fragment struct {
	// Profile is the profile that shipped this fragment. It is the label used to
	// disambiguate two same-named commands, so it has to be stable across
	// projects: config.Load already guarantees one component per profile, which
	// makes the profile name a unique name for the component too.
	Profile string
	// Src is the absolute path of the source file, for error messages.
	Src string
	// Content is the fragment's rendered bytes.
	Content string
}

// mergeLefthook composes several profiles' lefthook configurations into one.
//
// The rules, in the order they matter:
//
//  1. Every command keeps the `root:` it was written with. Nothing here reads
//     or rewrites it — fragments arrive already substituted, so scoping survives
//     by construction rather than by care.
//  2. A command two profiles ship IDENTICALLY appears once. `secrets` and
//     `conventional` are repo-root scans (no `root:`, the script reads the whole
//     staged set itself); emitting them per profile would run the same scan N
//     times on every commit and report every hit N times.
//  3. A command two profiles ship DIFFERENTLY is kept twice, suffixed with the
//     profile name. `pre-push.test` is the case that matters: `pnpm run
//     test:coverage` and `deno test` are both wanted and neither may win.
//  4. Anything else two profiles disagree on — `parallel`, `piped`, a top-level
//     `min_version` — is an error. Silently picking one profile's value is the
//     class of bug this whole file exists to end.
//
// Comments survive: yaml.v3 keeps head comments on the nodes they precede, and
// the fragments' rationale is most of their value.
func mergeLefthook(dest string, frags []Fragment) ([]byte, error) {
	if len(frags) < 2 {
		return nil, fmt.Errorf("merge %s: need at least two fragments, got %d", dest, len(frags))
	}

	var (
		order   []string               // top-level keys, in first-appearance order
		claims  = map[string][]entry{} // key -> every fragment's value for it
		headers []string               // each fragment's file header comment
	)
	for _, f := range frags {
		m, header, err := fragmentMapping(dest, f)
		if err != nil {
			return nil, err
		}
		if header != "" {
			headers = append(headers, fmt.Sprintf("# --- from the %s profile ---\n%s", f.Profile, header))
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			k, v := m.Content[i], m.Content[i+1]
			if _, seen := claims[k.Value]; !seen {
				order = append(order, k.Value)
			}
			claims[k.Value] = append(claims[k.Value], entry{key: k, value: v, profile: f.Profile})
		}
	}

	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, name := range order {
		es := claims[name]
		var key, value *yaml.Node
		if !isHook(es) {
			// Not a hook block: every fragment has to agree, because there is no
			// meaning to "half of min_version".
			merged, err := agree(dest, name, es)
			if err != nil {
				return nil, err
			}
			key, value = merged.key, merged.value
		} else {
			hook, err := mergeHook(dest, name, es)
			if err != nil {
				return nil, err
			}
			key, value = mergedKey(es), hook
		}
		// A blank line before each block after the first. yaml.v3 emits no blank
		// lines of its own, and a merged file is long enough — three hooks, a
		// dozen commands from two toolchains — that running them together makes
		// it materially harder to read than either fragment was.
		if len(root.Content) > 0 {
			spaced := *key
			spaced.HeadComment = "\n" + key.HeadComment
			key = &spaced
		}
		root.Content = append(root.Content, key, value)
	}

	doc := &yaml.Node{
		Kind:        yaml.DocumentNode,
		HeadComment: mergeHeader(dest, frags, headers),
		Content:     []*yaml.Node{root},
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("merge %s: encode: %w", dest, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("merge %s: encode: %w", dest, err)
	}
	return buf.Bytes(), nil
}

// entry is one fragment's key/value pair at some level of the document.
type entry struct {
	key     *yaml.Node
	value   *yaml.Node
	profile string
}

// mergedKey returns the key node a merged hook is emitted under, carrying every
// fragment's rationale for that hook rather than only the first one's.
//
// Taking es[0].key alone loses real content, and loses it invisibly. yaml.v3
// attaches a comment block to whatever follows it, so a fragment whose file
// header is not separated from `pre-commit:` by a blank line has that header on
// its FIRST KEY, not on the document — and every fragment but the leading one
// then contributes a key node nothing emits. profiles/supabase's explanation of
// why `deno check` and `deno test` belong at pre-push is exactly this kind of
// comment, and it is the sort of note that stops a hook being "simplified" back
// into the gap it was written to close.
func mergedKey(es []entry) *yaml.Node {
	key := *es[0].key
	type block struct{ profile, text string }
	var blocks []block
	for _, e := range es {
		text := e.key.HeadComment
		if strings.TrimSpace(text) == "" {
			continue
		}
		var dup bool
		for _, b := range blocks {
			if b.text == text {
				dup = true
				break
			}
		}
		if !dup {
			blocks = append(blocks, block{e.profile, text})
		}
	}
	switch len(blocks) {
	case 0:
		key.HeadComment = ""
	case 1:
		// One profile had anything to say: no attribution, because there is
		// nothing to tell it apart from.
		key.HeadComment = blocks[0].text
	default:
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			parts = append(parts, fmt.Sprintf("# --- from the %s profile ---\n%s", b.profile, b.text))
		}
		key.HeadComment = strings.Join(parts, "\n#\n")
	}
	return &key
}

// isHook reports whether a top-level key is a git hook block rather than a
// setting. A hook is a mapping carrying `commands` or `scripts`; anything else
// (min_version, colors, source_dir, skip_output) is a setting and must agree.
func isHook(es []entry) bool {
	for _, e := range es {
		if e.value.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(e.value.Content); i += 2 {
			switch e.value.Content[i].Value {
			case "commands", "scripts":
				return true
			}
		}
	}
	return false
}

// mergeHook merges one hook block: its `commands` map is unioned, and every
// other key must agree across the profiles that set it.
func mergeHook(dest, hook string, es []entry) (*yaml.Node, error) {
	var (
		order  []string
		claims = map[string][]entry{}
	)
	for _, e := range es {
		if e.value.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("merge %s: %s profile's %q is not a mapping", dest, e.profile, hook)
		}
		for i := 0; i+1 < len(e.value.Content); i += 2 {
			k, v := e.value.Content[i], e.value.Content[i+1]
			if _, seen := claims[k.Value]; !seen {
				order = append(order, k.Value)
			}
			claims[k.Value] = append(claims[k.Value], entry{key: k, value: v, profile: e.profile})
		}
	}

	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, name := range order {
		sub := claims[name]
		if name == "commands" || name == "scripts" {
			merged, err := mergeCommands(dest, hook, name, sub)
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, sub[0].key, merged)
			continue
		}
		merged, err := agree(dest, hook+"."+name, sub)
		if err != nil {
			return nil, err
		}
		out.Content = append(out.Content, merged.key, merged.value)
	}
	return out, nil
}

// mergeCommands unions the command maps of one hook.
//
// Same name AND same body: one command. Same name, different body: both, each
// suffixed with its profile. Different names: both, untouched.
func mergeCommands(dest, hook, kind string, es []entry) (*yaml.Node, error) {
	path := hook + "." + kind
	var (
		order  []string
		claims = map[string][]entry{}
	)
	for _, e := range es {
		if e.value.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("merge %s: %s profile's %q is not a mapping", dest, e.profile, path)
		}
		for i := 0; i+1 < len(e.value.Content); i += 2 {
			k, v := e.value.Content[i], e.value.Content[i+1]
			if _, seen := claims[k.Value]; !seen {
				order = append(order, k.Value)
			}
			claims[k.Value] = append(claims[k.Value], entry{key: k, value: v, profile: e.profile})
		}
	}

	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, name := range order {
		distinct, err := dedupe(claims[name])
		if err != nil {
			return nil, fmt.Errorf("merge %s: %s.%s: %w", dest, path, name, err)
		}
		if len(distinct) == 1 {
			out.Content = append(out.Content, distinct[0].key, distinct[0].value)
			continue
		}
		// More than one profile ships a command by this name and they are not the
		// same command. Neither may be dropped and neither may keep the bare name,
		// so both are qualified. A reader seeing `test-web` and `test-supabase`
		// knows immediately which component each covers; a reader seeing one
		// `test` cannot tell that the other was thrown away.
		for _, e := range distinct {
			key := *e.key
			key.Value = name + "-" + e.profile
			key.Style = 0
			key.HeadComment = joinComments(fmt.Sprintf(
				"# `%s` as the %s profile ships it. More than one profile ships a %s\n"+
					"# `%s` and they are not the same command, so each keeps its own under a\n"+
					"# qualified name — dropping either is the bug this merge exists to prevent.",
				name, e.profile, hook, name), e.key.HeadComment)
			out.Content = append(out.Content, &key, e.value)
		}
	}
	return out, nil
}

// dedupe collapses entries whose values are equal ignoring comments and quoting
// style, keeping the first of each distinct value.
//
// Comments are ignored deliberately: profiles/web's `secrets` says "Mirrors the
// iOS profile's `no-secrets` hook" and profiles/supabase's says the same thing
// with a different word. Treating that as two different commands would run the
// repository-wide secret scan twice on every commit — the exact duplication
// rule 2 above exists to prevent.
func dedupe(es []entry) ([]entry, error) {
	var out []entry
	var keys []string
	for _, e := range es {
		k, err := canonical(e.value)
		if err != nil {
			return nil, err
		}
		var dup bool
		for _, prev := range keys {
			if prev == k {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		keys = append(keys, k)
		out = append(out, e)
	}
	return out, nil
}

// agree returns the single entry every fragment must have written identically,
// or an error naming both profiles.
func agree(dest, path string, es []entry) (entry, error) {
	base := es[0]
	want, err := canonical(base.value)
	if err != nil {
		return entry{}, err
	}
	for _, e := range es[1:] {
		got, err := canonical(e.value)
		if err != nil {
			return entry{}, err
		}
		if got == want {
			continue
		}
		return entry{}, fmt.Errorf(
			"merge %s: the %s and %s profiles set %q to different values (%s vs %s) and nothing here can "+
				"choose between them. Make the two profiles agree, or move the setting onto the individual "+
				"commands where each profile owns its own",
			dest, base.profile, e.profile, path,
			strings.TrimSpace(want), strings.TrimSpace(got))
	}
	return base, nil
}

// canonical renders a node with comments, quoting style and source positions
// removed, so two nodes compare equal when they mean the same thing.
func canonical(n *yaml.Node) (string, error) {
	out, err := yaml.Marshal(bare(n))
	if err != nil {
		return "", fmt.Errorf("compare node: %w", err)
	}
	return string(out), nil
}

// bare deep-copies a node stripped of everything that is presentation rather
// than meaning.
func bare(n *yaml.Node) *yaml.Node {
	c := *n
	c.HeadComment, c.LineComment, c.FootComment = "", "", ""
	c.Style = 0
	c.Line, c.Column = 0, 0
	c.Content = nil
	for _, child := range n.Content {
		c.Content = append(c.Content, bare(child))
	}
	return &c
}

// fragmentMapping parses a fragment, returning its top-level mapping and the
// file header comment that preceded it.
func fragmentMapping(dest string, f Fragment) (*yaml.Node, string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(f.Content), &doc); err != nil {
		return nil, "", fmt.Errorf("merge %s: %s (%s profile) is not valid YAML: %w", dest, f.Src, f.Profile, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, "", fmt.Errorf("merge %s: %s (%s profile) is empty", dest, f.Src, f.Profile)
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, "", fmt.Errorf("merge %s: %s (%s profile) is not a mapping at the top level", dest, f.Src, f.Profile)
	}
	return m, doc.HeadComment, nil
}

// mergeHeader is the composed file's own explanation, followed by each
// fragment's original header attributed to the profile it came from.
//
// The originals are kept rather than summarised because they are not
// interchangeable: the web fragment documents `pnpm exec lefthook install` and
// the supabase one documents `npx lefthook install` / `brew install lefthook`
// for a repository with no Node toolchain. A reader of the merged file in a
// repository that has both needs both.
func mergeHeader(dest string, frags []Fragment, headers []string) string {
	names := make([]string, 0, len(frags))
	for _, f := range frags {
		names = append(names, f.Profile)
	}
	head := fmt.Sprintf(
		"# Composed by `lacquer sync` from the %s profiles.\n"+
			"# Do not edit: the next sync overwrites it. Edit the profile fragments\n"+
			"# (profiles/<name>/root/%s) in the lacquer instead.\n"+
			"#\n"+
			"# Every one of those profiles ships this same path, so they are merged\n"+
			"# here rather than one of them silently winning and the rest never being\n"+
			"# installed. Each command keeps the `root:` of the component it came\n"+
			"# from; a command the profiles ship identically appears once; a command\n"+
			"# they ship differently keeps both, each suffixed with its profile name.",
		strings.Join(names, " and "), filepath.ToSlash(dest))
	if len(headers) == 0 {
		return head
	}
	return head + "\n#\n" + strings.Join(headers, "\n#\n")
}

// joinComments concatenates two comment blocks, tolerating an empty second.
func joinComments(a, b string) string {
	if strings.TrimSpace(b) == "" {
		return a
	}
	return a + "\n#\n" + b
}
