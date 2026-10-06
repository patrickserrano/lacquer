package doctor

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// A probe declaring scratch = "component" gets its fixture inside the
// component, and the directory is gone when the probe ends. Biome 2.5.15 panics
// on a file outside vcs.root, so the biome probes depend on both halves: inside,
// or biome crashes; removed, or every doctor run leaves a malformed fixture in
// the project.
func TestComponentScratchIsInsideTheComponentAndRemoved(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"where\"\nwhy = \"x\"\nscratch = \"component\"\nfile = \"probe.ts\"\ncontent = \"bad\"\n"+
			"argv = [\"sh\", \"-c\", \"pwd -P; cat probe.ts\"]\nexpect = \"pass\"\n")
	if err := os.MkdirAll(filepath.Join(project, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Components: []config.Component{{Path: "admin", Profiles: []string{"p"}}}}
	var out bytes.Buffer
	res, err := Run(root, project, cfg, nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("probe did not run: %+v\n%s", res, out.String())
	}

	// Run it again with the output captured, through the same code path, to see
	// where it ran: the pass/fail result alone cannot say.
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"where\"\nwhy = \"x\"\nscratch = \"component\"\nfile = \"probe.ts\"\ncontent = \"bad\"\n"+
			"argv = [\"sh\", \"-c\", \"pwd -P; cat probe.ts; exit 1\"]\nexpect = \"pass\"\n")
	res, err = Run(root, project, cfg, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	comp, err := filepath.EvalSymlinks(filepath.Join(project, "admin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !strings.Contains(res[0].Detail, "tool said: "+comp+string(filepath.Separator)+".lacquer-doctor-") {
		t.Fatalf("the fixture did not run in a hidden directory inside the component %s: %+v", comp, res)
	}

	entries, err := os.ReadDir(filepath.Join(project, "admin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".lacquer-doctor-") {
			t.Errorf("the scratch directory %s was left in the component", e.Name())
		}
	}
}

func TestRejectsAnUnknownScratch(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"x\"\nscratch = \"project\"\nargv = [\"true\"]\nexpect = \"pass\"\n")
	if _, err := LoadProbes(root, "p"); err == nil {
		t.Fatal("an unknown scratch location must be rejected at load, not silently treated as the default")
	}
}

// Every biome probe that lints a fixture must put it inside the component. The
// real-biome test in internal/shipped proves this end to end, but only where a
// biome is installed; this holds the declaration still everywhere.
func TestShippedBiomeFixtureProbesUseComponentScratch(t *testing.T) {
	probes, err := LoadProbes("../..", "web")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range probes {
		if p.File == "" || !strings.Contains(strings.Join(p.Argv, " "), "node_modules/.bin/biome") {
			continue
		}
		n++
		if p.Scratch != "component" {
			t.Errorf("web probe %q lints a fixture with biome but has scratch=%q; biome 2.5.15 panics on a file outside vcs.root", p.Name, p.Scratch)
		}
	}
	if n == 0 {
		t.Fatal("found no biome fixture probes in the web profile, so this test checked nothing")
	}
}

// installBiome lays out what an install leaves in a component: the package's
// package.json and its schema file. linkFrom, when set, makes
// node_modules/@biomejs/biome a symlink into that store directory, as pnpm does.
func installBiome(t *testing.T, component, version, linkFrom string) {
	t.Helper()
	pkg := filepath.Join(component, "node_modules", "@biomejs", "biome")
	target := pkg
	if linkFrom != "" {
		target = linkFrom
	}
	write(t, filepath.Join(target, "package.json"), `{"name":"@biomejs/biome","version":"`+version+`"}`)
	write(t, filepath.Join(target, "configuration_schema.json"), "{}")
	if linkFrom != "" {
		if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(linkFrom, pkg); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBiomeSchemaCheck(t *testing.T) {
	for _, tc := range []struct {
		name, schema, installed string
		pnpm, extraFile         bool
		wantErr                 string // "" means it must pass
	}{
		{name: "local path, installed", schema: biomeLocalSchema, installed: "2.5.15"},
		{name: "local path, pnpm symlinked install", schema: biomeLocalSchema, installed: "2.5.15", pnpm: true},
		{name: "hosted URL, same version", schema: "https://biomejs.dev/schemas/2.5.15/schema.json", installed: "2.5.15"},
		{name: "hosted URL, older version", schema: "https://biomejs.dev/schemas/2.5.0/schema.json", installed: "2.5.15", wantErr: "is for biome 2.5.0 but @biomejs/biome 2.5.15 is installed"},
		{name: "local path, biome not installed", schema: biomeLocalSchema, wantErr: "@biomejs/biome is not installed"},
		{name: "local path to some other file", schema: "./schema.json", installed: "2.5.15", extraFile: true, wantErr: "not to the installed @biomejs/biome"},
		{name: "local path to nothing", schema: "./node_modules/@biomejs/nope/configuration_schema.json", installed: "2.5.15", wantErr: "does not resolve to a file"},
		{name: "no $schema", schema: "", installed: "2.5.15", wantErr: "has no $schema"},
		{name: "foreign URL", schema: "https://example.com/biome.json", installed: "2.5.15", wantErr: "neither a biomejs.dev schema URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			comp := filepath.Join(project, "admin")
			write(t, filepath.Join(comp, "biome.json"), `{"$schema":"`+tc.schema+`"}`)
			if tc.extraFile {
				write(t, filepath.Join(comp, "schema.json"), "{}")
			}
			if tc.installed != "" {
				link := ""
				if tc.pnpm {
					link = filepath.Join(comp, "node_modules", ".pnpm", "@biomejs+biome@"+tc.installed, "node_modules", "@biomejs", "biome")
				}
				installBiome(t, comp, tc.installed, link)
			}
			err := checkBiomeSchemaInstalled(project, comp)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want pass, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want an error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// Wired through Run, as the shipped web probe is: a matching install passes and
// a mismatched one fails the probe.
func TestBiomeSchemaProbeRunsThroughDoctor(t *testing.T) {
	// Resolved, because on macOS the temp dir sits behind the /var symlink and
	// safepath then rejects the component as escaping the root, a known issue
	// shared with the biome-ignores check and unrelated to this one.
	root := t.TempDir()
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "profiles", "web", "doctor.toml"), "[[probe]]\nname='schema'\ncheck='biome-schema'\nexpect='pass'\n")
	cfg := &config.Config{Components: []config.Component{{Path: "admin", Profiles: []string{"web"}}}}
	comp := filepath.Join(project, "admin")
	installBiome(t, comp, "2.5.15", "")
	for _, tc := range []struct {
		schema string
		ok     bool
	}{
		{biomeLocalSchema, true},
		{"https://biomejs.dev/schemas/2.5.14/schema.json", false},
	} {
		write(t, filepath.Join(comp, "biome.json"), `{"$schema":"`+tc.schema+`"}`)
		res, err := Run(root, project, cfg, nil, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].OK != tc.ok {
			t.Errorf("$schema %s: want OK=%v, got %+v", tc.schema, tc.ok, res)
		}
	}
}

// The shipped biome.json must render the local form, and the web profile must
// run the check that keeps it honest.
func TestShippedBiomeSchemaIsTheInstalledOne(t *testing.T) {
	data, err := os.ReadFile("../../profiles/web/config/biome.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"$schema": "`+biomeLocalSchema+`"`) {
		t.Errorf("profiles/web/config/biome.json no longer points $schema at %s; a hosted URL pins a version consumers do not run", biomeLocalSchema)
	}
	probes, err := LoadProbes("../..", "web")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probes {
		if p.Check == "biome-schema" {
			return
		}
	}
	t.Error("the web profile does not run the biome-schema check, so nothing notices $schema drifting from the installed biome")
}
