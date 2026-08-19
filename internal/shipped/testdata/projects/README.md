# Fixture projects

Four committed projects that `lacquer sync` is run against by
`internal/shipped/e2e_test.go`. They are the *pre-sync* state of a repository:
what a project looks like the moment before the lacquer touches it. Nothing the
lacquer writes is committed here — that is the point. The test copies a fixture
into a temp dir, makes it a real git repository, syncs, and asserts on what
lands.

They exist because the only end-to-end coverage the tool had ran `initcmd.Run`
against a temp directory holding one stub marker file. That proves detection
works on a directory nobody would recognise as a project. It does not prove the
tool works on the shapes this fleet actually has, and the shapes are where the
bugs were: a component under a `admin/` prefix, a second product with its own
scheme, a Swift manifest that is committed versus one that is only on disk.

## Why four, and not one

Each fixture is one **shape**, and the shapes differ in what they can prove. A
single combined fixture would fail as one unit and tell you nothing about which
property broke; four small ones each fail for exactly one reason.

| Fixture | Shape | What only this one proves |
| --- | --- | --- |
| `rootapp` | Single-stack iOS, `.xcodeproj` at the repo root | `COMPONENT_PREFIX` renders empty and `COMPONENT_TO_ROOT` renders `.`; a local SwiftPM package inside an app repo is **not** its own component; a repo with no committed `Package.resolved` gets **no** `swift` Dependabot entry |
| `multistack` | `ios/` + `admin/` (web) + `server/` (supabase) | Three profiles in one repo; per-component asset placement; `COMPONENT_PREFIX` = `admin/` and `COMPONENT_TO_ROOT` = `..` (the biome `vcs.root` case); root-level asset collisions between profiles |
| `duoapp` | One iOS repo, two shipped products | `[[product]]` schemes, test targets, UI test targets, per-product release secrets and tag prefixes; a committed `Package.resolved` inside the `.xcodeproj` bundle **does** produce a `swift` Dependabot entry |
| `spmpackage` | A bare SwiftPM package, no `.xcodeproj` anywhere | The lacquer's own declared gap: `detect.SwiftProfile` is `"swift"`, no `profiles/swift/` ships, so this reports as *unsupported* drift rather than adoptable — and must never start quietly succeeding |

## Reading one

Every fixture is a real, if small, project: sources, tests, a manifest, a
project-owned `.gitignore` carrying only build junk, and a `docs/brief.md`. The
`.gitignore` files deliberately contain **no** credential patterns — the whole
point of the `git check-ignore` assertions is that the *shipped* region is what
ignores `*.p8`, `Secrets.xcconfig` and `.env`, and a fixture that ignored them
itself would make those assertions pass for the wrong reason.

`project.pbxproj` files are trimmed, but not to a stub. A real one runs to
thousands of lines of object graph and a full copy would be noise, so these keep
only the objects the lacquer reads — and they keep *all* of them. The
`XCBuildConfiguration` blocks are the part that matters: `internal/baseline`
scans them for the settings the shipped standard asserts (Swift language mode,
warnings-as-errors, strict concurrency) and reports coverage as "n of m
Swift-compiling configurations". A pbxproj with no build settings yields m = 0,
which reads as "this component compiles no Swift", and every baseline assertion
then passes having looked at nothing. That was the state of these fixtures until
a mutation test — moving the asserted Swift version to one nothing declares —
went green.

## Changing one

A fixture is an assertion. If you change a manifest here, some test's expected
output changes with it — that is intended, and the test names say which
property moved. What must *not* happen is a fixture being edited to make a
failing test pass without the failure being understood: these shapes were
chosen because each one is a repository that exists.
