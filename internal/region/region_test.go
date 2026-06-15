package region

import "testing"

func TestStampedVersion(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		key       string
		wantVer   int
		wantFound bool
	}{
		{
			name:      "present",
			content:   "intro\n<!-- harness:core:start v4 -->\nbody\n<!-- harness:core:end -->\noutro",
			key:       "core",
			wantVer:   4,
			wantFound: true,
		},
		{
			name:      "absent",
			content:   "no markers here",
			key:       "core",
			wantVer:   0,
			wantFound: false,
		},
		{
			name:      "different key absent",
			content:   "<!-- harness:ios:start v2 -->\nx\n<!-- harness:ios:end -->",
			key:       "core",
			wantVer:   0,
			wantFound: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ver, found := StampedVersion(c.content, c.key)
			if ver != c.wantVer || found != c.wantFound {
				t.Fatalf("StampedVersion(%q) = (%d,%v), want (%d,%v)",
					c.key, ver, found, c.wantVer, c.wantFound)
			}
		})
	}
}

func TestMergeReplacesExistingBlock(t *testing.T) {
	content := "# CLAUDE.md\n\nlocal top\n\n" +
		"<!-- harness:core:start v3 -->\nOLD shared body\n<!-- harness:core:end -->\n\n" +
		"local bottom\n"
	got, err := Merge(content, "core", 5, "NEW shared body")
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	want := "# CLAUDE.md\n\nlocal top\n\n" +
		"<!-- harness:core:start v5 -->\nNEW shared body\n<!-- harness:core:end -->\n\n" +
		"local bottom\n"
	if got != want {
		t.Fatalf("Merge mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMergeAppendsWhenAbsent(t *testing.T) {
	content := "# CLAUDE.md\n\nProject Identity: rail\n"
	got, err := Merge(content, "ios", 2, "iOS shared rules")
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	want := "# CLAUDE.md\n\nProject Identity: rail\n\n" +
		"<!-- harness:ios:start v2 -->\niOS shared rules\n<!-- harness:ios:end -->\n"
	if got != want {
		t.Fatalf("Merge append mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMergeAppendsToEmpty(t *testing.T) {
	got, err := Merge("", "core", 1, "rules")
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	want := "<!-- harness:core:start v1 -->\nrules\n<!-- harness:core:end -->\n"
	if got != want {
		t.Fatalf("Merge empty mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMergeRejectsDanglingStart(t *testing.T) {
	content := "<!-- harness:core:start v1 -->\nbody with no end marker\n"
	_, err := Merge(content, "core", 2, "new")
	if err == nil {
		t.Fatal("expected error for dangling start marker, got nil")
	}
}
