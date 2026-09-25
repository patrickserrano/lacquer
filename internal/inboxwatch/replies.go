package inboxwatch

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// RepliesFile sits next to the inbox and remembers which entries the operator
// answered from the popup, which is how "replied" survives a restart.
//
// Its format is not ours to change. foxy-inbox writes and reads it, the two run
// side by side while one replaces the other, and the phone mirror may read it.
// Each line is what Python's json.dumps makes of {"id", "at", "text"}: keys in
// that order, ", " and ": " separators, every non-ASCII character escaped as
// \uXXXX, and "at" the local wall clock with no zone. encoding/json makes
// different bytes (no spaces, raw UTF-8), so the line is written by hand.
const RepliesFile = "inbox-replies.jsonl"

// Reply is the latest reply sent for one entry.
type Reply struct {
	At   string // "2006-01-02T15:04:05", local time
	Text string
}

// RepliesPath is where the replies for an inbox file live.
func RepliesPath(inboxPath string) string {
	return filepath.Join(filepath.Dir(inboxPath), RepliesFile)
}

// ReadReplies returns the latest reply per entry id. A line that is not a
// reply record is skipped, as foxy-inbox skips it; a missing file is no replies.
func ReadReplies(path string) (map[string]Reply, error) {
	out := map[string]Reply{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var rec struct {
			ID   *string `json:"id"`
			At   *string `json:"at"`
			Text *string `json:"text"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.ID == nil || rec.At == nil || rec.Text == nil {
			continue
		}
		out[*rec.ID] = Reply{At: *rec.At, Text: *rec.Text}
	}
	return out, sc.Err()
}

// replyLine is the bytes foxy-inbox's log_reply writes for one reply.
func replyLine(id string, at time.Time, text string) string {
	return `{"id": ` + pyString(id) + `, "at": ` + pyString(at.Format("2006-01-02T15:04:05")) +
		`, "text": ` + pyString(text) + "}\n"
}

// AppendReply records a reply the way foxy-inbox does. It is one write of one
// line to a file opened for append, so a reply from each tool cannot tear the other's.
func AppendReply(path, id string, at time.Time, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(replyLine(id, at, text)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// pyString is json.dumps(s) with the default ensure_ascii: printable ASCII as
// is, and everything else, control characters and DEL included, as \uXXXX (a
// surrogate pair above the BMP), with the short escapes Python uses.
func pyString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r >= ' ' && r <= '~':
				b.WriteRune(r)
			case r >= 0x10000:
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			default:
				if r == utf8.RuneError {
					r = 0xfffd
				}
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
