package inboxwatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- fixtures: the snapshot as the producer writes it (docs/asc-snapshot.md) ----

type obj = map[string]any

func rfc(d time.Duration) string { return t0.Add(-d).UTC().Format(time.RFC3339) }

// ascVer is a version whose state began since before t0.
func ascVer(id, platform, ver, state string, since time.Duration, source string, buildID any) obj {
	v := obj{"id": id, "platform": platform, "versionString": ver, "appStoreState": state, "buildId": buildID}
	if since >= 0 {
		v["stateSince"], v["stateSinceSource"] = rfc(since), source
	}
	return v
}

// ascBld is a build uploaded uploaded before t0.
func ascBld(id, platform, num, state string, uploaded time.Duration, attached any) obj {
	return obj{"id": id, "platform": platform, "number": num, "versionString": "1.9", "processingState": state,
		"expired": false, "uploadedDate": rfc(uploaded), "attachedVersionId": attached}
}

func ascApp(bundle, name string, versions, builds []obj) obj {
	if versions == nil {
		versions = []obj{}
	}
	if builds == nil {
		builds = []obj{}
	}
	return obj{"bundleId": bundle, "name": name, "ascAppId": "1234567890", "versions": versions, "builds": builds}
}

// ascSnap is a snapshot generated genAgo before t0.
func ascSnap(genAgo time.Duration, apps ...obj) obj {
	if apps == nil {
		apps = []obj{}
	}
	return obj{"schemaVersion": 1, "generatedAt": rfc(genAgo), "producer": "fleet-ops asc-status 1.4.0", "errors": []obj{}, "apps": apps}
}

func mustJSON(t *testing.T, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ascModel is a Model on the Stuck tab, the clock at t0+adv, whose snapshot has
// been read from raw exactly as Env reads it.
func ascModel(t *testing.T, adv time.Duration, raw []byte) Model {
	t.Helper()
	p := Program(tabModel(t, 160, 30))
	p = onTab(t, p.(Model), "5")
	p, _ = send(t, p, tickAt(adv), PRsEvent{At: t0.Add(adv)}, LaterEvent{At: t0.Add(adv)},
		LoadedEvent{Data: Data{ASC: withAt(ParseASC(raw, "/x/asc-snapshot.json"), t0.Add(adv))}, At: t0.Add(adv)})
	return p.(Model)
}

// healthyASC is a snapshot read at t0+adv, fresh, listing one app with nothing stuck.
func healthyASC(adv time.Duration) ASCState {
	st := ParseASC(mustJSON(nil, ascSnap(adv+time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "IN_REVIEW", 30*time.Minute, "firstSeen", nil)}, []obj{}))), "/x/asc-snapshot.json")
	st.At = t0.Add(adv)
	return st
}

// dailyBread is the app the tests hang versions and builds on.
func dailyBread(versions, builds []obj) obj {
	return ascApp("com.patrickserrano.dailybread", "Daily Bread", versions, builds)
}

func withAt(st ASCState, at time.Time) ASCState { st.At = at; return st }

func ascKeys(m Model) []string { return stuckRefs(m) }

// shownKeys is what the tab lists once dismissals are applied.
func shownKeys(m Model) []string {
	shown, _ := m.stuckShown(m.stuckReports())
	var out []string
	for _, items := range shown {
		for _, it := range items {
			out = append(out, it.Key)
		}
	}
	return out
}

func wantKeys(t *testing.T, m Model, want ...string) {
	t.Helper()
	got := ascKeys(m)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("stuck rows = %v, want %v\n%s", got, want, screen(m))
	}
}

func problemsOf(m Model) []string {
	var out []string
	for _, r := range m.stuckReports() {
		if r.Source == (ascSource{}).Name() {
			out = append(out, r.Problems...)
		}
	}
	return out
}

func wantProblem(t *testing.T, m Model, sub string) {
	t.Helper()
	for _, p := range problemsOf(m) {
		if strings.Contains(p, sub) {
			if !strings.Contains(screen(m), "couldn't check") {
				t.Errorf("a problem was recorded but the screen does not show it:\n%s", screen(m))
			}
			return
		}
	}
	t.Errorf("no problem containing %q; problems = %q\n%s", sub, problemsOf(m), screen(m))
}

const (
	rejKey  = "asc-rejected:com.patrickserrano.dailybread:IOS:1.9"
	waitKey = "asc-waiting:com.patrickserrano.dailybread:IOS:1.9"
	unatKey = "asc-unattached:com.patrickserrano.dailybread:IOS:412"
)

// ---- thresholds ----

func TestASCThresholdsAreTheOperators(t *testing.T) {
	if StuckASCRejectedAfter != 3*time.Hour {
		t.Errorf("rejected: %s, want 3h", StuckASCRejectedAfter)
	}
	if StuckASCUnattachedAfter != 3*time.Hour {
		t.Errorf("unattached: %s, want 3h", StuckASCUnattachedAfter)
	}
	if StuckASCWaitingAfter != 24*time.Hour {
		t.Errorf("waiting for review: %s, want 24h", StuckASCWaitingAfter)
	}
	if StuckASCSnapshotStaleAfter != 90*time.Minute {
		t.Errorf("stale snapshot: %s, want 90m", StuckASCSnapshotStaleAfter)
	}
}

// ---- the three conditions, just under and just over ----

func TestRejectedIsStuckAtThreeHoursAndNotBefore(t *testing.T) {
	for _, state := range []string{"REJECTED", "DEVELOPER_REJECTED"} {
		snap := func(since time.Duration) []byte {
			return mustJSON(t, ascSnap(10*time.Minute, dailyBread(
				[]obj{ascVer("v1", "IOS", "1.9", state, since, "firstSeen", nil)}, []obj{})))
		}
		wantKeys(t, ascModel(t, 0, snap(3*time.Hour-time.Minute)))
		wantKeys(t, ascModel(t, 0, snap(3*time.Hour)), rejKey)
		wantKeys(t, ascModel(t, 0, snap(3*time.Hour+time.Minute)), rejKey)
	}
}

func TestWaitingForReviewIsStuckAtTwentyFourHoursAndNotBefore(t *testing.T) {
	snap := func(since time.Duration) []byte {
		return mustJSON(t, ascSnap(10*time.Minute, dailyBread(
			[]obj{ascVer("v1", "IOS", "1.9", "WAITING_FOR_REVIEW", since, "reviewSubmission.submittedDate", nil)}, []obj{})))
	}
	wantKeys(t, ascModel(t, 0, snap(24*time.Hour-time.Minute)))
	wantKeys(t, ascModel(t, 0, snap(24*time.Hour+time.Minute)), waitKey)
	// 23h is not stuck for review however long it would be for a rejection.
	wantKeys(t, ascModel(t, 0, snap(23*time.Hour)))
}

func TestAValidBuildAttachedToNothingIsStuckAtThreeHoursAndNotBefore(t *testing.T) {
	snap := func(uploaded time.Duration) []byte {
		return mustJSON(t, ascSnap(10*time.Minute, dailyBread(
			[]obj{ascVer("v1", "IOS", "1.8", "READY_FOR_SALE", 90*24*time.Hour, "firstSeen", "b1")},
			[]obj{ascBld("b1", "IOS", "400", "VALID", 90*24*time.Hour, "v1"), ascBld("b2", "IOS", "412", "VALID", uploaded, nil)})))
	}
	wantKeys(t, ascModel(t, 0, snap(3*time.Hour-time.Minute)))
	wantKeys(t, ascModel(t, 0, snap(3*time.Hour+time.Minute)), unatKey)
}

func TestAnUnattachedBuildThatIsNotValidOrIsExpiredIsNotStuck(t *testing.T) {
	for name, mut := range map[string]func(b obj){
		"processing": func(b obj) { b["processingState"] = "PROCESSING" },
		"invalid":    func(b obj) { b["processingState"] = "INVALID" },
		"expired":    func(b obj) { b["expired"] = true },
	} {
		b := ascBld("b2", "IOS", "412", "VALID", 9*time.Hour, nil)
		mut(b)
		m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(nil, []obj{b}))))
		if len(ascKeys(m)) != 0 {
			t.Errorf("%s build is stuck: %v", name, ascKeys(m))
		}
	}
}

// A VALID build nothing attaches, while a newer build is attached, is a build
// that was superseded: it must not sit on the tab forever.
func TestASupersededUnattachedBuildIsNotStuck(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "PREPARE_FOR_SUBMISSION", 0, "firstSeen", "b3")},
		[]obj{
			ascBld("b2", "IOS", "411", "VALID", 20*time.Hour, nil), // older, never submitted
			ascBld("b3", "IOS", "412", "VALID", 10*time.Hour, "v1"),
		}))))
	wantKeys(t, m)
	// The same older build IS stuck when nothing newer exists: the mutation that
	// drops the newest-build rule must show here and above.
	m = ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(nil,
		[]obj{ascBld("b2", "IOS", "411", "VALID", 20*time.Hour, nil)}))))
	wantKeys(t, m, "asc-unattached:com.patrickserrano.dailybread:IOS:411")
}

// Of several VALID builds nothing attaches, only the newest is stuck, and a newer
// build of any state (still processing, say) means the older one was passed over.
func TestOnlyTheNewestUnattachedBuildIsStuck(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(nil, []obj{
		ascBld("b1", "IOS", "410", "VALID", 30*time.Hour, nil),
		ascBld("b2", "IOS", "411", "VALID", 20*time.Hour, nil),
		ascBld("b3", "IOS", "412", "VALID", 10*time.Hour, nil),
	}))))
	wantKeys(t, m, "asc-unattached:com.patrickserrano.dailybread:IOS:412")
	m = ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(nil, []obj{
		ascBld("b2", "IOS", "411", "VALID", 20*time.Hour, nil),
		ascBld("b3", "IOS", "412", "PROCESSING", 10*time.Hour, nil),
	}))))
	wantKeys(t, m)
}

// ASC's newer name for the live state is READY_FOR_DISTRIBUTION. Nothing here may
// depend on READY_FOR_SALE alone.
func TestEitherLiveStateSupersedesAnOlderUnattachedBuild(t *testing.T) {
	for _, live := range []string{"READY_FOR_SALE", "READY_FOR_DISTRIBUTION"} {
		m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(
			[]obj{ascVer("v1", "IOS", "1.9", live, 0, "firstSeen", "b3")},
			[]obj{
				ascBld("b2", "IOS", "411", "VALID", 20*time.Hour, nil),
				ascBld("b3", "IOS", "412", "VALID", 10*time.Hour, "v1"),
			}))))
		if got := ascKeys(m); len(got) != 0 {
			t.Errorf("%s: %v", live, got)
		}
		// And a build newer than the live version's is the one that is stuck.
		m = ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(
			[]obj{ascVer("v1", "IOS", "1.9", live, 0, "firstSeen", "b3")},
			[]obj{
				ascBld("b3", "IOS", "412", "VALID", 10*time.Hour, "v1"),
				ascBld("b4", "IOS", "413", "VALID", 5*time.Hour, nil),
			}))))
		if got := ascKeys(m); len(got) != 1 || got[0] != "asc-unattached:com.patrickserrano.dailybread:IOS:413" {
			t.Errorf("%s: newest build not stuck: %v", live, got)
		}
	}
}

// A build is superseded by a newer build the version list attaches even when the
// builds list does not say so, and never by a build of another platform.
func TestASupersededBuildIsJudgedPerPlatformAndByTheVersionsBuild(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "READY_FOR_SALE", 0, "firstSeen", "b3")},
		[]obj{
			ascBld("b2", "IOS", "411", "VALID", 20*time.Hour, nil),
			ascBld("b3", "IOS", "412", "VALID", 10*time.Hour, nil), // the version's build; attachedVersionId not filled in
		}))))
	// b3 is the version's own build, so not newer than itself; b2 is not the newest.
	wantKeys(t, m)
	// A newer macOS build does not supersede an iOS one.
	m = ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(nil, []obj{
		ascBld("b2", "IOS", "411", "VALID", 20*time.Hour, nil),
		ascBld("b3", "MAC_OS", "9", "VALID", 1*time.Hour, "vmac"),
	}))))
	wantKeys(t, m, "asc-unattached:com.patrickserrano.dailybread:IOS:411")
}

func TestOtherStatesAreNeverStuck(t *testing.T) {
	var vs []obj
	for i, st := range []string{"READY_FOR_SALE", "IN_REVIEW", "PENDING_DEVELOPER_RELEASE", "PREPARE_FOR_SUBMISSION", "PENDING_APPLE_RELEASE", "PROCESSING_FOR_APP_STORE"} {
		vs = append(vs, ascVer("v"+string(rune('a'+i)), "IOS", "1."+string(rune('0'+i)), st, 400*time.Hour, "firstSeen", nil))
	}
	wantKeys(t, ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(vs, []obj{})))))
}

// The clock moves with no new snapshot: a row appears when its threshold passes.
func TestAnASCRowAppearsAsTheClockPassesItsThreshold(t *testing.T) {
	raw := mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 2*time.Hour, "firstSeen", nil)}, []obj{})))
	wantKeys(t, ascModel(t, 0, raw))
	wantKeys(t, ascModel(t, 61*time.Minute, raw), rejKey)
}

// ---- what the row says ----

func TestARejectedRowSaysWhatItIsWhereItLinksAndWhatToDoAboutResolutionCenter(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 4*time.Hour+12*time.Minute, "firstSeen", nil)}, []obj{}))))
	s := screen(m)
	for _, want := range []string{
		"Daily Bread 1.9 (iOS)",
		"REJECTED",
		"at least 4h12m",
		"https://appstoreconnect.apple.com/apps/1234567890/distribution",
		"If you've replied in Resolution Center, dismiss this until Apple responds.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
}

func TestOnlyAFirstSeenTimeIsSaidToBeAtLeast(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "WAITING_FOR_REVIEW", 30*time.Hour, "reviewSubmission.submittedDate", nil)}, []obj{}))))
	s := screen(m)
	if strings.Contains(s, "at least") {
		t.Errorf("an exact time is said to be a floor:\n%s", s)
	}
	if !strings.Contains(s, "30h00m") || !strings.Contains(s, "Daily Bread 1.9 (iOS)") {
		t.Errorf("waiting row:\n%s", s)
	}
}

func TestOnlyARejectedRowCarriesTheResolutionCenterText(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "WAITING_FOR_REVIEW", 30*time.Hour, "reviewSubmission.submittedDate", nil)},
		[]obj{ascBld("b2", "IOS", "412", "VALID", 9*time.Hour, nil)}))))
	if strings.Contains(screen(m), "Resolution Center") {
		t.Errorf("a non-rejected row mentions Resolution Center:\n%s", screen(m))
	}
	wantKeys(t, m, waitKey, unatKey) // oldest first
}

func TestTheUnattachedRowNamesTheBuild(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(nil,
		[]obj{ascBld("b2", "IOS", "412", "VALID", 5*time.Hour, nil)}))))
	s := screen(m)
	for _, want := range []string{"Daily Bread", "412", "(iOS)", "5h00m", "attached to no version", "appstoreconnect.apple.com"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
}

func TestPlatformsAreNamedTheWayTheyAreSpoken(t *testing.T) {
	for in, want := range map[string]string{"IOS": "iOS", "MAC_OS": "macOS", "TV_OS": "tvOS", "VISION_OS": "visionOS", "WEIRD_OS": "WEIRD_OS"} {
		if got := ascPlatform(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestASnapshotThatNamesNoAppLinkStillShowsTheRow(t *testing.T) {
	app := dailyBread([]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 4*time.Hour, "firstSeen", nil)}, []obj{})
	delete(app, "ascAppId")
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, app)))
	wantKeys(t, m, rejKey)
	_, cmds := feed(t, m, "o")
	if len(cmds) != 0 {
		t.Errorf("o with no link asked for %v", kinds(cmds))
	}
}

func TestAnASCRowSanitisesWhatTheSnapshotSays(t *testing.T) {
	app := ascApp("com.x.a", "Evil\x1b[31m App", []obj{ascVer("v1", "IOS", "1.9\x1b[2J", "REJECTED", 4*time.Hour, "firstSeen", nil)}, []obj{})
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, app)))
	for _, l := range m.View().Lines {
		if strings.Contains(l, "\x1b[2J") || strings.Contains(l, "\x1b[31m App") {
			t.Errorf("an escape from the snapshot reached the screen: %q", l)
		}
	}
}

// ---- couldn't check: never an empty list ----

func TestAMissingSnapshotIsCouldntCheckAndNamesThePath(t *testing.T) {
	dir := t.TempDir()
	e := Env{InboxPath: filepath.Join(dir, "inbox.jsonl"), Now: func() time.Time { return t0 }}
	ev := e.load() // the inbox file is missing too: the snapshot is still read
	want := filepath.Join(dir, ASCSnapshotFile)
	if !strings.Contains(ev.Data.ASC.Err, want) || !strings.Contains(ev.Data.ASC.Err, "asc-status") {
		t.Errorf("Err = %q, want it to name %s and the producer", ev.Data.ASC.Err, want)
	}
	p := Program(tabModel(t, 160, 30))
	p = onTab(t, p.(Model), "5")
	p, _ = send(t, p, tickAt(0), PRsEvent{At: t0}, LaterEvent{At: t0}, LoadedEvent{Data: ev.Data, At: t0})
	m := p.(Model)
	wantProblem(t, m, want)
	if strings.Contains(screen(m), "nothing stuck") {
		t.Errorf("a missing snapshot reads as nothing stuck:\n%s", screen(m))
	}
}

func TestEnvReadsTheSnapshotBesideTheInboxFile(t *testing.T) {
	dir := t.TempDir()
	raw := mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 4*time.Hour, "firstSeen", nil)}, []obj{})))
	if err := os.WriteFile(filepath.Join(dir, ASCSnapshotFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ev := Env{InboxPath: filepath.Join(dir, "inbox.jsonl"), Now: func() time.Time { return t0 }}.load()
	if ev.Data.ASC.Err != "" || ev.Data.ASC.Snap == nil || len(ev.Data.ASC.Snap.Apps) != 1 {
		t.Fatalf("ASC = %+v", ev.Data.ASC)
	}
	// An unreadable path (a directory) is a problem, never "no snapshot".
	dir2 := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir2, ASCSnapshotFile), 0o700); err != nil {
		t.Fatal(err)
	}
	ev = Env{InboxPath: filepath.Join(dir2, "inbox.jsonl"), Now: func() time.Time { return t0 }}.load()
	if ev.Data.ASC.Err == "" || ev.Data.ASC.Snap != nil {
		t.Errorf("a directory in place of the snapshot: %+v", ev.Data.ASC)
	}
}

func TestAStaleSnapshotIsCouldntCheckAtNinetyOneMinutes(t *testing.T) {
	mk := func(age time.Duration) []byte {
		return mustJSON(t, ascSnap(age, dailyBread(
			[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 9*time.Hour, "firstSeen", nil)}, []obj{})))
	}
	wantKeys(t, ascModel(t, 0, mk(89*time.Minute)), rejKey)
	wantKeys(t, ascModel(t, 0, mk(90*time.Minute)), rejKey) // exactly the limit is still fresh
	m := ascModel(t, 0, mk(91*time.Minute))
	wantProblem(t, m, "1h31m old")
	wantKeys(t, m) // rows from a snapshot that old are not shown as current
	// Staleness is measured from generatedAt against the clock, so it comes with time.
	wantProblem(t, ascModel(t, 2*time.Hour, mk(0)), "old")
	if strings.Contains(screen(m), "nothing stuck") {
		t.Errorf("stale reads as nothing stuck:\n%s", screen(m))
	}
}

func TestAFutureGeneratedAtIsCouldntCheck(t *testing.T) {
	future := func(d time.Duration) []byte {
		s := ascSnap(0, dailyBread(nil, []obj{}))
		s["generatedAt"] = t0.Add(d).UTC().Format(time.RFC3339)
		return mustJSON(t, s)
	}
	wantProblem(t, ascModel(t, 0, future(6*time.Minute)), "future")
	if p := problemsOf(ascModel(t, 0, future(4*time.Minute))); len(p) != 0 {
		t.Errorf("4 minutes of clock skew is tolerated: %q", p)
	}
}

func TestAMalformedSnapshotIsCouldntCheckAndNeverNothingStuck(t *testing.T) {
	good := func() obj {
		return ascSnap(10*time.Minute, dailyBread(
			[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 9*time.Hour, "firstSeen", nil)},
			[]obj{ascBld("b1", "IOS", "412", "VALID", 9*time.Hour, nil)}))
	}
	app := func(s obj) obj { return s["apps"].([]obj)[0] }
	ver := func(s obj) obj { return app(s)["versions"].([]obj)[0] }
	bld := func(s obj) obj { return app(s)["builds"].([]obj)[0] }
	cases := map[string]struct {
		raw  []byte
		want string
	}{
		"not json":                        {[]byte(`{"schemaVersion": 1, "gener`), "asc-snapshot.json"},
		"empty file":                      {[]byte(``), "asc-snapshot.json"},
		"a json array":                    {[]byte(`[]`), "asc-snapshot.json"},
		"schema 2":                        {func() []byte { s := good(); s["schemaVersion"] = 2; return mustJSON(t, s) }(), "unsupported schema version 2"},
		"schema missing":                  {func() []byte { s := good(); delete(s, "schemaVersion"); return mustJSON(t, s) }(), "schemaVersion"},
		"schema a string":                 {func() []byte { s := good(); s["schemaVersion"] = "1"; return mustJSON(t, s) }(), "asc-snapshot.json"},
		"no generatedAt":                  {func() []byte { s := good(); delete(s, "generatedAt"); return mustJSON(t, s) }(), "generatedAt"},
		"bad generatedAt":                 {func() []byte { s := good(); s["generatedAt"] = "yesterday"; return mustJSON(t, s) }(), "asc-snapshot.json"},
		"no apps":                         {func() []byte { s := good(); delete(s, "apps"); return mustJSON(t, s) }(), "apps"},
		"null apps":                       {func() []byte { s := good(); s["apps"] = nil; return mustJSON(t, s) }(), "apps"},
		"app without bundle":              {func() []byte { s := good(); delete(app(s), "bundleId"); return mustJSON(t, s) }(), "bundleId"},
		"app without name":                {func() []byte { s := good(); delete(app(s), "name"); return mustJSON(t, s) }(), "name"},
		"app without versions":            {func() []byte { s := good(); delete(app(s), "versions"); return mustJSON(t, s) }(), "versions"},
		"app without builds":              {func() []byte { s := good(); delete(app(s), "builds"); return mustJSON(t, s) }(), "builds"},
		"version without state":           {func() []byte { s := good(); delete(ver(s), "appStoreState"); return mustJSON(t, s) }(), "appStoreState"},
		"version without id":              {func() []byte { s := good(); delete(ver(s), "id"); return mustJSON(t, s) }(), "id"},
		"version bad time":                {func() []byte { s := good(); ver(s)["stateSince"] = "soon"; return mustJSON(t, s) }(), "asc-snapshot.json"},
		"build without state":             {func() []byte { s := good(); delete(bld(s), "processingState"); return mustJSON(t, s) }(), "processingState"},
		"build without expired":           {func() []byte { s := good(); delete(bld(s), "expired"); return mustJSON(t, s) }(), "expired"},
		"build without upload date":       {func() []byte { s := good(); delete(bld(s), "uploadedDate"); return mustJSON(t, s) }(), "uploadedDate"},
		"build without attachedVersionId": {func() []byte { s := good(); delete(bld(s), "attachedVersionId"); return mustJSON(t, s) }(), "attachedVersionId"},
		"error without message":           {func() []byte { s := good(); s["errors"] = []obj{{"bundleId": "x"}}; return mustJSON(t, s) }(), "message"},
	}
	for name, c := range cases {
		m := ascModel(t, 0, c.raw)
		wantProblem(t, m, c.want)
		if len(ascKeys(m)) != 0 {
			t.Errorf("%s: rows from a snapshot that could not be read: %v", name, ascKeys(m))
		}
		if strings.Contains(screen(m), "nothing stuck") {
			t.Errorf("%s reads as nothing stuck:\n%s", name, screen(m))
		}
	}
	// The good one is stuck, so the fixture itself is not what is being rejected.
	wantKeys(t, ascModel(t, 0, mustJSON(t, good())), rejKey, "asc-unattached:com.patrickserrano.dailybread:IOS:412")
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	s := ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 9*time.Hour, "firstSeen", nil)}, []obj{}))
	s["addedLater"] = obj{"x": 1}
	s["apps"].([]obj)[0]["alsoNew"] = []int{1}
	wantKeys(t, ascModel(t, 0, mustJSON(t, s)), rejKey)
}

func TestEmptyAppsIsCouldntCheckNeverNothingStuck(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute)))
	wantProblem(t, m, "snapshot lists no apps")
	if strings.Contains(screen(m), "nothing stuck") {
		t.Errorf("no apps reads as nothing stuck:\n%s", screen(m))
	}
}

func TestAProducerErrorIsAProblemRowAndTheOtherAppsAreStillChecked(t *testing.T) {
	s := ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 4*time.Hour, "firstSeen", nil)}, []obj{}))
	s["errors"] = []obj{
		{"bundleId": "com.example.flare", "message": "HTTP 401 from /v1/apps"},
		{"message": "token expired"},
	}
	m := ascModel(t, 0, mustJSON(t, s))
	wantKeys(t, m, rejKey)
	wantProblem(t, m, "com.example.flare: HTTP 401 from /v1/apps")
	wantProblem(t, m, "token expired")
	if n := len(problemsOf(m)); n != 2 {
		t.Errorf("%d problems, want one per error entry: %q", n, problemsOf(m))
	}
}

func TestAFailedProducerRunWithNoAppsSaysWhy(t *testing.T) {
	s := ascSnap(10 * time.Minute)
	s["errors"] = []obj{{"message": "1Password: no session"}}
	m := ascModel(t, 0, mustJSON(t, s))
	wantProblem(t, m, "1Password: no session")
	// The producer's reason is enough: a second "no apps" is not piled on top.
	for _, p := range problemsOf(m) {
		if strings.Contains(p, "lists no apps") {
			t.Errorf("no-apps piled on a producer error: %q", problemsOf(m))
		}
	}
}

func TestAVersionInACheckedStateWithNoStateSinceIsNamedAndTheRestStillShow(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute,
		dailyBread([]obj{
			ascVer("v1", "IOS", "1.9", "REJECTED", -1, "", nil), // no stateSince
			ascVer("v2", "MAC_OS", "2.0", "WAITING_FOR_REVIEW", 30*time.Hour, "reviewSubmission.submittedDate", nil),
			ascVer("v3", "IOS", "1.7", "READY_FOR_SALE", -1, "", nil), // not a checked state: needs none
		}, []obj{}))))
	wantKeys(t, m, "asc-waiting:com.patrickserrano.dailybread:MAC_OS:2.0")
	wantProblem(t, m, "Daily Bread 1.9 (iOS)")
	if n := len(problemsOf(m)); n != 1 {
		t.Errorf("problems = %q", problemsOf(m))
	}
}

func TestBeforeTheSnapshotIsReadTheTabIsCheckingNotEmptyNorBroken(t *testing.T) {
	p := Program(tabModel(t, 160, 30))
	p = onTab(t, p.(Model), "5")
	s := screen(p.(Model))
	if !strings.Contains(s, "checking") || strings.Contains(s, "nothing stuck") {
		t.Errorf("before any read:\n%s", s)
	}
}

func TestAHealthySnapshotWithNothingStuckSaysSo(t *testing.T) {
	p := Program(tabModel(t, 160, 30))
	p = onTab(t, p.(Model), "5")
	raw := mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "IN_REVIEW", 1*time.Hour, "firstSeen", nil)}, []obj{})))
	p, _ = send(t, p, tickAt(0), PRsEvent{At: t0}, LaterEvent{At: t0},
		LoadedEvent{Data: Data{ASC: withAt(ParseASC(raw, "/x/asc-snapshot.json"), t0)}, At: t0})
	if s := screen(p.(Model)); !strings.Contains(s, "nothing stuck") || strings.Contains(s, "couldn't check") {
		t.Errorf("healthy and empty:\n%s", s)
	}
}

// ---- dismissal reuses 424a ----

func TestADismissedASCRowIsHiddenUntilThePeriodEndsThenReturns(t *testing.T) {
	raw := mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 4*time.Hour, "firstSeen", nil)}, []obj{})))
	m := ascModel(t, 0, raw)
	wantKeys(t, m, rejKey)

	dm, cmds := dismissKey(t, m, "12h")
	var p Program = dm
	if len(cmds) != 1 || cmds[0].Kind != CmdStuckDismiss || cmds[0].ID != rejKey || !cmds[0].Until.Equal(t0.Add(12*time.Hour)) {
		t.Fatalf("dismiss asked %+v", cmds)
	}
	p, _ = send(t, p, StuckDismissedEvent{Key: rejKey, Until: cmds[0].Until, Dismissed: map[string]time.Time{rejKey: cmds[0].Until}, At: t0})
	m = p.(Model)
	if got := shownKeys(m); len(got) != 0 {
		t.Errorf("a dismissed row is still listed: %v", got)
	}
	if h := plain(m.View().Lines[0]); !strings.Contains(h, "1 dismissed") {
		t.Errorf("header = %q", h)
	}
	// Still hidden just before the period ends. Each read of the snapshot keeps it hidden.
	p, _ = send(t, p, tickAt(12*time.Hour-time.Minute),
		LoadedEvent{Data: Data{ASC: ParseASC(mustJSON(t, ascSnap(10*time.Minute, dailyBread(
			[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 16*time.Hour-time.Minute, "firstSeen", nil)}, []obj{}))), "/x")}, At: t0.Add(12*time.Hour - time.Minute)})
	if got := shownKeys(p.(Model)); len(got) != 0 {
		t.Errorf("hidden row came back early: %v", got)
	}
}

func TestADismissedASCRowComesBackWhenTheDismissalExpires(t *testing.T) {
	raw := mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 4*time.Hour, "firstSeen", nil)}, []obj{})))
	m := ascModel(t, 0, raw)
	m.Stuck.Dismissed = map[string]time.Time{rejKey: t0.Add(time.Hour)}
	if got := shownKeys(m); len(got) != 0 {
		t.Errorf("dismissed row listed: %v", got)
	}
	m.Now = t0.Add(time.Hour + time.Second)
	if got := shownKeys(m); len(got) != 1 || got[0] != rejKey {
		t.Errorf("row did not come back when the dismissal ended: %v", got)
	}
}

func TestDismissingAnASCRowWritesItsKeyToTheDismissalsFile(t *testing.T) {
	dir := t.TempDir()
	env := Env{InboxPath: filepath.Join(dir, "inbox.jsonl"), Now: func() time.Time { return t0 }}
	ev := env.Exec(Cmd{Kind: CmdStuckDismiss, ID: rejKey, Until: t0.Add(6 * time.Hour)}).(StuckDismissedEvent)
	if ev.Err != "" {
		t.Fatal(ev.Err)
	}
	got, err := ReadDismissed(StuckDismissedPath(env.InboxPath))
	if err != nil || !got[rejKey].Equal(t0.Add(6*time.Hour)) {
		t.Errorf("dismissals = %v, %v", got, err)
	}
}

// ---- the GitHub write-back never fires for an App Store row ----

// 424a writes the operator's reply back as a comment on a GitHub ref. An App
// Store row has no GitHub ref, and nothing on the Stuck tab replies at all.
func TestNoGitHubWriteBackFiresForAnASCRow(t *testing.T) {
	m := ascModel(t, 0, mustJSON(t, ascSnap(10*time.Minute, dailyBread(
		[]obj{ascVer("v1", "IOS", "1.9", "REJECTED", 4*time.Hour, "firstSeen", nil)},
		[]obj{ascBld("b2", "IOS", "412", "VALID", 9*time.Hour, nil)}))))
	it, ok := m.selectedStuck()
	if !ok {
		t.Fatal("no row selected")
	}
	if it.Repo != "" || it.Number != 0 {
		t.Errorf("an App Store row carries a GitHub identity: %q #%d", it.Repo, it.Number)
	}
	for _, ref := range []string{it.Ref(), it.URL, "Daily Bread 1.9 (iOS)", "https://appstoreconnect.apple.com/apps/1234567890/distribution"} {
		if g, ok := ParseGitHubRef(ref); ok {
			t.Errorf("%q parses as the GitHub ref %v", ref, g)
		}
		fc := &fakeCmd{}
		env, _ := replyEnv(t, fc)
		ev := env.Exec(Cmd{Kind: CmdReply, ID: "e1", Text: "yes", Ref: ref}).(RepliedEvent)
		if ev.CommentErr != "" || ev.CommentUnsure {
			t.Errorf("%q: %+v", ref, ev)
		}
		for _, c := range fc.calls {
			if strings.HasPrefix(c, "gh ") {
				t.Errorf("%q: gh ran %q", ref, c)
			}
		}
	}
	// No key on the row reaches anything that writes to GitHub or types to the overseer.
	for _, raw := range []string{"\r", "o", "c", "r", "d", "d", "v", "y", "n", "e", "R", "\t"} {
		var cmds []Cmd
		var q Program = m
		q, cmds = feed(t, q, raw)
		for _, c := range cmds {
			switch c.Kind {
			case CmdReply, CmdDecide, CmdLine, CmdUnpark, CmdResolve, CmdIssue, CmdPopupIssue, CmdDiff, CmdPlan:
				t.Errorf("key %q on an App Store row asked for %v", raw, c.Kind)
			}
		}
		_ = q
	}
}

// ---- Check stays pure: the file is read through Env only ----

func TestCheckReadsNoFile(t *testing.T) {
	// A path that would exist if Check read it: it must not matter to Check.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ASCSnapshotFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := ascSource{}.Check(StuckInputs{ASC: ASCState{Answered: true, Snap: &ASCSnapshot{}, Path: filepath.Join(dir, ASCSnapshotFile)}}, t0)
	for _, p := range r.Problems {
		if strings.Contains(p, "unreadable") {
			t.Errorf("Check read the file: %q", p)
		}
	}
}
