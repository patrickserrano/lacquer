package shipped

import (
	"strconv"
	"strings"
	"testing"
)

// Execute every real installer: permission errors must not acquire retries,
// rate-limit hints must respect the existing budget, and integrity stays strict.
func TestReleaseInstallRateLimited403(t *testing.T) {
	for _, st := range releaseInstallSteps(t) {
		t.Run(st.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, failures, signal, hint, sleeps string
				tries, fallback                      int
				fail, corrupt, redirect              bool
			}{
				{"remaining zero", "403 403 403 403 403", "remaining", "", "15,30,60,120", 5, 1, false, false, false},
				{"retry header", "403 403 403 403 403", "retry", "1", "15,30,60,120", 5, 1, false, false, false},
				{"honour seconds", "403", "retry", "20", "20", 2, 0, false, false, false},
				{"hint whitespace", "403", "retry", "20 \t", "20", 2, 0, false, false, false},
				{"leading zeros", "403", "retry", "00020", "20", 2, 0, false, false, false},
				{"429 honours hint", "429", "retry", "20", "20", 2, 0, false, false, false},
				{"past date", "403", "retry", "Sat, 01 Jan 2000 00:00:00 GMT", "15", 2, 0, false, false, false},
				{"plain forbidden", "403", "", "", "", 1, 0, true, false, false},
				{"quota remains", "403", "available", "", "", 1, 0, true, false, false},
				{"404 stays fatal", "404", "remaining", "", "", 1, 0, true, false, false},
				{"redirect is not final", "403", "", "", "", 1, 0, true, false, true},
				{"hint beyond budget", "403", "retry", "226", "", 1, 1, false, false, false},
				{"huge hint", "403", "retry", "999999999999999999999999", "", 1, 1, false, false, false},
				{"date beyond budget", "403", "retry", "Fri, 01 Jan 2100 00:00:00 GMT", "", 1, 1, false, false, false},
				{"spent budget", "403 403 403 403 403", "retry", "20", "20,30,60", 4, 1, false, false, false},
				{"invalid hint", "403", "retry", "invalid", "15", 2, 0, false, false, false},
				{"checksum remains strict", "403 403 403 403 403", "remaining", "", "15,30,60,120", 5, 1, true, true, false},
			} {
				for _, target := range []string{"asset", "checksums.txt"} {
					t.Run(tc.name+"/"+target, func(t *testing.T) {
						code, out, trace := runReleaseInstall(t, st, "linux", "amd64", target, tc.failures, tc.corrupt, false, false,
							"RATE_SIGNAL="+tc.signal, "RETRY_AFTER="+tc.hint, "REDIRECT_RATE_LIMIT="+strconv.FormatBool(tc.redirect))
						if (code != 0) != tc.fail {
							t.Fatalf("exit %d, want failure=%t:\n%s\n%s", code, tc.fail, out, trace)
						}
						if strings.Count(trace, "public "+target+"\n") != tc.tries || strings.Count(trace, "gh release download") != tc.fallback {
							t.Fatalf("wrong attempts/fallback:\n%s", trace)
						}
						var waits []string
						total := 0
						for _, line := range strings.Split(trace, "\n") {
							if value, ok := strings.CutPrefix(line, "sleep "); ok {
								seconds, err := strconv.Atoi(value)
								if err != nil || seconds <= 0 {
									t.Fatalf("invalid wait %q", value)
								}
								total += seconds
								waits = append(waits, value)
							}
						}
						if strings.Join(waits, ",") != tc.sleeps || total > 225 {
							t.Fatalf("waits=%v, total=%d, want %q within 225s", waits, total, tc.sleeps)
						}
						if tc.corrupt && !strings.Contains(out, "Checksum verification failed") {
							t.Fatalf("corrupt fallback was not rejected by checksum verification:\n%s", out)
						}
					})
				}
			}
		})
	}
}
