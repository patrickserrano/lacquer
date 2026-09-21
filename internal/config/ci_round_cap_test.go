package config

import (
	"strings"
	"testing"
)

// The cap is a project-wide knob, and 2 is the number the issue (#422) and
// Stripe's write-up both settled on. A manifest that says nothing must get it.
func TestCIRoundCapDefaultsToTwo(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Project.CIRoundCap(); got != 2 {
		t.Errorf("CIRoundCap() with no key = %d, want 2", got)
	}
}

func TestCIRoundCapReadsTheManifest(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\nci_round_cap = 3\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Project.CIRoundCap(); got != 3 {
		t.Errorf("CIRoundCap() = %d, want 3", got)
	}
}

// A cap below one would refuse the FIRST push, including the one that opens the
// PR: it does not tighten the budget, it forbids the work. And an unbounded one
// is no cap. Both are rejected at the manifest, by name, rather than read as
// "use the default" — a typo'd limit that silently became 2 (or infinity) is a
// state indistinguishable from working.
func TestCIRoundCapRejectsValuesThatAreNotACap(t *testing.T) {
	for _, v := range []string{"0", "-1", "11", "100"} {
		_, err := loadString(t, "[project]\nname=\"x\"\nci_round_cap = "+v+"\n")
		if err == nil {
			t.Errorf("ci_round_cap = %s loaded clean", v)
			continue
		}
		if !strings.Contains(err.Error(), "ci_round_cap") {
			t.Errorf("ci_round_cap = %s: error does not name the key: %v", v, err)
		}
	}
}

// A string is a typo for a number, not a request for the default.
func TestCIRoundCapRejectsAString(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\nci_round_cap = \"two\"\n"); err == nil {
		t.Fatal(`ci_round_cap = "two" loaded clean`)
	}
}
