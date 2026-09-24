package shipped

// Guards the split of the marketing skill set out of core/skills and into
// profiles/marketing/skills (lacquer#452). core ships to every project
// regardless of profile, so a marketing skill left there would reach every
// iOS app in the fleet whether or not the project has any marketing surface
// — which is the exact state this split undoes.

import (
	"os"
	"path/filepath"
	"testing"
)

// marketingSkills is the full moved set, named explicitly rather than derived
// from a directory listing at test time — a regression that quietly deletes
// (or re-adds under core) one of these names must still fail even though
// nothing about the directory scan itself changed.
var marketingSkills = []string{
	"ab-testing", "ad-creative", "ads", "ai-seo", "analytics", "aso",
	"attribution", "churn-prevention", "co-marketing", "cold-email",
	"community-marketing", "competitor-profiling", "competitors",
	"content-strategy", "copy-editing", "copywriting", "cro",
	"customer-research", "directory-submissions", "emails", "events",
	"free-tools", "image", "influencer-marketing", "launch", "lead-magnets",
	"marketing-council", "marketing-ideas", "marketing-loops",
	"marketing-plan", "marketing-psychology", "offers", "onboarding",
	"paywalls", "popups", "pricing", "product-marketing",
	"programmatic-seo", "prospecting", "public-relations", "referrals",
	"revops", "sales-enablement", "schema", "seo-audit", "signup",
	"site-architecture", "sms", "social", "video",
}

// TestNoMarketingSkillShippedByCore is the guard the operator asked for
// verbatim: core must never again ship a marketing skill to every project.
// core ships everywhere unconditionally (internal/assets.Plan walks
// core/skills for every component, no profile check) — the marketing profile
// is the only thing that makes these skills opt-in.
func TestNoMarketingSkillShippedByCore(t *testing.T) {
	r := root(t)
	for _, name := range marketingSkills {
		if _, err := os.Stat(filepath.Join(r, "core", "skills", name)); err == nil {
			t.Errorf("core/skills/%s: a marketing skill is shipped by core, so every project gets it regardless of profile", name)
		}
	}
	if _, err := os.Stat(filepath.Join(r, "core", "skills", "LICENSE-upstream-marketingskills")); err == nil {
		t.Error("core/skills/LICENSE-upstream-marketingskills: the marketing skills' upstream license is still under core, but the skills it covers moved to profiles/marketing")
	}
}

// TestMarketingSkillsShippedByProfile is the other half: every skill the
// license and the site catalog claim exist actually ships from
// profiles/marketing/skills, each with a SKILL.md — not just a moved
// directory that lost its content on the way.
func TestMarketingSkillsShippedByProfile(t *testing.T) {
	r := root(t)
	for _, name := range marketingSkills {
		skillMD := filepath.Join(r, "profiles", "marketing", "skills", name, "SKILL.md")
		if _, err := os.Stat(skillMD); err != nil {
			t.Errorf("profiles/marketing/skills/%s/SKILL.md: missing (%v)", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r, "profiles", "marketing", "skills", "LICENSE-upstream-marketingskills")); err != nil {
		t.Error("profiles/marketing/skills/LICENSE-upstream-marketingskills: missing — the marketing skills are vendored under this license")
	}
}

// TestMarketingProfileShips checks the one file that decides whether the
// lacquer considers the profile to exist at all: ProfileShips
// (internal/detect/drift.go) gates purely on profiles/<p>/CLAUDE.<p>.md, and
// that gate is also what a later `lacquer sync` needs to render the profile
// into a component that declares it.
func TestMarketingProfileShips(t *testing.T) {
	r := root(t)
	if _, err := os.Stat(filepath.Join(r, "profiles", "marketing", "CLAUDE.marketing.md")); err != nil {
		t.Fatalf("profiles/marketing/CLAUDE.marketing.md: missing (%v) — ProfileShips would report this profile as not shipped", err)
	}
}
