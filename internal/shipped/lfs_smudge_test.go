package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJobsThatBuildAlsoMaterialiseLFS asserts that every workflow which
// compiles an asset catalog both FETCHES LFS objects and SMUDGES them into the
// working tree.
//
// `lfs: true` on actions/checkout does only the first. On a persistent
// self-hosted runner the files already exist from a previous checkout as their
// pointer stand-ins, so git sees them as unchanged and never runs the smudge
// filter: the objects land in .git/lfs/objects while the tree keeps the
// 130-byte pointers.
//
// Measured on the fleet's own runner rather than reasoned about: a watch app
// icon was a 130-byte pointer in the working tree while its 1,159,749-byte
// object sat in the cache, and `git lfs checkout` in that same workspace
// materialised 56 objects / 63 MB and turned the failing run green with no
// other change.
//
// The symptom is misattributed by design — actool reports the icon set "did not
// have any applicable content" rather than naming a pointer file, which sends
// the reader to the asset catalog instead of the checkout. That is why this gap
// stayed open after being diagnosed once: the `lfs: true` it pairs with already
// carries a comment describing this exact symptom, and that fix was incomplete.
//
// release.yml matters more than ci.yml here, not less. A CI failure is loud and
// stops at actool; the same gap on the release path puts a pointer file where
// an app icon belongs in a build headed for App Review. It was missing there
// entirely, and releases survived only because Actions gives a repository one
// workspace per runner, so the archive inherited a tree some earlier CI run had
// already materialised.
func TestJobsThatBuildAlsoMaterialiseLFS(t *testing.T) {
	for _, file := range []string{"ci.yml", "release.yml"} {
		raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "workflows", file))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)

		// Count DIRECTIVES, not mentions: the comments beside these steps quote
		// both strings, and a substring count folds the prose in with the YAML.
		// (This test caught exactly that in its own first draft.)
		var fetches, smudges int
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if trimmed == "lfs: true" || strings.HasPrefix(trimmed, "lfs: true{{") {
				fetches++
			}
			if strings.HasPrefix(trimmed, "run: git lfs checkout") {
				smudges++
			}
		}
		if fetches == 0 {
			t.Errorf("profiles/ios/workflows/%s compiles asset catalogs but no checkout sets `lfs: true` — "+
				"LFS assets stay as pointer files and actool reports the icon set has no applicable content", file)
			continue
		}
		if smudges < fetches {
			t.Errorf("profiles/ios/workflows/%s has %d checkout(s) with `lfs: true` but only %d "+
				"`git lfs checkout` step(s). `lfs: true` FETCHES objects; it does not check them out over "+
				"pointer files that a reused self-hosted workspace already has. Every LFS checkout needs a "+
				"smudge step after it.", file, fetches, smudges)
		}
	}
}
