// Package gateoff finds the ways one merged change switched a lacquer gate off
// (#576): .lacquer.lock moved to an older lacquer, .lacquer.toml gained or
// extended an exemption, or a file the lock manages was edited or deleted
// outside a sync.
//
// It compares two snapshots of a repository, the merge commit and its first
// parent, and reads only the blobs it needs. It is pure: the caller lists the
// trees, supplies the reader (the merge harvest reads through gh, a fleet
// dry-run through local git), and decides what a read error means.
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

// Reader returns a blob's content by id. Check returns its errors wrapped,
// never as an absent file; whether one is worth retrying is the caller's call.
type Reader func(blobID string) ([]byte, error)

// Finding is one gate the change turned off.
type Finding struct {
	// Kind and Subject identify the finding for dedupe: the same change seen
	// twice yields the same pair.
	Kind, Subject string
	// What says what happened, as a predicate: "downgraded .lacquer.lock …".
	What string
	// Reason is the reason the change recorded, or "" when it recorded none.
	Reason string
}

// Ref is the suffix an inbox entry for this finding is deduped by, appended to
// the PR's URL after "#".
func (f Finding) Ref() string { return "gate-off-" + f.Kind + "-" + f.Subject }

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
//   - the file is gone and the lock is byte-identical on both sides: deleted
//     outside a sync, since no sync ran;
//   - the head lock dropped it, the file is gone, and neither the lock's
//     version nor the manifest changed, so no sync could have dropped it:
//     deleted outside a sync, with the lock entry removed by hand too.
//
// A file gone from a change that DID rewrite the lock is not reported. The
// lock lists what a sync wrote to disk, not what is committed, and a sync
// legitimately stops committing files (a skill directory the new .gitignore
// region ignores is still on disk, and still in the lock). Telling that apart
// from a hand deletion would need the head's .gitignore evaluated, which this
// does not do; the fleet dry-run found thousands of such files and no hand
// deletion inside a sync.
//
// A file the lacquer has stopped shipping (an orphan the audit asks to have
// deleted) is still reported when deleted by hand: telling an orphan apart
// needs the locked release's render, which this does not have.
func Check(base, head Snapshot, read Reader) (Result, error) {
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
		grouped, err := groupManaged(get, base, head, changes, headManifest)
		if err != nil {
			return Result{}, err
		}
		res.Findings = append(res.Findings, grouped...)
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
			Kind: "lock-removed", Subject: base.Version.String(),
			What: fmt.Sprintf("removed %s, which named v%s", lock.Name, base.Version),
		}, true
	case lock.IsDowngrade(base, head):
		return Finding{
			Kind: "downgrade", Subject: base.Version.String() + "-" + head.Version.String(),
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
	lockSame := base.Blobs[lock.Name] == head.Blobs[lock.Name]
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
		if !listed {
			if !inHead && inBase && sameVersion && manifestSame {
				out = append(out, change{"deleted", key, path, path + " (and its " + lock.Name + " entry)"})
			}
			continue
		}
		if !inHead {
			if lockSame {
				out = append(out, change{"deleted", key, path, path})
			}
			continue
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

// groupManaged turns the changes into at most two findings, one for edits and
// one for deletions, each naming every file. A Dependabot bump or the cleanup
// after a release retires a file touches several managed files in one PR; one
// entry per file was how the fleet dry-run turned a handful of PRs into dozens
// of entries. Each finding's reason is the head's exclusion covering its files.
//
// An edit that changes nothing but `uses:` versions, in every file, says so
// with the bumps, so it can be read at a glance. audit.ActionBumps decides
// that, and it is a proof, not a guess: an action whose NAME changed is not a
// bump, and the file then reads as an ordinary edit.
func groupManaged(get func(Snapshot, string) ([]byte, bool, error), base, head Snapshot, changes []change, m *manifest) ([]Finding, error) {
	var out []Finding
	for _, kind := range []string{"edited", "deleted"} {
		var labels []string
		var mine []change
		seen := map[string]bool{}
		for _, c := range changes {
			if c.kind == kind && !seen[c.label] {
				seen[c.label] = true
				labels = append(labels, c.label)
				mine = append(mine, c)
			}
		}
		if len(mine) == 0 {
			continue
		}
		verb := kind
		if kind == "edited" {
			bumps, err := onlyActionBumps(get, base, head, mine)
			if err != nil {
				return nil, err
			}
			if len(bumps) > 0 {
				verb = "bumped only action versions (" + strings.Join(bumps, ", ") + ") in"
			}
		}
		out = append(out, Finding{
			Kind: kind, Subject: "files",
			What:   fmt.Sprintf("%s managed %s outside a sync", verb, strings.Join(labels, ", ")),
			Reason: reasons(m, mine),
		})
	}
	return out, nil
}

// onlyActionBumps is every action bump across the edited files, or nil when any
// of them changed anything else (or is a region, or is new).
func onlyActionBumps(get func(Snapshot, string) ([]byte, bool, error), base, head Snapshot, edited []change) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, c := range edited {
		if c.key != c.path {
			return nil, nil // a region: the project owns the rest of the file
		}
		before, present, err := get(base, c.path)
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, nil
		}
		after, _, err := get(head, c.path)
		if err != nil {
			return nil, err
		}
		bumps := audit.ActionBumps(string(before), string(after))
		if bumps == nil {
			return nil, nil
		}
		for _, b := range bumps {
			s := fmt.Sprintf("%s %s→%s", b.Action, b.From, b.To)
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// reasons is the head's exclusion reason for the changed files: the one reason
// when they share it, else each file's, or "" when none is covered.
func reasons(m *manifest, cs []change) string {
	if m == nil {
		return ""
	}
	byPath := map[string]string{}
	distinct := map[string]bool{}
	var paths []string
	for _, c := range cs {
		r := m.exclusionReason(c.path)
		if _, ok := byPath[c.path]; !ok {
			paths = append(paths, c.path)
		}
		byPath[c.path] = r
		distinct[r] = true
	}
	if len(distinct) == 1 {
		for r := range distinct {
			return r
		}
	}
	var parts []string
	for _, p := range paths {
		if r := byPath[p]; r != "" {
			parts = append(parts, p+": "+r)
		}
	}
	return strings.Join(parts, "; ")
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
