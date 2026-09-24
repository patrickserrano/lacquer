package doctor

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/actionlint"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/version"
)

func TestLintConfigDoctorRejectsMissingValues(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "core/doctor.toml"), "[[probe]]\nname='labels'\ncheck='actionlint-labels'\nexpect='pass'\n")
	write(t, filepath.Join(root, "profiles/web/doctor.toml"), "[[probe]]\nname='ignores'\ncheck='biome-ignores'\nexpect='pass'\n")
	write(t, filepath.Join(project, ".lacquer.toml"), "[project]\nname='demo'\n[web]\nbiome_ignores=['!**/payload-types.ts']\n[[component]]\npath='admin'\nprofiles=['web']\n")
	cfg, err := config.Load(filepath.Join(project, ".lacquer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	good, err := actionlint.Merge("", version.Version{})
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{false, true} {
		labels, biome := "self-hosted-runner:\n  labels: []\n", `{"files":{"includes":["**"]}}`
		if valid {
			labels = good
			biome = `{"files":{"includes":["**","!**/payload-types.ts"]}}`
		}
		write(t, filepath.Join(project, actionlint.Name), labels)
		write(t, filepath.Join(project, "admin/biome.json"), biome)
		res, err := Run(root, project, cfg, nil, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 2 {
			t.Fatalf("ran %d probes, want 2", len(res))
		}
		for _, r := range res {
			if r.OK != valid {
				t.Errorf("valid=%v: %+v", valid, r)
			}
		}
	}
}

func TestBuiltinProbeRejectsUnsupportedOptions(t *testing.T) {
	for _, opts := range []string{"check='unknown'\nexpect='pass'", "check='actionlint-labels'\nexpect='fail'", "check='actionlint-labels'\nexpect='pass'\nargv=['true']", "check='actionlint-labels'\nexpect='pass'\nexpect_output='ignored'"} {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "core/doctor.toml"), "[[probe]]\nname='test'\n"+opts+"\n")
		if _, err := LoadProbes(dir, CoreLayer); err == nil {
			t.Errorf("accepted unsupported built-in options: %s", opts)
		}
	}
}
