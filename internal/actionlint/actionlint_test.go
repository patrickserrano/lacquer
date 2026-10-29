package actionlint

import (
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/version"
)

func TestCheckRejectsEveryMissingLabel(t *testing.T) {
	good, err := Merge("", version.Version{})
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range strings.Split(Labels, "\n") {
		broken := strings.Replace(good, "    - "+label+"\n", "", 1)
		if err := Check([]byte(broken)); err == nil {
			t.Fatalf("accepted missing label %s", label)
		}
	}
}

func TestMergeRejectsMalformedConfig(t *testing.T) {
	for _, input := range []string{"self-hosted-runner: [bad]\n", "self-hosted-runner:\n  labels: nope\n", "self-hosted-runner:\n  labels: [1]\n", "paths: [\n", "# lacquer:actionlint:start v1.0.0\n"} {
		if _, err := Merge(input, version.Version{}); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestHostedRunnerValues(t *testing.T) {
	for _, ok := range []string{"blacksmith-2vcpu-ubuntu-2404", "ubuntu-latest"} {
		if err := ValidHostedValue(ok); err != nil {
			t.Errorf("rejected allowed value %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "blacksmith-4vcpu-ubuntu-2404", "blacksmith-2vcpu-ubuntu-2404-arm", "ubuntu-24.04", "macos-latest", "Ubuntu-Latest"} {
		if err := ValidHostedValue(bad); err == nil {
			t.Errorf("accepted unknown value %q", bad)
		}
	}
}

func TestCheckHostedRunsOn(t *testing.T) {
	for _, expr := range []string{HostedRunsOn, "${{ vars.CI_RUNNER || 'ubuntu-latest' }}"} {
		if err := CheckHostedRunsOn(expr); err != nil {
			t.Errorf("rejected %q: %v", expr, err)
		}
	}
	for _, expr := range []string{
		"blacksmith-2vcpu-ubuntu-2404",                                // allowed label, but hardcoded: ignores the switch
		"ubuntu-latest",                                               // same
		"blacksmith-4vcpu-ubuntu-2404",                                // 4-core
		"${{ vars.CI_RUNNER || 'blacksmith-4vcpu-ubuntu-2404' }}",     // 4-core fallback
		"${{ vars.CI_RUNNER || 'blacksmith-2vcpu-ubuntu-2404-arm' }}", // ARM fallback
		"${{ vars.CI_RUNNER || 'some-unknown-label' }}",               // unknown fallback
		"${{ vars.CI_RUNNER || '' }}",                                 // empty fallback
		"${{ vars.OTHER_RUNNER || 'blacksmith-2vcpu-ubuntu-2404' }}",  // a second variable
		"${{ vars.CI_RUNNER }}",                                       // no fallback: an unset variable is an empty label
		"${{ vars.CI_RUNNER || 'blacksmith-2vcpu-ubuntu-2404' }} ",    // trailing junk
	} {
		if err := CheckHostedRunsOn(expr); err == nil {
			t.Errorf("accepted %q", expr)
		}
	}
}
