package docsmirror

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is where this package sits relative to the repository root. The live
// checks below read the real rule files, because a gate that only ever sees
// fixtures is a gate that has never met the thing it guards.
const repoRoot = "../.."

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestUnpaired(t *testing.T) {
	const src = "core/CLAUDE.core.md"
	const mirror = "site/src/content/docs/guides/agent-rules.md"

	tests := []struct {
		name    string
		changed []string
		want    int
	}{
		{"nothing changed", nil, 0},
		{"unrelated files only", []string{"README.md", "internal/sync/sync.go"}, 0},
		{"source without mirror", []string{src}, 1},
		{"source and mirror together", []string{src, mirror}, 0},
		{"mirror alone is legitimate", []string{mirror}, 0},
		{
			"two sources, one mirror",
			[]string{src, mirror, "profiles/ios/CLAUDE.ios.md"},
			1,
		},
		{
			"every source, no mirrors",
			[]string{
				"core/CLAUDE.core.md",
				"profiles/ios/CLAUDE.ios.md",
				"profiles/web/CLAUDE.web.md",
				"profiles/supabase/CLAUDE.supabase.md",
			},
			4,
		},
		// git prints paths exactly; whitespace only shows up when a caller pipes
		// through something that keeps the newline. Blank entries must not be
		// read as a path.
		{"blank entries ignored", []string{"", "   ", src, mirror}, 0},
		// A path that CONTAINS a source path is a different file.
		{"substring is not a match", []string{"docs/core/CLAUDE.core.md"}, 0},
		{"suffixed path is not a match", []string{src + ".bak"}, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Unpaired(tc.changed)
			if len(got) != tc.want {
				t.Fatalf("Unpaired(%v) = %d violations %q, want %d", tc.changed, len(got), got, tc.want)
			}
		})
	}
}

func TestUnpairedMessageNamesBothPaths(t *testing.T) {
	// The whole point of the message is that the reader learns which file they
	// forgot; a bare "mirrors out of sync" sends them hunting.
	got := Unpaired([]string{"profiles/web/CLAUDE.web.md"})
	if len(got) != 1 {
		t.Fatalf("got %d violations, want 1", len(got))
	}
	for _, want := range []string{
		"profiles/web/CLAUDE.web.md",
		"site/src/content/docs/guides/web-rules.md",
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("message %q does not name %q", got[0], want)
		}
	}
}

func TestCodeBlocks(t *testing.T) {
	md := "intro prose\n" +
		"```toml\n" +
		"[project]\n" +
		"\n" +
		"exclude   =   []\n" +
		"```\n" +
		"more prose, with an inline `fence` that is not one\n" +
		"````md\n" +
		"```\n" + // a shorter fence inside a longer one does not close it
		"nested\n" +
		"```\n" +
		"````\n" +
		"~~~toml\n" + // tilde fences are not recognized; these files do not use them
		"not-a-block\n" +
		"~~~\n"

	got := CodeBlocks(md)
	if len(got) != 2 {
		t.Fatalf("got %d blocks, want 2: %+v", len(got), got)
	}

	if got[0].Info != "toml" {
		t.Errorf("block 0 info = %q, want %q", got[0].Info, "toml")
	}
	if got[0].Line != 2 {
		t.Errorf("block 0 line = %d, want 2", got[0].Line)
	}
	// Blank line dropped, whitespace run collapsed.
	if want := []string{"[project]", "exclude = []"}; !reflect.DeepEqual(got[0].Lines, want) {
		t.Errorf("block 0 lines = %q, want %q", got[0].Lines, want)
	}

	if got[1].Info != "md" {
		t.Errorf("block 1 info = %q, want %q", got[1].Info, "md")
	}
	if want := []string{"```", "nested", "```"}; !reflect.DeepEqual(got[1].Lines, want) {
		t.Errorf("block 1 lines = %q, want %q", got[1].Lines, want)
	}
}

func TestCodeBlocksUnterminatedFenceStillYieldsContent(t *testing.T) {
	// A truncated block must not silently check nothing — that is the failure
	// mode the whole file is about.
	got := CodeBlocks("```sh\nsupabase db push --linked\n")
	if len(got) != 1 {
		t.Fatalf("got %d blocks, want 1", len(got))
	}
	if want := []string{"supabase db push --linked"}; !reflect.DeepEqual(got[0].Lines, want) {
		t.Errorf("lines = %q, want %q", got[0].Lines, want)
	}
}

func TestCodeBlocksNoFences(t *testing.T) {
	if got := CodeBlocks("just prose\nand `inline code`\n"); len(got) != 0 {
		t.Errorf("got %d blocks, want 0: %+v", len(got), got)
	}
}

func TestMissingBlocks(t *testing.T) {
	source := "```toml\n" +
		"[project]\n" +
		"exclude = [\n" +
		"  { path = \"lefthook.yml\", reason = \"monorepo\" },\n" +
		"]\n" +
		"```\n" +
		"```sh\n" +
		"comment on table  public.sessions       is 'x';\n" +
		"```\n"

	tests := []struct {
		name   string
		mirror string
		want   []string
	}{
		{
			name:   "identical block",
			mirror: "```toml\n[project]\nexclude = [\n  { path = \"lefthook.yml\", reason = \"monorepo\" },\n]\n```\n",
		},
		{
			// This is the drift that actually shipped.
			name:   "stale value inside a block",
			mirror: "```toml\n[project]\nexclude = [\"lefthook.yml\"]\n```\n",
			want:   []string{`exclude = ["lefthook.yml"]`},
		},
		{
			// A mirror may show less. Demanding whole-block equality would fail
			// on every legitimate abridgement and the gate would get disabled.
			name:   "mirror omits a line",
			mirror: "```toml\n[project]\n```\n",
		},
		{
			// Column alignment is presentation, not a rule change.
			name:   "different column alignment",
			mirror: "```sql\ncomment on table  public.sessions          is 'x';\n```\n",
		},
		{
			// A page is free to tag a snippet differently.
			name:   "different info string",
			mirror: "```bash\ncomment on table public.sessions is 'x';\n```\n",
		},
		{
			name:   "no code blocks at all",
			mirror: "just prose about `exclude`\n",
		},
		{
			name:   "blank lines are not findings",
			mirror: "```toml\n\n[project]\n\n```\n",
		},
		{
			name:   "two stale lines both reported",
			mirror: "```toml\nexclude = [\"a.yml\"]\ninclude = [\"b.yml\"]\n```\n",
			want:   []string{`exclude = ["a.yml"]`, `include = ["b.yml"]`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, f := range MissingBlocks(source, tc.mirror) {
				got = append(got, f.Line)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MissingBlocks = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMissingBlocksReportsTheMirrorLocation(t *testing.T) {
	// Without a line number the failure sends the reader scanning a 300-line
	// page for the offending snippet.
	source := "```toml\n[project]\n```\n"
	mirror := "prose\nprose\n```toml\nexclude = [\"stale.yml\"]\n```\n"

	got := MissingBlocks(source, mirror)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	if got[0].Block.Line != 3 {
		t.Errorf("finding line = %d, want 3", got[0].Block.Line)
	}
	if got[0].Block.Info != "toml" {
		t.Errorf("finding info = %q, want %q", got[0].Block.Info, "toml")
	}
}

// TestMirrorsShowNoCodeTheSourceDoesNot is the content half of the gate. It runs
// on every `go test ./...`, so it does not depend on a pull-request context and
// cannot be dodged by pushing a source and its mirror in separate commits.
func TestMirrorsShowNoCodeTheSourceDoesNot(t *testing.T) {
	for _, p := range Pairs {
		t.Run(p.Mirror, func(t *testing.T) {
			source, mirror := read(t, p.Source), read(t, p.Mirror)

			blocks := CodeBlocks(mirror)
			if len(blocks) == 0 {
				t.Fatalf("%s has no fenced code blocks — either the page lost its examples or CodeBlocks stopped finding them; either way this check is asserting nothing", p.Mirror)
			}

			for _, f := range MissingBlocks(source, mirror) {
				t.Errorf("%s:%d (```%s) shows a line %s does not:\n\t%s\nThe mirror may say less than its source and may word it differently, but a code block is what a reader copies verbatim. Correct the page, or move the change into the source first.",
					p.Mirror, f.Block.Line, f.Block.Info, p.Source, f.Line)
			}
		})
	}
}

// TestPairFilesExist catches a typo or a rename in Pairs. A pair naming a file
// that is not there would otherwise make both checks pass by checking nothing.
func TestPairFilesExist(t *testing.T) {
	if len(Pairs) == 0 {
		t.Fatal("Pairs is empty")
	}
	for _, p := range Pairs {
		for _, path := range []string{p.Source, p.Mirror} {
			if _, err := os.Stat(filepath.Join(repoRoot, path)); err != nil {
				t.Errorf("Pairs names %s, which is not readable: %v", path, err)
			}
		}
	}
}

// workflowPair matches one `source<TAB>mirror` line of the PAIRS heredoc in the
// changed-together step of .github/workflows/ci.yml.
var workflowPair = regexp.MustCompile(`(?m)^\s*(\S+\.md)\t(\S+\.md)\s*$`)

// TestWorkflowPairsMatchPairs keeps the shell copy of the pair list honest.
//
// The changed-together gate runs in the commit-convention job — it needs the PR
// diff, and putting it there costs no extra billed job — so the list is written
// twice. That is exactly the arrangement this whole package exists to police, so
// it gets the same treatment: add a pair in one place only and this fails.
func TestWorkflowPairsMatchPairs(t *testing.T) {
	ci := read(t, ".github/workflows/ci.yml")

	var got []Pair
	for _, m := range workflowPair.FindAllStringSubmatch(ci, -1) {
		got = append(got, Pair{Source: m[1], Mirror: m[2]})
	}

	if !reflect.DeepEqual(got, Pairs) {
		t.Errorf("the PAIRS list in .github/workflows/ci.yml is\n\t%+v\nbut docsmirror.Pairs is\n\t%+v\nThey must match, in the same order. Keep the tab-separated `source<TAB>mirror` shape the workflow greps for.", got, Pairs)
	}
}
