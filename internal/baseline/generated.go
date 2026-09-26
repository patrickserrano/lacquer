package baseline

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// xcodeprojDir returns the .xcodeproj bundle for a path that names either the
// bundle or the project.pbxproj inside it.
func xcodeprojDir(path string) string {
	if filepath.Base(path) == "project.pbxproj" {
		return filepath.Dir(path)
	}
	return path
}

// EnsureXcodeproj makes sure the project.pbxproj for path can be read, and says
// so when it cannot be for a reason that is not the project's fault. It returns
// ("", nil) when ReadXcodeproj will find the file, a non-empty unchecked reason
// when the project cannot be verified in this environment, and an error when it
// should be treated as broken.
//
// The question is whether the pbxproj exists, never whether the .xcodeproj
// DIRECTORY does. A project can legitimately gitignore its XcodeGen-generated
// project while still committing project.xcworkspace/xcshareddata/swiftpm/
// Package.resolved inside it (Dependabot needs that file), so a clean checkout
// has the directory and not the file. Guarding on the directory passed exactly
// that shape and then failed inside ReadXcodeproj: dailybread #554's `lacquer
// audit` exited 1 with "read xcodeproj: open .../project.pbxproj: no such file
// or directory" on a project with nothing wrong.
//
// A gitignored XcodeGen project was first met live on sleevetap, whose own
// `lacquer audit` failed identically to the CI Baseline job it had already been
// fixed in (see ios profile's ci.yml "Generate Xcode project" step). A sibling
// project.yml is the signal that this is that case, not a renamed or mistyped
// path: regenerate here the same way, and only report unchecked -- not a hard
// error -- when this environment has no xcodegen to do that with (this runs from
// GitHub-hosted Linux runners too, via the web/supabase profiles' "No lacquer
// drift" job, which never has Xcode tooling at all). No sibling project.yml
// means this genuinely isn't an XcodeGen project, so the original error is
// returned -- a mistyped xcodeproj path must not read as a pass just because
// this fallback exists. A pbxproj that exists but is broken is not this
// function's business: ReadXcodeproj reports it.
func EnsureXcodeproj(path string) (unchecked string, err error) {
	proj := xcodeprojDir(path)
	pbx := filepath.Join(proj, "project.pbxproj")
	_, statErr := os.Stat(pbx)
	if statErr == nil {
		return "", nil
	}
	if !errors.Is(statErr, fs.ErrNotExist) {
		return "", statErr // present but unreadable: a real project that went unverified
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(proj), "project.yml")); err != nil {
		return "", statErr
	}
	genErr := regenerateXcodeproj(proj)
	if genErr == nil {
		if _, err := os.Stat(pbx); err == nil {
			return "", nil
		}
	}
	reason := "and this environment has no xcodegen to regenerate it for baseline verification"
	if _, lookErr := exec.LookPath("xcodegen"); lookErr == nil {
		reason = fmt.Sprintf("and `xcodegen generate` did not produce it (%v)", genErr)
	}
	return fmt.Sprintf("xcodeproj %q is XcodeGen-generated (project.yml present) and not committed, %s", proj, reason), nil
}

// regenerateXcodeproj runs `xcodegen generate` in the directory that should
// contain path, when that directory has a project.yml and this environment
// has xcodegen on PATH. Returns an error (never fatal to the caller -- see
// Run) when either precondition isn't met, or when generation itself fails.
func regenerateXcodeproj(path string) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(filepath.Join(dir, "project.yml")); err != nil {
		return err // no project.yml here: not an XcodeGen project, nothing to do
	}
	xcodegen, err := exec.LookPath("xcodegen")
	if err != nil {
		return err // this environment cannot generate it (e.g. a Linux runner)
	}
	cmd := exec.Command(xcodegen, "generate")
	cmd.Dir = dir
	return cmd.Run()
}
