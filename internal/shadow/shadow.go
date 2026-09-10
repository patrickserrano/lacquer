// Package shadow finds project-owned workflows that release a build alongside
// the lacquer's own release workflow.
//
// The failure it exists for: dailybread carried BOTH the managed
// `ios-release.yml` and a project-owned `testflight.yml`, and the project-owned
// one is what cut builds 313 through 317. Every hardening on the managed path
// was bypassed on the path that actually shipped: no provenance gate, no
// keychain restore after `security unlock-keychain` on a developer's login
// keychain, and an unpinned `pipx install codemagic-cli-tools --force`.
//
// `audit` could not see it. testflight.yml is not a managed file that drifted,
// and it is not an undeclared STACK either. It is an undeclared parallel
// IMPLEMENTATION of something the lacquer already ships — structurally the same
// gap as a whole toolchain nobody reports, one level down.
//
// This is expected to be common rather than exotic: a project hand-rolls
// TestFlight before adopting the lacquer, the lacquer arrives with its own
// release workflow, and nobody deletes the original because it works.
package shadow

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// releaseVerbs are the operations that mean a workflow ships a build.
//
// Detected on CONTENT, not filename. A shadow release path is named whatever
// its author called it — `testflight.yml` here, but `deploy.yml` or `ship.yml`
// elsewhere — and a filename allowlist would miss precisely the ones nobody
// thought to enrol. Same reasoning the retirement rule uses when it derives
// scheduled workflows from their `on:` block instead of a list.
var releaseVerbs = []string{
	"xcodebuild archive",
	"-exportArchive",
	"app-store-connect publish",
	"altool --upload-app",
	"xcrun altool",
	"fastlane pilot",
	"fastlane deliver",
}

// Managed names the lacquer's own release workflows. A project's copy of one of
// these is the hardened path, not a shadow of it.
var Managed = []string{"ios-release.yml", "release.yml", "web-release.yml"}

// Finding is one project-owned workflow that releases a build while a managed
// release workflow is also present.
type Finding struct {
	// Workflow is the repo-relative path of the shadow.
	Workflow string
	// Verbs are the release operations found in it, in file order.
	Verbs []string
	// Managed is the managed release workflow it runs alongside.
	Managed string
	// Risks are hardening steps the managed path has and this one lacks. Not
	// exhaustive and not a gate — enough to show what is actually being skipped,
	// because "you have two release paths" is abstract and "this one never
	// restores your login keychain" is not.
	Risks []string
}

// riskProbe is one hardening step worth naming when a shadow omits it.
type riskProbe struct {
	// need is a string the managed path contains.
	need string
	// say is what its absence costs.
	say string
	// only fires the probe when the shadow contains this (empty = always).
	only string
}

// Every `need` below was checked to exist in a rendered managed workflow. The
// first draft keyed two probes on `write-release-config.sh` and
// `security default-keychain`, neither of which appears anywhere in the profile
// — dead probes that could never fire, on a check whose whole job is noticing
// what is missing.
var riskProbes = []riskProbe{
	{need: "verify-ci-provenance", say: "no provenance gate — it can ship a commit CI never passed"},
	// The managed path records the keychain's existing settings and puts them
	// back, so it calls set-keychain-settings TWICE: once to widen the timeout,
	// once to restore. A shadow that unlocks and never restores leaves a
	// permanent change to a keychain the job does not own — on the dedicated
	// runner that is a developer's login keychain.
	{need: "set-keychain-settings", say: "unlocks the login keychain and never restores its settings", only: "unlock-keychain"},
	{need: "codemagic-cli-tools==", say: "installs codemagic-cli-tools unpinned", only: "pipx install codemagic-cli-tools"},
	// Rendered from [[product]].secrets. Absent from both files in a project
	// that declares none, which is why this probe is gated on the managed path
	// having it rather than asserted unconditionally.
	{need: "Write release configuration", say: "never writes the release configuration — the build can archive with every runtime key undefined"},
}

// Check returns every project-owned workflow that releases a build while a
// managed release workflow is present.
//
// The second condition is what keeps this honest. windsock ships a
// project-owned `macos-release.yml` and EXCLUDES `ios-release.yml`, with a
// recorded reason: it is a macOS-only app and the generic iOS archive workflow
// does not apply. That is a declared replacement, not a shadow, and flagging it
// would teach people the check cries wolf. An exclusion is already how this
// tool records "I have deliberately replaced this" — so a project that means it
// says so there, and this check stays quiet.
func Check(projectRoot string) []Finding {
	dir := filepath.Join(projectRoot, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	bodies := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		bodies[name] = string(b)
	}

	managedName, managedBody := "", ""
	for _, m := range Managed {
		if body, ok := bodies[m]; ok {
			managedName, managedBody = m, body
			break
		}
	}
	if managedName == "" {
		// No managed release path here — either the project does not release, or
		// it excluded the lacquer's workflow deliberately. Either way there is no
		// hardened path being bypassed, so there is nothing to report.
		return nil
	}

	var out []Finding
	for name, body := range bodies {
		if name == managedName {
			continue
		}
		var verbs []string
		for _, v := range releaseVerbs {
			if strings.Contains(body, v) {
				verbs = append(verbs, v)
			}
		}
		if len(verbs) == 0 {
			continue
		}
		f := Finding{
			Workflow: filepath.ToSlash(filepath.Join(".github", "workflows", name)),
			Verbs:    verbs,
			Managed:  filepath.ToSlash(filepath.Join(".github", "workflows", managedName)),
		}
		for _, p := range riskProbes {
			if !strings.Contains(managedBody, p.need) {
				continue // the managed path does not do this either
			}
			if strings.Contains(body, p.need) {
				continue // the shadow does it too
			}
			if p.only != "" && !strings.Contains(body, p.only) {
				continue // not applicable to this shadow
			}
			f.Risks = append(f.Risks, p.say)
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Workflow < out[j].Workflow })
	return out
}

// Format renders the report, or "" when there is nothing to say.
//
// Reports, does not gate. A second release path is not necessarily wrong — it
// may be the older one nobody has deleted, or a deliberate variant — and
// failing a build over it would be gating on something that endangers nothing
// today. What it always is, is a path where the hardening on the managed
// workflow does not apply.
func Format(fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nanother workflow releases builds alongside the lacquer's:\n")
	for _, f := range fs {
		b.WriteString("  " + f.Workflow + " — " + strings.Join(f.Verbs, ", ") + "\n")
		b.WriteString("    runs alongside " + f.Managed + ", which the lacquer hardens.\n")
		for _, r := range f.Risks {
			b.WriteString("    MISSING: " + r + "\n")
		}
	}
	b.WriteString("Whichever of these actually ships your builds is the one that matters, and it is not\n" +
		"necessarily the managed one. Every gate the lacquer adds to its release workflow —\n" +
		"provenance, secret shape checks, keychain restore, pinned tooling — applies only to that\n" +
		"file. A second path inherits none of it and drifts further with every release the\n" +
		"lacquer hardens.\n" +
		"Delete it, or fold its behaviour into the managed workflow. If it is a DELIBERATE\n" +
		"replacement, exclude the managed workflow in .lacquer.toml with a reason — that is how\n" +
		"this tool records the decision, and it silences this finding.\n")
	return b.String()
}
