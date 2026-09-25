package inboxwatch

import (
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/inbox/inboxtest"
)

func TestMain(m *testing.M) {
	os.Exit(inboxtest.Run(m, func() int { return m.Run() }))
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// plain is a rendered row as the operator reads it.
func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

func plainAll(f Frame) string {
	out := make([]string, len(f.Lines))
	for i, l := range f.Lines {
		out[i] = plain(l)
	}
	return strings.Join(out, "\n")
}

// styleOf is the SGR in force where needle first appears in a rendered row:
// what colour the operator sees that text in.
func styleOf(t *testing.T, row, needle string) string {
	t.Helper()
	i := strings.Index(row, needle)
	if i < 0 {
		t.Fatalf("%q is not in the row %q", needle, row)
	}
	sgrs := regexp.MustCompile(`\x1b\[[0-9;]*m`).FindAllStringIndex(row[:i], -1)
	if len(sgrs) == 0 {
		t.Fatalf("no colour set before %q in %q", needle, row)
	}
	last := sgrs[len(sgrs)-1]
	return row[last[0]:last[1]]
}

var t0 = time.Date(2026, 9, 24, 14, 0, 0, 0, time.Local)

func item(id string, typ inbox.Type, age time.Duration, title string) Item {
	return Item{ID: id, Type: typ, CreatedAt: t0.Add(-age), Title: title}
}

// model is a list model of a given size, with items loaded and the clock at t0.
func model(t *testing.T, cfg Config, w, h int, items ...Item) Model {
	t.Helper()
	m := NewModel(cfg, w, h)
	p, _ := m.Update(TickEvent{Now: t0})
	p, _ = p.Update(LoadedEvent{Data: Data{Items: items}, At: t0})
	return p.(Model)
}

// feed sends raw terminal bytes through the parser into a program and returns
// it with every Cmd it asked for.
func feed(t *testing.T, p Program, raw string) (Program, []Cmd) {
	t.Helper()
	evs, rest := ParseInput([]byte(raw), true)
	if len(rest) != 0 {
		t.Fatalf("input %q left %q unparsed", raw, rest)
	}
	var all []Cmd
	for _, ev := range evs {
		var cmds []Cmd
		p, cmds = p.Update(ev)
		all = append(all, cmds...)
	}
	return p, all
}

func kinds(cmds []Cmd) []CmdKind {
	var k []CmdKind
	for _, c := range cmds {
		k = append(k, c.Kind)
	}
	return k
}

func count(cmds []Cmd, k CmdKind) int {
	n := 0
	for _, c := range cmds {
		if c.Kind == k {
			n++
		}
	}
	return n
}

// fakeCmd records what would have run.
type fakeCmd struct {
	mu    sync.Mutex
	calls []string
	stdin []string
	out   map[string]string // by first arg, for list-panes
	fail  map[string]error  // by "name subcommand"
}

func (f *fakeCmd) Run(stdin, name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	f.stdin = append(f.stdin, stdin)
	if len(args) > 0 {
		if err := f.fail[name+" "+args[0]]; err != nil {
			return "", err
		}
		return f.out[name+" "+args[0]], nil
	}
	return "", nil
}
