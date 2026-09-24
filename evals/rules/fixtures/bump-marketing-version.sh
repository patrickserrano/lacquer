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

# Resolve base configurations before the no-op too: the project-level value can
# already equal the request while an xcconfig still supplies an older version.
# This is deliberately not an Xcode build-settings evaluator. Any referenced
# xcconfig defining the setting is refused (even if a pbxproj override exists),
# and unsupported paths/syntax fail closed. No Xcode invocation or build needed.
verify_hint='Verify the resolved MARKETING_VERSION for each target/configuration with xcodebuild -showBuildSettings -derivedDataPath DerivedData -project <project> -scheme <scheme> -configuration <configuration>, or read CFBundleShortVersionString in the built Info.plist.'
unknown_source() {
  die "refusing: cannot determine the MARKETING_VERSION source: $*. Inspect the referenced xcconfig and change it at its source. $verify_hint"
}

# Tokenize the OpenStep object graph with portable awk, including comments and
# quoted paths. Resolve <group> through parents, not relative to the pbxproj or
# by searching the repository (which can select a stale copy in a worktree).
config_paths=""
if grep -q baseConfigurationReference "$file"; then
  if ! config_paths=$(awk '
    function fail(message) { print message; exit 1 }
    function resolve(id,    tree,prefix) {
      if (visiting[id]++) fail("cyclic group/reference " id)
      if (!(id in kind)) fail("missing file/group reference " id)
      tree=source[id]
      if (tree == "SOURCE_ROOT") prefix=""
      else if (tree == "<absolute>") {
        if (substr(path[id],1,1) != "/") fail("nonabsolute path " path[id])
        prefix=""
      } else if (tree == "<group>") {
        if (id in parent) prefix=resolve(parent[id])
        else if (id != main) fail("unresolved group for " path[id])
      } else fail("unsupported sourceTree for " path[id] ": " tree)
      visiting[id]--
      if (prefix != "" && path[id] != "") prefix=prefix "/"
      return prefix path[id]
    }
    { input=input $0 "\n" }
    END {
      # Lex once so braces/comments inside quoted strings are not structure.
      for (i=1; i<=length(input);) {
        c=substr(input,i,1); pair=substr(input,i,2)
        if (c ~ /[[:space:]]/) { i++; continue }
        if (pair == "//") {
          while (i<=length(input) && substr(input,i,1)!="\n") i++
          continue
        }
        if (pair == "/*") {
          i+=2
          while (i<=length(input) && substr(input,i,2)!="*/") i++
          if (i>length(input)) fail("unterminated project comment")
          i+=2; continue
        }
        value=""
        if (c == "\"") {
          i++
          while (i<=length(input) && substr(input,i,1)!="\"") {
            c=substr(input,i++,1)
            if (c == "\\") fail("escaped project string; inspect baseConfigurationReference")
            value=value c
          }
          if (i>length(input)) fail("unterminated project string")
          i++
        } else if (c ~ /[{}()=;,]/) { value=c; i++ }
        else {
          while (i<=length(input) && substr(input,i,1) !~ /[[:space:]{}()=;,]/)
            value=value substr(input,i++,1)
        }
        token[++n]=value
      }
      for (i=1; i<=n; i++) {
        v=token[i]
        if (v == "{") { depth++; if (depth==3) id=token[i-2]; continue }
        if (v == "}") { depth--; continue }
        if (v ~ /^baseConfigurationReference/ && v != "baseConfigurationReference")
          fail("unsupported xcconfig reference " v)
        if (v == "baseConfigurationReference") {
          if (depth!=3 || token[i+1]!="=" || token[i+3]!=";") fail("unsupported baseConfigurationReference")
          refs[token[i+2]]=1
        }
        if (depth!=3 || token[i+1]!="=") continue
        val=token[i+2]
        if (v=="isa") kind[id]=val
        if (v=="path") path[id]=val
        if (v=="sourceTree") source[id]=val
        if (v=="mainGroup") main=val
        if (v=="projectDirPath" && val!="") fail("nonempty projectDirPath")
        if (v=="children") {
          if (val!="(") fail("unsupported group children")
          for (j=i+3; j<=n && token[j]!=")"; j++) {
            child=token[j]; if (child==",") continue
            if (child in parent) fail("ambiguous group parent for " child)
            parent[child]=id
          }
        }
      }
      if (depth!=0) fail("unbalanced project objects")
      for (ref in refs) {
        if (kind[ref]!="PBXFileReference") fail("unresolved xcconfig reference " ref)
        result=resolve(ref)
        if (result=="") fail("empty xcconfig path for " ref)
        print result
      }
    }
  ' "$file"); then
    unknown_source "$config_paths"
  fi
fi

# Walk both include forms relative to the including file. A missing optional
# include is harmless; a required missing file, variable path, or cycle is not.
config_sources=()
scan_config() {
  local config=$1 stack=$2 depth=${3:-0} directory line include optional records
  case $config in
    *'$'* | *'`'* | *$'\n'* | *'|'*) unknown_source "$config (unsupported path)" ;;
  esac
  [ -f "$config" ] && [ -r "$config" ] || unknown_source "$config (missing or unreadable xcconfig)"
  directory=$(cd "$(dirname "$config")" && pwd -P) || unknown_source "$config"
  config=$directory/$(basename "$config")
  case $stack in *"|$config|"*) unknown_source "$config (include cycle)" ;; esac
  # Bound recursion even for symlink aliases of the same include cycle.
  [ "$depth" -lt 64 ] || unknown_source "$config (include chain too deep)"
  if ! records=$(awk '
    {
      text=$0; clean=""
      while (length(text)) {
        if (comment) {
          end=index(text,"*/"); if (!end) { text=""; break }
          text=substr(text,end+2); comment=0
        } else {
          start=index(text,"/*")
          if (!start) { clean=clean text; break }
          clean=clean substr(text,1,start-1); text=substr(text,start+2); comment=1
        }
      }
      sub(/\/\/.*$/, "", clean)
      sub(/^[[:space:]]+/, "", clean); sub(/[[:space:]]+$/, "", clean)
      if (clean ~ /^MARKETING_VERSION([^A-Za-z0-9_]|$)/) print "version"
      else if (clean ~ /^#include\??[[:space:]]+"[^"\n]+"$/) print clean
      else if (clean ~ /^#/ || clean ~ /\\$/ || clean ~ /^\$/) { print "unsupported: " clean; exit 1 }
    }
    END { if (comment) { print "unterminated comment"; exit 1 } }
  ' "$config"); then
    unknown_source "$config: $records"
  fi
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    if [ "$line" = version ]; then
      config_sources+=("$config")
      continue
    fi
    optional=false
    case $line in '#include?'*) optional=true ;; esac
    include=${line#*\"}; include=${include%\"}
    case $include in *'$'* | *'`'* | *\\*) unknown_source "$config: $include" ;; esac
    case $include in /*) ;; *) include=$directory/$include ;; esac
    if [ "$optional" = true ] && [ ! -e "$include" ]; then continue; fi
    scan_config "$include" "$stack|$config|" "$((depth + 1))"
  done <<< "$records"
}
while IFS= read -r config; do
  [ -n "$config" ] || continue
  case $config in /*) ;; *) config=$(dirname "$dir")/$config ;; esac
  scan_config "$config" ""
done <<< "$config_paths"
if [ "${#config_sources[@]}" -gt 0 ]; then
  die "refusing to edit $rel: a referenced xcconfig sets MARKETING_VERSION. Change it in $(printf '%s\n' "${config_sources[@]}" | sort -u | tr '\n' ' '). $verify_hint"
fi

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
