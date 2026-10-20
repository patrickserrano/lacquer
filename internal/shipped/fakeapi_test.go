package shipped

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

// fakeAPI stands in for the GitHub REST API behind a fake `curl` and a fake
// `gh`, so a shipped workflow step that talks to the API can be EXECUTED against
// fixture responses instead of being asserted on as text. The decisions those
// steps make (reuse a run or not, let a release through or not) live in their
// comparisons, and only running them reaches the comparisons.
//
// A route is keyed "METHOD /path?query". A key ending in `*` matches by prefix,
// for a query the step computes at run time (a `created>=` window). A route can
// carry several bodies, served in order with the last one repeating, which is
// how a run goes from in_progress to completed across polls.
//
// Three statuses are not HTTP: "HANG" execs a long sleep (the caller's bound has
// to kill it), "EXITn" makes the tool exit n without a response, and
// "ERR:<text>" makes gh print <text> on stderr and exit 1 (curl, which has no
// such failure, treats it as 000), so a test can serve the exact message a
// real gh prints for a connection error or an HTTP error. An unrouted request
// answers 404, so a step that asks for something no fixture provided is
// refused, never silently satisfied.
//
// A status may be several, separated by "|": "502|502|204" answers the first
// request 502, the second 502, and the third and every later one 204.
//
// Every request is appended to `calls`, and every `-f key=value` field of a gh
// call to `fields`. A body may contain @@NONCE@@, replaced by the last
// dispatched `inputs[dispatch_id]`, so a fixture can name a run the step itself
// asked for.
type fakeAPI struct {
	t      *testing.T
	dir    string
	routes map[string]*fakeRoute
}

type fakeRoute struct {
	status  string
	bodies  [][]byte
	headers string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"curl", "gh"} {
		if err := os.WriteFile(filepath.Join(dir, "bin", tool), []byte(fakeToolScript), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &fakeAPI{t: t, dir: dir, routes: map[string]*fakeRoute{}}
}

// on routes key to one status and body. A body that is not []byte or string is
// marshalled as JSON.
func (f *fakeAPI) on(key, status string, body any) *fakeAPI {
	f.routes[key] = &fakeRoute{status: status, bodies: [][]byte{f.encode(body)}}
	return f
}

// seq routes key to a sequence of 200 bodies, the last repeating.
func (f *fakeAPI) seq(key string, bodies ...any) *fakeAPI {
	r := &fakeRoute{status: "200"}
	for _, b := range bodies {
		r.bodies = append(r.bodies, f.encode(b))
	}
	f.routes[key] = r
	return f
}

// withHeaders adds raw response header lines to an existing route.
func (f *fakeAPI) withHeaders(key string, lines ...string) *fakeAPI {
	f.routes[key].headers = strings.Join(lines, "\r\n") + "\r\n"
	return f
}

func (f *fakeAPI) encode(body any) []byte {
	switch b := body.(type) {
	case nil:
		return nil
	case []byte:
		return b
	case string:
		return []byte(b)
	}
	data, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	return data
}

// write materialises the routes table the fake tools read.
func (f *fakeAPI) write() {
	f.t.Helper()
	var table strings.Builder
	i := 0
	for key, r := range f.routes {
		prefix := filepath.Join(f.dir, fmt.Sprintf("body%d", i))
		for n, b := range r.bodies {
			if err := os.WriteFile(fmt.Sprintf("%s.%d", prefix, n), b, 0o644); err != nil {
				f.t.Fatal(err)
			}
		}
		hdr := ""
		if r.headers != "" {
			hdr = filepath.Join(f.dir, fmt.Sprintf("headers%d", i))
			if err := os.WriteFile(hdr, []byte(r.headers), 0o644); err != nil {
				f.t.Fatal(err)
			}
		}
		fmt.Fprintf(&table, "%s\t%s\t%s\t%d\t%s\n", key, r.status, prefix, len(r.bodies), hdr)
		i++
	}
	if err := os.WriteFile(filepath.Join(f.dir, "routes.tsv"), []byte(table.String()), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// calls returns every request the step made, in order.
func (f *fakeAPI) calls() []string {
	data, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return strings.Fields(strings.ReplaceAll(string(data), " ", "_"))
}

// fields returns every `-f` field the step sent.
func (f *fakeAPI) fields() string {
	data, _ := os.ReadFile(filepath.Join(f.dir, "fields"))
	return string(data)
}

// stepBound is the longest any faked step may run. Every fail path here uses
// bounds of a few seconds, so reaching it means the step waited on something
// its own bound should have cut off.
const stepBound = 60 * time.Second

// stepResult is one execution of a shipped step under the fake API.
type stepResult struct {
	out     string            // combined stdout and stderr
	failed  bool              // non-zero exit
	outputs map[string]string // what the step wrote to $GITHUB_OUTPUT
	summary string            // what it wrote to $GITHUB_STEP_SUMMARY
	elapsed time.Duration
}

// run executes script with bash -e (GitHub's default shell for a `run:` step)
// in dir, with the fake tools first on PATH and env added to the environment.
func (f *fakeAPI) run(script, dir string, env map[string]string) stepResult {
	f.t.Helper()
	f.write()
	output := filepath.Join(f.dir, "github_output")
	summary := filepath.Join(f.dir, "step_summary")
	for _, p := range []string{output, summary} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	// A hard bound on the step itself, so a wait that never ends fails the
	// test that exercised it, by name, instead of hanging the whole package
	// until go test's own timeout. The step runs in its own process group so
	// a sleeping fake is killed with it.
	ctx, cancel := context.WithTimeout(context.Background(), stepBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-e", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(f.dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_API_DIR="+f.dir,
		"GITHUB_OUTPUT="+output,
		"GITHUB_STEP_SUMMARY="+summary,
		"RUNNER_TEMP="+f.t.TempDir(),
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	start := time.Now()
	b, err := cmd.CombinedOutput()
	res := stepResult{out: string(b), failed: err != nil, outputs: map[string]string{}, elapsed: time.Since(start)}
	if ctx.Err() != nil {
		res.out += fmt.Sprintf("\nfake: the step was still running after %s and was killed", stepBound)
	}
	data, _ := os.ReadFile(output)
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			res.outputs[k] = v
		}
	}
	s, _ := os.ReadFile(summary)
	res.summary = string(s)
	return res
}

// zipped returns a zip archive holding one file, the shape the artifacts API
// serves an uploaded artifact in.
func zipped(t *testing.T, name string, content any) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	data, ok := content.([]byte)
	if !ok {
		if data, err = json.Marshal(content); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// commitRepo makes a git repository with one commit and returns its directory,
// the commit SHA and the commit's tree SHA.
func commitRepo(t *testing.T) (dir, sha, tree string) {
	t.Helper()
	dir = t.TempDir()
	gittest.Init(t, dir, "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("add", "README.md")
	git("commit", "-q", "-m", "init")
	// origin/main, so the release gate's ancestry test has a branch to answer
	// against.
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	return dir, git("rev-parse", "HEAD"), git("rev-parse", "HEAD^{tree}")
}

// fakeToolScript is both the fake `curl` and the fake `gh`; it tells which it
// is from its own name.
const fakeToolScript = `#!/usr/bin/env bash
tool=$(basename "$0")
method=GET out="" hdr="" fmt="" target=""
fields=()
if [ "$tool" = gh ]; then
  [ "${1:-}" = api ] || { echo "fake gh: only 'gh api' is faked (got: $*)" >&2; exit 2; }
  shift
fi
while [ $# -gt 0 ]; do
  case "$1" in
    -X|--method) method=$2; shift 2 ;;
    -o) out=$2; shift 2 ;;
    -D) hdr=$2; shift 2 ;;
    -w) fmt=$2; shift 2 ;;
    -f|-F|--field|--raw-field) fields+=("$2"); shift 2 ;;
    -H|--header|--max-time|--connect-timeout|--retry) shift 2 ;;
    -*) shift ;;
    *) target=$1; shift ;;
  esac
done
path="$target"
case "$path" in
  http://*|https://*) path="/${path#*://*/}" ;;
esac
case "$path" in /*) ;; *) path="/$path" ;; esac
key="$method $path"
echo "$key" >> "$FAKE_API_DIR/calls"
for f in "${fields[@]+"${fields[@]}"}"; do
  echo "$f" >> "$FAKE_API_DIR/fields"
  case "$f" in "inputs[dispatch_id]="*) printf '%s' "${f#*=}" > "$FAKE_API_DIR/nonce" ;; esac
done
line=""
while IFS= read -r row; do
  k=${row%%$'\t'*}
  case "$k" in
    *'*') [ "${key#"${k%\*}"}" != "$key" ] && { line=$row; break; } ;;
    *) [ "$k" = "$key" ] && { line=$row; break; } ;;
  esac
done < "$FAKE_API_DIR/routes.tsv"
status=404 body=/dev/null headers=""
if [ -n "$line" ]; then
  IFS=$'\t' read -r _ status prefix count headers <<< "$line"
  counter="$prefix.served"
  n=$(cat "$counter" 2>/dev/null || echo 0)
  echo $((n + 1)) > "$counter"
  IFS='|' read -r -a statuses <<< "$status"
  si=$n
  [ "$si" -lt "${#statuses[@]}" ] || si=$((${#statuses[@]} - 1))
  status=${statuses[$si]}
  [ "$n" -ge "$count" ] && n=$((count - 1))
  body="$prefix.$n"
fi
case "$status" in
  HANG) exec sleep 60 ;;
  EXIT*) exit "${status#EXIT}" ;;
  ERR:*) [ "$tool" = gh ] && { printf '%s\n' "${status#ERR:}" >&2; exit 1; }; status=000 ;;
esac
render() {
  if [ -f "$FAKE_API_DIR/nonce" ] && LC_ALL=C grep -q '@@NONCE@@' "$body"; then
    LC_ALL=C sed "s/@@NONCE@@/$(cat "$FAKE_API_DIR/nonce")/g" "$body"
  else
    cat "$body"
  fi
}
if [ "$tool" = curl ]; then
  if [ -n "$out" ]; then render > "$out"; else render; fi
  if [ -n "$hdr" ]; then
    { printf 'HTTP/2 %s\r\n' "$status"; [ -n "$headers" ] && cat "$headers"; printf '\r\n'; } > "$hdr"
  fi
  [ -n "$fmt" ] && printf '%s' "$status"
  exit 0
fi
case "$status" in
  2??) render; exit 0 ;;
  *) render >&2; echo "gh: HTTP $status" >&2; exit 1 ;;
esac
`
