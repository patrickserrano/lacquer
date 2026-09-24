// Package plugindefstest provides a fake `claude` binary for tests, so nothing
// that shells out to the CLI needs the real one installed.
package plugindefstest

import (
	"os"
	"path/filepath"
	"testing"
)

// FakeClaude installs a shell shim named `claude` and returns its path. It
// stands in for the real CLI so these tests need no network, no install and no
// particular version. It is deliberately strict about HOW it is called, because
// each of those is a way the real CLI silently checks nothing:
//
//   - not `plugin validate`            -> exit 64
//   - no --strict (warnings stay exit 0) -> exit 65
//   - no --json                          -> exit 65
//   - wrapper without a manifest author  -> exit 66 (--strict fails on the warning)
//   - wrapper with no definitions        -> exit 67 (the real CLI passes "nothing")
//
// A definition containing BROKEN-BY-CLI is reported as a warning for that file,
// which under --strict is a failure: that is the real CLI's shape for a missing
// frontmatter block.
func FakeClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "9.9.9 (Claude Code)"; exit 0; fi
[ "$1" = plugin ] && [ "$2" = validate ] || exit 64
strict=0; json=0
for a in "$@"; do
  case "$a" in --strict) strict=1;; --json) json=1;; esac
  dir="$a"
done
[ "$strict" = 1 ] && [ "$json" = 1 ] || exit 65
grep -q '"author"' "$dir/.claude-plugin/plugin.json" || exit 66
found=$(find "$dir/agents" "$dir/skills" "$dir/commands" -name '*.md' 2>/dev/null | head -1)
[ -n "$found" ] || exit 67
items=""; sep=""
for f in $(grep -rl BROKEN-BY-CLI "$dir/agents" "$dir/skills" "$dir/commands" 2>/dev/null); do
  items="$items$sep{\"file\":\"$f\",\"type\":\"agent\",\"errors\":[],\"warnings\":[{\"path\":\"frontmatter\",\"message\":\"No frontmatter block found.\",\"code\":null}]}"
  sep=","
done
if [ -n "$items" ]; then
  echo "{\"success\":false,\"strict\":true,\"target\":\"$dir\",\"manifest\":null,\"contents\":[$items]}"
  exit 1
fi
echo "{\"success\":true,\"strict\":true,\"target\":\"$dir\",\"manifest\":null,\"contents\":[]}"
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}
