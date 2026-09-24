For product matrices and watch tests, read the `ios-ci-configuration` skill.

## App Store Requirements

- **`ITSAppUsesNonExemptEncryption` must be set** in `Info.plist` (or as `INFOPLIST_KEY_ITSAppUsesNonExemptEncryption` build setting). Value is `NO` for apps using only standard HTTPS; `YES` for apps with custom encryption. Missing or wrong value causes export compliance failures on every TestFlight upload.

- **Terms of Use (EULA).** Configure a custom EULA in App Store Connect → **License Agreement** (if you set none, Apple's standard EULA applies automatically). *Separately*, App Review requires a **functional Terms of Use link in the App Store description** — link your custom EULA, or Apple's standard EULA: `https://www.apple.com/legal/internet-services/itunes/dev/stdeula/`. A missing/broken EULA link is a common rejection.

- **Privacy Policy link.** Required for every app: set the **Privacy Policy URL** in App Store Connect → App Information, AND make the policy reachable **inside the app**. Include the link in the App Store description too. A non-functional or missing privacy policy link is a frequent rejection.

- **Subscription / IAP apps (Guideline 3.1.2):** the **paywall/purchase screen itself** must clearly show price, duration, auto-renewal terms, and how to cancel — not just the description — and both the **privacy policy** and **terms of use (EULA)** links must be **clickable on that screen** (in the binary, not only in metadata). See [Premium / Subscription Gating](../../ios-project-development/references/project-rules.md#premium--subscription-gating-if-monetized).

- **No price in the app name or icon (Guideline 2.3.7).** "Free", "Lite (Free)", a price, or a `FREE` badge burned into the icon artwork all get rejected — in the **App Store name**, the **on-device name**, and the **icon image itself**. The description is exempt. On a free/paid pair this bites the free product: set both `CFBundleDisplayName` **and** `CFBundleName` (the fallback iOS shows in Settings, which otherwise defaults to `$(PRODUCT_NAME)`), and check the 1024pt icon for baked-in badge text.

- **A version train closes permanently once its version reaches `READY_FOR_SALE`.** Uploading another build against that same marketing version fails with **error 90186** ("Invalid Pre-Release Train"), no matter the build number. A shipped app needs a **version bump** to accept a new build. In a repo shipping several apps, this is why one release trigger must never fan out to every product: the already-shipped one can only fail.

**The toolchain is asserted, not inherited.** Every macOS job starts with a
`Verify the toolchain` step, and the workflow sets `DEVELOPER_DIR` explicitly
rather than taking whatever `xcode-select` points at. On a shared self-hosted
host the installed Xcode is HOST state: it changes with no PR, no warning, and
the first symptom is a red `main` on an unrelated merge. That happened —
Xcode 27.0 landed mid-session and turned a project's `main` red on a commit
whose own PR run had passed four minutes earlier.

The licence check is the load-bearing half. An unaccepted licence after an
upgrade does not report itself as a licence problem: the observed first symptom
was `unable to spawn process '.../embeddedBinaryValidationUtility' (No such
file or directory)` for a file sitting on disk, which sends you hunting a
corrupted install. `xcodebuild -checkFirstLaunchStatus` exits non-zero for
exactly that state and costs nothing.

```toml
[project]
xcode_version = "27.0"   # optional; "27.0 (27A266a)" pins the build too
```

Declaring it is an ASSERTION, not a path pin — these runners carry one Xcode
upgraded in place, so a versioned `DEVELOPER_DIR` would name a bundle that does
not exist. Declaring nothing still echoes the version into every job, so a
silent upgrade becomes visible in the log instead of inferred from a failure.

## Release archives go to the archive volume, not the repository

`.xcarchive` bundles are 60-100 MB each. Written into the repository checkout,
every release leaves one behind on the runner and trips any tool that walks the
working tree, so the release workflow writes them to a dedicated volume, namespaced by repository and run id:

```
/Volumes/Developer Archives/CI Archives/<repo>/<run-id>/<Product>.xcarchive
```

Override the root per project if your runner mounts it elsewhere:

```toml
[project]
archive_root = "/Volumes/Somewhere Else"   # optional
```

**The release FAILS if that path is missing, and does not fall back to the
repository.** A silent fallback would restore the exact problem the change
exists to prevent, and nobody would notice until a disk filled — the same
"quietly did the wrong thing and reported success" shape this profile keeps
hunting. The check runs *before* the build, so an unmounted volume costs you a
few seconds rather than a full archive.

## App Store Connect accepts a binary before it lists it

There is a window of minutes where Apple has taken your upload and the build is
not yet in the builds list or in `get-latest-testflight-build-number`. **Absence
is not proof the upload failed.** Reading it as proof is how a successful release
gets "recovered" into a duplicate binary.

This is why the TestFlight upload passes `--altool-retries 1`. The CLI's default
is **10**, and an upload is not idempotent: a succeeded-then-timed-out attempt is
retried, Apple answers 409
`ENTITY_ERROR.RELATIONSHIP.INVALID.INVALID_STATE` on `/data/relationships/buildUpload`
because the build now exists, and the step **fails a release that worked**.

Measured across three consecutive releases, every one making two altool
invocations from a single `publish` call:

| build | attempt 1 | attempt 2 | step reported |
|---|---|---|---|
| 314 | uploaded | 409 | passed, plus an ITMS-90189 "Redundant Binary Upload" email |
| 315 | uploaded | 409 | **failed — the build landed anyway** |
| 316 | failed | uploaded | passed |

One mechanism, three presentations, decided only by which attempt lands last.

Re-running the job is a correct recovery on its own: the build number is
re-derived from App Store Connect each run, so a re-run takes the next number
rather than repeating a consumed one. The retry only made that automatic, and
charged a non-idempotent double upload for it.

**If you ever add smarter 409 handling** — on conflict, ask ASC whether the build
exists and pass if so — the processing gap above is the thing to get right. A
poll loop that cannot tell "not there" from "not there yet" reproduces the bug it
was written to fix.
