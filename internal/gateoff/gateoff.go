// Package gateoff finds the ways one merged change switched a lacquer gate off
// (#576): .lacquer.lock moved to an older lacquer, .lacquer.toml gained or
// extended an exemption, or a file the lock manages was edited or deleted
// outside a sync.
//
// It compares two snapshots of a repository, the merge commit and its first
// parent, and reads only the blobs it needs. It is pure: the caller lists the
// trees, supplies the reader (the merge harvest reads through gh), and
// decides what a read error means.
//
// Nothing here blocks anything. A finding is a fact for a human to read; a
// legitimate downgrade or exemption is surfaced with its reason, and that is its
// whole path forward.
package gateoff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/lock"
	"github.com/patrickserrano/lacquer/internal/region"
)

// ManifestName is the project manifest's filename at the repository root.
const ManifestName = ".lacquer.toml"

// Snapshot is one commit's file list.
type Snapshot struct {
	// Blobs maps every file path in the commit to its blob id. A path missing
	// here is absent from the commit, so a caller that could not list the
	// whole tree must not build a Snapshot: a missing path would read as a
	// deleted file.
	Blobs map[string]string
}

// Options are facts about the change the commits cannot tell.
type Options struct {
	// ByDependabot is whether Dependabot authored the change. Its pure action
	// bumps are not findings (see Check).
	ByDependabot bool
}

// Reader returns a blob's content by id. Check returns its errors wrapped,
// never as an absent file; whether one is worth retrying is the caller's call.
type Reader func(blobID string) ([]byte, error)

// Finding is one gate the change turned off.
type Finding struct {
	// Kind and Subject say what kind of gate and which one: "exclude" and a
	// path, "edited" and a managed file.
	Kind, Subject string
	// What says what happened, as a predicate: "downgraded .lacquer.lock …".
	What string
	// Reason is the reason the change recorded, or "" when it recorded none.
	Reason string
}

// ReasonText is the reason as an inbox entry prints it.
func (f Finding) ReasonText() string {
	if f.Reason == "" {
		return "no reason recorded"
	}
	return "reason: " + f.Reason
}

// Result is what Check found.
type Result struct {
	Findings []Finding
	// Unreadable says, for each part of the change that could not be checked
	// and never will be by a retry (a file that does not parse), why. The
	// other parts are still checked and their findings kept.
	Unreadable []string
	// Managed is every path the head's .lacquer.lock lists, sorted, and empty
	// when the head has no lock. Meaningful only when ManagedKnown, which is
	// false when the head's lock could not be read.
	Managed      []string
	ManagedKnown bool
}

// Check compares base (the merge's first parent) with head (the merge commit).
// It returns an error only when read fails, which a retry may fix; every
// permanent problem is in Result.Unreadable instead.
//
// "Outside a sync" is defined by the lock. A sync rewrites the managed files
// and the lock together, so after a sync every managed file in the commit
// hashes to what the head lock records. For each key the base lock lists whose
// file the change touched:
//   - the head lock still lists it and the content (a region's body, for a
//     region) does not hash to the head lock's value: edited outside a sync;
//   - the head lock still lists it, the file still exists and its region is
//     gone: the region was deleted outside a sync;
//   - the file is gone, and either the lock is byte-identical on both sides
//     (no sync ran) or neither the lock's version nor the manifest changed (a
//     sync at the same version, from the same manifest, writes the same files
//     and cannot leave one it lists deleted): deleted outside a sync. When the
//     head lock dropped the entry too, the finding says so.
//
// A file gone from a change that moved the lock's version or changed the
// manifest is not reported. The lock lists what a sync wrote to disk, not what
// is committed, and a sync to another version or manifest legitimately stops
// committing files (a skill directory the new .gitignore region ignores is
// still on disk, and still in the lock). Telling that apart from a hand
// deletion would need the head's .gitignore evaluated, which this does not do.
//
// A file the lacquer has stopped shipping (an orphan the audit asks to have
// deleted) is still reported when deleted by hand: telling an orphan apart
// needs the locked release's render, which this does not have.
//
// An edit whose every changed line moves a `uses:` version of the same action
// (audit.ActionBumps) is said to be one. When Dependabot made it
// (opts.ByDependabot), it is not a finding at all: that is the routine bump of
// a pinned action, and any other line changed in the file reports as usual.
func Check(base, head Snapshot, read Reader, opts Options) (Result, error) {
	var res Result
	blobs := map[string][]byte{}
	get := func(s Snapshot, path string) ([]byte, bool, error) {
		id, ok := s.Blobs[path]
		if !ok {
			return nil, false, nil
		}
		if b, ok := blobs[id]; ok {
			return b, true, nil
		}
		b, err := read(id)
		if err != nil {
			return nil, true, fmt.Errorf("read %s (blob %s): %w", path, id, err)
		}
		blobs[id] = b
		return b, true, nil
	}

	baseLock, baseLocked, err := readLock(get, base, "the base", &res)
	if err != nil {
		return Result{}, err
	}
	headLock, headLocked, err := readLock(get, head, "the merge", &res)
	if err != nil {
		return Result{}, err
	}
	// A lock that did not parse is in res.Unreadable; both pointers are nil
	// then, and every comparison that needs it is skipped rather than guessed.
	baseOK := !baseLocked || baseLock != nil
	headOK := !headLocked || headLock != nil
	if headOK {
		res.ManagedKnown = true
		res.Managed = []string{}
		if headLock != nil {
			res.Managed = managedPaths(headLock)
		}
	}

	if baseOK && headOK {
		if f, ok := lockChange(baseLock, headLock); ok {
			res.Findings = append(res.Findings, f)
		}
	}

	// The manifest is read only when it changed, or when a managed-file
	// finding wants an exclusion's reason from it.
	manifestChanged := base.Blobs[ManifestName] != head.Blobs[ManifestName]
	var headManifest *manifest
	readManifest := func(s Snapshot, side string) (*manifest, error) {
		data, present, err := get(s, ManifestName)
		if err != nil || !present {
			return nil, err
		}
		m, perr := parseManifest(data)
		if perr != nil {
			res.Unreadable = append(res.Unreadable, fmt.Sprintf("%s at %s does not parse: %v", ManifestName, side, perr))
			return nil, nil
		}
		return &m, nil
	}
	if manifestChanged {
		baseManifest, err := readManifest(base, "the base")
		if err != nil {
			return Result{}, err
		}
		headManifest, err = readManifest(head, "the merge")
		if err != nil {
			return Result{}, err
		}
		_, baseHas := base.Blobs[ManifestName]
		_, headHas := head.Blobs[ManifestName]
		// A manifest that is present but did not parse has no usable side to
		// compare; an absent one is an empty manifest.
		if (baseManifest != nil || !baseHas) && (headManifest != nil || !headHas) {
			res.Findings = append(res.Findings, exemptionChanges(orEmpty(baseManifest), orEmpty(headManifest))...)
		}
	}

	if baseLock != nil && headOK {
		changes, err := managedChanges(get, base, head, baseLock, headLock, !manifestChanged)
		if err != nil {
			return Result{}, err
		}
		if len(changes) > 0 && !manifestChanged {
			if headManifest, err = readManifest(head, "the merge"); err != nil {
				return Result{}, err
			}
		}
		found, err := managedFindings(get, base, head, changes, headManifest, opts)
		if err != nil {
			return Result{}, err
		}
		res.Findings = append(res.Findings, found...)
	}
	return res, nil
}

func orEmpty(m *manifest) manifest {
	if m == nil {
		return manifest{}
	}
	return *m
}

// readLock reads and parses .lacquer.lock from s. present is whether s has the
// file; a lock that is present but does not parse is reported in res and
// returned as nil.
func readLock(get func(Snapshot, string) ([]byte, bool, error), s Snapshot, side string, res *Result) (*lock.Lock, bool, error) {
	data, present, err := get(s, lock.Name)
	if err != nil || !present {
		return nil, present, err
	}
	l, perr := lock.Parse(data)
	if perr != nil {
		res.Unreadable = append(res.Unreadable, fmt.Sprintf("%s at %s does not parse: %v", lock.Name, side, perr))
		return nil, true, nil
	}
	return l, true, nil
}

// lockChange reports a lock that moved to an older lacquer, or went away.
// base or head is nil when that side has no lock. Gaining a lock (a first
// sync), an upgrade and the same version are silent.
func lockChange(base, head *lock.Lock) (Finding, bool) {
	switch {
	case base == nil:
		return Finding{}, false
	case head == nil:
		return Finding{
			Kind: "lock-removed", Subject: lock.Name,
			What: fmt.Sprintf("removed %s, which named v%s", lock.Name, base.Version),
		}, true
	case lock.IsDowngrade(base, head):
		return Finding{
			Kind: "downgrade", Subject: lock.Name,
			What:   fmt.Sprintf("downgraded %s v%s→v%s", lock.Name, base.Version, head.Version),
			Reason: lock.DowngradeReason(base, head),
		}, true
	}
	return Finding{}, false
}

// managedPaths is every file a lock's keys name, once each, sorted.
func managedPaths(l *lock.Lock) []string {
	seen := map[string]bool{}
	out := []string{}
	for k := range l.Files {
		p, _, _ := strings.Cut(k, "#")
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// change is one managed key a change edited or deleted outside a sync.
type change struct {
	kind  string // "edited" or "deleted"
	key   string // the lock key
	path  string // the file
	label string // how a finding names it
}

// managedChanges applies the "outside a sync" rule (see Check) to every key the
// base lock lists whose file the change touched. headLock is nil when the head
// has no lock. manifestSame is whether .lacquer.toml is byte-identical on both
// sides.
func managedChanges(get func(Snapshot, string) ([]byte, bool, error), base, head Snapshot, baseLock, headLock *lock.Lock, manifestSame bool) ([]change, error) {
	keys := make([]string, 0, len(baseLock.Files))
	for k := range baseLock.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sameVersion := headLock != nil && headLock.Version == baseLock.Version
	// No sync could have deleted a file: none ran, or one ran at the same
	// version from the same manifest.
	noSyncDeletes := base.Blobs[lock.Name] == head.Blobs[lock.Name] || (sameVersion && manifestSame)
	var out []change
	for _, key := range keys {
		path, marker, isRegion := strings.Cut(key, "#")
		baseID, inBase := base.Blobs[path]
		headID, inHead := head.Blobs[path]
		if inBase == inHead && baseID == headID {
			continue // the change did not touch the file
		}
		want, listed := "", false
		if headLock != nil {
			want, listed = headLock.Files[key]
		}
		if !inHead {
			switch {
			case !noSyncDeletes || !inBase:
			case listed:
				out = append(out, change{"deleted", key, path, path})
			default:
				out = append(out, change{"deleted", key, path, path + " (and its " + lock.Name + " entry)"})
			}
			continue
		}
		if !listed {
			continue // a sync dropped it, and the file is still there
		}
		data, _, err := get(head, path)
		if err != nil {
			return nil, err
		}
		content := string(data)
		if isRegion {
			body, found := extractRegion(content, marker)
			if !found {
				out = append(out, change{"deleted", key, path, "region " + key})
				continue
			}
			content = body
		}
		if lock.Hash(content) != want {
			out = append(out, change{"edited", key, path, path})
		}
	}
	return out, nil
}

// managedFindings is one finding per changed file (a region's file counts
// once per kind). Each finding's reason is the head's exclusion covering it.
func managedFindings(get func(Snapshot, string) ([]byte, bool, error), base, head Snapshot, changes []change, m *manifest, opts Options) ([]Finding, error) {
	var out []Finding
	seen := map[string]bool{}
	for _, c := range changes {
		if seen[c.kind+"\x00"+c.label] {
			continue
		}
		seen[c.kind+"\x00"+c.label] = true
		what := fmt.Sprintf("%s managed %s outside a sync", c.kind, c.label)
		if c.kind == "edited" {
			bumps, err := actionBumps(get, base, head, c)
			if err != nil {
				return nil, err
			}
			if len(bumps) > 0 {
				if opts.ByDependabot {
					continue
				}
				what = fmt.Sprintf("bumped only action versions (%s) in managed %s outside a sync", strings.Join(bumps, ", "), c.label)
			}
		}
		reason := ""
		if m != nil {
			reason = m.exclusionReason(c.path)
		}
		out = append(out, Finding{Kind: c.kind, Subject: c.label, What: what, Reason: reason})
	}
	return out, nil
}

// actionBumps is every action bump in an edited whole file, or nil when any
// other line changed (or it is a region, or it is new). audit.ActionBumps
// decides it, and it is a proof, not a guess: an action whose NAME changed is
// not a bump.
func actionBumps(get func(Snapshot, string) ([]byte, bool, error), base, head Snapshot, c change) ([]string, error) {
	if c.key != c.path {
		return nil, nil // a region: the project owns the rest of the file
	}
	before, present, err := get(base, c.path)
	if err != nil || !present {
		return nil, err
	}
	after, _, err := get(head, c.path)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, b := range audit.ActionBumps(string(before), string(after)) {
		s := fmt.Sprintf("%s %s→%s", b.Action, b.From, b.To)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

// extractRegion finds a managed region's body in whichever comment syntax the
// file uses. Trying both rather than mapping destinations to syntaxes means a
// new `#`-comment region file cannot be read with the wrong one: a markdown
// marker never matches a `#` file and the reverse.
func extractRegion(content, marker string) (string, bool) {
	if body, ok := region.Markdown.ExtractBody(content, marker); ok {
		return body, true
	}
	return region.Hash.ExtractBody(content, marker)
}
