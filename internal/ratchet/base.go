package ratchet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/config"
)

// The base compare (#571). On a pull request, the gate also reads the ratchet
// file from the PR's base and compares it with the head's, so a PR cannot
// loosen or delete its own gate. Without it, deleting the file read as "not
// enrolled" (green, floor included) and a hand-raised number needed no reason.
//
// "Base" is the FIRST PARENT of the checked-out commit. A pull_request run
// checks out GitHub's merge commit (refs/pull/N/merge), and its first parent is
// the base-branch commit that merge was built on. The event's base.sha is not
// used: it can move after the merge commit is made, and then names a base the
// tested tree never had.

// BaseFetchTimeout bounds the one-commit fetch of the base in a shallow
// checkout, so an unreachable remote fails the gate instead of hanging the job.
var BaseFetchTimeout = 2 * time.Minute

// Base is what the PR's base commit holds.
type Base struct {
	Commit string
	// Baseline is nil when Name does not exist at Commit: the base was not
	// enrolled. Every way of failing to KNOW that is an error from ReadBase
	// instead, never a nil here.
	Baseline *Baseline
}

// ReadBase identifies the base of the merge commit checked out at root, fetches
// that one commit from origin (depth 1) when a shallow checkout lacks it, and
// reads Name from it. Three outcomes, kept apart on purpose:
//
//   - the file is absent at the base: Base{Baseline: nil}, no error (first enrollment);
//   - the base could not be identified, fetched or read: an error;
//   - the base's file does not parse: an error.
//
// A fetch failure must never read as "the base was not enrolled".
func ReadBase(root string) (Base, error) {
	commit, data, present, err := ReadBaseFile(root, Name)
	if err != nil {
		return Base{}, err
	}
	base := Base{Commit: commit}
	if !present {
		return base, nil
	}
	b, err := parseBase(data)
	if err != nil {
		return Base{}, fmt.Errorf("%s at the base commit %s does not parse: %w", Name, commit, err)
	}
	base.Baseline = b
	return base, nil
}

// ReadBaseFile reads name, a path relative to root, from the PR's base: the
// first parent of the merge commit checked out at root, fetched from origin
// (depth 1) when a shallow checkout lacks it. It returns the base commit, the
// file's content, and whether the file exists there. present is false with a
// nil error ONLY when the base demonstrably has no such file; every way of
// failing to know that (no merge commit, a failed fetch, an unreadable tree,
// something other than a regular file) is an error.
func ReadBaseFile(root, name string) (commit string, data []byte, present bool, err error) {
	g := func(args ...string) ([]byte, error) { return git(context.Background(), root, args...) }
	prefix, err := g("rev-parse", "--show-prefix")
	if err != nil {
		return "", nil, false, fmt.Errorf("the checkout is not a git work tree, so the PR's base cannot be read: %w", err)
	}
	raw, err := g("cat-file", "commit", "HEAD")
	if err != nil {
		return "", nil, false, fmt.Errorf("cannot read the checked-out commit: %w", err)
	}
	parents := commitParents(raw)
	if len(parents) != 2 {
		return "", nil, false, fmt.Errorf("the checked-out commit has %d parent(s), not 2; a pull_request run checks out GitHub's PR merge commit, and its first parent is the base, so the base cannot be identified", len(parents))
	}
	commit = parents[0]
	if _, err := g("cat-file", "-e", commit+"^{commit}"); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), BaseFetchTimeout)
		_, ferr := git(ctx, root, "fetch", "--no-tags", "--no-recurse-submodules", "--depth=1", "origin", commit)
		cancel()
		if ferr != nil {
			return "", nil, false, fmt.Errorf("could not fetch the base commit %s: %w", commit, ferr)
		}
		if _, err := g("cat-file", "-e", commit+"^{commit}"); err != nil {
			return "", nil, false, fmt.Errorf("the base commit %s is still missing after fetching it: %w", commit, err)
		}
	}
	path := strings.TrimSpace(string(prefix)) + name
	entry, err := g("ls-tree", "-z", commit, "--", path)
	if err != nil {
		return "", nil, false, fmt.Errorf("cannot list %s at the base commit %s: %w", path, commit, err)
	}
	if len(entry) == 0 {
		return commit, nil, false, nil
	}
	if mode, _, _ := strings.Cut(string(entry), " "); mode != "100644" && mode != "100755" {
		return "", nil, false, fmt.Errorf("%s at the base commit %s is not a regular file (mode %s)", path, commit, mode)
	}
	data, err = g("cat-file", "blob", commit+":"+path)
	if err != nil {
		return "", nil, false, fmt.Errorf("cannot read %s at the base commit %s: %w", path, commit, err)
	}
	return commit, data, true, nil
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git %s: %w", args[0], err)
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return out, nil
}

// commitParents reads the parent lines of a raw commit object. It reads the
// object itself rather than resolving HEAD^1, because a depth-1 checkout grafts
// the parents away from rev-parse but keeps them in the object.
func commitParents(raw []byte) []string {
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			break
		}
		if sha, ok := strings.CutPrefix(line, "parent "); ok {
			out = append(out, sha)
		}
	}
	return out
}

// parseBase decodes the base's file. It is stricter than nothing and looser
// than Read: the base was validated by whatever lacquer version it was written
// under, so a metric that version required and this one does not (or the
// reverse) must not fail every PR against it. Malformed TOML, unknown tables
// and negative values are still errors.
func parseBase(data []byte) (*Baseline, error) {
	var b Baseline
	md, err := toml.Decode(string(data), &b)
	if err != nil {
		return nil, err
	}
	if len(md.Undecoded()) > 0 {
		return nil, fmt.Errorf("unknown keys %v", md.Undecoded())
	}
	for key, value := range b.Ratchet {
		if value < 0 {
			return nil, fmt.Errorf("%s needs a nonnegative baseline", key)
		}
	}
	return &b, nil
}

// Loosened is one key a PR raised against its base with a new reason, or a
// coverage key it removed, with a new reason, together with its product.
type Loosened struct {
	Key  string `json:"key"`
	From int    `json:"from"`
	// To is the raised value; absent when Removed.
	To      int    `json:"to,omitempty"`
	Removed bool   `json:"removed,omitempty"`
	Reason  string `json:"reason"`
}

// BaseVerdict is the base compare's output: each failure is one line naming its
// key, and each sanctioned loosening or enrollment is reported, never silent.
type BaseVerdict struct {
	Failures []string
	Loosened []Loosened
	// Enrolled is each coverage key this PR enrolls while the base already
	// gates coverage, with its new reason (#578).
	Enrolled []Enrollment
}

// Enrollment is a coverage key a PR enrolled while its base already gated
// coverage: the reasoned path of a product-count change (#578). A 2→1 or 1→2
// PR measures a key the base never recorded; enrolling it, with a reason, is
// how that PR keeps coverage gated, and it is reported like a loosening.
type Enrollment struct {
	Key    string `json:"key"`
	To     int    `json:"to"`
	Reason string `json:"reason"`
}

// CompareBase compares every key the base records with the head. head is nil
// when the PR deleted the file. live is the set of coverage keys the head's
// manifest still measures (LiveCoverageKeys). A key is:
//
//	file deleted                         FAIL, always
//	removed, local metric                FAIL, always
//	removed, its product still in live   FAIL: a disguised loosening
//	removed with its product, no NEW reason  FAIL
//	removed with its product, new reason     loosened (to=removed): passes, reported
//	raised, no NEW reason                FAIL: new means nonempty and unlike the base's
//	raised, new reason                   loosened: passes, and is reported
//	lowered or equal                     passes silently
//
// A coverage key only the head records is a first enrollment, and passes,
// when the base gates no coverage at all. When the base does gate coverage,
// the PR changed what is measured (a product added, removed or renamed), so
// the new key needs a NEW reason: with one it passes and is reported
// (Enrollment), without one it fails (#578). Every key in the file is compared,
// not only the product a run measures: deleting another product's key is the
// same hole.
func CompareBase(base, head *Baseline, live map[string]bool) BaseVerdict {
	var v BaseVerdict
	if base == nil {
		return v
	}
	keys := make([]string, 0, len(base.Ratchet))
	for key := range base.Ratchet {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		from := base.Ratchet[key]
		to, ok := 0, false
		if head != nil {
			to, ok = head.Ratchet[key]
		}
		switch {
		case head == nil:
			v.Failures = append(v.Failures, fmt.Sprintf("ratchet: %s is enrolled on the PR's base (%d) and this PR deletes %s, which removes the gate. Restore the file", key, from, Name))
		case !ok && (!IsCoverageKey(key) || live[key]):
			why := "it is a local metric, which is always measured"
			if IsCoverageKey(key) {
				why = "its product is still in .lacquer.toml, so this is a loosening in disguise"
			}
			v.Failures = append(v.Failures, fmt.Sprintf("ratchet: %s is enrolled on the PR's base (%d) and this PR removes it from %s, which removes the gate, and %s. Restore the key", key, from, Name, why))
		case !ok:
			reason, problem := newReason(base, head, key)
			if problem != "" {
				v.Failures = append(v.Failures, fmt.Sprintf("ratchet: %s is enrolled on the PR's base (%d) and this PR removes it with its product, %s. Removing it needs a new [reasons] entry for %s saying why", key, from, problem, key))
				continue
			}
			v.Loosened = append(v.Loosened, Loosened{Key: key, From: from, Removed: true, Reason: reason})
		case to > from:
			reason, problem := newReason(base, head, key)
			if problem != "" {
				v.Failures = append(v.Failures, fmt.Sprintf("ratchet: %s is raised %d → %d on this PR %s. Raise it with: %s", key, from, to, problem, loosenHint(key, to, problem != noReason)))
				continue
			}
			v.Loosened = append(v.Loosened, Loosened{Key: key, From: from, To: to, Reason: reason})
		}
	}
	if head == nil || !Enrolled(base) {
		return v
	}
	added := make([]string, 0, len(head.Ratchet))
	for key := range head.Ratchet {
		if _, had := base.Ratchet[key]; !had && IsCoverageKey(key) {
			added = append(added, key)
		}
	}
	slices.Sort(added)
	for _, key := range added {
		reason, problem := newReason(base, head, key)
		if problem != "" {
			v.Failures = append(v.Failures, fmt.Sprintf("ratchet: %s is enrolled on this PR while the PR's base already gates coverage, %s. A product-count change keeps coverage gated by enrolling the new key with a reason: lacquer ratchet --enroll %s --to %d --reason \"<why the product count changed>\"", key, problem, key, head.Ratchet[key]))
			continue
		}
		v.Enrolled = append(v.Enrolled, Enrollment{Key: key, To: head.Ratchet[key], Reason: reason})
	}
	return v
}

const noReason = "without a new reason in [reasons]"

// newReason is head's reason for key when it is new: nonempty and different
// from the base's. Otherwise problem says which of the two it is not, quoting
// a reused base reason so the author sees what to change.
func newReason(base, head *Baseline, key string) (reason, problem string) {
	reason = strings.TrimSpace(head.Reasons[key])
	old := strings.TrimSpace(base.Reasons[key])
	switch {
	case reason == "":
		return "", noReason
	case reason == old:
		return "", fmt.Sprintf("without a new reason: [reasons] still holds the base's reason %q, and a new reason must differ from the base's", old)
	}
	return reason, ""
}

func loosenHint(key string, to int, reused bool) string {
	why := "<why>"
	if reused {
		why = "<a new reason, different from the base's>"
	}
	if IsCoverageKey(key) {
		return fmt.Sprintf("lacquer ratchet --loosen %s --to %d --reason \"%s\"", key, to, why)
	}
	return fmt.Sprintf("lacquer ratchet --loosen %s --reason \"%s\"", key, why)
}

// LiveCoverageKeys is the set of coverage keys whose product the manifest
// still declares: ios_uncovered_lines_<slug> for each product's slug, and the
// lone ios_uncovered_lines while the project renders a single product, which
// is the only render that measures it (`--product -`). Per-slug presence is by
// slug, not by product count, so a 2→1 matrix change that renames a surviving
// product's key still counts it as present. Once no single-product render
// exists the lone key measures nothing, and removing it falls under the
// removal-with-reason rule (#578). A project with no iOS component declares
// none.
func LiveCoverageKeys(cfg *config.Config) map[string]bool {
	live := map[string]bool{}
	if !hasIOS(cfg) {
		return live
	}
	if len(cfg.Products()) == 1 {
		live[Coverage] = true
	}
	for _, p := range cfg.Products() {
		live[CoverageKey(p.Slug())] = true
	}
	return live
}

// Summary is the human line for a loosening, for the job summary.
func (l Loosened) Summary() string {
	return fmt.Sprintf("loosened %s %d→%s: %s", l.Key, l.From, l.ToText(), l.Reason)
}

// ToText is the raised value, or "removed".
func (l Loosened) ToText() string {
	if l.Removed {
		return "removed"
	}
	return fmt.Sprint(l.To)
}

// Summary is the human line for an enrollment, for the job summary.
func (e Enrollment) Summary() string {
	return fmt.Sprintf("enrolled %s at %d: %s", e.Key, e.To, e.Reason)
}

// EnrolledPrefix starts the machine-readable enrollment line, stable and
// versioned like LoosenedPrefix and emitted the same way:
//
//	lacquer-ratchet-enrolled/v1 {"key":"<key>","to":<int>,"reason":"<text>"}
//
// It is its own line rather than a /v1 loosening with a new field: /v1 is
// never edited, and a harvest that predates it reads a loosening line with an
// unknown field as damaged.
const EnrolledPrefix = "lacquer-ratchet-enrolled/v1 "

// MachineLine is the enrollment in the stable format above.
func (e Enrollment) MachineLine() string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(e) // a struct of strings and ints cannot fail to encode
	return EnrolledPrefix + strings.TrimSuffix(buf.String(), "\n")
}

// Annotation is MachineLine as a `::notice` workflow command.
func (e Enrollment) Annotation() string {
	msg := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(e.MachineLine())
	return "::notice title=lacquer ratchet enrolled::" + msg
}

// ParseEnrolled reads one enrollment MachineLine, with ParseLoosened's
// contract: ok is false for other text, err set for a damaged line.
func ParseEnrolled(s string) (e Enrollment, ok bool, err error) {
	body, found := strings.CutPrefix(strings.TrimSpace(s), EnrolledPrefix)
	if !found {
		return Enrollment{}, false, nil
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return Enrollment{}, true, fmt.Errorf("malformed %s line: %w", strings.TrimSpace(EnrolledPrefix), err)
	}
	if dec.More() {
		return Enrollment{}, true, errors.New("malformed " + strings.TrimSpace(EnrolledPrefix) + " line: trailing data")
	}
	if !IsCoverageKey(e.Key) || e.To < 0 || strings.TrimSpace(e.Reason) == "" {
		return Enrollment{}, true, fmt.Errorf("malformed %s line: %q", strings.TrimSpace(EnrolledPrefix), body)
	}
	return e, true, nil
}

// LoosenedPrefix starts the machine-readable loosening line. The format is
// stable and versioned:
//
//	lacquer-ratchet-loosened/v1 {"key":"<key>","from":<int>,"to":<int>,"reason":"<text>"}
//	lacquer-ratchet-loosened/v1 {"key":"<key>","from":<int>,"removed":true,"reason":"<text>"}
//
// the second for a coverage key removed with its product; one JSON object on one line after a single space. The gate emits it as the
// message of a `::notice` workflow command, so it becomes a check-run
// annotation with exactly this text; the raw log line carries the same text in
// workflow-command encoding (% as %25, CR as %0D, LF as %0A). The merged-PR
// harvest reads the annotation and raises an unread inbox item. A change to the
// shape is a /v2, never an edit of /v1.
const LoosenedPrefix = "lacquer-ratchet-loosened/v1 "

// MachineLine is the loosening in the stable format above.
func (l Loosened) MachineLine() string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(l) // a struct of strings and ints cannot fail to encode
	return LoosenedPrefix + strings.TrimSuffix(buf.String(), "\n")
}

// Annotation is MachineLine as a `::notice` workflow command, escaped so that
// the annotation GitHub creates carries MachineLine exactly.
func (l Loosened) Annotation() string {
	msg := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(l.MachineLine())
	return "::notice title=lacquer ratchet loosened::" + msg
}

// ParseLoosened reads one MachineLine. ok is false for any text that is not a
// loosening line; err is set for one that claims the prefix but is malformed,
// so a damaged line is reported rather than dropped.
func ParseLoosened(s string) (l Loosened, ok bool, err error) {
	body, found := strings.CutPrefix(strings.TrimSpace(s), LoosenedPrefix)
	if !found {
		return Loosened{}, false, nil
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return Loosened{}, true, fmt.Errorf("malformed %s line: %w", strings.TrimSpace(LoosenedPrefix), err)
	}
	if dec.More() {
		return Loosened{}, true, errors.New("malformed " + strings.TrimSpace(LoosenedPrefix) + " line: trailing data")
	}
	valid := l.To > l.From
	if l.Removed {
		valid = l.To == 0 && IsCoverageKey(l.Key)
	}
	if !known(l.Key) || !valid || strings.TrimSpace(l.Reason) == "" {
		return Loosened{}, true, fmt.Errorf("malformed %s line: %q", strings.TrimSpace(LoosenedPrefix), body)
	}
	return l, true, nil
}
