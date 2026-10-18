package swiftformatpin

import (
	"regexp"
	"strings"
	"testing"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// The default must be a pin this lacquer can actually install. A Default with
// no table row would render an install step that has nothing to verify against.
func TestDefaultHasAChecksum(t *testing.T) {
	sum, err := Checksum(Default)
	if err != nil {
		t.Fatalf("Default %q has no checksum row: %v", Default, err)
	}
	if !sha256Hex.MatchString(sum) {
		t.Errorf("checksum for %s = %q, want 64 lowercase hex digits", Default, sum)
	}
}

// Every row must be shaped so it can be substituted into shell and YAML without
// quoting: a semver key and a lowercase sha256.
func TestEveryRowIsWellFormed(t *testing.T) {
	if len(Versions()) == 0 {
		t.Fatal("the table is empty")
	}
	semver := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	for _, v := range Versions() {
		if !semver.MatchString(v) {
			t.Errorf("version key %q is not MAJOR.MINOR.PATCH", v)
		}
		sum, _ := Checksum(v)
		if !sha256Hex.MatchString(sum) {
			t.Errorf("checksum for %s = %q, want 64 lowercase hex digits", v, sum)
		}
	}
}

// An unlisted version is refused with the sentence the operator is told to act
// on, and it names the version. A silent fallback to the default would let a
// project believe it was pinned to something it was not.
func TestUnknownVersionIsRefusedByName(t *testing.T) {
	_, err := Checksum("9.9.9")
	if err == nil {
		t.Fatal("Checksum accepted a version with no row")
	}
	want := "swiftformat_version 9.9.9 has no checksum in this lacquer; bump lacquer"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// Resolve is the one place a blank project setting becomes the default.
func TestResolve(t *testing.T) {
	if got := Resolve(""); got != Default {
		t.Errorf("Resolve(\"\") = %q, want %q", got, Default)
	}
	if got := Resolve("0.62.1"); got != "0.62.1" {
		t.Errorf("Resolve(0.62.1) = %q, want the override", got)
	}
}
