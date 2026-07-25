---
title: "ScreenCaptureKit — Screen Recording & Sharing"
description: "Reference for the macos-development skill."
---

:::note
Generated from [`profiles/ios/skills/macos-development/references/screencapturekit.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/macos-development/references/screencapturekit.md) — edit that file, not this page.
:::

# ScreenCaptureKit — Screen Recording & Sharing

macOS-only, GPU-accelerated, privacy-gated screen capture — displays,
windows, applications, and their audio — as a live stream, a one-shot
screenshot, or a recorded file. Replaces `CGDisplayStream` and
`CGWindowListCreateImage`; supersedes `AVCaptureScreenInput`. For iOS screen
recording, use ReplayKit (`RPScreenRecorder`/broadcast extensions) — out of
scope here.

## Core mental model

A four-stage pipeline, and stages 2–3 can be swapped **on the fly** without
tearing down the stream:

1. **Enumerate** — `SCShareableContent` lists capturable displays, windows, apps.
2. **Filter** — `SCContentFilter` narrows to exactly what to capture.
3. **Configure** — `SCStreamConfiguration` sets resolution, fps, audio, cursor, color, HDR.
4. **Stream** — `SCStream` (filter + configuration + delegate) delivers `CMSampleBuffer`s to an `SCStreamOutput` on a queue you provide.

For consent, prefer the system `SCContentSharingPicker` (macOS 14+) over
building your own selection UI.

## System requirements

| API | Availability |
|---|---|
| `SCStream`, `SCShareableContent`, `SCContentFilter`, `SCStreamConfiguration`, `SCStreamOutput` | macOS |
| `SCContentSharingPicker`, `SCScreenshotManager`, Presenter Overlay (`outputEffectDidStart`) | macOS 14+ |
| `SCRecordingOutput`, microphone capture (`captureMicrophone`), HDR (`captureDynamicRange`) | macOS 15+ |
| Mac Catalyst | 18.2+ |
| iOS/iPadOS | Not available — use ReplayKit |

Requires the **Screen Recording** TCC permission — without it,
`SCShareableContent` returns no content (empty, not an error). A background/
login-item capturer (VNC, remote desktop) additionally needs the
**Persistent Content Capture** entitlement. Full entitlement/TCC diagnosis
pattern in `sandbox-and-file-access.md`.

## Critical gotchas

| Gotcha | Why it bites | Fix |
|---|---|---|
| Building for iOS | ScreenCaptureKit is macOS-only | Use ReplayKit on iOS |
| No frames ever arrive | Screen Recording permission not granted | Handle the empty `SCShareableContent` case explicitly |
| UI hitches / dropped frames | Heavy work on the sample-handler queue | Dedicated **serial** `DispatchQueue`; copy what you need and return fast |
| Memory balloons / stream stalls | Holding IOSurface-backed buffers past `queueDepth` | Process and release each `CMSampleBuffer` promptly; tune `queueDepth` |
| Reinventing consent UI | Misses Video menu-bar item, Presenter Overlay integration | Use `SCContentSharingPicker` (macOS 14+) |
| Treating idle frames as new content | `.idle` status means no new IOSurface | Read `SCStreamFrameInfo.status`; skip `.idle` |
| "Hall of mirrors" recursion | Capturing your own window inside a display filter | Exclude your app in the `SCContentFilter` |

## The capture pipeline

```swift
import ScreenCaptureKit

// 1. Enumerate
let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
guard let display = content.displays.first else { return }

// 2. Filter — exclude our own app to avoid the hall of mirrors
let myApp = content.applications.first { $0.bundleIdentifier == Bundle.main.bundleIdentifier }
let filter = SCContentFilter(display: display, excludingApplications: myApp.map { [$0] } ?? [], exceptingWindows: [])

// 3. Configure
let config = SCStreamConfiguration()
config.width = 1920; config.height = 1080
config.minimumFrameInterval = CMTime(value: 1, timescale: 60)   // 60 fps
config.showsCursor = true
config.capturesAudio = true
config.queueDepth = 5                                            // buffered frames

// 4. Stream
let stream = SCStream(filter: filter, configuration: config, delegate: self)   // self: SCStreamDelegate
try stream.addStreamOutput(self, type: .screen, sampleHandlerQueue: DispatchQueue(label: "capture.video"))
try await stream.startCapture()
```

`SCShareableContent` exposes `.displays` (`SCDisplay`: `displayID`, `width`,
`height`, `frame`), `.windows` (`SCWindow`: `windowID`, `frame`, `title`,
`isOnScreen`, `isActive`, `owningApplication`, `windowLayer`), `.applications`
(`SCRunningApplication`: `bundleIdentifier`, `applicationName`, `processID`)
— all read-only metadata snapshots. **Audio filters only at the application
level**, never per-window.

Other `SCContentFilter` initializers: `SCContentFilter(desktopIndependentWindow:)`
follows one window across displays; `SCContentFilter(display:including:)`
captures only specific windows on a display.

## Consent (macOS 14+)

Don't build a custom picker — `SCContentSharingPicker` gives the system
selection UI, Video menu-bar item, Presenter Overlay, and per-stream
re-picking, and hands you a ready-made `SCContentFilter`:

```swift
let picker = SCContentSharingPicker.shared
picker.add(self)                     // self: SCContentSharingPickerObserver
picker.isActive = true
picker.maximumStreamCount = 1
var config = SCContentSharingPickerConfiguration()
config.allowedPickerModes = [.singleWindow, .multipleWindows, .singleApplication, .multipleApplications, .singleDisplay]
picker.defaultConfiguration = config
picker.present()                     // also present(for:) / present(using:) / present(for:using:)

func contentSharingPicker(_ picker: SCContentSharingPicker, didUpdateWith filter: SCContentFilter, for stream: SCStream?) {
    // create a new stream with `filter`, or stream?.updateContentFilter(filter)
}
func contentSharingPicker(_ picker: SCContentSharingPicker, didCancelFor stream: SCStream?) { }
func contentSharingPickerStartDidFailWithError(_ error: Error) { }
```

Implement the cancel/fail callbacks too, or stream state drifts from what the
user actually chose.

## Output callback and threading

Samples arrive on the serial queue you provided — check frame status before
using a video frame, and keep this callback fast (long work here
back-pressures the pipeline and drops frames):

```swift
func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
    switch type {
    case .screen:
        guard let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false)
                as? [[SCStreamFrameInfo: Any]],
              let raw = attachments.first?[.status] as? Int,
              let status = SCFrameStatus(rawValue: raw), status == .complete else { return }
        // sampleBuffer is IOSurface-backed — use now, don't retain past queueDepth
    case .audio:
        break   // PCM audio CMSampleBuffer
    case .microphone:
        break   // macOS 15+
    default:
        break
    }
}
```

`SCStreamFrameInfo` keys: `.status`, `.displayTime`, `.scaleFactor`,
`.contentRect`, `.dirtyRects`, `.contentScale`. `SCFrameStatus` values:
`.complete`, `.idle`, `.blank`, `.suspended`, `.started`, `.stopped` — only
`.complete` carries a usable new frame.

`SCStreamDelegate`: `stream(_:didStopWithError:)`,
`outputEffectDidStart(for:)`/`outputEffectDidStop(for:)` (Presenter Overlay
began/ended, macOS 14+).

## Hot updates

The framework's whole point — adjust quality on the fly instead of tearing
down and recreating the stream:

```swift
try await stream.updateConfiguration(newConfig)     // resolution, fps, audio toggle
try await stream.updateContentFilter(newFilter)      // switch window/display/exclusions
```

## Screenshots (macOS 14+)

Skip the stream for a single frame — `SCScreenshotManager` reuses the same
filter/configuration, class methods, no instance needed:

```swift
let image = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)   // CGImage
let buffer = try await SCScreenshotManager.captureSampleBuffer(contentFilter: filter, configuration: config)  // more pixel formats
```

Replaces `CGWindowListCreateImage`; its window-image options now live on
`SCStreamConfiguration`, "windows above ID" enumeration lives on
`SCShareableContent`.

## Recording to a file (macOS 15+)

`SCRecordingOutput` records the stream straight to a movie — no manual
`AVAssetWriter` plumbing:

```swift
let recordingConfig = SCRecordingOutputConfiguration()
recordingConfig.outputURL = outputURL
recordingConfig.outputFileType = .mov         // AVFileType
recordingConfig.videoCodecType = .h264        // AVVideoCodecType
let recording = SCRecordingOutput(configuration: recordingConfig, delegate: self)   // SCRecordingOutputDelegate
try stream.addRecordingOutput(recording)
try await stream.startCapture()
// recording.recordedDuration / recording.recordedFileSize while running
try stream.removeRecordingOutput(recording)
```

`SCRecordingOutputDelegate`: `recordingOutputDidStartRecording(_:)`,
`recordingOutput(_:didFailWithError:)`, `recordingOutputDidFinishRecording(_:)`.

## Configuration reference (SCStreamConfiguration)

```swift
let config = SCStreamConfiguration()
config.width = 3840; config.height = 2160
config.minimumFrameInterval = CMTime(value: 1, timescale: 60)
config.pixelFormat = kCVPixelFormatType_32BGRA
config.colorSpaceName = CGColorSpace.sRGB
config.showsCursor = true
config.scalesToFit = true
config.queueDepth = 5                    // in-flight frame buffers — memory vs. smoothness
config.capturesShadowsOnly = false

config.capturesAudio = true              // macOS 13+
config.sampleRate = 48_000
config.channelCount = 2
config.excludesCurrentProcessAudio = true

config.captureMicrophone = true          // macOS 15+
config.microphoneCaptureDeviceID = nil
config.captureDynamicRange = .hdrLocalDisplay   // .sdr | .hdrLocalDisplay | .hdrCanonicalDisplay
config.showMouseClicks = true
```

## Migration

| Deprecated | Replacement |
|---|---|
| `CGDisplayStream` | `SCStream` with a display `SCContentFilter` |
| `CGWindowListCreateImage` | `SCScreenshotManager.captureImage(contentFilter:configuration:)` |
| `AVCaptureScreenInput` (superseded, not deprecated) | `SCStream` |
| Manual `AVAssetWriter` for screen recording | `SCRecordingOutput` (macOS 15+) |

## Common mistakes

- Shipping screen capture on iOS — that's ReplayKit, not ScreenCaptureKit.
- Not handling the empty `SCShareableContent` case when permission is denied.
- Doing real work (encoding, disk I/O, UI updates) directly on the sample-handler queue.
- Retaining IOSurface-backed video buffers past the queue depth — exhausts the pool, stalls capture.
- Ignoring `SCStreamFrameInfo.status` and treating `.idle` frames as new content.
- Capturing your own app's window inside a display filter instead of excluding it.
- Hand-rolling a selection UI instead of `SCContentSharingPicker`.
