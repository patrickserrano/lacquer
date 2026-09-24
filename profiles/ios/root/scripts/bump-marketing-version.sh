#!/usr/bin/env bash
# Bump MARKETING_VERSION in an Xcode project, and prove nothing else moved.
#
#   bump-marketing-version.sh <new-version> [path/to/project.pbxproj | path/to/X.xcodeproj]
#
# This is the sanctioned way to change a version in a project.pbxproj. The
# PreToolUse hook blocks Edit/Write on Xcode project files, and its message
# points here. Opening Xcode to bump a version is the wrong tool: it rewrites
# unrelated project lines on save (lacquer#410), and that noise is what a
# reviewer then has to read around.
#
# It replaces every `MARKETING_VERSION = <old>;` line and then checks the RESULT,
# not its own command: `git diff` must touch only the project file, every changed
# line must be a MARKETING_VERSION line, and the number of lines it replaced must
# match. On any failure the file goes back to what it was and the exit is
# non-zero. Zero replaced lines is a failure too — a script that "succeeds" by
# doing nothing reads exactly like a bump that worked.
#
# Refuses if the project file has uncommitted changes, because afterwards its own
# edit could not be told apart from yours. Running it with the version the
# project already has is a no-op that says so.
#
# Every MARKETING_VERSION line is set to the one value. A project whose targets
# deliberately carry different versions (an app and an extension) has them
# unified; the report names each old value it replaced, so that is visible.
#
# Without a path argument, it finds the one tracked project.pbxproj under the
# current directory (run it from the component root). Two candidates is an error,
# not a guess: pass the path.
#
# Refuses generator specs in the project ancestry and gitignored project files.
# Change MARKETING_VERSION in the generator spec or the xcconfig it references,
# regenerate, and verify the resolved build setting; a pbxproj diff is not proof
# that a generated project will build with the requested version (#445).
set -euo pipefail

# git exports these to hooks in a linked worktree; with them set, git answers for
# the wrong directory. Cleared, it rediscovers the repository from the file.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE

die() {
  echo "bump-marketing-version: $*" >&2
  exit 1
}

[ "$#" -ge 1 ] && [ "$#" -le 2 ] || die "usage: bump-marketing-version.sh <new-version> [project.pbxproj]"

new=$1
# Digits and dots only. The value is written into the project file through sed, so
# anything else is refused rather than escaped.
[[ $new =~ ^[0-9]+(\.[0-9]+){0,3}$ ]] || die "'$new' is not a plain version (digits and dots, e.g. 1.0.1)"

# Check the selected project's ancestry, not just the caller's working directory:
# an explicit path or a generator with a nested output directory must not bypass
# the refusal. Stop at the repository boundary so unrelated parent specs do not
# classify this project as generated.
refuse_generator_spec() {
  local current boundary spec
  current=$(cd "$1" && pwd -P)
  boundary=$(git -C "$current" rev-parse --show-toplevel) || die "$current is not inside a git repository"
  while :; do
    for spec in project.yml project.yaml; do
      if [ -f "$current/$spec" ]; then
        die "refusing to edit a generated project: $current/$spec exists. Change MARKETING_VERSION in that spec or the xcconfig it references, run xcodegen generate with that spec, and verify the resolved MARKETING_VERSION for each release target/configuration."
      fi
    done
    [ "$current" != "$boundary" ] && [ "$current" != / ] || break
    current=$(dirname "$current")
  done
}

# --- find the project file ---------------------------------------------------
if [ "$#" -eq 2 ]; then
  file=$2
  [ -d "$file" ] && file=$file/project.pbxproj
  [ -f "$file" ] || die "no such project file: $2"
else
  refuse_generator_spec "$PWD"
  candidates=()
  while IFS= read -r line; do
    [ -n "$line" ] && candidates+=("$line")
  done < <(git ls-files -- '*.xcodeproj/project.pbxproj')
  [ "${#candidates[@]}" -gt 0 ] || die "no tracked *.xcodeproj/project.pbxproj under $PWD; run from the component root or pass the path. For generated/gitignored projects, change MARKETING_VERSION in the generator spec or referenced xcconfig and regenerate instead"
  if [ "${#candidates[@]}" -gt 1 ]; then
    printf 'bump-marketing-version: %d project files found; pass the one to bump:\n' "${#candidates[@]}" >&2
    printf '  %s\n' "${candidates[@]}" >&2
    exit 1
  fi
  file=${candidates[0]}
fi

dir=$(cd "$(dirname "$file")" && pwd -P)
name=$(basename "$file")
file=$dir/$name

repo=$(git -C "$dir" rev-parse --show-toplevel) || die "$file is not inside a git repository, so the result cannot be verified"
refuse_generator_spec "$(dirname "$dir")"
# --no-index also catches a generated file that was force-added to git.
if git -C "$repo" check-ignore --no-index -q -- "$file"; then
  die "refusing to edit $file: the project file is gitignored and may be generated. Change MARKETING_VERSION in the generator spec or referenced xcconfig, regenerate, and verify the resolved build setting."
else
  ignored_status=$?
  [ "$ignored_status" -eq 1 ] || die "could not check whether $file is gitignored"
fi
rel=$(git -C "$repo" ls-files --full-name -- "$file")
[ -n "$rel" ] || die "$file is not tracked by git, so the result cannot be verified"

git_() { git -C "$repo" "$@"; }

# --- what is there now --------------------------------------------------------
setting='^[[:space:]]*MARKETING_VERSION = [^;]*;[[:space:]]*$'
olds=$(grep -E "$setting" "$file" | sed -E 's/^[[:space:]]*MARKETING_VERSION = ([^;]*);.*$/\1/' || true)
[ -n "$olds" ] || die "$rel has no MARKETING_VERSION = <value>; lines; nothing to bump"
total=$(printf '%s\n' "$olds" | wc -l | tr -d ' ')
if printf '%s\n' "$olds" | grep -q '[^0-9.]'; then
  die "$rel has a MARKETING_VERSION that is not a plain version ($(printf '%s\n' "$olds" | grep '[^0-9.]' | sort -u | tr '\n' ' ')); refusing to overwrite it"
fi

if [ "$(printf '%s\n' "$olds" | grep -cvxF "$new" || true)" -eq 0 ]; then
  echo "MARKETING_VERSION is already $new ($total lines in $rel); nothing to do"
  exit 0
fi

# The no-op above is safe on a dirty file (it writes nothing), so it comes first:
# running the same bump twice says "already", not "uncommitted changes".
# --- refuse on uncommitted changes to the file --------------------------------
if [ -n "$(git_ status --porcelain -- "$rel")" ]; then
  die "$rel has uncommitted changes (staged or not); commit or discard them first so the diff is only this bump"
fi

# --- replace, then verify the result ------------------------------------------
backup=$(mktemp "${TMPDIR:-/tmp}/bump-marketing-version.XXXXXX")
work=$(mktemp "${TMPDIR:-/tmp}/bump-marketing-version.XXXXXX")
cleanup() { rm -f "$backup" "$work"; }
trap cleanup EXIT
cat "$file" >"$backup"

restore() {
  cat "$backup" >"$file"
  echo "bump-marketing-version: $* — $rel restored, nothing changed" >&2
  exit 1
}

# Anchored to the start of the line so a comment or string that merely mentions
# the setting is left alone. Written back through cat, not mv, to keep the file's
# mode and inode.
sed -E "s/^([[:space:]]*MARKETING_VERSION = )[^;]*;/\\1$new;/" "$file" >"$work" || restore "sed failed"
cat "$work" >"$file"

changed=$(printf '%s\n' "$olds" | grep -cvxF "$new" || true)

# 1. Only the project file may differ from HEAD.
others=$(git_ diff HEAD --name-only | grep -vxF "$rel" || true)
[ -z "$others" ] || restore "the diff also touches: $(echo "$others" | tr '\n' ' ')"

# 2. Every changed line is a MARKETING_VERSION line; and the counts match.
removed=0
added=0
while IFS= read -r line; do
  case $line in
    '+++'* | '---'*) continue ;;
    -*) removed=$((removed + 1)) ;;
    +*) added=$((added + 1)) ;;
    *) continue ;;
  esac
  [[ $line =~ ^[-+][[:space:]]*MARKETING_VERSION\ =\  ]] || restore "a changed line is not a MARKETING_VERSION line: $line"
done < <(git_ diff HEAD -U0 --no-color -- "$rel")

[ "$removed" -eq "$changed" ] && [ "$added" -eq "$changed" ] ||
  restore "expected $changed MARKETING_VERSION lines to change but the diff has $removed removed and $added added"

now=$(grep -cE "^[[:space:]]*MARKETING_VERSION = $new;[[:space:]]*\$" "$file" || true)
[ "$now" -eq "$total" ] || restore "expected $total lines at $new afterwards, found $now"

# --- report --------------------------------------------------------------------
printf '%s\n' "$olds" | grep -vxF "$new" | sort | uniq -c | while read -r n old; do
  printf 'MARKETING_VERSION %s → %s (%s line%s)\n' "$old" "$new" "$n" "$([ "$n" -eq 1 ] || echo s)"
done
echo "verified: the diff touches only $rel, and every changed line is a MARKETING_VERSION line ($changed changed of $total)"
