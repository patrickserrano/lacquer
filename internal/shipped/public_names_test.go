package shipped

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// publicDenylistEnv names the file of patterns the operator keeps outside this
// public repository. The list itself must never be committed here.
const publicDenylistEnv = "LACQUER_PUBLIC_DENYLIST"

// publicDenylistRequiredEnv, set to "1", turns the two skips below (variable
// unset, file unreadable) into failures. CI sets it on a run that holds the
// secret, so a run that was meant to check cannot pass by silently not checking.
const publicDenylistRequiredEnv = "LACQUER_PUBLIC_DENYLIST_REQUIRED"

type nameHit struct {
	file string
	line int
	text string
}

// parseDenylist reads one RE2 regex per line. Matching is case-insensitive
// unless the line starts with (?-i). Blank lines and # comments are skipped.
// Errors name the line number only, never the pattern.
func parseDenylist(raw string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	sc := bufio.NewScanner(strings.NewReader(raw))
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "(?-i)") {
			line = "(?i)" + line
		}
		re, err := regexp.Compile(line)
		if err != nil {
			return nil, fmt.Errorf("denylist line %d is not a valid RE2 regex", n)
		}
		out = append(out, re)
	}
	return out, sc.Err()
}

// formatHit renders one finding for a test log. This repository is public and
// CI logs are readable by anyone, so it names the file and line and nothing
// else: never the matched text and never the pattern.
func formatHit(h nameHit) string {
	return fmt.Sprintf("%s:%d: matches the private denylist", h.file, h.line)
}

// loadDenylist reads the denylist file named by path. A missing or unreadable
// file yields a skip reason when required is false and an error when it is
// true; exactly one of the second and third results is set on those paths.
func loadDenylist(path string, required bool) (raw []byte, skip string, err error) {
	if path == "" {
		msg := fmt.Sprintf("%s is unset: no private denylist supplied, nothing to check", publicDenylistEnv)
		if required {
			return nil, "", fmt.Errorf("%s, but %s=1 says this run must check", msg, publicDenylistRequiredEnv)
		}
		return nil, msg, nil
	}
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		msg := fmt.Sprintf("%s points at a file that cannot be read: %v", publicDenylistEnv, rerr)
		if required {
			return nil, "", fmt.Errorf("%s, but %s=1 says this run must check", msg, publicDenylistRequiredEnv)
		}
		return nil, msg, nil
	}
	return raw, "", nil
}

func isBinary(b []byte) bool {
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// scanPublicNames reports every line of the given files (paths relative to
// root) that matches a pattern. Binary and unreadable-as-regular files are skipped.
func scanPublicNames(root string, files []string, patterns []*regexp.Regexp) ([]nameHit, error) {
	var hits []nameHit
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue // deleted in the worktree, a symlink to nowhere, a submodule
		}
		if isBinary(b) {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, re := range patterns {
				if m := re.FindString(line); m != "" {
					hits = append(hits, nameHit{file: f, line: i + 1, text: m})
				}
			}
		}
	}
	return hits, nil
}

func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files failed, not a git checkout: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// This repository is public and its profiles ship into every consumer. The
// operator keeps a private list of names that must not appear in it; this test
// fails when any tracked file matches an entry. It reports file:line only, never
// the matched text and never the list.
func TestShippedContentNamesNoPrivateConsumer(t *testing.T) {
	raw, skip, err := loadDenylist(os.Getenv(publicDenylistEnv), os.Getenv(publicDenylistRequiredEnv) == "1")
	if err != nil {
		t.Fatal(err)
	}
	if skip != "" {
		t.Skip(skip)
	}
	patterns, err := parseDenylist(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) == 0 {
		t.Fatalf("%s is set but holds no patterns; an empty list cannot fail, so it is not a check", publicDenylistEnv)
	}
	r := root(t)
	hits, err := scanPublicNames(r, trackedFiles(t, r), patterns)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		t.Error(formatHit(h))
	}
}

// The scanner can fail and can stay silent. Neither needs the private file.
func TestScanPublicNamesFailsOnPlantedName(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("clean.md", "a consumer app and a paid app\n")
	write("planted.md", "first line\nthe Zorblaxian app leaked here\n")
	write("binary.bin", "Zorblax\x00Zorblax")
	files := []string{"clean.md", "planted.md", "binary.bin", "missing.md"}

	patterns, err := parseDenylist("# comment\n\nzorblax\\w*\n(?-i)CaseSensitiveOnly\n")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := scanPublicNames(dir, files, patterns)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].file != "planted.md" || hits[0].line != 2 || hits[0].text != "Zorblaxian" {
		t.Fatalf("want exactly planted.md:2 Zorblaxian, got %+v", hits)
	}

	// (?-i) entries do not match other cases.
	write("case.md", "casesensitiveonly\n")
	hits, _ = scanPublicNames(dir, []string{"case.md"}, patterns)
	if len(hits) != 0 {
		t.Fatalf("(?-i) entry matched a different case: %+v", hits)
	}
}

func TestScanPublicNamesSilentOnCleanTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("nothing private here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patterns, err := parseDenylist("zorblax\n")
	if err != nil {
		t.Fatal(err)
	}
	hits, _ := scanPublicNames(dir, []string{"a.md"}, patterns)
	if len(hits) != 0 {
		t.Fatalf("clean tree produced hits: %+v", hits)
	}
}

func TestParseDenylistRejectsBadRegexWithoutEchoingIt(t *testing.T) {
	_, err := parseDenylist("ok\n(unclosed\n")
	if err == nil {
		t.Fatal("invalid regex accepted")
	}
	if strings.Contains(err.Error(), "unclosed") || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error must name the line and not the pattern: %v", err)
	}
}

// The failure message is what lands in a public CI log. It must locate the hit
// and must not repeat what was matched.
func TestFormatHitNamesLocationNotMatchedText(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "planted.md"), []byte("first\nthe Zorblaxian app leaked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patterns, err := parseDenylist("zorblax\\w*\n")
	if err != nil {
		t.Fatal(err)
	}
	hits, _ := scanPublicNames(dir, []string{"planted.md"}, patterns)
	if len(hits) != 1 {
		t.Fatalf("want one hit, got %d", len(hits))
	}
	msg := formatHit(hits[0])
	if !strings.Contains(msg, "planted.md:2") {
		t.Fatalf("message must carry file:line, got %q", msg)
	}
	if strings.Contains(strings.ToLower(msg), "zorblax") {
		t.Fatalf("message repeats the matched text: %q", msg)
	}
}

func TestLoadDenylistSkipsUnlessRequired(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.txt")
	for _, path := range []string{"", missing} {
		raw, skip, err := loadDenylist(path, false)
		if err != nil || skip == "" || raw != nil {
			t.Fatalf("path %q, not required: want a skip, got raw=%v skip=%q err=%v", path, raw, skip, err)
		}
		raw, skip, err = loadDenylist(path, true)
		if err == nil || skip != "" || raw != nil {
			t.Fatalf("path %q, required: want an error and no skip, got raw=%v skip=%q err=%v", path, raw, skip, err)
		}
	}
	present := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(present, []byte("zorblax\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, skip, err := loadDenylist(present, true)
	if err != nil || skip != "" || string(raw) != "zorblax\n" {
		t.Fatalf("readable file: got raw=%q skip=%q err=%v", raw, skip, err)
	}
}
