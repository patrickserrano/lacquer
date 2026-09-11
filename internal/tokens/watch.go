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
    timeout-minutes: 20
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
        run: |
          set -uo pipefail
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

          DEVICE_ID=$(xcrun simctl create "$SIM_NAME" "$WATCH_DEVICE_TYPE" "$RUNTIME")
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
          PAIRS=$(xcrun simctl list pairs)
          if printf '%s\n' "$PAIRS" | grep -q "$DEVICE_ID"; then
            echo "::error::$SIM_NAME ($DEVICE_ID) is PAIRED with a phone simulator. A paired watch activates a real WCSession, so this suite would run against different behaviour than it was written for and report that as a pass."
            printf '%s\n' "$PAIRS"
            exit 1
          fi
          echo "Confirmed UNPAIRED: $DEVICE_ID appears in no entry of simctl list pairs."

          xcrun simctl boot "$DEVICE_ID"
          xcrun simctl bootstatus "$DEVICE_ID" -b

          # Poll for the watch shell, which is CAROUSEL and not SpringBoard.
          # Measured on a booted watchOS 27.0 simulator: 'launchctl list' carries
          # com.apple.Carousel and no SpringBoard at all, so the Test job's
          # readiness poll copied across would time out and warn on a device that
          # had been ready for forty seconds.
          for i in $(seq 1 20); do
            if xcrun simctl spawn "$DEVICE_ID" launchctl list 2>/dev/null | grep -q "$WATCH_READY_SERVICE"; then
              echo "$WATCH_READY_SERVICE ready after $((i*3))s"
              break
            fi
            sleep 3
            if [ "$i" -eq 20 ]; then
              echo "::warning::$WATCH_READY_SERVICE readiness poll timed out after 60s, proceeding anyway"
            fi
          done

      - name: Run Watch Tests
        id: tests
        # The hang-prone step: a simulator test session can stall silently, hence
        # the heartbeat below. The ceiling frees the runner instead of letting a
        # wedged session squat the shared Mac for the job maximum.
        timeout-minutes: 15
        run: |
          set -o pipefail
          DEVICE_ID="${{ steps.simulator.outputs.device_id }}"
          rm -rf WatchTestResults.xcresult

          # Heartbeat, so Actions does not treat a long compile as a stuck step.
          ( while true; do sleep 60; echo "[watchdog $(date -u +%H:%M:%S)] xcodebuild still running..."; done ) &
          WATCHDOG_PID=$!

          # A SEPARATE derivedDataPath from the iOS jobs. They share one with each
          # other already; adding a third concurrent writer to it on the same
          # runner is not a trade this job needs to make, and a watch scheme's
          # build products have no reason to land in the iOS cache the SPM-cache
          # validation steps inspect.
          EXIT_CODE=0
          xcodebuild test \
            -project "@@XCODEPROJ@@" \
            -scheme "$WATCH_SCHEME" \
            -destination "${WATCH_DESTINATION_PREFIX},id=$DEVICE_ID" \
            -derivedDataPath @@COMPONENT_PREFIX@@WatchDerivedData \
            "-only-testing:$WATCH_TEST_TARGET" \
            -parallel-testing-enabled NO \
            -resultBundlePath WatchTestResults.xcresult \
            CODE_SIGNING_REQUIRED=NO \
          2>&1 | tee watch-xcodebuild.log | xcbeautify --renderer github-actions || EXIT_CODE=$?

          kill $WATCHDOG_PID 2>/dev/null || true

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
          if-no-files-found: ignore
          retention-days: 1

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
