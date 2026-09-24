package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/console"
)

// Exercise the real CLI, config decoding, child argv, JSONL and listing together.
// The child is a recorder, not evidence of the model a live service used.
func TestConsoleDispatchModel(t *testing.T) {
	lq := realLacquer(t)
	for _, mode := range []string{"bg", "tmux"} {
		for _, tt := range []struct {
			name, config, model, effort string
			role                        bool
			flags                       []string
		}{
			{name: "IC default", model: "sonnet"},
			{name: "fleet defaults", config: "ic_model = \"opus\"\nic_effort = \"low\"\n", model: "opus", effort: "low"},
			{name: "flags override fleet", config: "ic_model = \"sonnet\"\nic_effort = \"high\"\n", flags: []string{"--model", "opus", "--effort", "low"}, model: "opus", effort: "low"},
			{name: "model alone keeps fleet effort", config: "ic_effort = \"high\"\n", flags: []string{"--model=opus"}, model: "opus", effort: "high"},
			{name: "effort alone", flags: []string{"--effort=medium"}, model: "sonnet", effort: "medium"},
			{name: "role inherits", role: true},
			{name: "role defaults", role: true, config: "model = \"opus\"\neffort = \"medium\"\n", model: "opus", effort: "medium"},
			{name: "flags override role", role: true, config: "model = \"opus\"\neffort = \"high\"\n", flags: []string{"--model=sonnet", "--effort=low"}, model: "sonnet", effort: "low"},
		} {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				f := newPlaceFleet(t)
				bin := t.TempDir()
				// No real tmux server. Execute exactly the command tmux would receive.
				script := "#!/bin/sh\n[ \"$1\" = new-session ] || exit 1\nshift 6\nexec \"$@\"\n"
				if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				args := []string{"--sessions", f.sessions}
				if tt.role {
					data := "[[role]]\nname = \"lead\"\ntask = \"do the unit\"\ndir = \"" + f.proj + "\"\nmode = \"" + mode + "\"\n" + tt.config
					if err := os.WriteFile(f.roles, []byte(data), 0o644); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--roles", f.roles, "dispatch-role", "lead")
				} else {
					data, err := os.ReadFile(f.roster)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(f.roster, append([]byte(tt.config), data...), 0o644); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--roster", f.roster, "--mode", mode, "dispatch", "proj", "do the unit")
				}
				args = append(args, tt.flags...)
				dryArgs := append(append([]string{}, args...), "--dry-run")
				dry, dryErr, dryCode := runConsole(t, lq, dryArgs)
				if dryCode != 0 {
					t.Fatalf("dry run: %d %s%s", dryCode, dry, dryErr)
				}
				if tt.model != "" && !strings.Contains(dry, "--model "+tt.model) {
					t.Errorf("dry run omitted model: %s", dry)
				}
				if tt.effort != "" && !strings.Contains(dry, "--effort "+tt.effort) {
					t.Errorf("dry run omitted effort: %s", dry)
				}
				if len(f.launches(t)) != 0 || len(readRecords(t, f.sessions)) != 0 {
					t.Fatal("dry run launched or recorded a session")
				}
				out, errb, code := runConsole(t, lq, args)
				if code != 0 {
					t.Fatalf("exit %d: %s%s", code, out, errb)
				}
				want := []string{}
				if mode == "bg" {
					want = append(want, "--bg")
				}
				want = append(want, "--dangerously-skip-permissions", "--settings", `{"sandbox":{"enabled":false}}`)
				if tt.model != "" {
					want = append(want, "--model", tt.model)
				}
				if tt.effort != "" {
					want = append(want, "--effort", tt.effort)
				}
				want = append(want, "do the unit")
				calls := f.launches(t)
				if len(calls) != 1 {
					t.Fatalf("launches = %+v", calls)
				}
				if !reflect.DeepEqual(calls[0].args, want) {
					t.Errorf("argv = %q; want %q", calls[0].args, want)
				}
				data, err := os.ReadFile(f.sessions)
				if err != nil {
					t.Fatal(err)
				}
				var record map[string]any
				if err := json.Unmarshal(data, &record); err != nil {
					t.Fatal(err)
				}
				if record["model"] != tt.model || record["effort"] != tt.effort {
					t.Errorf("recorded settings = %v/%v; want %q/%q", record["model"], record["effort"], tt.model, tt.effort)
				}
				out, errb, code = runConsole(t, lq, []string{"--sessions", f.sessions, "watch"})
				model, effort := tt.model, tt.effort
				if model == "" {
					model = "inherited/unknown"
				}
				if effort == "" {
					effort = "inherited/unknown"
				}
				if !strings.Contains(out, "model="+model) || !strings.Contains(out, "effort="+effort) {
					t.Errorf("listing omits settings (exit %d): %s%s", code, out, errb)
				}
			})
		}
	}
}

// A watchdog must not discard the operator's overrides when restarting a unit.
func TestConsoleModelRelaunchAndLegacyRecord(t *testing.T) {
	lq := realLacquer(t)
	for _, kind := range []console.Kind{console.ProjectKind, console.RoleKind} {
		t.Run(string(kind), func(t *testing.T) {
			f := newPlaceFleet(t)
			name := "proj"
			if kind == console.RoleKind {
				name = "pm-bg"
			}
			r := console.Record{Kind: kind, Name: name, Mode: console.Background, Dir: f.proj, Task: "do the unit", Model: "opus", Effort: "low", LaunchError: "failed"}
			if err := console.AppendRecord(f.sessions, r); err != nil {
				t.Fatal(err)
			}
			out, errb, code := runConsole(t, lq, []string{"--roster", f.roster, "--roles", f.roles, "--sessions", f.sessions, "watch", "--relaunch"})
			if code != 0 {
				t.Fatalf("relaunch: %d %s%s", code, out, errb)
			}
			recs := readRecords(t, f.sessions)
			if len(recs) != 1 || recs[0].Model != "opus" || recs[0].Effort != "low" {
				t.Fatalf("replacement: %+v", recs)
			}
			calls := f.launches(t)
			if len(calls) != 1 || !reflect.DeepEqual(calls[0].args[4:8], []string{"--model", "opus", "--effort", "low"}) {
				t.Fatalf("relaunch argv: %+v", calls)
			}
			out, errb, code = runConsole(t, lq, []string{"--roster", f.roster, "--sessions", f.sessions})
			if code != 0 || !strings.Contains(out, "model=opus effort=low") {
				t.Fatalf("dashboard: %d %s%s", code, out, errb)
			}
		})
	}
	f := newPlaceFleet(t)
	if err := os.WriteFile(f.sessions, []byte(`{"kind":"project","name":"proj","mode":"bg"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errb, code := runConsole(t, lq, []string{"--sessions", f.sessions, "watch"})
	if code != 0 || !strings.Contains(out, "model=inherited/unknown effort=inherited/unknown") {
		t.Fatalf("legacy record: %d %s%s", code, out, errb)
	}
}

func TestConsoleModelFailedLaunchRecorded(t *testing.T) {
	lq := realLacquer(t)
	f := newPlaceFleet(t)
	if err := os.WriteFile(f.failFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, errb, code := runConsole(t, lq, []string{"--roster", f.roster, "--sessions", f.sessions, "--mode", "bg", "--model", "opus", "--effort", "low", "dispatch", "proj", "do the unit"})
	if code == 0 {
		t.Fatalf("failed child reported success: %s%s", out, errb)
	}
	records := readRecords(t, f.sessions)
	if len(records) != 1 || records[0].Model != "opus" || records[0].Effort != "low" || records[0].LaunchError == "" {
		t.Fatalf("failed launch lost settings: %+v", records)
	}
}
