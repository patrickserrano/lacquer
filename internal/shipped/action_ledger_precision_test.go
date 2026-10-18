package shipped

import (
	"strings"
	"testing"
)

// compareLedger is driven with synthetic ref sets here, so each way the ledger
// can fail is proven to fail without editing the real ledger. The one tolerated
// case is a same-major patch tag; everything the test caught before still has
// to be caught.
func TestCompareLedgerPrecision(t *testing.T) {
	shipped := map[string][]string{
		"actions/setup-node@v7": {"profiles/web/workflows/ci.yml"},
		"actions/cache@v6":      {"profiles/ios/workflows/ci.yml"},
	}
	cases := []struct {
		name   string
		ledger []string
		want   []string // substrings, one per expected problem, in order
	}{
		{"exact mirror", []string{"actions/cache@v6", "actions/setup-node@v7"}, nil},
		{"same-major minor tag mirrors the floating major", []string{"actions/cache@v6", "actions/setup-node@v7.1.0"}, nil},
		{"same-major two-part tag mirrors the floating major", []string{"actions/cache@v6.2", "actions/setup-node@v7"}, nil},
		{"missing ref", []string{"actions/cache@v6"}, []string{"actions/setup-node@v7 is shipped by"}},
		{"different major is missing and extra", []string{"actions/cache@v6", "actions/setup-node@v8.0.0"},
			[]string{"actions/setup-node@v7 is shipped by", "actions/setup-node@v8.0.0 is in"}},
		{"different major without precision", []string{"actions/cache@v6", "actions/setup-node@v8"},
			[]string{"actions/setup-node@v7 is shipped by", "actions/setup-node@v8 is in"}},
		{"extra ref", []string{"actions/cache@v6", "actions/setup-node@v7", "actions/checkout@v7"}, []string{"actions/checkout@v7 is in"}},
		{"different action with the same version is not a mirror", []string{"actions/cache@v6", "evil/setup-node@v7.1.0"},
			[]string{"actions/setup-node@v7 is shipped by", "evil/setup-node@v7.1.0 is in"}},
		{"two spellings of one action", []string{"actions/cache@v6", "actions/setup-node@v7", "actions/setup-node@v7.1.0"},
			[]string{"lists actions/setup-node@v7 more than once"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := compareLedger(shipped, c.ledger)
			if len(got) != len(c.want) {
				t.Fatalf("want %d problem(s) %q, got %d: %q", len(c.want), c.want, len(got), got)
			}
			for i := range got {
				if !strings.Contains(got[i], c.want[i]) {
					t.Errorf("problem %d = %q, want it to contain %q", i, got[i], c.want[i])
				}
			}
		})
	}
}
