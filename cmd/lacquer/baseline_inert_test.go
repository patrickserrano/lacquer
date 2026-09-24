package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAuditEmptySwiftDiscoveryReportsUnknown(t *testing.T) {
	hr, pr := auditFixture(t, strings.ReplaceAll(pbxCompliant, "SWIFT_VERSION = 6;", ""), "")
	chdir(t, pr)
	var out, errb bytes.Buffer
	code := run([]string{"audit"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
	if code != 0 {
		t.Fatalf("UNKNOWN must not fabricate a violation: exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "baseline: UNKNOWN") || strings.Contains(out.String(), "baseline: ok") {
		t.Fatalf("empty discovery silently passed:\n%s", out.String())
	}
}
