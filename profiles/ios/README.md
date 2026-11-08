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
