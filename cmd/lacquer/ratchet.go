package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/doctor"
	"github.com/patrickserrano/lacquer/internal/ratchet"
)

func runRatchet(args []string, root string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ratchet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	write := fs.Bool("write", false, "initialize or tighten the committed baseline")
	loosen := fs.String("loosen", "", "raise this metric to its current measurement")
	reason := fs.String("reason", "", "reason for the explicit increase")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || (*write && *loosen != "") || (*reason != "" && *loosen == "") {
		fmt.Fprintln(stderr, "use ratchet [--write | --loosen METRIC --reason TEXT]")
		return 2
	}
	cfg, err := config.Load(filepath.Join(root, ".lacquer.toml"))
	if err != nil {
		return fail(stderr, err)
	}
	if *loosen != "" {
		if err := ratchet.Loosen(root, cfg, *loosen, *reason); err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "ratchet: recorded %s increase with reason in %s\n", *loosen, ratchet.Name)
		return 0
	}
	if *write {
		findings, err := ratchet.Tighten(root, cfg, true)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprint(stdout, ratchet.Format(findings))
		if ratchet.Blocking(findings) > 0 {
			return 4
		}
		fmt.Fprintf(stdout, "ratchet: baseline saved in %s\n", ratchet.Name)
		return 0
	}
	values, err := ratchet.Measure(root, cfg)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "ratchet: claude_lines = %d\nratchet: unjustified_suppressions = %d\n", values[ratchet.ClaudeLines], values[ratchet.Suppressions])
	findings, err := ratchet.Check(root, cfg)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprint(stdout, ratchet.Format(findings))
	b, err := ratchet.Read(root)
	if err != nil {
		return fail(stderr, err)
	}
	if b == nil {
		fmt.Fprintln(stdout, "ratchet: no baseline; run lacquer ratchet --write and commit the file")
	}
	if ratchet.Blocking(findings) > 0 {
		return 4
	}
	return 0
}

// Exercise the real CLI gate on a scratch project, not just Compare. The
// positive control rejects a gate that fails for unrelated setup errors.
func ratchetProbe() doctor.Result {
	r := doctor.Result{Component: ".", Profile: doctor.CoreLayer, Name: "ratchet audit rejects a regression after tightening"}
	dir, err := os.MkdirTemp("", "lacquer-ratchet-probe-")
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	defer os.RemoveAll(dir)
	err = proveRatchet(dir)
	r.OK = err == nil
	if err != nil {
		r.Detail = err.Error()
	}
	return r
}

func proveRatchet(dir string) error {
	content := filepath.Join(dir, "content")
	project := filepath.Join(dir, "project")
	for name, body := range map[string]string{
		"content/VERSION": "1.0.0\n", "content/core/CLAUDE.core.md": "rules\n",
		"project/.lacquer.toml": "[project]\nname = 'probe'\n[[component]]\npath = '.'\nprofiles = []\n",
		"project/CLAUDE.md":     strings.Repeat("line\n", 100), "project/probe.ts": "// eslint-disable no-console\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(content, "profiles"), 0o755); err != nil {
		return err
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "probe.ts"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git: %w: %s", err, out)
		}
	}
	old, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(project); err != nil {
		return err
	}
	defer os.Chdir(old)
	env := func(key string) string {
		switch key {
		case "LACQUER_ROOT":
			return content
		case "LACQUER_ALLOW_UNVERIFIED_ROOT", "LACQUER_ALLOW_STALE_BINARY":
			return "1"
		}
		return ""
	}
	var out bytes.Buffer
	invoke := func(args ...string) int { out.Reset(); return run(args, env, &out, &out) }
	if code := invoke("sync"); code != 0 {
		return fmt.Errorf("initial sync = %d: %s", code, &out)
	}
	if code := invoke("ratchet", "--write"); code != 0 {
		return fmt.Errorf("initialize = %d: %s", code, &out)
	}
	claude := filepath.Join(project, "CLAUDE.md")
	data, err := os.ReadFile(claude)
	if err != nil {
		return err
	}
	if err := os.WriteFile(claude, []byte(strings.Replace(string(data), strings.Repeat("line\n", 100), "line\n", 1)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(project, "probe.ts"), []byte("const ok = 1;\n"), 0o644); err != nil {
		return err
	}
	if code := invoke("sync"); code != 0 {
		return fmt.Errorf("tighten = %d: %s", code, &out)
	}
	if code := invoke("audit"); code != 0 {
		return fmt.Errorf("positive control = %d: %s", code, &out)
	}
	if err := os.WriteFile(filepath.Join(project, "probe.ts"), []byte("// eslint-disable no-console\n"), 0o644); err != nil {
		return err
	}
	if code := invoke("audit"); code != 4 || !strings.Contains(out.String(), "ratchet: unjustified_suppressions regressed 0 → 1") {
		return fmt.Errorf("known-bad audit = %d, expected 4 and 0 → 1 diagnostic: %s", code, &out)
	}
	if err := os.WriteFile(filepath.Join(project, "probe.ts"), []byte("const ok = 1;\n"), 0o644); err != nil {
		return err
	}
	data, err = os.ReadFile(claude)
	if err != nil {
		return err
	}
	if err := os.WriteFile(claude, append(data, []byte("extra prose\n")...), 0o644); err != nil {
		return err
	}
	if code := invoke("audit"); code != 4 || !strings.Contains(out.String(), "ratchet: claude_lines regressed") {
		return fmt.Errorf("CLAUDE regression = %d: %s", code, &out)
	}
	return nil
}
