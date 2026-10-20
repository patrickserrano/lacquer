---
name: ios-release-guide
description: Release an iOS app, upload to TestFlight, or fix App Store validation, archive placement, and upload processing.
---

# iOS Release Guide

## Project conventions

Read [references/project-rules.md](references/project-rules.md) for the relevant
section when handling this task. Read only what applies; examples do not
authorize releases, deployments or changes outside the user's scope.
Always-loaded safety rules still apply.

- App Store Requirements
- Release archives go to the archive volume, not the repository
- App Store Connect accepts a binary before it lists it

## App Store submission requirements (dated)

Apple's wording is kept as written: "starting April 2027", no day of the month,
because neither post gives one.

| Rule | Effective | Source |
|---|---|---|
| iOS and iPadOS apps must be built with the iOS 27 & iPadOS 27 SDK or later. The same post sets the same rule for tvOS, visionOS and watchOS apps (the tvOS 27, visionOS 27 and watchOS 27 SDKs) and states none for macOS. | Starting April 2027 | "App Store submissions now open for the latest OS releases", Sep 9, 2026: <https://developer.apple.com/news/?id=k1mtkt1k> |
| Any apps or games submitted must include screenshots for iPhone Duo. | Starting April 2027 | "Prepare and submit your apps for iPhone Duo", Oct 5, 2026: <https://developer.apple.com/news/?id=kkphp5qo> |

The Duo post also gives the customer availability date, October 23, 2026. The
previous cycle's rule (the 26 SDK family, April 2026) is recorded in
[watchOS platform basics](../watchos-development/references/platform-basics.md).
