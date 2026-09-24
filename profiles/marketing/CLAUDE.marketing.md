# Marketing profile rules

Synced into the `CLAUDE.md` of any component declaring the `marketing` profile.
Unlike `ios`, `web`, and `supabase`, `marketing` is never auto-detected — there is
no marketing "stack" on disk to find. Add it to a component's `profiles` in
`.lacquer.toml` deliberately, when marketing/growth work is actually in scope for
that component (an agency, a growth team, a founder running their own GTM).

This profile ships skills only: no CI workflow, no git hooks, no lint config, no
`doctor.toml`/`baseline.toml`/`fix.toml`. There is nothing here to build, lint, or
test, so it adds no gate and no CI job — just a shelf of skills Claude reaches for
by description match when a marketing task comes up.

## What's in here

~50 skills covering the marketing/growth surface: paid ads and ad creative,
SEO (technical, AI-search, programmatic, schema), copywriting and copy editing,
conversion funnels (signup, onboarding, paywalls, popups, CRO), lifecycle
(email, SMS), growth channels (social, PR, influencer, community, referrals,
events, co-marketing), pricing/offers, and planning (marketing-plan,
marketing-ideas, marketing-council, customer-research, competitor research).
Each skill's own `SKILL.md` frontmatter describes exactly when to reach for it —
that routing lives in the skill, not here.

Vendored from an upstream marketing-skills package (Corey Haines); see
`skills/LICENSE-upstream-marketingskills`.

## Where to start

If this is the first marketing skill invoked in a project, start with
**product-marketing**: it builds `.agents/product-marketing.md`, the positioning
and audience context every other marketing skill in this profile reads before
doing its own work. Skipping it means every skill re-derives (or guesses at)
the same context independently.

For an open-ended "what should we do" question, **marketing-council** or
**marketing-ideas** are the entry points; for a specific channel or asset, go
straight to the matching skill (ads, copywriting, seo-audit, and so on) — each
one's frontmatter cross-references the adjacent skills it hands off to.
