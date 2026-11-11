package tokens

import (
	"fmt"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
)

const (
	// IOSCIOwnSourceWarningsBuild and IOSCIOwnSourceWarningsTest are the opt-in
	// step that fails the Build (Release) and Test jobs when xcodebuild printed a
	// Swift `warning:` whose file is inside the repository's own sources. Each
	// renders one step or nothing, and sits at the end of the preceding step's
	// last line so that the empty rendering leaves no blank line: a project that
	// has not set [ci].ios_fail_on_own_source_warnings keeps its workflow byte
	// for byte.
	IOSCIOwnSourceWarningsBuild = "{{IOS_CI_OWN_SOURCE_WARNINGS_BUILD}}"
	IOSCIOwnSourceWarningsTest  = "{{IOS_CI_OWN_SOURCE_WARNINGS_TEST}}"
)

// OwnSourceWarningsStepName is the rendered step's name. Tests find the step by
// it, and so does anyone reading a run.
const OwnSourceWarningsStepName = "Fail on Swift warnings in the repository's own sources"

// CIOwnSourceWarningsBuild is the gate after the Release build, which tees the
// raw xcodebuild output to xcodebuild-release.log.
func CIOwnSourceWarningsBuild(cfg *config.Config) string {
	return ownSourceWarningsStep(cfg, "xcodebuild-release.log")
}

// CIOwnSourceWarningsTest is the gate after the tests, which tee the raw
// xcodebuild output to xcodebuild.log.
func CIOwnSourceWarningsTest(cfg *config.Config) string {
	return ownSourceWarningsStep(cfg, "xcodebuild.log")
}

func ownSourceWarningsStep(cfg *config.Config, log string) string {
	if !cfg.CI.IOSFailOnOwnSourceWarnings {
		return ""
	}
	script := strings.NewReplacer(
		"__LOG__", fmt.Sprintf("%q", log),
		"__ALLOWLIST__", fmt.Sprintf("%q", cfg.CI.IOSOwnSourceWarningsAllowlist()),
	).Replace(ownSourceWarningsScript)

	var b strings.Builder
	b.WriteString("\n\n      - name: " + OwnSourceWarningsStepName + "\n")
	b.WriteString("        # Opt-in ([ci].ios_fail_on_own_source_warnings). Some Swift 6 diagnostics stay\n")
	b.WriteString("        # warnings under SWIFT_TREAT_WARNINGS_AS_ERRORS and -warnings-as-errors, so the\n")
	b.WriteString("        # flags alone cannot keep them out. This reads the raw log the step above\n")
	b.WriteString("        # tee'd, and fails on a Swift warning in a file of THIS repository: not an SPM\n")
	b.WriteString("        # checkout, not DerivedData, not a path outside the checkout. Known warnings go\n")
	b.WriteString("        # in the project's allowlist, one extended regex per line, matched against the\n")
	b.WriteString("        # whole warning line; a missing allowlist is an empty one. It only runs after\n")
	b.WriteString("        # the build or test step passed, so xcodebuild's own exit status still fails\n")
	b.WriteString("        # the job first. A warm DerivedData only reprints files that recompiled.\n")
	b.WriteString("        working-directory: \".\"\n")
	b.WriteString("        run: |\n")
	for _, line := range strings.Split(strings.TrimRight(script, "\n"), "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("          " + line + "\n")
	}
	// The caller's template continues on the next line, so drop the final newline.
	return strings.TrimSuffix(b.String(), "\n")
}

// ownSourceWarningsScript is the body of the gate step. It is POSIX-ish shell
// plus awk and grep -E only, because it runs on a macOS runner (bash 3.2, BSD
// awk and grep) and under GitHub's default `bash -e`, and is tested on both.
//
// The decisions, each with a test in internal/shipped/own_source_warnings_test.go:
//   - A "Swift warning" is a line `<path>.swift:<line>:<col>: warning: ...`.
//     Objective-C, linker and build-setting warnings carry no such path.
//   - "Own" means the path is under the checkout (a relative path is the
//     checkout's by definition) and has no SourcePackages, .build or
//     *DerivedData* directory below it.
//   - The checkout is spelled every way the runner might: $PWD, its physical
//     path, $GITHUB_WORKSPACE and its physical path.
//   - The allowlist is checked pattern by pattern before use: an invalid regex
//     or one that matches an empty line (which would allow every warning) fails
//     the step, whether or not a warning needed it.
//   - A missing or empty log fails. A gate that found nothing to read has not
//     verified anything.
const ownSourceWarningsScript = `# Not -e: GitHub runs this under bash -e, and every failure below is decided
# explicitly so that the step reaches its own verdict.
set +e
set -u
log=__LOG__
allow=__ALLOWLIST__

if [ ! -s "$log" ]; then
  echo "::error::$log is missing or empty, so there is nothing to scan for Swift warnings. The step above writes it; a gate that found no log has not looked at anything."
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# The allowlist: one extended regex per line; blank lines and # comments are
# ignored. A missing file is an empty allowlist.
: > "$work/patterns"
if [ -f "$allow" ]; then
  tr -d '\r' < "$allow" | grep -v -E '^[[:space:]]*(#|$)' > "$work/patterns"
fi
while IFS= read -r re; do
  printf '\n' | grep -E -e "$re" > /dev/null 2> "$work/regex-err"
  rc=$?
  if [ "$rc" -ge 2 ]; then
    echo "::error::$allow holds a line that is not a valid extended regular expression: $re"
    cat "$work/regex-err"
    exit 1
  fi
  if [ "$rc" -eq 0 ]; then
    echo "::error::$allow holds a line that matches an empty string, so it would allow every warning: $re"
    exit 1
  fi
done < "$work/patterns"

# Every spelling of the checkout the log might use.
{
  printf '%s\n' "$PWD"
  (cd "$PWD" && pwd -P)
  if [ -n "${GITHUB_WORKSPACE:-}" ]; then
    printf '%s\n' "$GITHUB_WORKSPACE"
    (cd "$GITHUB_WORKSPACE" 2> /dev/null && pwd -P)
  fi
} | sort -u > "$work/roots"

if ! awk -v ROOTS="$work/roots" -v OWN="$work/own" -v SUMMARY="$work/summary" '
  BEGIN {
    n = 0
    while ((getline r < ROOTS) > 0) { if (r != "") root[++n] = r }
    close(ROOTS)
    printf "" > OWN
  }
  {
    if (!match($0, /\.swift:[0-9]+:[0-9]+: warning: /)) next
    if (seen[$0]++) next
    total++
    path = substr($0, 1, RSTART + 5)
    own = 0
    if (substr(path, 1, 1) != "/") {
      rel = path
      own = 1
    } else {
      for (i = 1; i <= n; i++) {
        pre = root[i] "/"
        if (substr(path, 1, length(pre)) == pre) { rel = substr(path, length(pre) + 1); own = 1; break }
      }
    }
    if (own) {
      sub_path = "/" rel
      if (sub_path ~ /\/(SourcePackages|\.build)\//) own = 0
      else if (sub_path ~ /\/[^\/]*DerivedData[^\/]*\//) own = 0
    }
    if (own) print $0 > OWN
  }
  END { printf "%d\n", total + 0 > SUMMARY }
' "$log"; then
  echo "::error::Could not scan $log for Swift warnings."
  exit 1
fi
read -r total < "$work/summary"
own_n=$(( $(wc -l < "$work/own") ))

: > "$work/allowed"
cat "$work/own" > "$work/offending"
if [ -s "$work/patterns" ] && [ -s "$work/own" ]; then
  grep -E -f "$work/patterns" "$work/own" > "$work/allowed"
  if [ "$?" -ge 2 ]; then echo "::error::Could not apply $allow."; exit 1; fi
  grep -v -E -f "$work/patterns" "$work/own" > "$work/offending"
  if [ "$?" -ge 2 ]; then echo "::error::Could not apply $allow."; exit 1; fi
fi
allowed_n=$(( $(wc -l < "$work/allowed") ))
offending_n=$(( $(wc -l < "$work/offending") ))
other_n=$(( total - own_n ))

echo "Swift warnings in $log: $total unique; $own_n in the repository's own sources ($allowed_n allowlisted by $allow), $other_n in dependencies, DerivedData or outside the checkout (not gated)."

if [ "$offending_n" -gt 0 ]; then
  echo "Swift warnings in the repository's own sources that are not allowlisted:"
  LC_ALL=C sort "$work/offending" | awk '{ print "  " $0 }'
  echo "::error::$offending_n Swift warning(s) in the repository's own sources. Fix them, or add a regex for each accepted one to $allow."
  exit 1
fi
`
