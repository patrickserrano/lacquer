package assets

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gittest"
)

// The settings merger decides what Claude Code runs in every managed repository
// that has more than one profile, and its worst failures are silent: a dropped
// hook is a guard that never fires, a doubled one is a hook that fires twice per
// stop. So the assertions here parse the result and count, rather than grep.

const settingsDest = ".claude/settings.json"

// settingsDoc is the shape of a merged file, parsed.
type settingsDoc struct {
	Hooks map[string][]struct {
		Matcher *string `json:"matcher"`
		Hooks   []struct {
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func parseSettings(t *testing.T, body []byte) settingsDoc {
	t.Helper()
	var d settingsDoc
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("merged settings.json is not valid JSON: %v\n%s", err, body)
	}
	return d
}

// renderSettings plans the REAL profiles for a project shaped like cfg and
// returns the rendered `.claude/settings.json`, or nil if it is not in the plan.
func renderSettings(t *testing.T, cfg *config.Config) []byte {
	t.Helper()
	plan, err := Plan(realLacquer(t), cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var found []Asset
	for _, a := range plan {
		if filepath.ToSlash(a.Dest) == settingsDest {
			found = append(found, a)
		}
	}
	if len(found) == 0 {
		return nil
	}
	if len(found) != 1 {
		t.Fatalf("the plan holds %d %s entries, want at most 1", len(found), settingsDest)
	}
	body, missing, err := Render(found[0], cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("unresolved tokens in %s: %v", settingsDest, missing)
	}
	return body
}

// fleet shapes, named for the repository that has each.
func shape(components ...config.Component) *config.Config {
	return &config.Config{Components: components}
}

var (
	shapeIOSOnly    = shape(config.Component{Path: ".", Profiles: []string{"ios"}})
	shapeIOSWeb     = shape(config.Component{Path: ".", Profiles: []string{"ios"}}, config.Component{Path: "web", Profiles: []string{"web"}})
	shapeRail       = shape(config.Component{Path: ".", Profiles: []string{"ios"}}, config.Component{Path: "server", Profiles: []string{"supabase"}})
	shapeAll        = shape(config.Component{Path: ".", Profiles: []string{"ios"}}, config.Component{Path: "web", Profiles: []string{"web"}}, config.Component{Path: "server", Profiles: []string{"supabase"}})
	shapeWebOnly    = shape(config.Component{Path: ".", Profiles: []string{"web"}})
	shapeSupaOnly   = shape(config.Component{Path: ".", Profiles: []string{"supabase"}})
	shapePixelFox   = shape(config.Component{Path: ".", Profiles: []string{"web", "marketing"}})
	stopCommandText = "lacquer console inbox hook stop"
)

func stopHookCount(d settingsDoc) int {
	n := 0
	for _, g := range d.Hooks["Stop"] {
		for _, h := range g.Hooks {
			if strings.Contains(h.Command, stopCommandText) {
				n++
			}
		}
	}
	return n
}

func readGolden(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/ios_only_settings.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestIOSOnlySettingsAreByteIdenticalToBeforeTheMerger is the dangerous case.
// The golden file is the rendering origin/main (v1.59.0, before this merger and
// before web/supabase shipped settings) produced for an ios-only project, and
// thirteen repositories receive exactly that. The unit is delivering a hook to
// other profiles; it must not move one byte of this one.
func TestIOSOnlySettingsAreByteIdenticalToBeforeTheMerger(t *testing.T) {
	got := renderSettings(t, shapeIOSOnly)
	if !bytes.Equal(got, readGolden(t)) {
		t.Fatalf("an ios-only project's %s is no longer byte-identical to the pre-merger rendering.\n--- got ---\n%s", settingsDest, got)
	}
}

// hookSet flattens a settings file to the multiset of (event, matcher, entry)
// triples it configures, sorted. It is what Claude Code effectively runs: it
// fires every group whose matcher matches, so how one profile's entries are
// split across same-matcher groups is presentation, and dropping, adding or
// changing an entry is not.
func hookSet(t *testing.T, body []byte) []string {
	t.Helper()
	top, err := orderedObject(string(body))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, kv := range top {
		if kv.key != "hooks" {
			out = append(out, "top|"+kv.key)
			continue
		}
		evs, err := orderedObject(string(kv.val))
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			var groups []json.RawMessage
			if err := json.Unmarshal(ev.val, &groups); err != nil {
				t.Fatal(err)
			}
			for _, g := range groups {
				m := "<absent>"
				var entries []json.RawMessage
				gk, err := orderedObject(string(g))
				if err != nil {
					t.Fatal(err)
				}
				for _, k := range gk {
					switch k.key {
					case "matcher":
						m = string(k.val)
					case "hooks":
						if err := json.Unmarshal(k.val, &entries); err != nil {
							t.Fatal(err)
						}
					}
				}
				for _, e := range entries {
					c, err := canonicalJSON(e)
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, ev.key+"|"+m+"|"+c)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// ios+web, ios+supabase and all three each configure exactly what the iOS file
// does: every iOS hook once, and exactly one Stop hook. The comparison is on the
// flattened (event, matcher, entry) set, which fails on a dropped hook, a
// doubled one or a changed one.
//
// It is not byte equality with the iOS file, and that is a deliberate,
// documented consequence of the merge rule. The iOS file writes two separate
// `Edit|Write` groups and two `Bash` groups under PreToolUse; the rule is "union
// the groups keyed by matcher", so a merged file holds one of each with the
// entries in the same order. Claude Code runs every matching group's hooks, so
// the behaviour is unchanged.
func TestMergedSettingsConfigureTheIOSHooksOnceAndTheStopHookOnce(t *testing.T) {
	want := hookSet(t, readGolden(t))
	var first []byte
	for _, name := range []string{"ios+web", "ios+supabase", "ios+web+supabase"} {
		cfg := map[string]*config.Config{"ios+web": shapeIOSWeb, "ios+supabase": shapeRail, "ios+web+supabase": shapeAll}[name]
		t.Run(name, func(t *testing.T) {
			got := renderSettings(t, cfg)
			if n := stopHookCount(parseSettings(t, got)); n != 1 {
				t.Errorf("%d Stop hooks running `%s`, want exactly 1", n, stopCommandText)
			}
			if g := hookSet(t, got); !reflect.DeepEqual(g, want) {
				t.Errorf("merged hooks differ from the iOS file's.\n got %q\nwant %q", g, want)
			}
			if first == nil {
				first = got
			} else if !bytes.Equal(first, got) {
				t.Errorf("the same profiles rendered differently depending on which other profile joined them")
			}
		})
	}
}

// One profile, or web+marketing (marketing ships no settings), is not a merge:
// the file is the profile's own, Stop hook only.
func TestSingleClaimantSettingsAreTheProfilesOwnFile(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg     *config.Config
		profile string
	}{
		"web-only":      {shapeWebOnly, "web"},
		"supabase-only": {shapeSupaOnly, "supabase"},
		"web+marketing": {shapePixelFox, "web"},
	} {
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile("../../profiles/" + tc.profile + "/root/.claude/settings.json")
			if err != nil {
				t.Fatal(err)
			}
			got := renderSettings(t, tc.cfg)
			if !bytes.Equal(got, want) {
				t.Fatalf("%s differs from profiles/%s's own file:\n%s", settingsDest, tc.profile, got)
			}
			d := parseSettings(t, got)
			if len(d.Hooks) != 1 || stopHookCount(d) != 1 {
				t.Errorf("want a Stop-only file with one Stop hook, got events %v, %d Stop hooks", keysOf(d.Hooks), stopHookCount(d))
			}
		})
	}
}

// The Stop entry in web and supabase is the iOS one, character for character:
// same command (including `|| true`) and timeout. Compared as decoded JSON so a
// reformat is not a failure but a changed command is.
func TestWebAndSupabaseShipTheIOSStopEntry(t *testing.T) {
	stop := func(profile string) string {
		b, err := os.ReadFile("../../profiles/" + profile + "/root/.claude/settings.json")
		if err != nil {
			t.Fatal(err)
		}
		top, err := orderedObject(string(b))
		if err != nil {
			t.Fatal(err)
		}
		if len(top) != 1 || top[0].key != "hooks" {
			t.Fatalf("profiles/%s settings.json has top-level keys other than hooks", profile)
		}
		evs, err := orderedObject(string(top[0].val))
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			if ev.key == "Stop" {
				if profile != "ios" && len(evs) != 1 {
					t.Errorf("profiles/%s ships events besides Stop", profile)
				}
				c, err := canonicalJSON(ev.val)
				if err != nil {
					t.Fatal(err)
				}
				return c
			}
		}
		t.Fatalf("profiles/%s has no Stop event", profile)
		return ""
	}
	want := stop("ios")
	for _, p := range []string{"web", "supabase"} {
		if got := stop(p); got != want {
			t.Errorf("profiles/%s's Stop entry drifted from iOS's:\n got %s\nwant %s", p, got, want)
		}
	}
	if _, err := os.Stat("../../profiles/marketing/root/.claude/settings.json"); err == nil {
		t.Error("marketing ships a settings.json; it is meant to ship none")
	}
}

func keysOf[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// The rules, on small fragments
// ---------------------------------------------------------------------------

func settingsFrags(bodies ...string) []Fragment { return fragments(bodies...) }

func mustMerge(t *testing.T, bodies ...string) []byte {
	t.Helper()
	out, err := mergeSettings(settingsDest, settingsFrags(bodies...))
	if err != nil {
		t.Fatalf("mergeSettings: %v", err)
	}
	return out
}

func hookCmd(c string) string { return `{"type":"command","command":"` + c + `"}` }

func settingsWith(event string, groups ...string) string {
	return `{"hooks":{"` + event + `":[` + strings.Join(groups, ",") + `]}}`
}

func group(matcher string, cmds ...string) string {
	m := ""
	if matcher != "" {
		m = `"matcher":"` + matcher + `",`
	}
	return `{` + m + `"hooks":[` + strings.Join(cmds, ",") + `]}`
}

func commandsOf(t *testing.T, body []byte, event string) [][]string {
	t.Helper()
	var out [][]string
	for _, g := range parseSettings(t, body).Hooks[event] {
		var cs []string
		for _, h := range g.Hooks {
			cs = append(cs, h.Command)
		}
		out = append(out, cs)
	}
	return out
}

func TestSettingsTwoMatchersUnderOneEventAreBothKept(t *testing.T) {
	body := mustMerge(t,
		settingsWith("PreToolUse", group("Edit|Write", hookCmd("a"))),
		settingsWith("PreToolUse", group("Bash", hookCmd("b"))))
	d := parseSettings(t, body)
	if len(d.Hooks["PreToolUse"]) != 2 {
		t.Fatalf("want both matcher groups kept, got %d:\n%s", len(d.Hooks["PreToolUse"]), body)
	}
	if *d.Hooks["PreToolUse"][0].Matcher != "Edit|Write" || *d.Hooks["PreToolUse"][1].Matcher != "Bash" {
		t.Errorf("groups are not in first-seen order:\n%s", body)
	}
}

func TestSettingsSameMatcherDifferentEntriesGivesTheUnion(t *testing.T) {
	body := mustMerge(t,
		settingsWith("PostToolUse", group("Bash", hookCmd("a"))),
		settingsWith("PostToolUse", group("Bash", hookCmd("b"))))
	got := commandsOf(t, body, "PostToolUse")
	if len(got) != 1 || len(got[0]) != 2 || got[0][0] != "a" || got[0][1] != "b" {
		t.Fatalf("want one Bash group holding a then b, got %v", got)
	}
}

func TestSettingsIdenticalEntriesAreDedupedIgnoringKeyOrder(t *testing.T) {
	body := mustMerge(t,
		settingsWith("Stop", group("", `{"type":"command","command":"x","timeout":10}`)),
		settingsWith("Stop", group("", `{"timeout":10,  "command":"x","type":"command"}`)))
	if got := commandsOf(t, body, "Stop"); len(got) != 1 || len(got[0]) != 1 {
		t.Fatalf("an identical entry must appear once, got %v\n%s", got, body)
	}
	// Full equality, not command equality: a different timeout is a different entry.
	body = mustMerge(t,
		settingsWith("Stop", group("", `{"type":"command","command":"x","timeout":10}`)),
		settingsWith("Stop", group("", `{"type":"command","command":"x","timeout":99}`)))
	if got := commandsOf(t, body, "Stop"); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("entries differing in timeout must both be kept, got %v", got)
	}
}

// An absent matcher and `"matcher": ""` are different keys, per the brief.
func TestSettingsAbsentMatcherIsNotTheEmptyMatcher(t *testing.T) {
	body := mustMerge(t,
		settingsWith("SessionStart", group("", hookCmd("a"))),
		settingsWith("SessionStart", `{"matcher":"","hooks":[`+hookCmd("b")+`]}`))
	if n := len(parseSettings(t, body).Hooks["SessionStart"]); n != 2 {
		t.Fatalf("absent and empty matchers were folded into %d group(s), want 2:\n%s", n, body)
	}
}

func TestSettingsUnionsEventsAndKeepsTheOnesOnlyOneProfileHas(t *testing.T) {
	body := mustMerge(t,
		settingsWith("SessionStart", group("", hookCmd("s"))),
		settingsWith("Stop", group("", hookCmd("t"))))
	d := parseSettings(t, body)
	if len(d.Hooks) != 2 || len(d.Hooks["SessionStart"]) != 1 || len(d.Hooks["Stop"]) != 1 {
		t.Fatalf("an event only one profile ships was dropped:\n%s", body)
	}
}

func TestSettingsConflictingTopLevelKeyFailsNamingBothProfilesAndTheKey(t *testing.T) {
	_, err := mergeSettings(settingsDest, settingsFrags(
		`{"model":"opus","hooks":{}}`, `{"model":"sonnet","hooks":{}}`))
	if err == nil {
		t.Fatal("two profiles set `model` differently and the merge picked one")
	}
	for _, want := range []string{"alpha", "beta", `"model"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s: %v", want, err)
		}
	}
	// Equal values are fine, and a key only one profile sets is kept.
	body := mustMerge(t, `{"model":"opus","env":{"A":"1"}}`, `{"model":"opus"}`)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil || string(got["model"]) != `"opus"` || got["env"] == nil {
		t.Fatalf("agreeing/unique keys were not kept: %v\n%s", err, body)
	}
	// Equal as JSON, not as bytes.
	mustMerge(t, `{"env":{"A":"1","B":"2"}}`, `{"env":{ "B":"2", "A":"1" }}`)
}

func TestSettingsConflictingKeyInOneMatcherGroupFails(t *testing.T) {
	_, err := mergeSettings(settingsDest, settingsFrags(
		`{"hooks":{"Stop":[{"matcher":"x","note":"1","hooks":[]}]}}`,
		`{"hooks":{"Stop":[{"matcher":"x","note":"2","hooks":[]}]}}`))
	if err == nil || !strings.Contains(err.Error(), `"note"`) || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("a group-level disagreement must fail naming both profiles and the key, got %v", err)
	}
}

func TestSettingsRefusesMalformedFragments(t *testing.T) {
	for name, tc := range map[string][]string{
		"not json":          {`{"hooks":{}}`, `nope`},
		"top level array":   {`{"hooks":{}}`, `[]`},
		"hooks not object":  {`{"hooks":{}}`, `{"hooks":[]}`},
		"event not array":   {`{"hooks":{}}`, `{"hooks":{"Stop":{}}}`},
		"group not object":  {`{"hooks":{}}`, `{"hooks":{"Stop":[1]}}`},
		"duplicate key":     {`{"hooks":{}}`, `{"a":1,"a":2}`},
		"trailing data":     {`{"hooks":{}}`, `{"a":1} {"b":2}`},
		"entries not array": {`{"hooks":{}}`, `{"hooks":{"Stop":[{"hooks":{}}]}}`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeSettings(settingsDest, settingsFrags(tc...)); err == nil {
				t.Fatal("malformed fragment accepted")
			}
		})
	}
	if _, err := mergeSettings(settingsDest, settingsFrags(`{}`)); err == nil {
		t.Error("a merge of one fragment was accepted")
	}
}

// Output order is documented and fixed: first-seen across fragments in profile
// order. Asserting the exact bytes pins the order itself, not just its stability.
func TestSettingsOutputOrderIsFirstSeenAndPrettyPrinted(t *testing.T) {
	a := `{"z":1,"hooks":{"Stop":[{"hooks":[{"command":"s","type":"command"}]}],"Alpha":[{"matcher":"m","hooks":[{"command":"a"}]}]}}`
	b := `{"y":2,"hooks":{"Beta":[{"matcher":"m","hooks":[{"command":"b"}]}],"Stop":[{"hooks":[{"command":"t"}]}]}}`
	want := `{
  "z": 1,
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "command": "s",
            "type": "command"
          },
          {
            "command": "t"
          }
        ]
      }
    ],
    "Alpha": [
      {
        "matcher": "m",
        "hooks": [
          {
            "command": "a"
          }
        ]
      }
    ],
    "Beta": [
      {
        "matcher": "m",
        "hooks": [
          {
            "command": "b"
          }
        ]
      }
    ]
  },
  "y": 2
}
`
	if got := string(mustMerge(t, a, b)); got != want {
		t.Fatalf("output order/format changed.\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

// Determinism: merging twice is byte-identical, and 50 repetitions over a
// fragment set wide enough that a map-ordered emit would shuffle (Go randomises
// map iteration per range) all agree.
func TestSettingsMergeIsDeterministic(t *testing.T) {
	var events []string
	var evsB []string
	for i := 0; i < 12; i++ {
		e := string(rune('A' + i))
		events = append(events, `"`+e+`":[`+group("m1", hookCmd("a"+e))+`,`+group("m2", hookCmd("b"+e))+`]`)
		evsB = append(evsB, `"`+e+`":[`+group("m2", hookCmd("c"+e))+`,`+group("m3", hookCmd("d"+e))+`]`)
	}
	a := `{"k1":1,"k2":2,"k3":3,"k4":4,"hooks":{` + strings.Join(events, ",") + `}}`
	b := `{"k5":5,"k6":6,"k3":3,"hooks":{` + strings.Join(evsB, ",") + `}}`
	first := mustMerge(t, a, b)
	for i := 0; i < 50; i++ {
		if got := mustMerge(t, a, b); !bytes.Equal(got, first) {
			t.Fatalf("run %d differs from the first:\n%s\n---\n%s", i, got, first)
		}
	}
	// The result is a fixed point of "merge with yourself": re-merging changes nothing.
	if again := mustMerge(t, string(first), string(first)); !bytes.Equal(again, first) {
		t.Fatalf("merging the result with itself changed it:\n%s", again)
	}
	// And Render twice through the real plan agrees.
	x, y := renderSettings(t, shapeAll), renderSettings(t, shapeAll)
	if !bytes.Equal(x, y) {
		t.Fatal("two renders of the same plan differ")
	}
}

// ---------------------------------------------------------------------------
// Through the plan: what a sync actually does
// ---------------------------------------------------------------------------

func TestSettingsConflictSurfacesThroughPlanAndFailsTheWrite(t *testing.T) {
	h := t.TempDir()
	stubProfile(t, h, "alpha", settingsDest, `{"model":"opus","hooks":{}}`)
	stubProfile(t, h, "beta", settingsDest, `{"model":"sonnet","hooks":{}}`)
	cfg := twoProfileConfig()

	plan, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan rejected a registered destination outright: %v", err)
	}
	proj := t.TempDir()
	gittest.Init(t, proj, "-q")
	err = Copy(proj, plan, cfg)
	if err == nil {
		t.Fatal("Copy accepted a settings.json whose two profiles disagree about `model`")
	}
	for _, want := range []string{"alpha", "beta", `"model"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("sync error does not name %s: %v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(proj, ".claude", "settings.json")); statErr == nil {
		t.Error("a settings.json was written despite the conflict")
	}
}

func TestSettingsRegistered(t *testing.T) {
	var ok bool
	for _, d := range MergeableDests() {
		if d == settingsDest {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("%s has no merger registered: any two profiles shipping it fail the sync", settingsDest)
	}
}
