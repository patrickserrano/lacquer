# iOS profile

Shared iOS CI builds and tests declared products after generating any
`project.yml` with XcodeGen. The settings below are opt-in; omitted settings
retain the profile's default behavior.

## Test timeouts

For a project whose unit suite exceeds the default step timeout, set both
limits in `.lacquer.toml`:

```toml
[ci]
ios_test_job_timeout_minutes = 90
ios_test_step_timeout_minutes = 60
```

The defaults are **20 minutes for the Test job** and **10 minutes for its Run
Tests step**. Each value must be an integer from 1 to 360, and the step limit
must not exceed the job limit, including when the other value is omitted.
These settings do not change test selection or other jobs' timeouts.

## Failing on Swift warnings in your own sources

Some Swift 6 diagnostics (the `[#SendableMetatypes]` group is one) stay
warnings on current Xcode even under `SWIFT_TREAT_WARNINGS_AS_ERRORS=YES`,
`-warnings-as-errors` or `-Werror <group>`, while an ordinary warning escalates
under the same flags. A CI that relies on those flags alone goes green with the
warning printed in its log. This opt-in gate reads the log instead:

```toml
[ci]
ios_fail_on_own_source_warnings = true
# optional; this is the default
ios_own_source_warnings_allowlist = ".lacquer-warning-allowlist"
```

**Default off.** With the key absent or `false` the rendered workflow is
byte-for-byte what it was before the option existed.

When on, a step follows the Release build and another follows the tests. Each
reads the raw `xcodebuild` log the job already keeps (`xcodebuild-release.log`,
`xcodebuild.log`) and fails if it holds a Swift warning, a line of the form
`<path>.swift:<line>:<col>: warning: …`, whose file is inside the repository.
These are not gated: a path under `SourcePackages`, `.build` or any
`*DerivedData*` directory (SPM checkouts and generated sources), and a path
outside the checkout. A relative path counts as the repository's own. The
checkout is recognised however the runner spells it (`$PWD`, `$GITHUB_WORKSPACE`
and their physical paths). Objective-C, linker and build-setting warnings are
not Swift warnings and are not gated.

On failure the step prints each offending line, once however often xcodebuild
repeated it, then a count, and exits non-zero. It runs only after the build or
test step passed, so `xcodebuild`'s own failure still fails the job first and
its exit status is never replaced. A passing run prints how many warnings it
scanned and how they split, so a pass shows the scan ran.

**Allowlist.** Known and accepted warnings go in a file of extended regular
expressions, one per line, in the repository (default
`.lacquer-warning-allowlist`, beside `.lacquer.toml`). Each is matched against
the whole warning line, path included, so `SendableMetatypes` allows the group
and `Feature\.swift:41:17` allows one site. Blank lines and `#` comments are
ignored. A missing file is an empty allowlist. The step fails, whether or not a
warning needed the allowlist, if a line is not a valid regex or matches an empty
string (which would allow everything). Setting
`ios_own_source_warnings_allowlist` without the gate on is an error at sync.
Edit the allowlist in a pull request like any code: it is a project file, not
managed by lacquer.

**What it cannot see.** The gate reads what the build printed. The dedicated
runner keeps `DerivedData` between runs, so a warm incremental build reprints
only the files that recompiled. A warning enters through a changed file, which
recompiles, so it is caught on the pull request that introduces it; one already
on the default branch and untouched is not reprinted and not re-reported. The
Swift package and watchOS test steps are not covered.

## Generated projects and package pins

A project that git-ignores its generated `.xcodeproj` can keep one committed
lockfile outside that directory:

```toml
[ios]
package_resolved = "ci_scripts/Package.resolved"
```

The path is relative to the repository root, even for a nested iOS component;
an existing source path such as `Config/Package.resolved` works too. Commit
that file and retain it as the only source of truth. Do not commit another
copy inside the generated project.

When enabled, the existing XcodeGen bootstrap is followed by a copy into
`<project.xcodeproj>/project.xcworkspace/xcshareddata/swiftpm/Package.resolved`
before package resolution, caching, or builds. CI and release workflows use
the same source. Missing or untracked sources fail rather than falling back
to a fresh resolution. A tracked destination is rejected to prevent two
committed lockfiles from drifting. With no `ios.package_resolved`, no copy step
is rendered.

Both `project.xcodeproj` and `ios.package_resolved` are repository-relative.
For a component at `ios/`, declare `xcodeproj = "ios/Demo.xcodeproj"` under
`[project]`. XcodeGen runs inside `ios/`; the copy step explicitly runs at the
repository root, so neither path gets the component prefix applied twice.

The package guard remains strict: pins that disagree with the generated
project's package requirements fail the job. Update the source lockfile when
changing requirements; copying it never resolves or repairs pins.

## CI release downloads

CI installers across the iOS, web and Supabase profiles retry transient release
downloads five times, waiting 15, 30, 60 and 120 seconds. Each curl attempt has a
5-second connect timeout and 10-second total timeout; HTTP 404 fails immediately.
HTTP 403 retries only when the final response has a rate-limit signal
(`x-ratelimit-remaining: 0` or `retry-after`). Retry-After seconds/HTTP dates
can extend a delay within the total 225-second wait budget; a delay that will
not fit goes straight to authenticated fallback. Plain 403 still fails fast.
After exhaustion, the step uses `gh release download` with the workflow token,
bounded to 30 seconds. Hosted runner images include gh; self-hosted runners
without it use authenticated API requests with curl and Python 3's JSON parser.
The API requests have 15-second limits each. No extra token scope is required
for these public assets. Both the archive and `checksums.txt` use this policy.
The pinned tag and checksum source stay the same; checksum mismatches fail
without retrying, and the checks never run an unverified download.

## Temporary lint and formatting debt

```toml
[baseline.relax]
lint = { until = "2026-12-01", reason = "Existing style violations being migrated" }
format = { until = "2026-12-31", reason = "Existing formatting violations being migrated independently" }
```

`lint` applies only to SwiftLint; `format` applies only to SwiftFormat. Each
tool still runs and logs findings, but its failure is non-blocking through its
own date (inclusive, UTC). Each entry needs its own nonempty reason and valid
expiry date. Expired or malformed entries fail their tool's step, even when
the tool passes. Removing an entry restores strict checks for that tool.
The existing `scripts/docs-relaxation.sh` reader applies the runtime date; no
workflow regeneration is needed when the date passes.

## Documentation commit hook (opt-in)

Documentation coverage is a writing convention, not a default commit gate.
A default iOS sync installs no `swiftlint-docs` pre-commit entry,
`scripts/docs-hook.sh`, or component `.swiftlint-docs.yml`. To retain the
previous documentation check explicitly:

```toml
[ios]
documentation_hook = true
```

This installs the existing hook and files unchanged. Its two doctor probes
run only with the opt-in; otherwise doctor explicitly reports them skipped.
In iOS-only projects, `scripts/docs-relaxation.sh` is included only for the
opt-in or a declared lint/format relaxation, including a project-owned style
workflow. Other profiles, including
mixed-profile projects, retain their existing reader bytes. The ordinary
SwiftLint and SwiftFormat gates are unchanged.

Existing projects lose the hook entry on their next sync. Sync also removes
these retired files when they are clean tracked regular files, recorded in
the lock, and match a known published legacy render byte-for-byte. Their lock entries
are cleared, so the next `lacquer audit --ci` passes without hand deletions.
The now-unused reader is retired too in iOS-only projects.
Edited, untracked, symlinked or project-owned files are preserved and reported
for review; a matching hand-edited lock alone cannot authorize removal.
The general orphan policy is unchanged and still blocks audit on leftovers.
Existing `[baseline.relax] documentation` entries remain valid and can be
removed when the documentation hook is disabled. With the opt-in, the existing
runtime relaxation and expiry checks still apply.
