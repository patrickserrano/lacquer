---
name: ios-debugger-agent
description: Build, run, and debug iOS apps on a simulator. Use when asked to run an iOS app, interact with the simulator UI, capture logs, or diagnose runtime behavior.
---

# iOS Debugger Agent

## Overview

Build and run iOS projects on a booted simulator, interact with the UI, and capture logs for debugging.

## Prerequisites

- Xcode installed with command-line tools
- A booted iOS simulator (check with `xcrun simctl list devices`)
- Project with `.xcodeproj` or `.xcworkspace`

## Core Workflow

### 1) Discover the booted simulator

```bash
xcrun simctl list devices | grep -E "Booted"
```

If none are booted, ask the user to boot one:
```bash
xcrun simctl boot "iPhone 15 Pro"
```

### 2) Build the project

For a workspace:
```bash
xcodebuild -workspace MyApp.xcworkspace -scheme MyApp -destination 'platform=iOS Simulator,name=iPhone 15 Pro' build
```

For a project:
```bash
xcodebuild -project MyApp.xcodeproj -scheme MyApp -destination 'platform=iOS Simulator,name=iPhone 15 Pro' build
```

### 3) Install and launch the app

Find the built app:
```bash
find ~/Library/Developer/Xcode/DerivedData -name "*.app" -path "*Debug-iphonesimulator*" | head -1
```

Install on simulator:
```bash
xcrun simctl install booted /path/to/MyApp.app
```

Launch the app:
```bash
xcrun simctl launch booted com.example.MyApp
```

### 4) Capture logs

Stream logs from the app:
```bash
xcrun simctl spawn booted log stream --predicate 'subsystem == "com.example.MyApp"' --level debug
```

Or capture all simulator logs:
```bash
xcrun simctl spawn booted log stream --level debug
```

## UI Interaction

### Take a screenshot
```bash
xcrun simctl io booted screenshot screenshot.png
```

### Record video
```bash
xcrun simctl io booted recordVideo video.mp4
# Press Ctrl+C to stop recording
```

### Open a URL in the simulator
```bash
xcrun simctl openurl booted "myapp://deeplink"
```

### Send push notification
```bash
xcrun simctl push booted com.example.MyApp notification.apns
```

## Troubleshooting

- **Build fails**: Check scheme name with `xcodebuild -list`
- **App won't launch**: Verify bundle ID with `defaults read /path/to/MyApp.app/Info.plist CFBundleIdentifier`
- **Simulator not found**: List available simulators with `xcrun simctl list devices available`
- **Clean build**: `xcodebuild clean` or delete DerivedData

## Common Commands Reference

| Task | Command |
|------|---------|
| List simulators | `xcrun simctl list devices` |
| Boot simulator | `xcrun simctl boot "iPhone 15 Pro"` |
| Shutdown simulator | `xcrun simctl shutdown booted` |
| Erase simulator | `xcrun simctl erase booted` |
| Install app | `xcrun simctl install booted /path/to/App.app` |
| Uninstall app | `xcrun simctl uninstall booted com.example.app` |
| Launch app | `xcrun simctl launch booted com.example.app` |
| Terminate app | `xcrun simctl terminate booted com.example.app` |
| Get app container | `xcrun simctl get_app_container booted com.example.app` |
