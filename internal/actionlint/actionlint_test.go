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
