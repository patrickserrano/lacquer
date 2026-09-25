package inboxwatch

import (
	"fmt"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// showWidth is where foxy-inbox wraps a body when it prints one.
const showWidth = 98

// Show is one entry's full detail as text, what `inbox watch --show ID` prints
// (foxy-inbox's detail_text). The latest record for an id wins. Every string in
// it is written by an agent, so it goes through the same sanitizer as the
// screen: this text is written to a terminal too.
func Show(path, id string) (string, error) {
	all, _, err := inbox.ReadAll(path)
	if err != nil {
		return "", err
	}
	var e inbox.Entry
	found := false
	for _, en := range all {
		if en.ID == id {
			e, found = en, true
		}
	}
	if !found {
		return "", fmt.Errorf("no inbox entry %s", clean(id))
	}
	lines := []string{fmt.Sprintf("%s  %s", clean(strings.ToUpper(string(e.Type))), clean(e.ID)), clean(e.Title), ""}
	created := ""
	if !e.CreatedAt.IsZero() {
		created = e.CreatedAt.Format(time.RFC3339Nano)
	}
	for _, f := range []struct{ key, val string }{{"project", e.Project}, {"createdAt", created}, {"ref", e.Ref}} {
		if f.val != "" {
			lines = append(lines, fmt.Sprintf("%9s: %s", f.key, clean(f.val)))
		}
	}
	lines = append(lines, "")
	body := strings.ReplaceAll(strings.ReplaceAll(e.Body, "\r\n", "\n"), "\r", "\n") // CRLF bodies would show ^M
	for _, para := range strings.Split(body, "\n") {
		lines = append(lines, wrap(clean(para), showWidth)...)
	}
	return strings.Join(lines, "\n") + "\n", nil
}
