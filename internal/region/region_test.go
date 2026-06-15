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
