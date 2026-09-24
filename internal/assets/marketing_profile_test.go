package assets

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// TestPlanNeverShipsAMarketingSkillWithoutTheProfile plans against the real
// repo's own content (realLacquer, defined in retired_test.go) for a component
// that declares "ios" but not "marketing", and asserts the render contains no
// marketing skill. core/skills is walked unconditionally for every component
// (see Plan below), so the only thing that has ever kept a marketing skill out
// of a plain iOS project is that it isn't there — this pins that.
func TestPlanNeverShipsAMarketingSkillWithoutTheProfile(t *testing.T) {
	cfg := &config.Config{
		Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
	}
	plan, err := Plan(realLacquer(t), cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, a := range plan {
		rest, ok := strings.CutPrefix(filepath.ToSlash(a.Dest), ".claude/skills/")
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(rest, "/")
		if marketingSkillSet[name] {
			t.Errorf("component declaring only \"ios\" got marketing skill %q at %s", name, a.Dest)
		}
	}
}

// TestPlanShipsMarketingSkillsToADeclaringComponent is the positive case: a
// component that DOES declare "marketing" gets the skills, proving the profile
// actually wires up rather than merely being absent from core.
func TestPlanShipsMarketingSkillsToADeclaringComponent(t *testing.T) {
	cfg := &config.Config{
		Components: []config.Component{{Path: ".", Profiles: []string{"marketing"}}},
	}
	plan, err := Plan(realLacquer(t), cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	dests := map[string]bool{}
	for _, a := range plan {
		dests[filepath.ToSlash(a.Dest)] = true
	}
	for name := range marketingSkillSet {
		if !dests[filepath.Join(".claude", "skills", name, "SKILL.md")] {
			t.Errorf("component declaring \"marketing\" is missing skill %q", name)
		}
	}
}

var marketingSkillSet = func() map[string]bool {
	names := []string{
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
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}()
