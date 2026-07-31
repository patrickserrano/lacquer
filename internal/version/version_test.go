package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tt := range []struct {
		in         string
		want       Version
		wantErr    bool
		wantString string
	}{
		{in: "0.72.0", want: Version{0, 72, 0}, wantString: "0.72.0"},
		{in: "1.4.2", want: Version{1, 4, 2}, wantString: "1.4.2"},
		{in: "v0.73.0", want: Version{0, 73, 0}, wantString: "0.73.0"}, // a leading v is accepted
		{in: " 0.72.1\n", want: Version{0, 72, 1}, wantString: "0.72.1"},

		// A bare integer is the legacy content counter. release.yml has always
		// tagged v0.${VERSION}.0, so N has a canonical embedding as 0.N.0 — which
		// is what makes legacy stamps order correctly against new semvers instead
		// of being incomparable.
		{in: "72", want: Version{0, 72, 0}, wantString: "0.72.0"},
		{in: "0", want: Version{0, 0, 0}, wantString: "0.0.0"},

		{in: "", wantErr: true},
		{in: "1.2", wantErr: true},
		{in: "1.2.3.4", wantErr: true},
		{in: "1.2.x", wantErr: true},
		{in: "-1.2.3", wantErr: true},
		{in: "abc", wantErr: true},
	} {
		got, err := Parse(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("Parse(%q): want error, got %v", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		if got.String() != tt.wantString {
			t.Errorf("Parse(%q).String() = %q, want %q", tt.in, got.String(), tt.wantString)
		}
	}
}

func TestLess(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"0.72.0", "0.73.0", true},
		{"0.73.0", "0.72.0", false},
		{"0.72.0", "0.72.1", true},
		{"0.72.1", "0.72.0", false},
		{"0.72.0", "0.72.0", false}, // equal is not less
		{"0.99.0", "1.0.0", true},
		{"1.0.0", "0.99.0", false},
		// The legacy embedding must order against real semvers: a project stamped
		// v72 is behind 0.73.0 and current against 0.72.0.
		{"72", "0.73.0", true},
		{"72", "0.72.0", false},
		{"0.72.0", "72", false},
		// Minor must dominate patch, not sort lexically (0.9.0 < 0.10.0).
		{"0.9.0", "0.10.0", true},
		{"0.10.0", "0.9.0", false},
	} {
		a, err := Parse(tt.a)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.a, err)
		}
		b, err := Parse(tt.b)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.b, err)
		}
		if got := a.Less(b); got != tt.want {
			t.Errorf("Parse(%q).Less(%q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestRead(t *testing.T) {
	for _, tt := range []struct {
		name, contents, want string
	}{
		{"semver", "0.73.0\n", "0.73.0"},
		{"legacy integer", "72\n", "0.72.0"},
		{"no trailing newline", "0.73.1", "0.73.1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(tt.contents), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := Read(dir)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("Read = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestReadMissingFile(t *testing.T) {
	if _, err := Read(t.TempDir()); err == nil {
		t.Fatal("Read with no VERSION file: want error, got nil")
	}
}

// A malformed VERSION must fail loudly. Silently defaulting would let every
// project's stamped marker be compared against a bogus latest.
func TestReadMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("not-a-version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil {
		t.Fatal("Read with malformed VERSION: want error, got nil")
	}
}

// --- JSON round-tripping, for .lacquer.lock ---

// Every project synced before semver has `"version": 72` in its .lacquer.lock.
// If that fails to unmarshal, lock.Read returns an error and audit stops working
// for that project entirely — so a JSON number must still be accepted, and mean
// 0.72.0.
func TestUnmarshalJSONAcceptsLegacyNumber(t *testing.T) {
	var got struct{ Version Version }
	if err := json.Unmarshal([]byte(`{"Version": 72}`), &got); err != nil {
		t.Fatalf("unmarshal legacy number: %v", err)
	}
	if got.Version.String() != "0.72.0" {
		t.Errorf("got %s, want 0.72.0", got.Version)
	}
}

func TestUnmarshalJSONAcceptsSemverString(t *testing.T) {
	var got struct{ Version Version }
	if err := json.Unmarshal([]byte(`{"Version": "0.73.1"}`), &got); err != nil {
		t.Fatalf("unmarshal semver string: %v", err)
	}
	if got.Version.String() != "0.73.1" {
		t.Errorf("got %s, want 0.73.1", got.Version)
	}
}

// New locks write the readable string form, not a struct dump.
func TestMarshalJSONWritesString(t *testing.T) {
	v, err := Parse("0.73.1")
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(struct{ Version Version }{v})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != `{"Version":"0.73.1"}` {
		t.Errorf("marshal = %s, want {\"Version\":\"0.73.1\"}", out)
	}
}

func TestUnmarshalJSONRejectsGarbage(t *testing.T) {
	for _, in := range []string{`{"Version": "not-a-version"}`, `{"Version": true}`, `{"Version": {}}`} {
		var got struct{ Version Version }
		if err := json.Unmarshal([]byte(in), &got); err == nil {
			t.Errorf("unmarshal %s: want error, got %v", in, got.Version)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	v, err := Parse("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var back Version
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back != v {
		t.Errorf("round trip = %+v, want %+v", back, v)
	}
}
