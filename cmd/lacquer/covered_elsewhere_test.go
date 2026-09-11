package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project.pbxproj with the two shapes that matter: the target the synthesised
// product's selector names, and a watch bundle no selector can reach.
const watchPbxproj = `// !$*UTF8*$!
{
	objects = {
		AAA /* Tests */ = {
			isa = PBXNativeTarget;
			name = fixtureTests;
			productType = "com.apple.product-type.bundle.unit-test";
		};
		BBB /* Watch */ = {
			isa = PBXNativeTarget;
			name = "fixture Watch AppTests";
			productType = "com.apple.product-type.bundle.unit-test";
		};
	};
}
`

const watchWorkflow = `name: Watch CI
on:
  pull_request:
    branches: [main]

jobs:
  watch-tests:
    runs-on: [self-hosted, macOS]
    steps:
      - uses: actions/checkout@v4
      - run: |
          xcodebuild test \
            -project fixture.xcodeproj \
            -scheme "fixture Watch App" \
            -destination "platform=watchOS Simulator,id=$WATCH_ID" \
            -only-testing:"fixture Watch AppTests"
`

// uncoveredSection returns the "test targets no selector covers" block of an
// audit report, so a test can assert about THAT list rather than about the whole
// report — the fixture has other uncovered targets, and a whole-output search
// would pass for the wrong reason.
func uncoveredSection(out string) string {
	_, rest, ok := strings.Cut(out, "test targets no selector covers:")
	if !ok {
		return ""
	}
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// The wiring test. The unit tests prove Verify decides correctly; this proves
// `audit` actually consults it — #118's lesson, that a function passing its own
// test says nothing about the shape CI runs. It is also the only place the
// manifest field, the pbxproj parse and the report meet.
func TestAuditVerifiesCoveredElsewhereAgainstTheRepository(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})

	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	audit := func() string {
		t.Helper()
		var out, errb bytes.Buffer
		run([]string{"audit"}, env, &out, &errb)
		return out.String() + errb.String()
	}

	write("fixture.xcodeproj/project.pbxproj", watchPbxproj)
	write(".github/workflows/watch-ci.yml", watchWorkflow)
	writeManifest(t, dir, "xcodeproj = \"fixture.xcodeproj\"\n"+
		"covered_elsewhere = [{ target = \"fixture Watch AppTests\", "+
		"workflow = \".github/workflows/watch-ci.yml\", "+
		"reason = \"watchOS bundle: different scheme and destination, not expressible in a [[product]] leg\" }]")

	out := audit()
	if uncoveredSection(out) != "" && strings.Contains(uncoveredSection(out), "fixture Watch AppTests") {
		t.Errorf("the verified watch target is still reported as running nowhere — the false positive is intact:\n%s", out)
	}
	if !strings.Contains(out, "covered by a workflow this lacquer does not manage") {
		t.Errorf("audit never reports the accepted declaration, so the exception is invisible:\n%s", out)
	}

	// Now take the workflow away, which is what a rename or a deletion looks
	// like. Nothing else changes — and the finding must come straight back,
	// because the declaration is no longer true of this repository.
	if err := os.Remove(filepath.Join(dir, ".github/workflows/watch-ci.yml")); err != nil {
		t.Fatal(err)
	}
	out = audit()
	if !strings.Contains(out, "fixture Watch AppTests") || !strings.Contains(out, "NOT CONFIRMED") {
		t.Errorf("a declaration whose workflow no longer exists went on suppressing the finding:\n%s", out)
	}
	if !strings.Contains(out, "does not exist in this project") {
		t.Errorf("audit does not say which check failed:\n%s", out)
	}
}
