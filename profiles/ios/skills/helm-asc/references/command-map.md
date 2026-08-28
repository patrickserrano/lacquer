# Command Map

Use this file to choose the correct `helm-asc` branch quickly. Prefer these branch commands first, then inspect nested `--help` only when you need specific flags.

## CLI file locations

- `helm-asc paths`

Use `paths --agent` before staging generated upload files or when recovering from `FILE_ACCESS` errors. The returned `uploadsInbox` is the preferred place for agent-created files that will be passed back into Helm upload commands.

## Account setup

- `helm-asc auth list`
- `helm-asc auth add`
- `helm-asc auth switch`

Use `auth` when the task is about adding a Helm setup, switching the active App Store Connect account, or confirming which account is active.

## App discovery and app-scoped collections

- `helm-asc apps list`
- `helm-asc apps <app-id>`
- `helm-asc apps <app-id> open`
- `helm-asc apps <app-id> tags`
- `helm-asc apps <app-id> update`
- `helm-asc apps <app-id> prepare-for-sale`
- `helm-asc apps <app-id> versions`
- `helm-asc apps <app-id> builds`
- `helm-asc apps <app-id> reviews`
- `helm-asc apps <app-id> inAppEvents`
- `helm-asc apps <app-id> submissions`
- `helm-asc apps <app-id> testFlightGroups`
- `helm-asc apps <app-id> testFlightGroupAliases`

Start here when you only know an app name, bundle ID, or app ID.

## App versions

- `helm-asc apps <app-id> versions`
- `helm-asc apps <app-id> versions create`
- `helm-asc version <version-id>`
- `helm-asc version <version-id> open`
- `helm-asc version <version-id> update`
- `helm-asc version <version-id> review-info`
- `helm-asc version <version-id> review-info update`
- `helm-asc version <version-id> review-info attachment upload`
- `helm-asc version <version-id> review-info attachment delete`
- `helm-asc version <version-id> ship`
- `helm-asc version <version-id> reject`

Use `apps <app-id> versions` to discover or create versions. Switch to `version <version-id>` once you know the version ID.

## Localizations

- `helm-asc version <version-id> localizations`
- `helm-asc version <version-id> localizations create`
- `helm-asc version <version-id> localizations download`
- `helm-asc version <version-id> localizations upload`
- `helm-asc localization fields`
- `helm-asc localization <localization-id>`
- `helm-asc localization <localization-id> update`
- `helm-asc localization <localization-id> download`
- `helm-asc localization <localization-id> upload`
- `helm-asc localization <localization-id> delete`

Use the version-scoped commands for bundles across many locales. Use the singular localization commands when you already know the localization ID or need the field catalog.

## Screenshots and previews

- `helm-asc version <version-id> screenshots`
- `helm-asc version <version-id> screenshots download`
- `helm-asc version <version-id> screenshots upload`
- `helm-asc version <version-id> screenshots delete`
- `helm-asc screenshot <screenshot-id> delete`
- `helm-asc version <version-id> previews`
- `helm-asc version <version-id> previews download`
- `helm-asc version <version-id> previews upload`
- `helm-asc version <version-id> previews delete`
- `helm-asc preview <preview-id> update`
- `helm-asc preview <preview-id> delete`

Use the version-scoped commands for batch media workflows and the singular commands for one-item cleanup or preview frame-time updates.

## TestFlight

- `helm-asc apps <app-id> testFlightGroups`
- `helm-asc apps <app-id> testFlightGroups create`
- `helm-asc apps <app-id> testFlightGroupAliases`
- `helm-asc apps <app-id> testFlightGroupAliases create`
- `helm-asc apps <app-id> testFlightGroupAliases <id-or-name>`
- `helm-asc apps <app-id> testFlightGroupAliases <id-or-name> update`
- `helm-asc apps <app-id> testFlightGroupAliases <id-or-name> delete`
- `helm-asc testFlightGroup <group-id>`
- `helm-asc testFlightGroup <group-id> update`
- `helm-asc testFlightGroup <group-id> delete`
- `helm-asc testFlightGroup <group-id> testers`
- `helm-asc testFlightGroup <group-id> builds`
- `helm-asc testFlightGroup <group-id> add`
- `helm-asc build <build-id>`
- `helm-asc build <build-id> update`
- `helm-asc build <build-id> expire`
- `helm-asc build <build-id> attach`
- `helm-asc build <build-id> submit-for-review`
- `helm-asc apps <app-id> builds`
- `helm-asc apps <app-id> builds upload`

Start with the app-scoped lists, then move to `build` or `testFlightGroup` once IDs are known. Use TestFlight group aliases when the user refers to saved Helm groups such as Favorites, QA, or Smoke; `build attach` and `builds upload` can accept `--aliases <name-or-id,...>` instead of or alongside `--groups`.

## Reviews and submissions

- `helm-asc apps <app-id> reviews`
- `helm-asc apps <app-id> reviews link`
- `helm-asc apps <app-id> reviews signature`
- `helm-asc review <review-id>`
- `helm-asc review <review-id> reply`
- `helm-asc apps <app-id> submissions`
- `helm-asc apps <app-id> submissions create`
- `helm-asc submission <submission-id>`
- `helm-asc submission <submission-id> add-version`
- `helm-asc submission <submission-id> remove-version`
- `helm-asc submission <submission-id> add-event`
- `helm-asc submission <submission-id> remove-event`
- `helm-asc submission <submission-id> submit`
- `helm-asc submission <submission-id> cancel`

Use the app-scoped list commands to discover review or submission IDs. Use the singular commands once you are targeting one review or submission.

## In-App Events

- `helm-asc apps <app-id> inAppEvents`
- `helm-asc apps <app-id> inAppEvents create`
- `helm-asc apps <app-id> inAppEvents duplicate`
- `helm-asc inAppEvent fields`
- `helm-asc inAppEvent <event-id>`
- `helm-asc inAppEvent <event-id> update`
- `helm-asc inAppEvent <event-id> localizations`
- `helm-asc inAppEvent <event-id> localizations create`
- `helm-asc inAppEvent <event-id> localizations update`
- `helm-asc inAppEvent <event-id> localizations delete`
- `helm-asc inAppEvent <event-id> assets upload`
- `helm-asc inAppEvent <event-id> assets delete`
- `helm-asc inAppEvent <event-id> archive`
- `helm-asc inAppEvent <event-id> delete`
- `helm-asc submission <submission-id> add-event`
- `helm-asc submission <submission-id> remove-event`

Start with `apps <app-id> inAppEvents --agent` to discover event IDs. Use `inAppEvent <event-id> --full --agent` before mutations when you need all localizations. Use `update --dry-run` and localization `create/update --dry-run` before writes. For asset uploads, run `paths --agent`, stage agent-created files under `uploadsInbox`, and pass the absolute path. Submitting events for review is a submission workflow: create or discover an iOS submission, attach the event with `submission <submission-id> add-event --event <event-id> --agent`, then submit only when explicitly requested.

## In-App Purchases

- `helm-asc apps <app-id> inAppPurchases`
- `helm-asc apps <app-id> subscriptionGroups`
- `helm-asc apps <app-id> billing-grace-period`
- `helm-asc apps <app-id> billing-grace-period update`
- `helm-asc apps <app-id> billing-grace-period off`
- `helm-asc apps <app-id> promoted-purchases`
- `helm-asc apps <app-id> promoted-purchases create`
- `helm-asc apps <app-id> promoted-purchases order`
- `helm-asc apps <app-id> promoted-purchases <promoted-purchase-id>`
- `helm-asc apps <app-id> promoted-purchases <promoted-purchase-id> update`
- `helm-asc apps <app-id> promoted-purchases <promoted-purchase-id> delete`
- `helm-asc inAppPurchase <in-app-purchase-id>`
- `helm-asc inAppPurchase <in-app-purchase-id> update`
- `helm-asc inAppPurchase <in-app-purchase-id> localizations`
- `helm-asc inAppPurchase <in-app-purchase-id> prices`
- `helm-asc inAppPurchase <in-app-purchase-id> prices plan`
- `helm-asc inAppPurchase <in-app-purchase-id> prices set`
- `helm-asc inAppPurchase <in-app-purchase-id> prices schedule`
- `helm-asc inAppPurchase <in-app-purchase-id> availability`
- `helm-asc inAppPurchase <in-app-purchase-id> availability update`
- `helm-asc inAppPurchase <in-app-purchase-id> availability remove-from-sale`
- `helm-asc inAppPurchase <in-app-purchase-id> review-screenshot view`
- `helm-asc inAppPurchase <in-app-purchase-id> review-screenshot upload`
- `helm-asc inAppPurchase <in-app-purchase-id> review-screenshot delete`
- `helm-asc inAppPurchase <in-app-purchase-id> promotional-image view`
- `helm-asc inAppPurchase <in-app-purchase-id> promotional-image upload`
- `helm-asc inAppPurchase <in-app-purchase-id> promotional-image delete`
- `helm-asc inAppPurchase <in-app-purchase-id> offers`
- `helm-asc inAppPurchase <in-app-purchase-id> offers offer-code create`
- `helm-asc inAppPurchase <in-app-purchase-id> offers offer-code <offer-code-id>`
- `helm-asc inAppPurchase <in-app-purchase-id> offers offer-code <offer-code-id> prices`
- `helm-asc inAppPurchase <in-app-purchase-id> offers offer-code <offer-code-id> create-custom-code`
- `helm-asc inAppPurchase <in-app-purchase-id> offers offer-code <offer-code-id> create-one-time-code`
- `helm-asc inAppPurchase <in-app-purchase-id> offers offer-code <offer-code-id> deactivate`
- `helm-asc inAppPurchase <in-app-purchase-id> offers offer-code <offer-code-id> export-one-time-codes`
- `helm-asc inAppPurchase <in-app-purchase-id> promoted-purchase`
- `helm-asc inAppPurchase <in-app-purchase-id> checklist`
- `helm-asc inAppPurchase <in-app-purchase-id> submit`
- `helm-asc subscription <subscription-id>` (preferred when the subscription ID is known)
- `helm-asc subscription <subscription-id> update`
- `helm-asc subscription <subscription-id> localizations`
- `helm-asc subscription <subscription-id> localizations download`
- `helm-asc subscription <subscription-id> localizations upload`
- `helm-asc subscription <subscription-id> prices`
- `helm-asc subscription <subscription-id> prices plan`
- `helm-asc subscription <subscription-id> prices set`
- `helm-asc subscription <subscription-id> prices schedule`
- `helm-asc subscription <subscription-id> availability`
- `helm-asc subscription <subscription-id> availability update`
- `helm-asc subscription <subscription-id> availability remove-from-sale`
- `helm-asc subscription <subscription-id> review-screenshot view`
- `helm-asc subscription <subscription-id> review-screenshot upload`
- `helm-asc subscription <subscription-id> review-screenshot delete`
- `helm-asc subscription <subscription-id> promotional-image view`
- `helm-asc subscription <subscription-id> promotional-image upload`
- `helm-asc subscription <subscription-id> promotional-image delete`
- `helm-asc subscription <subscription-id> offers`
- `helm-asc subscription <subscription-id> offers offer-code create`
- `helm-asc subscription <subscription-id> offers offer-code <offer-code-id>`
- `helm-asc subscription <subscription-id> offers offer-code <offer-code-id> prices`
- `helm-asc subscription <subscription-id> offers offer-code <offer-code-id> create-custom-code`
- `helm-asc subscription <subscription-id> offers offer-code <offer-code-id> create-one-time-code`
- `helm-asc subscription <subscription-id> offers offer-code <offer-code-id> deactivate`
- `helm-asc subscription <subscription-id> offers offer-code <offer-code-id> export-one-time-codes`
- `helm-asc subscription <subscription-id> offers introductory`
- `helm-asc subscription <subscription-id> offers introductory prices`
- `helm-asc subscription <subscription-id> offers introductory create`
- `helm-asc subscription <subscription-id> offers introductory delete`
- `helm-asc subscription <subscription-id> offers promotional`
- `helm-asc subscription <subscription-id> offers promotional create`
- `helm-asc subscription <subscription-id> offers promotional <promotional-offer-id>`
- `helm-asc subscription <subscription-id> offers promotional <promotional-offer-id> prices`
- `helm-asc subscription <subscription-id> offers promotional <promotional-offer-id> update-prices`
- `helm-asc subscription <subscription-id> offers promotional <promotional-offer-id> delete`
- `helm-asc subscription <subscription-id> offers win-back`
- `helm-asc subscription <subscription-id> offers win-back create`
- `helm-asc subscription <subscription-id> offers win-back <win-back-offer-id>`
- `helm-asc subscription <subscription-id> offers win-back <win-back-offer-id> prices`
- `helm-asc subscription <subscription-id> offers win-back <win-back-offer-id> update`
- `helm-asc subscription <subscription-id> offers win-back <win-back-offer-id> delete`
- `helm-asc subscription <subscription-id> promoted-purchase`
- `helm-asc subscription <subscription-id> checklist`
- `helm-asc subscription <subscription-id> submit`
- `helm-asc subscription <subscription-id> delete`
- `helm-asc subscriptionGroup <subscription-group-id>`
- `helm-asc subscriptionGroup <subscription-group-id> localizations`
- `helm-asc subscriptionGroup <subscription-group-id> subscriptions`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id>` (group-scoped discovery/backwards compatibility alias)
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> localizations`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> localizations download`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> localizations upload`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> prices`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> prices plan`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> prices set`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> prices schedule`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> availability`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> availability update`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> availability remove-from-sale`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> review-screenshot view`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> review-screenshot upload`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> review-screenshot delete`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> promotional-image view`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> promotional-image upload`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> promotional-image delete`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers offer-code create`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers offer-code <offer-code-id>`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers offer-code <offer-code-id> prices`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers offer-code <offer-code-id> create-custom-code`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers offer-code <offer-code-id> create-one-time-code`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers offer-code <offer-code-id> deactivate`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers offer-code <offer-code-id> export-one-time-codes`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers introductory`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers introductory prices`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers introductory create`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers introductory delete`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers promotional`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers promotional create`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers promotional <promotional-offer-id>`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers promotional <promotional-offer-id> prices`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers promotional <promotional-offer-id> update-prices`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers promotional <promotional-offer-id> delete`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers win-back`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers win-back create`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers win-back <win-back-offer-id>`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers win-back <win-back-offer-id> prices`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers win-back <win-back-offer-id> update`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> offers win-back <win-back-offer-id> delete`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> promoted-purchase`
- `helm-asc subscriptionGroup <subscription-group-id> subscription <subscription-id> checklist`
- `helm-asc subscriptionGroup <subscription-group-id> submit`

Start with `apps <app-id> inAppPurchases` for consumable, non-consumable, and non-renewing products. Use `apps <app-id> subscriptionGroups`, then `subscriptionGroup <group-id> subscriptions`, to discover auto-renewable subscriptions. Once you know the subscription ID, prefer `subscription <subscription-id>` for product-level subscription work; keep `subscriptionGroup <group-id> subscription <subscription-id>` for group-scoped discovery and backwards compatibility. List commands support focused discovery with `--state`, `--sort`, and `--limit` where applicable. Pricing and availability mutations require `--dry-run` or explicit confirmation/`--yes`. Use product `prices plan` and `prices set` for strategy-based equalization, PPP, percentage, copy-from-product, and CSV pricing; use product `prices schedule` only when you already have explicit App Store Connect price point IDs. Use nested offer `... prices` commands to validate actual offer price values; offer detail commands may only show metadata and `priceCount`. App Store Connect does not expose PPP index provenance, so only PPP strategy `set` records Helm-local PPP metadata. Offer creation commands accept shared offer pricing flags: `--pricing free|custom`, explicit `--price-point ISO3=id` or `--free-territory ISO3`, or calculated `--strategy percentage|equalization|ppp` with `--territory` or `--all-territories`. Do not mix explicit prices with calculated strategy flags. Use `inAppPurchase <id> update --review-note <text>` or `subscription <id> update --review-note <text>` for App Review notes; asset upload/delete helpers manage review screenshots and promotional images separately.
