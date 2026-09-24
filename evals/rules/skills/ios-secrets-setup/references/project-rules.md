## Secrets & Service Keys

Two separate buckets — never mix them.

### App-runtime keys → `Secrets.xcconfig` (compiled into the app)

Service keys the app needs at runtime (RevenueCat, Aptabase, …) live in a
gitignored `Secrets.xcconfig`, never in source or the committed `project.yml`.
The lacquer syncs a `Secrets.xcconfig.example` template into the component dir.
See the `ios-secrets-setup` skill for wiring a new key through `project.yml`
into `Info.plist` and reading it at runtime. `Secrets.xcconfig` values are
**build-time** — they are baked into the binary, so treat them as obfuscated,
not secret. A truly sensitive secret belongs on a server, never in the app.

> **RevenueCat ships two different keys — do not confuse them.** The
> `REVENUECAT_API_KEY` above is the **public SDK key** (`appl_…`), safe to compile
> into the app. RevenueCat's **REST API** uses a separate **secret key** (`sk_…`)
> that grants full account access — it must **never** go in `Secrets.xcconfig` or
> the binary. It is a CI/server secret (`REVENUECAT_REST_API_KEY`, below).

#### At release time, the real values come from `[project].secrets`

CI seeds `Secrets.xcconfig` from the committed example, because tests must run
without production keys. **A release must not.** An archive built from the
example ships wired to `appl_xxxxxxxx`, and nothing looks wrong until the
revenue does not arrive — or until App Review opens the paywall.

Declare the keys the release needs and the shared workflow writes them. A
single-app project (no `[[product]]` block) declares them under `[project]`:

```toml
[project]
# ...name, scheme, bundle_id, asc_app_id as usual...
# Where the values are written, relative to the component root. Defaults to
# Secrets.xcconfig, which is what the example file and .gitignore assume.
secrets_file = "xcconfig/Secrets.xcconfig"
# xcconfig key -> the NAME of the GitHub Actions secret holding its value.
# Never the value: this file is committed.
secrets = { REVENUECAT_API_KEY = "REVENUECAT_API_KEY", SENTRY_DSN = "SENTRY_DSN" }
# Optional shape check. Non-empty is not the same as correct.
secret_formats = { REVENUECAT_API_KEY = "appl_*", SENTRY_DSN = "https://*@*/*" }
```

A project with several `[[product]]` blocks declares the same three keys on
each product instead, because a paid app's key written into the free app's
build is a bad release, not a failed one. Setting them under `[project]` as well
is rejected: which product they belong to would have to be guessed. **If your
app reads keys from `Secrets.xcconfig` and none of this is declared, the release
archives with the committed placeholders** — declare them.

`release.yml` then runs `scripts/write-release-config.sh`, which seeds the
committed `<secrets_file>.example` and substitutes the declared keys into it.
Four things it does that a hand-written `sed` step does not:

- **Fails closed on an unset OR empty secret.** An unset GitHub secret expands
  to the empty string, and an empty xcconfig value is not an error to
  `xcodebuild` — it would build, sign, upload, and be wrong.
- **Fails closed on a wrong-shaped value**, per `secret_formats`. The two ways
  these go wrong in practice — pasting the other app's key, and leaving Google's
  public test AdMob id in place — both produce perfectly non-empty values.
- **Escapes `//` as `/$()/`**, because xcconfig treats `//` as the start of a
  comment. A bare `https://host` truncates to `https:`, which is non-empty, so
  every accessor that only tests for blank passes it through and the service is
  silently pointed at nothing.
- **Seeds from the example first**, so keys the project references but does not
  hold in secrets are still defined. The xcconfig is the target's base
  configuration file; writing only the declared keys leaves the rest undefined.

> **Why this is a script and not a step body.** The step it replaces was dropped
> by an onboarding sync in one repo, and the next four releases archived with
> every app-runtime key unset. `Purchases.configure` never ran, RevenueCatUI's
> paywall calls `fatalError("Purchases has not been configured.")` in any
> non-DEBUG build, and 1.1.0 was rejected under **Guideline 2.1(a)** — with CI
> green throughout. A script can be RUN against known-bad input; a program
> pasted into a YAML string can only be read. `lacquer doctor` runs this one
> with a required secret missing and requires it to fail.

**`lacquer sync` now refuses to drop a secret.** If the workflow a project has
today reads a `${{ secrets.NAME }}` the incoming lacquer version does not, the
sync stops and names it. That is the guard the onboarding above did not have —
the audit's clobber check compares against the lock baseline, and at onboarding
there is no baseline, so the one sync that discards all of a project's local
knowledge is the one it cannot see. Resolve it by declaring the keys as above,
or by excluding the path with a reason and an expiry. `--force` does not lift it.

If the credential is genuinely **obsolete**, retire it by name instead — the
other two answers are both wrong for that case, since declaring it resurrects
the secret you are removing and excluding the workflow freezes the whole file
out of every later improvement to buy one deletion:

```toml
[project]
retired_secrets = [
  { name = "SANITY_API_READ_TOKEN", reason = "migrated off Sanity to Payload CMS" },
]
```

`reason` is required and there is no `until`: a retired secret is retired, not
deferred. The requirement is the point — the guard's principle is that a
credential may only stop being read by a deliberate act a reviewer can see, and
a bare name list would be a silent opt-out of it.

### CI / server secrets → GitHub Actions (never in the app)

The release and quality workflows — and any server-side job that calls a vendor
REST API — read these from repo/org **GitHub Actions secrets**, never from an
xcconfig.

**Organization secrets only exist for organizations.** `gh secret set --org` 404s
against a personal account, so check which `example-org` is before choosing:

```bash
gh api /orgs/example-org >/dev/null 2>&1 \
  && gh secret set <NAME> --org example-org \
  || gh secret set <NAME> -R example-org/<repo>   # personal account: per repo
```

This is not a nitpick. Every repo in this fleet was missing
`CLAUDE_CODE_OAUTH_TOKEN` for months because the instruction here was
unconditionally org-level and the account owning them is personal — so the
command silently could not have worked, and four workflows failed on every run.
When a secret is per-repo, fan it out deliberately rather than one at a time:

```bash
for r in $(gh repo list <owner> --limit 200 --json name --jq '.[].name'); do
  gh secret set <NAME> -R <owner>/"$r" < secret.txt
done
```

| Secret | Used by | Source |
|--------|---------|--------|
| `ASC_KEY_ID` | release | App Store Connect → Users and Access → Integrations → API key |
| `ASC_ISSUER_ID` | release | same page (issuer ID) |
| `ASC_KEY_CONTENT` | release | the `.p8` private key contents |
| `APPLE_TEAM_ID` | release | Apple Developer membership |
| `KEYCHAIN_PASSWORD` | release (signing) | the dedicated runner's **login**-keychain password — set this as an **org-level** secret so every repo's release can unlock the system keychain (release never creates its own, and its final `always()` step restores the keychain's prior settings and re-locks it, so neither the unlocked window nor the timeout change outlives the run) |
| `SENTRY_AUTH_TOKEN` | release (dSYM upload) | Sentry → Settings → Auth Tokens, scoped to `project:releases` |
| `SENTRY_ORG` | release (dSYM upload) | the Sentry org slug (e.g. `pixel-fox-studio`) |
| `SENTRY_PROJECT` | release (dSYM upload) | the Sentry project slug (e.g. `rail`) — differs per repo, so this one is never org-level |
| `REVENUECAT_REST_API_KEY` | server/REST API calls | RevenueCat → API keys → **secret** key (`sk_…`) — full account access |

Three identical workarounds in three repos is a lacquer defect, not a project
defect.

`GITHUB_TOKEN` is provided automatically by Actions — do not set it.

**The release job borrows your login keychain, so it must give it back.** It
unlocks the login keychain to sign, and sets an auto-lock timeout to keep it open
across a 45-minute job. That timeout is a change to a keychain the job does not
own. Left in place on a runner Mac that is also somebody's personal machine,
`lock-on-sleep timeout=3600s` — macOS defaults to neither — locks it hourly and
on every sleep, and Messages signs itself out days later, with nothing in any
run saying why.

The final `always()` step now captures the prior settings and restores them.
Note which way the harm runs: with no timeout by default, *setting* one makes the
keychain lock more often, not less.

If your runner is genuinely dedicated hardware nobody logs into, none of this is
visible. If it is also a machine you use, it is worth knowing that CI reaches
your login keychain at all — a dedicated CI keychain would avoid that entirely,
at the cost of the interactive-unlock dialog this design was written to dodge.

**The Sentry dSYM upload is opt-in and fails open.** All three `SENTRY_*` secrets
must be present or the step skips — a project with no Sentry gets a clean release,
not a red one. The presence check is a **job-level** `env` var (`HAS_SENTRY_TOKEN`)
rather than one declared in the step's own `env:` block: `secrets` is not usable in
a step-level `if`, and a var set in that same step's `env:` is not in scope for its
`if` either, so the obvious-looking version of this gate skips silently on every
release and looks configured while uploading nothing.

**`inputs` is populated ONLY on `workflow_dispatch` (and `workflow_call`), so on
any other trigger a `|| default` is unconditional.** A workflow started by
`workflow_run`, `push` or `schedule` sees an empty `inputs`, and

```yaml
WHATS_NEW: ${{ github.event.inputs.whats_new || '• Bug fixes and performance improvements' }}
```

ships the placeholder every single time. It reads as configurable, it reads as
deliberate, and it is dead on the trigger that fires ~100% of the time. Measured
in dailybread, where every automatic TestFlight build shipped that exact string
to testers.

Same shape as the `HAS_SENTRY_TOKEN` note above and as the `--test-cases`
silent-skip: an expression that cannot distinguish *"the user chose nothing"*
from *"this trigger has no user to ask"*, defaulting to the quiet answer. If a
value must differ per trigger, branch on `github.event_name` and say so, rather
than leaning on a fallback that hides which branch you are in.


### A release must come from a commit CI passed

`ios-release.yml` opens with a `verify-ci-provenance` job that refuses the run
unless the exact SHA being released has a **completed, successful `CI OK` check
run**, and — for a tag — unless that commit is **reachable from the repository's
default branch**. It runs first, on Linux, so a release that must not happen
costs two minutes on a hosted runner rather than forty-five on the dedicated Mac.

`CI OK` is already the required check for **merging**. It was enforced nowhere
for **releasing**: a tag can be pushed at any commit — an unreviewed branch, a
revert of the fix, a commit that was never pushed for review — and every job
downstream built, signed and uploaded it exactly as if it had come off a green
main. The dispatch path is gated too, so a `workflow_dispatch` cannot launder an
unverified commit; only the ancestry half is tag-only, because a dispatch runs
from a branch and "is this on main yet" is not the question it is asking.

Two consequences worth knowing before you hit it:

- **Tag the merge commit, not the branch tip.** A tag on a branch that has not
  landed is blocked by design.
- **Let CI finish before you tag.** The check run must be *completed* and
  *successful* for that SHA; a tag pushed in the same breath as the commit
  arrives before CI has reported and is refused.
