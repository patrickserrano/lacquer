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

// Framework build output carries a generated package.json, which detection
// reads as a web component. `lacquer init` on a Next.js site reported five
// components — ".", ".next", ".next/build", ".next/dev", ".next/dev/build" —
// and syncing that would have written CLAUDE.md regions and CI workflows into
// build artifacts.
func TestSkipsFrameworkBuildOutput(t *testing.T) {
	for _, name := range []string{
		".next", ".nuxt", ".svelte-kit", ".astro", ".output",
		".vercel", ".netlify", ".turbo", ".parcel-cache",
		".angular", ".docusaurus", "dist", "out", ".cache",
	} {
		if !Skip(name) {
			t.Errorf("Skip(%q) = false; build output must never be walked into", name)
		}
	}
}

// The skip list must not swallow ordinary source directories.
func TestDoesNotSkipRealSourceDirs(t *testing.T) {
	for _, name := range []string{"src", "app", "lib", "api", "Sources", "supabase", "ios", "web", "docs"} {
		if Skip(name) {
			t.Errorf("Skip(%q) = true; that is project source", name)
		}
	}
}
