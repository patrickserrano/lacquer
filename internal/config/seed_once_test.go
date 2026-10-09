package config

import (
	"strings"
	"testing"
)

func TestSeedOnceLoadsAndMatchesExactly(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\nseed_once = [\"ios/Secrets.xcconfig.example\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Project.SeedsOnce("ios/Secrets.xcconfig.example") {
		t.Error("the declared path is not seed-once")
	}
	// Exact, never a prefix: a prefix would freeze files nobody named.
	for _, other := range []string{"ios", "ios/Secrets.xcconfig", "ios/Secrets.xcconfig.example/x", "Secrets.xcconfig.example"} {
		if cfg.Project.SeedsOnce(other) {
			t.Errorf("SeedsOnce(%q) = true; seed_once must match only the exact path it names", other)
		}
	}
}

func TestSeedOnceRejectsBadEntries(t *testing.T) {
	for _, tc := range []struct{ name, toml, want string }{
		{"absolute", `seed_once = ["/etc/passwd"]`, "seed_once"},
		{"traversal", `seed_once = ["../x"]`, "seed_once"},
		{"unclean", `seed_once = ["./ios/x"]`, "clean"},
		{"directory", `seed_once = ["ios/"]`, "directory"},
		{"empty", `seed_once = [""]`, "seed_once"},
		{"duplicate", `seed_once = ["a/b", "a/b"]`, "twice"},
		{"also excluded", "exclude = [\"ios\"]\nseed_once = [\"ios/Secrets.xcconfig.example\"]", "exclude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, "[project]\nname=\"x\"\n"+tc.toml+"\n")
			if err == nil {
				t.Fatalf("loaded a manifest with %s", tc.toml)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
