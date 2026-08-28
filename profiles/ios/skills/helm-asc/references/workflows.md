# Workflows

Use these patterns when the user asks for an end-to-end App Store Connect workflow through Helm.

For any workflow, resolve `helm-asc` once using the setup steps in `SKILL.md`, then reuse the resolved command path. Prefer direct JSON commands over shell pipelines. If an output is too large to inspect comfortably in the terminal result, write it to a file and read that file with the host's file-read tool.

When the agent generates files that Helm will upload, run `helm-asc paths --agent` and stage them under `uploadsInbox` before passing the path to an upload command.

## Find app -> list versions -> create version

1. Run `helm-asc apps list --agent` with a filter like `--bundle-id` or `--name` until exactly one app matches.
2. Capture the app ID.
3. Run `helm-asc apps <app-id> versions --agent` to inspect existing versions and states.
4. If the user wants a new version, run `helm-asc apps <app-id> versions create ... --agent`.
5. If the creation fails, inspect the JSON error message before retrying. App Store Connect often blocks version creation based on current review state.

## Inspect localization fields -> download bundle -> upload bundle

1. Run `helm-asc localization fields --agent` if you need the canonical field names before editing.
2. Run `helm-asc version <version-id> localizations --agent` to discover locale coverage and localization IDs.
3. Download the bundle with `helm-asc version <version-id> localizations download --agent`.
4. Edit the generated locale files under the JSON `rootPath`.
5. Validate with `helm-asc version <version-id> localizations upload --path <rootPath> --dry-run --agent`.
6. If the dry run is clean, rerun without `--dry-run`.
7. If the command exits with partial failure, retry only the failed locales or fields.

## Download or upload screenshots

1. Run `helm-asc version <version-id> screenshots --agent` to inspect locale and device coverage first.
2. Download with `helm-asc version <version-id> screenshots download --agent` when the user needs an editable baseline.
3. Before upload, ensure the directory layout under the JSON `rootPath` matches the CLI contract: `<root>/<locale>/<deviceType>/NN_<filename>.<ext>`.
4. Run `helm-asc version <version-id> screenshots upload --path <rootPath> --dry-run --agent`.
5. Only remove `--dry-run` after the plan looks correct.
6. Use `--replace` only when the user clearly wants existing screenshots replaced.

## Download or upload previews

1. Run `helm-asc version <version-id> previews --agent` to discover preview IDs, locales, and device types.
2. Download with `helm-asc version <version-id> previews download --agent` when the user needs local assets.
3. Prepare the same directory contract used by the CLI for uploads under the JSON `rootPath`.
4. Run `helm-asc version <version-id> previews upload --path <rootPath> --dry-run --agent`.
5. If the user needs to change frame timing on one preview, switch to `helm-asc preview <preview-id> update ... --agent`.

## Upload build -> attach groups -> submit for beta review

1. Discover the target app with `helm-asc apps list --agent`.
2. Discover existing groups with `helm-asc apps <app-id> testFlightGroups --agent`.
3. If the user names saved Helm group sets, discover aliases with `helm-asc apps <app-id> testFlightGroupAliases --agent`; use alias IDs when names are ambiguous.
4. If the build archive was produced by the agent, stage it under the `uploadsInbox` returned by `helm-asc paths --agent`.
5. Upload the build with `helm-asc apps <app-id> builds upload ... --agent`. Pass `--groups <id1,id2,...>`, `--aliases <name-or-id,...>`, or both when the user wants automatic attachment after processing.
6. If the workflow depends on the processed build record, use the upload mode that waits for processing.
7. Capture the resulting build ID from the JSON response.
8. Attach groups with either `helm-asc build <build-id> attach ... --agent` or `helm-asc testFlightGroup <group-id> add ... --agent`, depending on which IDs you already have. `build attach` accepts `--aliases` as well as `--groups`.
9. Submit the build with `helm-asc build <build-id> submit-for-review --agent`.
10. If the upload or attach steps return partial failure, inspect the per-group or localization rows and retry only the failed slices.

Do not submit for beta review automatically just because `attach` succeeded. Only include `--submit` during upload, or run `submit-for-review`, when the user has asked for submission.

## List reviews -> reply to a review

1. Run `helm-asc apps <app-id> reviews --agent` to locate the target review ID.
2. If the user wants canned review-request copy, use `helm-asc apps <app-id> reviews signature --agent`.
3. Reply with `helm-asc review <review-id> reply --text <text> --agent` or `helm-asc review <review-id> reply --file <path> --agent`.
4. Re-fetch the review if you need to confirm the stored reply state.

When the reply body is more than a short sentence, write it to a file and use `--file <path>` so quoting, newlines, and apostrophes cannot be mangled by the shell.

## List, create, or update in-app events

1. Discover the target app with `helm-asc apps list --agent`.
2. List events with `helm-asc apps <app-id> inAppEvents --agent`. Use `--state`, `--badge`, `--sort`, or `--limit` to narrow large result sets.
3. Create a new event with `helm-asc apps <app-id> inAppEvents create --reference-name <name> --primary-locale <locale> --agent`.
4. Inspect a single event with `helm-asc inAppEvent <event-id> --full --agent` before mutating it.
5. For metadata or schedule changes, run `helm-asc inAppEvent <event-id> update ... --dry-run --agent` first.
6. If the dry run is correct, rerun without `--dry-run`.

## Edit in-app event localizations

1. Run `helm-asc inAppEvent fields --agent` if you need supported field names and enum values.
2. Fetch current localizations with `helm-asc inAppEvent <event-id> localizations --agent`.
3. Create a missing locale with `helm-asc inAppEvent <event-id> localizations create --locale <locale> --name <name> --short-description <text> --long-description <text> --dry-run --agent`.
4. Update an existing locale with `helm-asc inAppEvent <event-id> localizations update --locale <locale> ... --dry-run --agent`.
5. Remove `--dry-run` only after the planned event output matches the intended changes.

## Upload in-app event assets

1. Run `helm-asc inAppEvent <event-id> localizations --agent` to discover localization IDs or confirm locale codes.
2. If the agent created the file, run `helm-asc paths --agent` and stage it under the returned `uploadsInbox`.
3. Upload with `helm-asc inAppEvent <event-id> assets upload --localization <localization-id-or-locale> --type eventCard|detailsPage <absolute-path> --agent`.
4. Delete a mistaken asset only with explicit user approval: `helm-asc inAppEvent <event-id> assets delete --asset <asset-id> --asset-kind image|video --yes --agent`.

## Duplicate an in-app event

1. Discover the source event with `helm-asc apps <app-id> inAppEvents --agent`.
2. Inspect it with `helm-asc inAppEvent <source-event-id> --full --agent`.
3. Duplicate with `helm-asc apps <app-id> inAppEvents duplicate --source <source-event-id> --reference-name <new-name> --agent`.
4. Re-fetch the new event and update dates, localizations, and assets as needed. Asset copy is not automatic.

## Submit an in-app event for review

1. Discover the app and event with `helm-asc apps <app-id> inAppEvents --agent`.
2. Confirm the event is ready with `helm-asc inAppEvent <event-id> --full --agent`.
3. Discover or create an iOS submission with `helm-asc apps <app-id> submissions --agent` or `helm-asc apps <app-id> submissions create --platform iOS --agent`.
4. Attach the event with `helm-asc submission <submission-id> add-event --event <event-id> --agent`.
5. Submit only when the user has explicitly approved it: `helm-asc submission <submission-id> submit --yes --agent`.

## Create or price IAP offers

1. Discover the product first. Use `helm-asc apps <app-id> inAppPurchases --agent` for non-subscription products, or `helm-asc apps <app-id> subscriptionGroups --agent` followed by `helm-asc subscriptionGroup <group-id> subscriptions --agent` for subscriptions.
2. Inspect current offers with `helm-asc inAppPurchase <product-id> offers --agent` or `helm-asc subscription <subscription-id> offers --agent`.
3. For offer-code groups, create the group before creating custom or one-time codes: `helm-asc subscription <subscription-id> offers offer-code create --name <name> --customer-eligibility <value> ... --dry-run --agent`.
4. For subscription offer-code groups, pass `--offer-mode` and `--duration` together when the offer is not using product defaults.
5. Choose one pricing mode. Use `--pricing free` for free offers, explicit `--price-point ISO3=id` or `--free-territory ISO3` when you already know price point IDs, or calculated `--strategy percentage|equalization|ppp`.
6. For calculated offer prices, scope territories with `--territory <ISO3>` repeats or `--all-territories`. `--base-territory` is optional and advanced; omit it unless the user asks to override the product or app base territory.
7. Use `--strategy percentage` with exactly one of `--percentage` or `--multiplier`. Use `--strategy equalization` with optional `--base-price` or `--base-price-point`. Use `--strategy ppp` with `--ppp-index` and any needed PPP fallback flags.
8. Do not combine explicit price flags with calculated strategy flags. If a mixed strategy command fails validation, inspect the nearest `offers ... create --help` output and rerun a single-strategy dry run.
9. After a clean dry run and explicit user approval, rerun without `--dry-run`; pass `--yes` only when the user has approved the write.
10. Validate direct offer prices with the nested read-only price commands instead of relying on `priceCount` or the App Store Connect UI: `offers offer-code <offer-code-id> prices`, `offers introductory prices`, `offers promotional <promotional-offer-id> prices`, or `offers win-back <win-back-offer-id> prices`.
