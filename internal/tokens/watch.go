package tokens

import (
	"fmt"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
)

// The IOS_CI_WATCH_* family renders the watch-test job, and wires it into the
// "CI OK" gate. Every one of them is EMPTY for a project that declares no
// [product.watch_tests], which is the constraint this file is subordinate to:
// the fleet's iOS repositories must keep receiving a byte-identical ci.yml, and
// TestIOSCISingleProductRenderIsUnchanged pins that by requiring an entry in
// legacyIOSCITokens for every {{IOS_CI_*}} token in the template.
const (
	// IOSCIWatchTestJob is the whole `watch-test:` job, or nothing.
	//
	// A separate job rather than another leg of `test:`, and that is a decision
	// about blast radius rather than taste. The test job's Setup Simulator step
	// pins an iOS runtime, creates an iPhone and polls for SpringBoard; its Check
	// Coverage step reads xccov for the product's .app; its Suppress
	// crash-reporter and Delete-simulators steps both name the iPhone device it
	// made; and {{IOS_CI_SCHEME}} is shared with the Build (Release) job, so a
	// test-only matrix leg is not expressible without splitting that token too.
	// Making those five steps per-leg conditional means splicing shell branches
	// into text that twelve repositories receive unchanged. Rendering a new job
	// touches none of it.
	IOSCIWatchTestJob = "{{IOS_CI_WATCH_TEST_JOB}}"
	// IOSCIWatchNeed, IOSCIWatchEcho and IOSCIWatchResult add the job to the "CI
	// OK" aggregator's needs[], its log line, and its result loop.
	//
	// All three, not just the first. `needs` alone would make CI OK wait for the
	// watch job and then not read its result — a required check that waits for a
	// job it ignores is worse than not waiting, because it looks like coverage.
	IOSCIWatchNeed   = "{{IOS_CI_WATCH_NEED}}"
	IOSCIWatchEcho   = "{{IOS_CI_WATCH_ECHO}}"
	IOSCIWatchResult = "{{IOS_CI_WATCH_RESULT}}"
)

// watchLeg is one product's watch test bundle, resolved against the platform
// table.
type watchLeg struct {
	product string
	slug    string
	tests   config.WatchTests
	sim     config.SimulatorPlatform
}

// watchLegs is every product that declares a watch bundle, in manifest order.
//
// A product whose platform is not in config.SimulatorPlatforms is DROPPED rather
// than rendered with empty values. config.Load rejects it, so reaching here means
// a Config built in memory that skipped validation — and an empty
// `-destination` is not an error to xcodebuild, it is a destination it chooses
// for you. Dropping the leg renders no job, which is loud; rendering it would
// run the watch suite somewhere unnamed and report the result as this one.
func watchLegs(products []config.Product) []watchLeg {
	var out []watchLeg
	for _, p := range products {
		w := p.WatchTests
		if w == nil || w.Scheme == "" || w.TestTarget == "" {
			continue
		}
		sim, ok := w.Simulator()
		if !ok {
			continue
		}
		out = append(out, watchLeg{product: p.Name, slug: p.Slug(), tests: *w, sim: sim})
	}
	return out
}

// CIWatchNeed adds watch-test to the CI OK gate's needs[].
func CIWatchNeed(products []config.Product) string {
	if len(watchLegs(products)) == 0 {
		return ""
	}
	return ", watch-test"
}

// CIWatchEcho adds the job's result to the gate's one-line log.
func CIWatchEcho(products []config.Product) string {
	if len(watchLegs(products)) == 0 {
		return ""
	}
	return " watch-test=${{ needs.watch-test.result }}"
}

// CIWatchResult adds the job's result to the gate's failure loop.
//
// It renders the line separator too (a trailing backslash on the PREVIOUS line),
// because the token sits at the end of that line: an empty render must leave the
// list exactly as it was, and a token on its own line would leave a blank one.
func CIWatchResult(products []config.Product) string {
	if len(watchLegs(products)) == 0 {
		return ""
	}
	return " \\\n            \"${{ needs.watch-test.result }}\""
}

// CIWatchTestJob renders the watch-test job, or nothing at all.
//
// prefix is the component prefix the surrounding template was rendered with, so
// the paths here agree with the iOS jobs' {{COMPONENT_PREFIX}} ones.
func CIWatchTestJob(cfg *config.Config, prefix string) string {
	legs := watchLegs(cfg.Products())
	if len(legs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(watchJobHeader)
	for _, l := range legs {
		fmt.Fprintf(&b, "\n          - product: %q", l.product)
		fmt.Fprintf(&b, "\n            slug: %q", l.slug)
		fmt.Fprintf(&b, "\n            scheme: %q", l.tests.Scheme)
		fmt.Fprintf(&b, "\n            test_target: %q", l.tests.TestTarget)
		// The simulator facts travel with the leg rather than being baked into
		// the step text, so two products could name different platforms without
		// the job growing a branch. Every value comes from the platform table,
		// never from the manifest — see config.SimulatorPlatform.
		fmt.Fprintf(&b, "\n            device_type: %q", l.sim.DeviceType)
		fmt.Fprintf(&b, "\n            runtime: %q", l.sim.Runtime)
		fmt.Fprintf(&b, "\n            download_platform: %q", l.sim.DownloadPlatform)
		fmt.Fprintf(&b, "\n            ready_service: %q", l.sim.ReadyService)
		fmt.Fprintf(&b, "\n            sim_prefix: %q", l.sim.SimPrefix)
		fmt.Fprintf(&b, "\n            destination_prefix: %q", l.sim.DestinationPrefix)
	}
	body := strings.NewReplacer(
		"@@COMPONENT_PREFIX@@", prefix,
		"@@XCODEPROJ@@", cfg.Project.Xcodeproj,
	).Replace(watchJobBody)
	b.WriteString(body)
	return b.String()
}

// watchJobHeader is everything up to the matrix legs.
//
// The job's `if:` is character-for-character the `test` job's, and deliberately
// so: it is the same-repo guard that keeps fork-PR code off the self-hosted Mac.
// A new job that builds this repository's code with a weaker condition is how
// that guard stops meaning anything.
const watchJobHeader = `

  watch-test:
    name: Watch Tests ${{ matrix.watch.product }}
    # GENERATED from [product.watch_tests] (or its single-product spelling,
    # [project].watch_tests). A project that declares none does not render this
    # job at all, and its ci.yml is byte-identical to the one it already had.
    #
    # It exists because a watchOS suite is not reachable from the Test job under
    # any manifest a project could write. The bundle is a testable of a DIFFERENT
    # scheme, so naming it in extra_test_targets fails hard with 'isn't a member
    # of the specified test plan or scheme'; and the Test job carries exactly one
    # test destination, 'platform=iOS Simulator'. Projects solved that with a
    # hand-written workflow, which the uncovered-target audit then reported as a
    # violation -- the lacquer forcing a workaround and auditing the workaround.
    runs-on: [self-hosted, macOS, ARM64, dedicated]
    needs: [lint, changes]
    # SECURITY: same-repo guard -- fork-PR code must not build/test on the
    # self-hosted runner. Identical to the Test job's condition on purpose.
    # 'needs.changes.outputs.xcodeproj': a pre-code component has nothing to
    # test yet.
    if: (github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository) && needs.changes.outputs.code == 'true' && needs.changes.outputs.xcodeproj == 'true'
    # Above the simulator step (14, including the host-wide boot-lock wait)
    # plus Run Watch Tests (40, including one retry), or this backstop fires
    # first and neither the lock's fallback nor the retry ever runs: 14 + 40
    # + 6 for the steps before them = 60.
    timeout-minutes: 60
    strategy:
      # One product's watch suite failing must not cancel another's: a cancelled
      # sibling reports no result at all, and CI OK reads a cancellation as a
      # failure -- so one real failure would present as every product broken.
      fail-fast: false
      matrix:
        watch:`

// watchJobBody is the env block and every step. @@COMPONENT_PREFIX@@ and
// @@XCODEPROJ@@ stand in for the two project paths; they are substituted here
// rather than left as {{...}} tokens because tokens.Substitute makes one pass in
// map order, so a token introduced BY a substitution may or may not be replaced.
const watchJobBody = `
    env:
      WATCH_PRODUCT: ${{ matrix.watch.product }}
      WATCH_SLUG: ${{ matrix.watch.slug }}
      WATCH_SCHEME: ${{ matrix.watch.scheme }}
      WATCH_TEST_TARGET: ${{ matrix.watch.test_target }}
      WATCH_DEVICE_TYPE: ${{ matrix.watch.device_type }}
      WATCH_RUNTIME: ${{ matrix.watch.runtime }}
      WATCH_DOWNLOAD_PLATFORM: ${{ matrix.watch.download_platform }}
      WATCH_READY_SERVICE: ${{ matrix.watch.ready_service }}
      WATCH_SIM_PREFIX: ${{ matrix.watch.sim_prefix }}
      WATCH_DESTINATION_PREFIX: ${{ matrix.watch.destination_prefix }}
    steps:
      - name: Verify the toolchain
        run: |
          set -uo pipefail
          # The LICENCE check, not the version assertion. An unaccepted Xcode
          # licence after a host upgrade does not report itself as one: the
          # observed first symptom is a missing-file error for a file that is
          # sitting on disk, and every xcodebuild and simctl call below fails
          # without saying why. The version ASSERTION is left to the Test job,
          # which runs in the same workflow on the same host -- duplicating it
          # would turn one toolchain fact into two red jobs.
          if ! xcodebuild -checkFirstLaunchStatus >/dev/null 2>&1; then
            echo "::error::Xcode's first-launch tasks are not complete on this runner -- most often an unaccepted licence after an upgrade. Run: sudo xcodebuild -license accept && sudo xcodebuild -runFirstLaunch"
            exit 1
          fi
          xcodebuild -version 2>/dev/null | tr '\n' ' '
          echo

      - uses: actions/checkout@v7
        with:
          lfs: true

      - name: Materialise LFS assets
        # 'lfs: true' FETCHES the objects; it does not guarantee they are SMUDGED
        # into the working tree. On a persistent self-hosted runner the files are
        # already present as their pointer stand-ins from an earlier checkout, so
        # git sees them as unchanged. actool then reports the icon set "did not
        # have any applicable content" rather than naming a pointer file -- and
        # the case that was measured on this fleet's runner was a WATCH app icon.
        run: git lfs checkout

      - name: Create Secrets.xcconfig (build-time placeholder)
        run: |
          # project.yml may reference a gitignored Secrets.xcconfig as its base
          # config; a clean checkout has no such file and xcodebuild fails before
          # compiling anything. Seed every committed example beside itself.
          #
          # The iOS jobs carry a SECOND clause that also seeds <scheme>/ when the
          # component root holds the only example. It is deliberately not
          # repeated here: it resolves against the iOS scheme's directory, and a
          # watch scheme's folder is somewhere else -- so copying it would put a
          # file where nothing reads it and exit 0, which is worse than not
          # trying.
          find @@COMPONENT_PREFIX@@. -name 'Secrets.xcconfig.example' \
            -not -path '*/DerivedData*' -not -path '*/build/*' -not -path '*/.build/*' \
            -print0 2>/dev/null | while IFS= read -r -d '' ex; do
              target="${ex%.example}"
              [ -f "$target" ] || cp "$ex" "$target"
            done

      - name: Generate Xcode project (XcodeGen)
        run: |
          # Guarded on project.yml exactly as the iOS jobs are. A project that
          # gitignores its .xcodeproj (a real, deliberate choice: the pbxproj is
          # merge-hostile) has none in a clean checkout, and every step below
          # fails before it can open one.
          if [ -f "@@COMPONENT_PREFIX@@project.yml" ]; then
            (cd @@COMPONENT_PREFIX@@. && xcodegen generate)
          fi

      - name: Install xcbeautify
        run: brew list xcbeautify >/dev/null 2>&1 || brew install xcbeautify

      - name: Resolve the watch simulator runtime
        id: runtime
        run: |
          set -uo pipefail
          # A watch runtime is NOT preinstalled on a fresh runner, and
          # -downloadPlatform is a no-op once it is present.
          if ! xcrun simctl list runtimes | grep -q "$WATCH_RUNTIME"; then
            echo "$WATCH_RUNTIME not installed; downloading the $WATCH_DOWNLOAD_PLATFORM platform (this can take several minutes)..."
            xcodebuild -downloadPlatform "$WATCH_DOWNLOAD_PLATFORM" 2>&1 | tail -5 || true
          fi

          RUNTIME="$WATCH_RUNTIME"
          if ! xcrun simctl list runtimes | grep -q "$RUNTIME"; then
            # Same shape as the Test job's iOS fallback, and for the same reason:
            # a hard failure on a missing pin would break every repository on the
            # day Apple ships a major. A warning, because a lagging pin is
            # usually fine and must not be SILENT -- the pairing and readiness
            # behaviour this job depends on were measured on the pinned runtime,
            # not on whatever replaced it.
            FALLBACK=$(xcrun simctl list runtimes | grep -oE 'com\.apple\.CoreSimulator\.SimRuntime\.watchOS[0-9-]*' | sort -V | tail -1)
            if [ -z "$FALLBACK" ]; then
              echo "::error::No watchOS simulator runtimes are installed and -downloadPlatform $WATCH_DOWNLOAD_PLATFORM did not provide one."
              xcrun simctl list runtimes
              exit 1
            fi
            echo "::warning::Pinned runtime $RUNTIME not found, falling back to $FALLBACK. Update the watch platform table in the lacquer."
            RUNTIME="$FALLBACK"
          fi
          echo "runtime=$RUNTIME" >> "$GITHUB_OUTPUT"
          echo "Using watch runtime: $RUNTIME"

      - name: Create an UNPAIRED watch simulator
        id: simulator
        # The cold boot waits for the host-wide boot lock, bounded at 8 minutes
        # (SIM_BOOT_LOCK_WAIT_SECONDS), on top of the Test job's 6-minute boot
        # budget: 6 + 8 = 14. Without a ceiling of its own this step inherited
        # the job's.
        timeout-minutes: 14
        run: |
          set -uo pipefail
          cat >"$RUNNER_TEMP/lacquer-sim.sh" <<'LACQUER_SIM_LIB'
          # lacquer simulator helpers. Written by the step that creates this job's
          # simulator and sourced by every later step that boots it or tests on it. The
          # SAME text ships in the iOS Test job and the watch-test job, and a lacquer test
          # fails if the two copies differ, so the lock path and every constant below are
          # one fact rather than two.

          # ---- One simulator cold boot at a time, per host -----------------------------
          #
          # /tmp, not $HOME or $RUNNER_TEMP: the runners on one Mac run as more than one
          # user, and $RUNNER_TEMP is per runner. /tmp is the one directory every runner
          # on the host shares. The file is opened READ-ONLY, which is all flock(2) needs,
          # so a lock file created by one user still works for every other.
          #
          # lockf(1) is macOS's own flock wrapper; nothing is installed. The lock lives on
          # an open file descriptor (9), so the KERNEL releases it when this job's shell
          # exits for any reason, a cancel or a SIGKILL included. There is no lock file to
          # go stale and nothing that can wedge the fleet behind a dead job.
          SIM_BOOT_LOCK=/tmp/lacquer-simulator-boot.lock
          # Queued boots run one at a time, so a job waits behind every job ahead of it:
          # eight minutes is about four cold boots queued. Past that it proceeds WITHOUT
          # the lock and says so. A slow boot is better than a job that never runs.
          SIM_BOOT_LOCK_WAIT_SECONDS=480
          SIM_BOOT_LOCK_POLL_SECONDS=30
          SIM_BOOT_LOCK_HELD=0

          sim_boot_lock_acquire() {
            local start waited slice rc holder
            SIM_BOOT_LOCK_HELD=0
            if ! command -v lockf >/dev/null 2>&1; then
              echo "::warning::lockf(1) is not on this runner, so this simulator cold-boots WITHOUT the host-wide boot lock ($SIM_BOOT_LOCK)."
              return 0
            fi
            if [ ! -e "$SIM_BOOT_LOCK" ]; then
              ( umask 000; : >>"$SIM_BOOT_LOCK" ) 2>/dev/null || true
            fi
            if ! { exec 9<"$SIM_BOOT_LOCK"; } 2>/dev/null; then
              echo "::warning::Cannot open $SIM_BOOT_LOCK, so this simulator cold-boots WITHOUT the host-wide boot lock."
              return 0
            fi
            start=$(date +%s)
            waited=0
            while :; do
              slice=$((SIM_BOOT_LOCK_WAIT_SECONDS - waited))
              if [ "$slice" -gt "$SIM_BOOT_LOCK_POLL_SECONDS" ]; then
                slice=$SIM_BOOT_LOCK_POLL_SECONDS
              fi
              if [ "$slice" -lt 0 ]; then
                slice=0
              fi
              if lockf -s -t "$slice" 9; then rc=0; else rc=$?; fi
              waited=$(($(date +%s) - start))
              if [ "$rc" -eq 0 ]; then
                SIM_BOOT_LOCK_HELD=1
                echo "Acquired the host-wide simulator boot lock after ${waited}s."
                ( umask 000; echo "${GITHUB_REPOSITORY:-?} run ${GITHUB_RUN_ID:-?} job ${GITHUB_JOB:-?}, since $(date -u +%H:%M:%SZ)" >"$SIM_BOOT_LOCK.owner" ) 2>/dev/null || true
                return 0
              fi
              holder=$(cat "$SIM_BOOT_LOCK.owner" 2>/dev/null || true)
              # 75 is EX_TEMPFAIL, "somebody else holds it". Anything else is lockf
              # itself failing, and waiting longer will not fix that.
              if [ "$rc" -ne 75 ]; then
                echo "::warning::lockf exited $rc on $SIM_BOOT_LOCK, so this simulator cold-boots WITHOUT the host-wide boot lock."
                exec 9<&-
                return 0
              fi
              if [ "$waited" -ge "$SIM_BOOT_LOCK_WAIT_SECONDS" ]; then
                echo "::warning::Waited ${waited}s for the host-wide simulator boot lock (last holder: ${holder:-unknown}) and gave up, so this simulator cold-boots WITHOUT it."
                exec 9<&-
                return 0
              fi
              echo "[boot-lock $(date -u +%H:%M:%S)] waited ${waited}s of ${SIM_BOOT_LOCK_WAIT_SECONDS}s; another simulator is cold-booting on this host (last holder: ${holder:-unknown})"
            done
          }

          sim_boot_lock_release() {
            if [ "$SIM_BOOT_LOCK_HELD" = 1 ]; then
              exec 9<&-
              SIM_BOOT_LOCK_HELD=0
              echo "Released the host-wide simulator boot lock."
            fi
          }

          # sim_boot_and_wait <device-id> <ready-service>
          #
          # Call it as: sim_boot_and_wait ... 9<&-  A child process that inherits the
          # lock's descriptor HOLDS THE LOCK for as long as it lives, and nothing here
          # needs the descriptor, so no process started here inherits it.
          sim_boot_and_wait() {
            local dev=$1 svc=$2 i=1
            xcrun simctl boot "$dev"
            xcrun simctl bootstatus "$dev" -b
            # bootstatus -b means the kernel is up; the shell (SpringBoard on a phone,
            # Carousel on a watch) finishes seconds later.
            echo "Polling for $svc readiness (up to 60s)..."
            while [ "$i" -le 20 ]; do
              if xcrun simctl spawn "$dev" launchctl list 2>/dev/null | grep -qi "$svc"; then
                echo "$svc ready after $((i * 3))s"
                return 0
              fi
              sleep 3
              i=$((i + 1))
            done
            echo "::warning::$svc readiness poll timed out after 60s, proceeding anyway"
          }

          # ---- Running the suite: stall watchdog, diagnostics, one narrow retry --------
          #
          # A STALL is xcodebuild going silent: the tee'd log stops growing. It is
          # measured on the log's size, never on the heartbeat lines this watchdog prints
          # itself. A stall is diagnosed and FAILED, never retried: a retry could hide a
          # real hang in the app under test.
          #
          # MID-SUITE, once the test runner has connected: 3 minutes. Measured over 31
          # green Test jobs in 5 repositories, the longest gap between two test results
          # was 15s, and the longest gap of any kind was 54s (xcodebuild starting up,
          # before the runner connects). rail's two mid-suite stalls went silent for 8
          # minutes and more, until the step timed out.
          SIM_TEST_STALL_SECONDS=180
          # BEFORE the runner connects, silence is allowed for longer, because that is
          # where xcodebuild waits out a runner that never connects and then reports it
          # itself, with the signature the retry below needs. It was measured waiting
          # 421.5s (flare, "The test runner hung before establishing connection").
          # Firing sooner would turn the one retryable failure into a non-retryable stall.
          SIM_TEST_CONNECT_SECONDS=600
          # "The runner connected": the first line either test framework prints once the
          # runner is up. XCTest: "Test Suite 'All tests' started at ...". Swift
          # Testing: "Test run started." Neither appears before a pre-connection failure.
          SIM_TEST_CONNECTED_RE="Test Suite '.*' started|Test run started"
          SIM_TEST_HEARTBEAT_SECONDS=60
          SIM_TEST_DIAG_DIR=simulator-diagnostics
          # Where a simulator app's crash report lands: the HOST's DiagnosticReports of
          # the user running this job, as <App>-<timestamp>.ips. Host-wide, so every
          # other repository's crashes are in here too.
          SIM_TEST_CRASH_DIR="$HOME/Library/Logs/DiagnosticReports"
          # The only failure that retries: the test runner never connected. The exact
          # lines seen on this fleet are "Test crashed with signal trap before
          # establishing connection", "The test runner crashed before establishing
          # connection" and "Early unexpected exit, operation never finished
          # bootstrapping".
          SIM_TEST_PRECONNECT_RE='before establishing connection|never finished bootstrapping'

          # sim_bounded <seconds> <command...>: run it, and SIGKILL it (and its
          # children) if it outlives <seconds>. Diagnostics must never become the hang.
          sim_bounded() {
            local secs=$1 pid rc
            shift
            "$@" &
            pid=$!
            ( i=0
              while kill -0 "$pid" 2>/dev/null; do
                if [ "$i" -ge "$secs" ]; then
                  pkill -9 -P "$pid" 2>/dev/null
                  kill -9 "$pid" 2>/dev/null
                  exit 0
                fi
                sleep 1
                i=$((i + 1))
              done ) >/dev/null 2>&1 &
            if wait "$pid"; then rc=0; else rc=$?; fi
            return "$rc"
          }

          sim_test_regex_escape() {
            printf '%s' "$1" | sed 's/[][\.*^$+?(){}|]/\\&/g'
          }

          sim_test_device_log() {
            xcrun simctl spawn "$SIM_TEST_DEVICE_ID" log show --last 5m --style compact 2>&1 | head -c 20000000
          }

          # THIS run's processes only: anything whose argv carries this workspace's path
          # or this run's device id. Never machine-wide; other repositories' jobs are
          # running tests on this same Mac.
          sim_test_own_pids() {
            local pat=""
            if [ -n "${GITHUB_WORKSPACE:-}" ]; then
              pat=$(sim_test_regex_escape "$GITHUB_WORKSPACE")
            fi
            if [ -n "${SIM_TEST_DEVICE_ID:-}" ]; then
              pat="${pat:+$pat|}$SIM_TEST_DEVICE_ID"
            fi
            if [ -n "$pat" ]; then
              pgrep -f "$pat" || true
            fi
          }

          # Bounded in time (about two minutes at worst) and in size (the device log is
          # capped at 20 MB).
          sim_test_capture_diagnostics() {
            local log=$1 dir=$SIM_TEST_DIAG_DIR pids pid n=0
            mkdir -p "$dir"
            pids=$(sim_test_own_pids)
            {
              echo "No output to $log for ${SIM_TEST_STALL_SECONDS}s or more, at $(date -u)."
              echo "device: ${SIM_TEST_DEVICE_ID:-?}  workspace: ${GITHUB_WORKSPACE:-?}"
              uptime
              echo
              echo "== this run's processes =="
              for pid in $pids; do
                ps -o pid=,ppid=,etime=,%cpu=,stat=,command= -p "$pid" 2>/dev/null || true
              done
            } >"$dir/summary.txt"
            tail -n 400 "$log" >"$dir/xcodebuild-tail.log" 2>/dev/null || true
            for pid in $pids; do
              n=$((n + 1))
              if [ "$n" -gt 6 ]; then
                break
              fi
              sim_bounded 20 sample "$pid" 3 -file "$dir/sample-$pid.txt" >/dev/null 2>&1 || true
            done
            if [ -n "${SIM_TEST_DEVICE_ID:-}" ]; then
              sim_bounded 60 sim_test_device_log >"$dir/device-log.txt" 2>&1 || true
            fi
          }

          # xcodebuild's own argv carries no workspace path (-project is relative), so it
          # is matched by this run's device id in its -destination instead. Then the rest
          # of the pipeline (tee, xcbeautify): the step shell's other children, which is
          # exactly that pipeline and this watchdog. Without that, a stalled pipeline
          # only ends when every process holding its pipes does.
          sim_test_kill_this_run() {
            local self p
            if [ -n "${GITHUB_WORKSPACE:-}" ]; then
              pkill -9 -f "$GITHUB_WORKSPACE" 2>/dev/null || true
            fi
            if [ -n "${SIM_TEST_DEVICE_ID:-}" ]; then
              pkill -9 -f "id=$SIM_TEST_DEVICE_ID" 2>/dev/null || true
            fi
            self=$(exec sh -c 'echo "$PPID"')
            for p in $(pgrep -P "$SIM_TEST_STEP_PID" 2>/dev/null || true); do
              if [ "$p" != "$self" ]; then
                kill -9 "$p" 2>/dev/null || true
              fi
            done
          }

          # Ticks once a second so it notices the end of the run promptly and leaves no
          # stray sleep behind; prints its heartbeat every SIM_TEST_HEARTBEAT_SECONDS.
          # Writes "<log>.stalled" (mid-suite or pre-connect) before it kills anything.
          sim_test_watchdog() {
            local log=$1 size last=0 changed now silent beat=0 connected=0 grepped=-1 kind
            changed=$(date +%s)
            while [ ! -e "$log.done" ]; do
              sleep 1
              size=$(wc -c <"$log" 2>/dev/null | tr -d ' ') || size=0
              now=$(date +%s)
              if [ "${size:-0}" != "$last" ]; then
                last=${size:-0}
                changed=$now
              fi
              silent=$((now - changed))
              beat=$((beat + 1))
              if [ "$beat" -ge "$SIM_TEST_HEARTBEAT_SECONDS" ]; then
                beat=0
                echo "[watchdog $(date -u +%H:%M:%S)] xcodebuild still running (last output ${silent}s ago)"
              fi
              if [ "$silent" -lt "$SIM_TEST_STALL_SECONDS" ]; then
                continue
              fi
              # Read the log once per silence: it cannot change while it is silent.
              if [ "$connected" = 0 ] && [ "$grepped" != "$last" ]; then
                grepped=$last
                if grep -qE "$SIM_TEST_CONNECTED_RE" "$log" 2>/dev/null; then
                  connected=1
                fi
              fi
              kind=""
              if [ "$connected" = 1 ]; then
                kind=mid-suite
              elif [ "$silent" -ge "$SIM_TEST_CONNECT_SECONDS" ]; then
                kind=pre-connect
              fi
              if [ -n "$kind" ]; then
                echo "$kind $silent" >"$log.stalled"
                echo "[watchdog] No xcodebuild output for ${silent}s ($kind). Capturing diagnostics, then stopping this run's test processes."
                sim_test_capture_diagnostics "$log" || true
                sim_test_kill_this_run
                return 0
              fi
            done
          }

          # sim_test_attempt <log> <artifact> <command...>
          sim_test_attempt() {
            local log=$1 artifact=$2 wd rc
            shift 2
            rm -f "$log.stalled" "$log.done"
            : >"$log"
            # The attempt's start, as a file's mtime, for "crash reports written since".
            : >"$log.started"
            sim_test_watchdog "$log" &
            wd=$!
            if "$@" 2>&1 | tee "$log" | xcbeautify --renderer github-actions; then rc=0; else rc=$?; fi
            : >"$log.done"
            wait "$wd" 2>/dev/null || true
            rm -f "$log.done"
            # Checked BEFORE any retry decision, and it exits: a stall never retries.
            if [ -e "$log.stalled" ]; then
              case $(cat "$log.stalled") in
                pre-connect*) echo "::error::no test output for $((SIM_TEST_CONNECT_SECONDS / 60)) min — stalled before the test runner connected; diagnostics uploaded as $artifact" ;;
                *) echo "::error::no test output for $((SIM_TEST_STALL_SECONDS / 60)) min — stalled mid-suite; diagnostics uploaded as $artifact" ;;
              esac
              echo "test_result=stalled" >>"$GITHUB_OUTPUT"
              exit 1
            fi
            SIM_TEST_EXIT_CODE=$rc
          }

          # How many REAL tests ran, from the result bundle: every "Test Case" node except
          # those under the "System Failures" suite. A runner that never connected is
          # recorded as ONE failed test there, named "<App> (<pid>) encountered an error",
          # so totalTestCount reads 1 and the summary's counts cannot tell it apart from
          # one real failure. Measured on flare's three pre-connection failures (0 real
          # test cases each) and two green bundles (911 and 204). Prints nothing when the
          # bundle cannot be read, and nothing never counts as zero.
          sim_test_executed_count() {
            local tree
            tree=$(xcrun xcresulttool get test-results tests --path "$1" 2>/dev/null) || return 0
            printf '%s' "$tree" | jq -r '
              def cases: if .nodeType == "Test Suite" and .name == "System Failures" then empty
                elif .nodeType == "Test Case" then .
                else (.children // [])[] | cases end;
              if (.testNodes | type) == "array" then [.testNodes[] | cases] | length else empty end
            ' 2>/dev/null || true
          }

          # sim_test_should_retry <log> <xcresult>: succeeds only for the pre-connection
          # signature with ZERO tests executed.
          sim_test_should_retry() {
            local sig executed
            sig=$(grep -oE "$SIM_TEST_PRECONNECT_RE" "$1" 2>/dev/null | head -n 1) || true
            if [ -z "$sig" ]; then
              return 1
            fi
            executed=$(sim_test_executed_count "$2")
            if [ "$executed" != "0" ]; then
              echo "xcodebuild reported '$sig', but ${executed:-an unknown number of} tests executed, so this is not the never-connected failure. Not retrying."
              return 1
            fi
            SIM_TEST_RETRY_SIGNATURE=$sig
            return 0
          }

          # sim_test_host_crash <since-file>: the crash report of an app installed on THIS
          # run's device, written during this attempt, or nothing.
          #
          # The same signature with zero tests is ALSO what an app that crashes at launch
          # produces: flare on iOS 27 trapped every time in App.body, evaluated off-main
          # (SIGTRAP on com.apple.SwiftUI.AsyncRenderer in _swift_task_checkIsolatedSwift).
          # Retrying that costs another attempt, fails again, and blames the simulator for
          # an app bug. Matched on this run's DEVICE ID in the report's procPath (which
          # only an app installed on this very device carries), and on the file being
          # newer than the attempt: the directory is host-wide, and other repositories'
          # reports, or this app's own from another run, are in it too.
          sim_test_host_crash() {
            local f path
            if [ ! -d "$SIM_TEST_CRASH_DIR" ]; then
              return 0
            fi
            while IFS= read -r f; do
              path=$(tail -n +2 "$f" 2>/dev/null | jq -r '.procPath // empty' 2>/dev/null) || path=""
              case "$path" in
                */Devices/"$SIM_TEST_DEVICE_ID"/data/Containers/Bundle/Application/*)
                  echo "$f"
                  return 0 ;;
              esac
            done < <(find "$SIM_TEST_CRASH_DIR" -maxdepth 1 -name '*.ips' -newer "$1" 2>/dev/null)
          }

          # One line naming the crash: process, signal and exception, the faulting
          # thread's name or queue, and its top frames.
          sim_test_crash_summary() {
            tail -n +2 "$1" 2>/dev/null | jq -r '
              (.threads[.faultingThread] // {}) as $t
              | "\(.procName // "?") crashed: \(.exception.signal // "?") (\(.exception.type // "?")) on \($t.name // $t.queue // "thread \(.faultingThread)") in \([($t.frames // [])[:6][] | .symbol // empty] | join(" < "))"
            ' 2>/dev/null || basename "$1"
          }

          # sim_test_run <device-id> <ready-service> <log> <xcresult> <artifact> -- <command...>
          #
          # Sets SIM_TEST_EXIT_CODE. Needs 'set -o pipefail' so that xcodebuild's exit
          # code survives the pipe through tee and xcbeautify.
          sim_test_run() {
            local svc=$2 log=$3 xcr=$4 artifact=$5 crash
            SIM_TEST_DEVICE_ID=$1
            SIM_TEST_STEP_PID=$$
            shift 5
            if [ "${1:-}" = "--" ]; then
              shift
            fi
            case ":$SHELLOPTS:" in
              *:pipefail:*) ;;
              *) echo "::error::sim_test_run needs set -o pipefail, or xcodebuild's exit code is lost in the pipe."; exit 1 ;;
            esac
            sim_test_attempt "$log" "$artifact" "$@"
            if [ "$SIM_TEST_EXIT_CODE" -ne 0 ] && sim_test_should_retry "$log" "$xcr"; then
              # ReportCrash writes the report seconds after the crash; give it a moment.
              crash=$(sim_test_host_crash "$log.started")
              if [ -z "$crash" ]; then
                sleep 5
                crash=$(sim_test_host_crash "$log.started")
              fi
              if [ -n "$crash" ]; then
                mkdir -p "$SIM_TEST_DIAG_DIR"
                cp "$crash" "$SIM_TEST_DIAG_DIR/" 2>/dev/null || true
                echo "::error::The app under test crashed at launch, before the test runner connected: $(sim_test_crash_summary "$crash"). That is the app failing, not the simulator failing to start, so it is NOT retried. Crash report uploaded as $artifact."
                return 0
              fi
              echo "::warning::'$SIM_TEST_RETRY_SIGNATURE' with zero tests executed: the test runner never connected, which is the simulator failing to start, not a test failing. Retrying ONCE on this run's freshly erased device $SIM_TEST_DEVICE_ID."
              mv "$log" "${log%.log}-attempt1.log"
              rm -rf "${xcr%.xcresult}-attempt1.xcresult"
              if [ -e "$xcr" ]; then
                mv "$xcr" "${xcr%.xcresult}-attempt1.xcresult"
              fi
              if [ -n "${GITHUB_WORKSPACE:-}" ]; then
                pkill -9 -f "$GITHUB_WORKSPACE" 2>/dev/null || true
              fi
              xcrun simctl shutdown "$SIM_TEST_DEVICE_ID" 2>/dev/null || true
              xcrun simctl erase "$SIM_TEST_DEVICE_ID"
              sim_boot_lock_acquire
              sim_boot_and_wait "$SIM_TEST_DEVICE_ID" "$svc" 9<&-
              sim_boot_lock_release
              # Erasing reset the device's defaults; the crash-reporter dialog stays off.
              xcrun simctl spawn "$SIM_TEST_DEVICE_ID" defaults write com.apple.CrashReporter DialogType none 2>/dev/null || true
              sim_test_attempt "$log" "$artifact" "$@"
            fi
          }
          LACQUER_SIM_LIB
          . "$RUNNER_TEMP/lacquer-sim.sh"
          RUNTIME="${{ steps.runtime.outputs.runtime }}"

          # RUN-SCOPED NAME, and the cleanup at the end of this job deletes only
          # this exact name. The Test job learned this the expensive way: a fixed
          # "CI-iPhone" on a Mac shared by fourteen repositories meant one job's
          # cleanup deleted the device another was mid-test on, which surfaces as
          # "the test runner crashed before establishing connection" and reads
          # like an app bug. GITHUB_RUN_ID is shared by the legs of one run, so
          # the product slug has to be in the name too.
          SIM_NAME="${WATCH_SIM_PREFIX}-${GITHUB_RUN_ID}-${WATCH_SLUG}"

          # This run's leftovers only. The trailing " (" anchors the match to the
          # END of the name in 'simctl list devices' output, where it always
          # follows -- without it a slug that is a prefix of another product's
          # slug matches that product's LIVE device. '|| true' is load-bearing on
          # a clean runner: under 'bash -e' the grep pipeline exits 1 when there
          # is nothing stale, which would kill the step.
          STALE=$(xcrun simctl list devices | grep "$SIM_NAME (" | grep -oE '[A-F0-9-]{36}' || true)
          for id in $STALE; do
            echo "Deleting stale watch simulator: $id"
            xcrun simctl shutdown "$id" 2>/dev/null || true
            xcrun simctl delete "$id" 2>/dev/null || true
          done

          # ONE COLD BOOT AT A TIME ON THIS HOST, shared with the Test job's
          # iPhone boots through the same lock file: several cold boots at once
          # are what pushed the shared Mac to load ~700 and killed a project's
          # tests at bootstrap. Create + boot + the Carousel wait only; the
          # tests still run concurrently. 9<&- keeps the lock's descriptor out
          # of every process started under it.
          sim_boot_lock_acquire
          DEVICE_ID=$(xcrun simctl create "$SIM_NAME" "$WATCH_DEVICE_TYPE" "$RUNTIME" 9<&-)
          echo "device_id=$DEVICE_ID" >> "$GITHUB_OUTPUT"
          echo "Created $SIM_NAME ($DEVICE_ID) on $RUNTIME"

          # THE DEVICE MUST BE UNPAIRED, and this is the non-obvious part of
          # running watch tests at all. A watch simulator PAIRED to a phone
          # activates a real WCSession, so a suite written against the unpaired
          # state exercises a different code path -- silently, and only on CI.
          #
          # Creating a device is what makes this reliable. "Find an existing
          # unpaired watch" is not a strategy: measured on this fleet's runner at
          # watchOS 27.0, three of the five stock watch simulators were already
          # paired to phones, and which three is not a property any job can rely
          # on. A freshly created device appears in 'simctl list devices' and in
          # no entry of 'simctl list pairs' -- measured on the same runner.
          #
          # Asserted rather than trusted, because being wrong here costs a green
          # run over the wrong behaviour. No '|| true': under 'bash -e' a failing
          # 'simctl list pairs' aborts the step, where swallowing it would leave
          # PAIRS empty and an empty pair list reads exactly like "unpaired".
          PAIRS=$(xcrun simctl list pairs 9<&-)
          if printf '%s\n' "$PAIRS" | grep -q "$DEVICE_ID"; then
            echo "::error::$SIM_NAME ($DEVICE_ID) is PAIRED with a phone simulator. A paired watch activates a real WCSession, so this suite would run against different behaviour than it was written for and report that as a pass."
            printf '%s\n' "$PAIRS"
            exit 1
          fi
          echo "Confirmed UNPAIRED: $DEVICE_ID appears in no entry of simctl list pairs."

          # Boot, then poll for the watch shell, which is CAROUSEL and not
          # SpringBoard. Measured on a booted watchOS 27.0 simulator: 'launchctl
          # list' carries com.apple.Carousel and no SpringBoard at all, so the
          # Test job's readiness service copied across would time out and warn
          # on a device that had been ready for forty seconds.
          sim_boot_and_wait "$DEVICE_ID" "$WATCH_READY_SERVICE" 9<&-
          sim_boot_lock_release

      - name: Run Watch Tests
        id: tests
        # The hang-prone step: a simulator test session can stall silently. 15
        # minutes is one attempt's ceiling; the one narrow retry adds a second
        # attempt, up to 8 minutes waiting for the host-wide boot lock and ~2 to
        # erase and boot again: 15 + 8 + 2 + 15 = 40. A stall is caught by the
        # watchdog at SIM_TEST_STALL_SECONDS of silence, long before this.
        timeout-minutes: 40
        run: |
          set -o pipefail
          DEVICE_ID="${{ steps.simulator.outputs.device_id }}"
          . "$RUNNER_TEMP/lacquer-sim.sh"
          rm -rf WatchTestResults.xcresult WatchTestResults-attempt1.xcresult watch-xcodebuild-attempt1.log

          # A SEPARATE derivedDataPath from the iOS jobs. They share one with each
          # other already; adding a third concurrent writer to it on the same
          # runner is not a trade this job needs to make, and a watch scheme's
          # build products have no reason to land in the iOS cache the SPM-cache
          # validation steps inspect.
          #
          # Through the same watchdog as the Test job. A STALL (no new output
          # for SIM_TEST_STALL_SECONDS) captures diagnostics to the artifact
          # named below, kills only this run's processes and FAILS: never a
          # retry, which could hide a real hang. The ONE retry is for the
          # runner never connecting ("... before establishing connection" /
          # "never finished bootstrapping") with ZERO tests executed and no
          # crash report from the app on this device for the attempt; a crash
          # report means the app died at launch, and is reported, not retried.
          sim_test_run "$DEVICE_ID" "$WATCH_READY_SERVICE" watch-xcodebuild.log WatchTestResults.xcresult "watch-test-diagnostics-$WATCH_SLUG" -- \
            xcodebuild test \
            -project "@@XCODEPROJ@@" \
            -scheme "$WATCH_SCHEME" \
            -destination "${WATCH_DESTINATION_PREFIX},id=$DEVICE_ID" \
            -derivedDataPath @@COMPONENT_PREFIX@@WatchDerivedData \
            "-only-testing:$WATCH_TEST_TARGET" \
            -parallel-testing-enabled NO \
            -resultBundlePath WatchTestResults.xcresult \
            CODE_SIGNING_REQUIRED=NO
          EXIT_CODE=$SIM_TEST_EXIT_CODE

          # RECORDED, not acted on. The exit code is evidence for the assertion
          # below, which is this job's only verdict. Exiting here on a non-zero
          # code would skip the step that catches the failure an exit code cannot
          # express: a suite that ran nothing exits 0.
          echo "exit_code=$EXIT_CODE" >> "$GITHUB_OUTPUT"
          echo "xcodebuild exited $EXIT_CODE"

      - name: Assert the watch suite ran and passed
        run: |
          set -uo pipefail
          EXIT_CODE="${{ steps.tests.outputs.exit_code }}"

          # NO '|| true' ANYWHERE IN THIS READ PATH. This step is the job's only
          # verdict, and every way of failing to reach one has to be a failure:
          #
          #   bundle absent       -- xcodebuild died before writing results, so
          #                          there is no evidence the suite ran
          #   totalTestCount == 0 -- the selector matched nothing. xcodebuild
          #                          EXITS 0 for that, and the summary then reads
          #                          "0 failed", which is indistinguishable from a
          #                          pass to everything downstream
          #   result != Passed    -- including "unknown", which is xcresulttool
          #                          saying it could not tell. "I could not tell"
          #                          is not a pass.
          #
          # The middle one is why this step exists rather than trusting the exit
          # code: a renamed scheme or a renamed target produces a run that
          # executes nothing, reports no failures, and is green.
          if [ ! -d "WatchTestResults.xcresult" ]; then
            echo "::error::No result bundle at WatchTestResults.xcresult (xcodebuild exit=$EXIT_CODE). The watch suite produced no evidence that it ran, so this job cannot report a pass."
            if [ -f watch-xcodebuild.log ]; then
              tail -60 watch-xcodebuild.log
            fi
            exit 1
          fi

          SUMMARY_JSON=$(xcrun xcresulttool get test-results summary --path WatchTestResults.xcresult)
          RESULT=$(printf '%s' "$SUMMARY_JSON" | jq -r '.result // empty')
          TOTAL=$(printf '%s' "$SUMMARY_JSON" | jq -r '.totalTestCount // empty')
          FAILED=$(printf '%s' "$SUMMARY_JSON" | jq -r '.failedTests // empty')
          echo "result=${RESULT:-<none>} total=${TOTAL:-<none>} failed=${FAILED:-<none>} xcodebuild_exit=$EXIT_CODE"

          if [ -z "$TOTAL" ] || [ "$TOTAL" = "0" ]; then
            echo "::error::The watch suite executed $TOTAL tests. -only-testing:$WATCH_TEST_TARGET matched nothing, and xcodebuild exits 0 for a selector that matches nothing -- so this would otherwise be a green check over a suite that never ran."
            exit 1
          fi

          if [ "$RESULT" != "Passed" ]; then
            echo "::error::Watch tests did not pass (result=$RESULT, failed=${FAILED:-?}, xcodebuild exit=$EXIT_CODE)."
            # NAME the failures in the log. A bare count is the weakest useful
            # signal a CI job can emit: enough to block, not enough to act on,
            # and just enough to make re-running feel reasonable. Capped at 20 so
            # a suite-wide breakage does not bury the step.
            printf '%s' "$SUMMARY_JSON" | jq -r '
              def indent: gsub("\n"; "\n            ");
              (.testFailures // []) as $f
              | ($f[0:20][] | "  FAILED: \(.targetName // "?")/\(.testName // "?")\n            \(.failureText // "(no failure text)" | indent)"),
                (if ($f|length) > 20 then "  ... and \(($f|length) - 20) more" else empty end)
            '
            echo "Raw xcodebuild tail:"
            tail -60 watch-xcodebuild.log
            exit 1
          fi

          echo "Watch suite $WATCH_TEST_TARGET: $TOTAL tests, result $RESULT."
          echo "Watch tests ($WATCH_TEST_TARGET): $TOTAL executed, $RESULT" >> "$GITHUB_STEP_SUMMARY"

      - name: Upload Watch Test Results
        uses: actions/upload-artifact@v7
        if: always()
        continue-on-error: true
        with:
          name: watch-test-results-${{ matrix.watch.slug }}
          # The raw tee'd log alongside the bundle: a formatter has been observed
          # swallowing xcodebuild's output entirely on this runner, and the step
          # output above only ever shows a tail.
          path: |
            WatchTestResults.xcresult
            watch-xcodebuild.log
            WatchTestResults-attempt1.xcresult
            watch-xcodebuild-attempt1.log
          if-no-files-found: ignore
          retention-days: 1

      # Only when Run Watch Tests caught a stall or an app crash at launch;
      # otherwise a no-op. always(), because a stall FAILS that step.
      - name: Upload watch test diagnostics
        uses: actions/upload-artifact@v7
        if: always()
        continue-on-error: true
        with:
          name: watch-test-diagnostics-${{ matrix.watch.slug }}
          path: simulator-diagnostics
          if-no-files-found: ignore
          retention-days: 7

      # LAST in the job, and scoped to this leg's exact name. always(), so
      # cancelled and failed runs clean up too -- those are precisely the runs
      # that leak, and same-ref runs cancel each other here by design. Without
      # this every run leaves a simulator behind until the nightly cleanup, and a
      # busy day fills the disk on the one shared Mac.
      - name: Delete this run's watch simulator
        if: always()
        run: |
          set -uo pipefail
          ids=$(xcrun simctl list devices | grep "${WATCH_SIM_PREFIX}-${GITHUB_RUN_ID}-${WATCH_SLUG} (" | grep -oE '[A-F0-9-]{36}' || true)
          for id in $ids; do
            echo "Deleting this run's watch simulator: $id"
            xcrun simctl shutdown "$id" 2>/dev/null || true
            xcrun simctl delete "$id" 2>/dev/null || true
          done`
