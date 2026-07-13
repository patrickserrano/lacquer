package skipdirs

import "testing"

func TestSkip(t *testing.T) {
	cases := map[string]bool{
		".git":                    true,
		".worktrees":              true,
		"node_modules":            true,
		".build":                  true,
		"vendor":                  true,
		".agents":                 true,
		"Pods":                    true,
		"Carthage":                true,
		"DerivedData":             true,
		"DerivedData-shots":       true,
		"DerivedData-feature-123": true,
		"DerivedData2":            true, // prefix match, no separator required
		"src":                     false,
		"ios":                     false,
		"DerivedDataButNotReally": true, // prefix match is intentionally permissive
		"NotDerivedData":          false,
	}
	for name, want := range cases {
		if got := Skip(name); got != want {
			t.Errorf("Skip(%q) = %v, want %v", name, got, want)
		}
	}
}
