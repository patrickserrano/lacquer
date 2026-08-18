package config

import (
	"strings"
	"testing"
)

// The shape the design settled on, matching [baseline.relax]'s spelling.
func TestLoadRetired(t *testing.T) {
	cfg, err := loadWith(t, `
[project]
name = "queueify"
retired = { since = "2026-08-18", reason = "not a viable app" }

[[component]]
path = "."
profiles = ["web"]
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Project.IsRetired() {
		t.Fatal("IsRetired = false on a manifest that declares [project].retired")
	}
	if got := cfg.Project.Retired.Since; got != "2026-08-18" {
		t.Errorf("since = %q, want 2026-08-18", got)
	}
	if got := cfg.Project.Retired.Reason; got != "not a viable app" {
		t.Errorf("reason = %q, want %q", got, "not a viable app")
	}
	if _, err := cfg.Project.Retired.SinceDate(); err != nil {
		t.Errorf("SinceDate: %v", err)
	}
}

// A project with no [project].retired is live, and nothing about it changes.
func TestLoadWithoutRetired(t *testing.T) {
	cfg, err := loadWith(t, "[project]\nname = \"queueify\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.IsRetired() {
		t.Error("IsRetired = true on a manifest that says nothing about retirement")
	}
	if cfg.Project.Retired != nil {
		t.Errorf("Retired = %+v, want nil", cfg.Project.Retired)
	}
}

// Every way of half-writing it must fail at load. A tolerated malformed entry is
// a project that believes it is retired while still paying for every nightly job
// it has — the exact failure the manifest is supposed to record.
func TestLoadRejectsMalformedRetired(t *testing.T) {
	cases := []struct {
		name, body, wantErr string
	}{
		{
			"bare true",
			"[project]\nname = \"x\"\nretired = true\n",
			"must be a table",
		},
		{
			"bare string",
			"[project]\nname = \"x\"\nretired = \"2026-08-18\"\n",
			"must be a table",
		},
		{
			"no reason",
			"[project]\nname = \"x\"\nretired = { since = \"2026-08-18\" }\n",
			"non-empty reason",
		},
		{
			"blank reason",
			"[project]\nname = \"x\"\nretired = { since = \"2026-08-18\", reason = \"   \" }\n",
			"non-empty reason",
		},
		{
			"no since",
			"[project]\nname = \"x\"\nretired = { reason = \"not viable\" }\n",
			"since date",
		},
		{
			"unparseable since",
			"[project]\nname = \"x\"\nretired = { since = \"August 2026\", reason = \"not viable\" }\n",
			"invalid since",
		},
		{
			"since is not a string",
			"[project]\nname = \"x\"\nretired = { since = 2026, reason = \"not viable\" }\n",
			"since must be a string",
		},
		{
			// Retirement has no expiry. Silently dropping the key would leave the
			// author believing the project un-retires itself on that date.
			"until",
			"[project]\nname = \"x\"\nretired = { since = \"2026-08-18\", reason = \"x\", until = \"2027-01-01\" }\n",
			"unknown [project].retired key",
		},
		{
			"typo'd key",
			"[project]\nname = \"x\"\nretired = { since = \"2026-08-18\", resaon = \"x\" }\n",
			"unknown [project].retired key",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadWith(t, c.body)
			if err == nil {
				t.Fatalf("Load accepted a malformed retirement:\n%s", c.body)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, c.wantErr)
			}
		})
	}
}
