---
name: macos-spm-app-packaging
description: Scaffold, build, and package SwiftPM-based macOS apps without Xcode project. Use when you need a from-scratch macOS app layout, SwiftPM targets/resources, custom .app bundle assembly, or signing/notarization steps outside Xcode.
---

# macOS SwiftPM App Packaging

## Overview

Bootstrap a complete SwiftPM macOS app, then build, package, and run it without Xcode. This skill covers the full workflow from project scaffolding to release distribution.

## Project Scaffolding

### Basic Structure

```
MyApp/
├── Package.swift
├── Sources/
│   └── MyApp/
│       ├── MyApp.swift          # @main App entry
│       └── ContentView.swift
├── Resources/
│   ├── Assets.xcassets/
│   └── Info.plist
├── Scripts/
│   ├── package_app.sh
│   ├── compile_and_run.sh
│   └── sign-and-notarize.sh
└── version.env
```

### Package.swift

```swift
// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "MyApp",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "MyApp", targets: ["MyApp"])
    ],
    targets: [
        .executableTarget(
            name: "MyApp",
            resources: [
                .process("Resources")
            ]
        )
    ]
)
```

### version.env

```bash
APP_NAME="MyApp"
BUNDLE_ID="com.example.myapp"
VERSION="1.0.0"
BUILD_NUMBER="1"
MIN_MACOS="14.0"
# Set to 1 for menu bar apps
MENU_BAR_APP=0
```

## Build and Run

### Build with SwiftPM

```bash
# Debug build
swift build

# Release build
swift build -c release

# Run tests
swift test
```

### Package as .app Bundle

Create `Scripts/package_app.sh`:

```bash
#!/bin/bash
set -e

source version.env

BUILD_DIR=".build/release"
APP_BUNDLE="$BUILD_DIR/$APP_NAME.app"
CONTENTS="$APP_BUNDLE/Contents"
MACOS="$CONTENTS/MacOS"
RESOURCES="$CONTENTS/Resources"

# Build release
swift build -c release

# Create bundle structure
rm -rf "$APP_BUNDLE"
mkdir -p "$MACOS" "$RESOURCES"

# Copy binary
cp "$BUILD_DIR/$APP_NAME" "$MACOS/"

# Copy resources
cp -r Resources/* "$RESOURCES/" 2>/dev/null || true

# Generate Info.plist
cat > "$CONTENTS/Info.plist" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>$APP_NAME</string>
    <key>CFBundleIdentifier</key>
    <string>$BUNDLE_ID</string>
    <key>CFBundleName</key>
    <string>$APP_NAME</string>
    <key>CFBundleVersion</key>
    <string>$BUILD_NUMBER</string>
    <key>CFBundleShortVersionString</key>
    <string>$VERSION</string>
    <key>LSMinimumSystemVersion</key>
    <string>$MIN_MACOS</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
$([ "$MENU_BAR_APP" = "1" ] && echo "    <key>LSUIElement</key>
    <true/>")
</dict>
</plist>
EOF

echo "Created $APP_BUNDLE"
```

### Development Run Script

Create `Scripts/compile_and_run.sh`:

```bash
#!/bin/bash
set -e

source version.env

# Kill existing instance
pkill -x "$APP_NAME" 2>/dev/null || true

# Package
./Scripts/package_app.sh

# Launch
open ".build/release/$APP_NAME.app"
```

## Code Signing

### Development Signing

```bash
# Sign for local development
codesign --force --sign - ".build/release/MyApp.app"

# Or with a specific identity
codesign --force --sign "Developer ID Application: Your Name" ".build/release/MyApp.app"
```

### Create Stable Dev Identity

```bash
# Generate self-signed certificate for consistent dev signing
security create-keychain -p "" dev-signing.keychain
security default-keychain -s dev-signing.keychain
# Follow prompts in Keychain Access to create certificate
```

## Notarization and Release

Create `Scripts/sign-and-notarize.sh`:

```bash
#!/bin/bash
set -e

source version.env

APP_PATH=".build/release/$APP_NAME.app"
ZIP_PATH=".build/release/$APP_NAME-$VERSION.zip"

# Sign with Developer ID
codesign --force --options runtime --sign "Developer ID Application: Your Name" "$APP_PATH"

# Create zip for notarization
ditto -c -k --keepParent "$APP_PATH" "$ZIP_PATH"

# Submit for notarization
xcrun notarytool submit "$ZIP_PATH" \
    --apple-id "your@email.com" \
    --team-id "TEAM_ID" \
    --password "@keychain:AC_PASSWORD" \
    --wait

# Staple the ticket
xcrun stapler staple "$APP_PATH"

# Re-zip with stapled ticket
rm "$ZIP_PATH"
ditto -c -k --keepParent "$APP_PATH" "$ZIP_PATH"

echo "Release ready: $ZIP_PATH"
```

## Sparkle Updates (Optional)

### Generate Appcast Entry

```bash
#!/bin/bash
source version.env

ZIP_PATH=".build/release/$APP_NAME-$VERSION.zip"
SIZE=$(stat -f%z "$ZIP_PATH")
SIGNATURE=$(./bin/sign_update "$ZIP_PATH")
DATE=$(date -R)

cat << EOF
<item>
    <title>Version $VERSION</title>
    <pubDate>$DATE</pubDate>
    <sparkle:version>$BUILD_NUMBER</sparkle:version>
    <sparkle:shortVersionString>$VERSION</sparkle:shortVersionString>
    <enclosure
        url="https://example.com/releases/$APP_NAME-$VERSION.zip"
        length="$SIZE"
        type="application/octet-stream"
        sparkle:edSignature="$SIGNATURE"
    />
</item>
EOF
```

## GitHub Release

```bash
# Create tag
git tag -a "v$VERSION" -m "Release $VERSION"
git push origin "v$VERSION"

# Create GitHub release
gh release create "v$VERSION" \
    ".build/release/$APP_NAME-$VERSION.zip" \
    --title "v$VERSION" \
    --notes "Release notes here"
```

## Checklist

### Scaffolding
- [ ] Package.swift with correct targets and resources
- [ ] version.env with app metadata
- [ ] Info.plist template or generation script
- [ ] Basic app entry point (@main App)

### Build
- [ ] `swift build` succeeds
- [ ] `swift test` passes
- [ ] Resources copied correctly

### Packaging
- [ ] .app bundle structure correct
- [ ] Info.plist generated with correct values
- [ ] App launches from Finder

### Release
- [ ] Code signed with Developer ID
- [ ] Notarized and stapled
- [ ] Zip created for distribution
- [ ] (Optional) Sparkle appcast updated
