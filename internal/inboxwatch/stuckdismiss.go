package inboxwatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StuckDismissedFile sits next to the inbox and holds the Stuck rows the
// operator has dismissed for a period. It is this tab's own file: the inbox and
// inbox-replies.jsonl keep the formats they have.
//
//	{"pr-failing:owner/repo#12": {"until": "2026-09-28T14:00:00Z", "at": "2026-09-25T14:00:00Z"}}
//
// It is keyed by the condition (StuckItem.Key), so a dismissal is for one thing
// being stuck in one way, and it ends by itself: an expired one shows the row again.
const StuckDismissedFile = "stuck-dismissed.json"

// MaxDismissal bounds a dismissal. A row hidden for good is a row nobody sees,
// which is what the period exists to prevent.
const MaxDismissal = 90 * 24 * time.Hour

// StuckDismissedPath is where the dismissals for an inbox file live.
func StuckDismissedPath(inboxPath string) string {
	return filepath.Join(filepath.Dir(inboxPath), StuckDismissedFile)
}

// dismissMu makes the read-modify-write one step within this process, so two
// quick dismissals cannot lose one.
var dismissMu sync.Mutex

type dismissal struct {
	Until string `json:"until"`
	At    string `json:"at"`
}

// ReadDismissed is condition key -> when the dismissal ends. A missing file is
// none. A file that cannot be read is an error, never an empty map: the tab then
// says so and hides nothing.
func ReadDismissed(path string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var raw map[string]dismissal
	if err := json.Unmarshal(b, &raw); err != nil {
		return out, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	for k, d := range raw {
		until, err := time.Parse(time.RFC3339, d.Until)
		if err != nil {
			return map[string]time.Time{}, fmt.Errorf("%s: %q has no valid until", filepath.Base(path), k)
		}
		out[k] = until
	}
	return out, nil
}

// Dismiss hides the condition key until the given time and returns what is now
// in force. Dismissals that have already expired are dropped as it writes. The
// file is replaced whole through a temp file and a rename, so a reader never
// sees half of one; a file that cannot be read is refused rather than written
// over.
func Dismiss(path, key string, until, now time.Time) (map[string]time.Time, error) {
	dismissMu.Lock()
	defer dismissMu.Unlock()
	if key == "" {
		return nil, errors.New("no condition to dismiss")
	}
	if !until.After(now) {
		return nil, errors.New("a dismissal must end in the future")
	}
	if until.Sub(now) > MaxDismissal {
		return nil, fmt.Errorf("a dismissal is at most %d days", int(MaxDismissal/(24*time.Hour)))
	}
	cur, err := ReadDismissed(path)
	if err != nil {
		return nil, fmt.Errorf("not overwriting an unreadable file: %w", err)
	}
	raw := map[string]dismissal{}
	kept := map[string]time.Time{}
	for k, u := range cur {
		if u.After(now) {
			kept[k] = u
		}
	}
	kept[key] = until
	// Stored with the time each was made, where known; rewritten from the map.
	old := map[string]dismissal{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &old)
	}
	for k, u := range kept {
		d := dismissal{Until: u.UTC().Format(time.RFC3339)}
		if k == key {
			d.At = now.UTC().Format(time.RFC3339)
		} else {
			d.At = old[k].At
		}
		raw[k] = d
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".stuck-dismissed-*")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(name)
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return nil, err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return nil, err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return nil, err
	}
	return kept, nil
}

var periodRe = regexp.MustCompile(`^([0-9]{1,4})([mhdw])$`)

// ParsePeriod reads "90m", "12h", "3d" or "1w". Nothing else: no default, no
// "forever", nothing past MaxDismissal.
func ParsePeriod(s string) (time.Duration, error) {
	m := periodRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, fmt.Errorf("%q is not a period: try 12h, 1d, 3d, 7d", s)
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
	d := time.Duration(n) * unit
	if d <= 0 {
		return 0, errors.New("a period must be more than zero")
	}
	if d > MaxDismissal {
		return 0, fmt.Errorf("a dismissal is at most %d days", int(MaxDismissal/(24*time.Hour)))
	}
	return d, nil
}
