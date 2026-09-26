package inboxwatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The App Store conditions on the Stuck tab (lacquer #424b) are computed from a
// file, not from a call. lacquer holds no App Store Connect credentials and never
// calls the ASC API: a separate producer (fleet-ops' asc-status, on a schedule)
// writes ASCSnapshotFile and lacquer only reads it. The schema, and what each
// condition reads from it, is docs/asc-snapshot.md.
//
// The thresholds are the operator's (#424), set on 2026-09-20 and 2026-09-25.
// Do not raise or loosen them here: one is changed at a time, with a recorded
// reason, never as a batch.
const (
	// StuckASCRejectedAfter is how long a version may stay REJECTED or
	// DEVELOPER_REJECTED.
	StuckASCRejectedAfter = 3 * time.Hour
	// StuckASCUnattachedAfter is how long a VALID build may be attached to no version.
	StuckASCUnattachedAfter = 3 * time.Hour
	// StuckASCWaitingAfter is how long a version may stay WAITING_FOR_REVIEW.
	StuckASCWaitingAfter = 24 * time.Hour

	// StuckASCSnapshotStaleAfter is how old the snapshot's generatedAt may be
	// before the tab says it cannot check: the producer runs every 30 to 60
	// minutes, so 90 is one missed run.
	StuckASCSnapshotStaleAfter = 90 * time.Minute
	// ascClockSkew is how far in the future generatedAt may be: a clock that
	// disagrees with the producer's by a few minutes is not a fault.
	ascClockSkew = 5 * time.Minute
)

// ASCSnapshotFile sits next to the inbox file, as stuck-dismissed.json does.
const ASCSnapshotFile = "asc-snapshot.json"

const ascProducerName = "fleet-ops asc-status"

// ASCSnapshotPath is where the snapshot for an inbox file lives.
func ASCSnapshotPath(inboxPath string) string {
	return filepath.Join(filepath.Dir(inboxPath), ASCSnapshotFile)
}

// resolutionCenterNote is what a rejected row says. The API never shows a
// re-review after a reply in Resolution Center, so a version Apple is reviewing
// again still reads REJECTED, and the way out is a dismissal.
const resolutionCenterNote = "If you've replied in Resolution Center, dismiss this until Apple responds."

// ASCState is what the Stuck tab knows of the snapshot: one read of the file.
type ASCState struct {
	Answered bool      // whether the file has been read at all yet
	At       time.Time // when it was read
	Path     string
	// Snap is the parsed snapshot; Err is why there is none. Exactly one is set
	// once Answered, and Err is what the tab says it "couldn't check".
	Snap *ASCSnapshot
	Err  string
}

// ASCSnapshot is docs/asc-snapshot.md, schema version 1, parsed and validated.
type ASCSnapshot struct {
	GeneratedAt time.Time
	Producer    string
	Errors      []ASCError
	Apps        []ASCApp
}

type ASCError struct{ BundleID, Message string }

type ASCApp struct {
	BundleID, Name, AppID string
	Versions              []ASCVersion
	Builds                []ASCBuild
}

type ASCVersion struct {
	ID, Platform, VersionString, State string
	// Since is when the version entered State, zero when the producer gave none;
	// SinceSource says how it knew.
	Since       time.Time
	SinceSource string
	BuildID     string
}

type ASCBuild struct {
	ID, Platform, Number, State string
	Expired                     bool
	Uploaded                    time.Time
	AttachedVersionID           string
}

// The wire shapes. Pointers and RawMessage tell "absent" from "zero": a required
// field that is missing is a snapshot lacquer refuses, never a default.
type (
	wireSnapshot struct {
		SchemaVersion *int        `json:"schemaVersion"`
		GeneratedAt   *time.Time  `json:"generatedAt"`
		Producer      string      `json:"producer"`
		Errors        []wireError `json:"errors"`
		Apps          *[]wireApp  `json:"apps"`
	}
	wireError struct {
		BundleID string  `json:"bundleId"`
		Message  *string `json:"message"`
	}
	wireApp struct {
		BundleID *string        `json:"bundleId"`
		Name     *string        `json:"name"`
		AppID    string         `json:"ascAppId"`
		Versions *[]wireVersion `json:"versions"`
		Builds   *[]wireBuild   `json:"builds"`
	}
	wireVersion struct {
		ID               *string    `json:"id"`
		Platform         *string    `json:"platform"`
		VersionString    *string    `json:"versionString"`
		AppStoreState    *string    `json:"appStoreState"`
		StateSince       *time.Time `json:"stateSince"`
		StateSinceSource string     `json:"stateSinceSource"`
		BuildID          *string    `json:"buildId"`
	}
	wireBuild struct {
		ID                *string         `json:"id"`
		Platform          *string         `json:"platform"`
		Number            *string         `json:"number"`
		ProcessingState   *string         `json:"processingState"`
		Expired           *bool           `json:"expired"`
		UploadedDate      *time.Time      `json:"uploadedDate"`
		AttachedVersionID json.RawMessage `json:"attachedVersionId"` // absent, null or a string
	}
)

// ParseASC reads the snapshot's bytes. Anything that is not a well-formed
// version 1 snapshot is an Err, never an empty Snap: "checked and nothing is
// stuck" must not be what a broken file says.
func ParseASC(raw []byte, path string) ASCState {
	st := ASCState{Answered: true, Path: path}
	snap, err := parseASC(raw)
	if err != nil {
		st.Err = fmt.Sprintf("%s: %s", path, err)
		return st
	}
	st.Snap = &snap
	return st
}

func parseASC(raw []byte) (ASCSnapshot, error) {
	var w wireSnapshot
	if err := json.Unmarshal(raw, &w); err != nil {
		return ASCSnapshot{}, fmt.Errorf("not a readable snapshot: %w", err)
	}
	if w.SchemaVersion == nil {
		return ASCSnapshot{}, errors.New("missing required field schemaVersion")
	}
	if *w.SchemaVersion != 1 {
		return ASCSnapshot{}, fmt.Errorf("unsupported schema version %d", *w.SchemaVersion)
	}
	if w.GeneratedAt == nil {
		return ASCSnapshot{}, errors.New("missing required field generatedAt")
	}
	if w.Apps == nil {
		return ASCSnapshot{}, errors.New("missing required field apps")
	}
	out := ASCSnapshot{GeneratedAt: *w.GeneratedAt, Producer: w.Producer}
	for i, e := range w.Errors {
		if e.Message == nil {
			return ASCSnapshot{}, fmt.Errorf("missing required field errors[%d].message", i)
		}
		out.Errors = append(out.Errors, ASCError{BundleID: e.BundleID, Message: *e.Message})
	}
	for i, a := range *w.Apps {
		app, err := parseApp(a)
		if err != nil {
			return ASCSnapshot{}, fmt.Errorf("apps[%d]: %w", i, err)
		}
		out.Apps = append(out.Apps, app)
	}
	return out, nil
}

func missing(field string) error { return fmt.Errorf("missing required field %s", field) }

func parseApp(a wireApp) (ASCApp, error) {
	switch {
	case a.BundleID == nil:
		return ASCApp{}, missing("bundleId")
	case a.Name == nil:
		return ASCApp{}, missing("name")
	case a.Versions == nil:
		return ASCApp{}, missing("versions")
	case a.Builds == nil:
		return ASCApp{}, missing("builds")
	}
	app := ASCApp{BundleID: *a.BundleID, Name: *a.Name, AppID: a.AppID}
	for i, v := range *a.Versions {
		switch {
		case v.ID == nil:
			return ASCApp{}, fmt.Errorf("versions[%d]: %w", i, missing("id"))
		case v.Platform == nil:
			return ASCApp{}, fmt.Errorf("versions[%d]: %w", i, missing("platform"))
		case v.VersionString == nil:
			return ASCApp{}, fmt.Errorf("versions[%d]: %w", i, missing("versionString"))
		case v.AppStoreState == nil:
			return ASCApp{}, fmt.Errorf("versions[%d]: %w", i, missing("appStoreState"))
		}
		nv := ASCVersion{ID: *v.ID, Platform: *v.Platform, VersionString: *v.VersionString, State: *v.AppStoreState, SinceSource: v.StateSinceSource}
		if v.StateSince != nil {
			nv.Since = *v.StateSince
		}
		if v.BuildID != nil {
			nv.BuildID = *v.BuildID
		}
		app.Versions = append(app.Versions, nv)
	}
	for i, b := range *a.Builds {
		switch {
		case b.ID == nil:
			return ASCApp{}, fmt.Errorf("builds[%d]: %w", i, missing("id"))
		case b.Platform == nil:
			return ASCApp{}, fmt.Errorf("builds[%d]: %w", i, missing("platform"))
		case b.Number == nil:
			return ASCApp{}, fmt.Errorf("builds[%d]: %w", i, missing("number"))
		case b.ProcessingState == nil:
			return ASCApp{}, fmt.Errorf("builds[%d]: %w", i, missing("processingState"))
		case b.Expired == nil:
			return ASCApp{}, fmt.Errorf("builds[%d]: %w", i, missing("expired"))
		case b.UploadedDate == nil:
			return ASCApp{}, fmt.Errorf("builds[%d]: %w", i, missing("uploadedDate"))
		case b.AttachedVersionID == nil:
			return ASCApp{}, fmt.Errorf("builds[%d]: %w", i, missing("attachedVersionId"))
		}
		nb := ASCBuild{ID: *b.ID, Platform: *b.Platform, Number: *b.Number, State: *b.ProcessingState, Expired: *b.Expired, Uploaded: *b.UploadedDate}
		if string(b.AttachedVersionID) != "null" {
			if err := json.Unmarshal(b.AttachedVersionID, &nb.AttachedVersionID); err != nil {
				return ASCApp{}, fmt.Errorf("builds[%d]: attachedVersionId: %w", i, err)
			}
		}
		app.Builds = append(app.Builds, nb)
	}
	return app, nil
}

// maxASCBytes bounds what is read: a snapshot of a few apps is kilobytes.
const maxASCBytes = 8 << 20

// readASC is the one place the snapshot file is read.
func (e Env) readASC() ASCState {
	path := ASCSnapshotPath(e.InboxPath)
	at := e.now()
	fail := func(msg string) ASCState { return ASCState{Answered: true, At: at, Path: path, Err: msg} }
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fail(fmt.Sprintf("no snapshot at %s; the producer is %s", path, ascProducerName))
	}
	if err != nil {
		return fail(fmt.Sprintf("cannot read %s: %v", path, err))
	}
	defer f.Close()
	info, err := f.Stat()
	switch {
	case err != nil:
		return fail(fmt.Sprintf("cannot read %s: %v", path, err))
	case !info.Mode().IsRegular():
		return fail(fmt.Sprintf("cannot read %s: not a regular file", path))
	case info.Size() > maxASCBytes:
		return fail(fmt.Sprintf("cannot read %s: %d bytes is more than a snapshot can be", path, info.Size()))
	}
	buf, err := io.ReadAll(io.LimitReader(f, maxASCBytes+1))
	if err != nil {
		return fail(fmt.Sprintf("cannot read %s: %v", path, err))
	}
	st := ParseASC(buf, path)
	st.At = at
	return st
}

// ascPlatform is a platform as it is said aloud.
func ascPlatform(p string) string {
	switch p {
	case "IOS":
		return "iOS"
	case "MAC_OS":
		return "macOS"
	case "TV_OS":
		return "tvOS"
	case "VISION_OS":
		return "visionOS"
	}
	return p
}

func ascLink(app ASCApp) string {
	if app.AppID == "" {
		return ""
	}
	return "https://appstoreconnect.apple.com/apps/" + app.AppID + "/distribution"
}

func ascRejectedKey(a ASCApp, v ASCVersion) string {
	return fmt.Sprintf("asc-rejected:%s:%s:%s", a.BundleID, v.Platform, v.VersionString)
}
func ascWaitingKey(a ASCApp, v ASCVersion) string {
	return fmt.Sprintf("asc-waiting:%s:%s:%s", a.BundleID, v.Platform, v.VersionString)
}
func ascUnattachedKey(a ASCApp, b ASCBuild) string {
	return fmt.Sprintf("asc-unattached:%s:%s:%s", a.BundleID, b.Platform, b.Number)
}

// ascSource: the three App Store conditions, computed from the snapshot. One
// source rather than three so a snapshot that cannot be read is said once.
type ascSource struct{}

func (ascSource) Name() string {
	return fmt.Sprintf("App Store: rejected %s, build unattached %s, in review %s",
		thresholdLabel(StuckASCRejectedAfter), thresholdLabel(StuckASCUnattachedAfter), thresholdLabel(StuckASCWaitingAfter))
}

func (s ascSource) Check(in StuckInputs, now time.Time) StuckReport {
	st := in.ASC
	r := StuckReport{Source: s.Name(), Answered: st.Answered, At: st.At}
	if !st.Answered {
		return r
	}
	if st.Err != "" {
		r.Problems = append(r.Problems, st.Err)
		return r
	}
	snap := st.Snap
	if snap == nil {
		r.Problems = append(r.Problems, "no snapshot was read")
		return r
	}
	from := ""
	if snap.Producer != "" {
		from = " (from " + snap.Producer + ")"
	}
	age := now.Sub(snap.GeneratedAt)
	switch {
	case -age > ascClockSkew:
		r.Problems = append(r.Problems, fmt.Sprintf("snapshot is dated %s in the future%s", stuckFor(-age), from))
		return r
	case age > StuckASCSnapshotStaleAfter:
		// A row from a snapshot this old may long since have cleared, so none is shown.
		r.Problems = append(r.Problems, fmt.Sprintf("snapshot is %s old%s", stuckFor(age), from))
		return r
	}
	for _, e := range snap.Errors {
		who := "the producer's whole run"
		if e.BundleID != "" {
			who = e.BundleID
		}
		r.Problems = append(r.Problems, fmt.Sprintf("%s: %s%s", who, e.Message, from))
	}
	if len(snap.Apps) == 0 && len(snap.Errors) == 0 {
		r.Problems = append(r.Problems, "snapshot lists no apps"+from)
	}
	for _, app := range snap.Apps {
		s.versions(&r, app, now)
		s.builds(&r, app, now)
	}
	return r
}

func (s ascSource) versions(r *StuckReport, app ASCApp, now time.Time) {
	for _, v := range app.Versions {
		var after time.Duration
		var key, what string
		rejected := false
		switch v.State {
		case "REJECTED", "DEVELOPER_REJECTED":
			after, key, what, rejected = StuckASCRejectedAfter, ascRejectedKey(app, v), "rejected", true
		case "WAITING_FOR_REVIEW":
			after, key, what = StuckASCWaitingAfter, ascWaitingKey(app, v), "waiting for review"
		default:
			continue
		}
		label := fmt.Sprintf("%s %s (%s)", app.Name, v.VersionString, ascPlatform(v.Platform))
		if v.Since.IsZero() || v.SinceSource == "" {
			r.Problems = append(r.Problems, fmt.Sprintf("%s is %s with no stateSince, so not timed", label, v.State))
			continue
		}
		if now.Sub(v.Since) < after {
			continue
		}
		// firstSeen is when the producer first noticed the state, so the real time
		// may be longer: the row says so rather than claim more than is known.
		age := stuckFor(now.Sub(v.Since))
		why := fmt.Sprintf("%s for %s since %s", what, age, stuckStamp(v.Since))
		if v.SinceSource == "firstSeen" {
			why = fmt.Sprintf("%s for at least %s (first seen %s)", what, age, stuckStamp(v.Since))
		}
		it := StuckItem{Key: key, Source: s.Name(), Display: label, Title: v.State, URL: ascLink(app), Since: v.Since, Why: why}
		if rejected {
			it.Note = resolutionCenterNote
		}
		r.Items = append(r.Items, it)
	}
}

// builds: a VALID, unexpired build attached to nothing, that is the newest of its
// app and platform and newer than the build of every version of that platform.
// A build that fails the last two was superseded, and would otherwise sit on the
// tab forever.
func (s ascSource) builds(r *StuckReport, app ASCApp, now time.Time) {
	byID := map[string]ASCBuild{}
	for _, b := range app.Builds {
		byID[b.ID] = b
	}
	// The uploaded time of every build something attaches, by platform.
	attached := map[string][]time.Time{}
	for _, b := range app.Builds {
		if b.AttachedVersionID != "" {
			attached[b.Platform] = append(attached[b.Platform], b.Uploaded)
		}
	}
	for _, v := range app.Versions {
		if b, ok := byID[v.BuildID]; ok && v.BuildID != "" {
			attached[v.Platform] = append(attached[v.Platform], b.Uploaded)
		}
	}
	for _, b := range app.Builds {
		if b.State != "VALID" || b.Expired || b.AttachedVersionID != "" || now.Sub(b.Uploaded) < StuckASCUnattachedAfter {
			continue
		}
		superseded := false
		for _, o := range app.Builds {
			if o.Platform == b.Platform && o.Uploaded.After(b.Uploaded) {
				superseded = true
			}
		}
		for _, u := range attached[b.Platform] {
			if !b.Uploaded.After(u) {
				superseded = true
			}
		}
		if superseded {
			continue
		}
		r.Items = append(r.Items, StuckItem{
			Key: ascUnattachedKey(app, b), Source: s.Name(),
			Display: fmt.Sprintf("%s build %s (%s)", app.Name, b.Number, ascPlatform(b.Platform)),
			Title:   "VALID build attached to no version", URL: ascLink(app), Since: b.Uploaded,
			Why: fmt.Sprintf("valid and attached to no version since %s", stuckStamp(b.Uploaded)),
		})
	}
}
