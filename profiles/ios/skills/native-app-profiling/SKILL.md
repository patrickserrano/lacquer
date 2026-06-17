---
name: native-app-profiling
description: Profile native macOS/iOS apps using Time Profiler via CLI (xctrace). Use when asked to identify performance hotspots, profile CPU usage, or diagnose slow code paths without opening Instruments.
---

# Native App Performance Profiling (CLI)

## Overview

Record Time Profiler via `xctrace`, extract samples, symbolicate, and identify hotspots without opening Instruments.

## Quick Start

### 1) Record Time Profiler

**Attach to running process:**
```bash
# Get the PID first
pgrep -x "AppName"

# Record for 90 seconds
xcrun xctrace record \
    --template 'Time Profiler' \
    --time-limit 90s \
    --output /tmp/App.trace \
    --attach <pid>
```

**Launch and record:**
```bash
xcrun xctrace record \
    --template 'Time Profiler' \
    --time-limit 90s \
    --output /tmp/App.trace \
    --launch -- /path/to/App.app/Contents/MacOS/App
```

### 2) Export Time Samples

List available schemas in the trace:
```bash
xcrun xctrace export --input /tmp/App.trace --toc
```

Export time profile data:
```bash
xcrun xctrace export \
    --input /tmp/App.trace \
    --xpath '/trace-toc/run/data/table[@schema="time-profile"]' \
    --output /tmp/time-profile.xml
```

### 3) Get Load Address for Symbolication

While the app is running, get the `__TEXT` segment load address:
```bash
vmmap <pid> | grep "__TEXT"
```

Look for the load address (typically starts with `0x1...`).

### 4) Symbolicate Stack Frames

Use `atos` to symbolicate addresses:
```bash
atos -o /path/to/App.app/Contents/MacOS/App -l 0x100000000 <address>
```

## Workflow Notes

- **Correct binary**: Confirm you're profiling the right build (local vs /Applications)
- **Trigger the slow path**: During capture, perform the action that's slow
- **Capture duration**: If stacks are empty, capture longer or avoid idle time
- **Symbol matching**: Binary symbols must match the trace (same build)

## Available Templates

List all profiling templates:
```bash
xcrun xctrace list templates
```

Common templates:
- `Time Profiler` - CPU sampling
- `Allocations` - Memory allocations
- `Leaks` - Memory leak detection
- `System Trace` - System-level activity
- `Animation Hitches` - UI performance

## Common Commands

| Task | Command |
|------|---------|
| List templates | `xcrun xctrace list templates` |
| List devices | `xcrun xctrace list devices` |
| Record help | `xcrun xctrace help record` |
| Export help | `xcrun xctrace help export` |
| Get PID | `pgrep -x "AppName"` |
| Get load address | `vmmap <pid> \| grep __TEXT` |
| Symbolicate | `atos -o <binary> -l <load-addr> <address>` |

## Analyzing Results

### Manual Analysis

1. Export the trace to XML
2. Parse the call tree data
3. Look for frames with high sample counts
4. Focus on your app's code (filter out system frameworks)

### Identify Hotspots

Look for:
- Functions with high self-time (time spent in function itself)
- Deep call stacks indicating inefficient algorithms
- Repeated patterns suggesting optimization opportunities

## Gotchas

- **ASLR**: Runtime `__TEXT` load address changes each launch - get it from `vmmap`
- **Build mismatch**: Symbols must match the exact build that was profiled
- **Idle time**: Profiling idle app produces empty/useless data
- **Permissions**: May need to run with `sudo` for some operations

## iOS Profiling

For iOS apps on simulator:
```bash
xcrun xctrace record \
    --template 'Time Profiler' \
    --device <simulator-udid> \
    --time-limit 60s \
    --output /tmp/iOS-App.trace \
    --launch -- <bundle-id>
```

Get simulator UDID:
```bash
xcrun simctl list devices | grep Booted
```

## Automation Script

Basic recording script:
```bash
#!/bin/bash
set -e

APP_NAME="$1"
DURATION="${2:-60}"
OUTPUT="${3:-/tmp/$APP_NAME.trace}"

if [ -z "$APP_NAME" ]; then
    echo "Usage: $0 <app-name> [duration-seconds] [output-path]"
    exit 1
fi

PID=$(pgrep -x "$APP_NAME" || true)

if [ -n "$PID" ]; then
    echo "Attaching to running $APP_NAME (PID: $PID)"
    xcrun xctrace record \
        --template 'Time Profiler' \
        --time-limit "${DURATION}s" \
        --output "$OUTPUT" \
        --attach "$PID"
else
    echo "App not running. Please start $APP_NAME first."
    exit 1
fi

echo "Trace saved to: $OUTPUT"
echo "To analyze: xcrun xctrace export --input $OUTPUT --toc"
```

## Checklist

- [ ] Correct binary path identified
- [ ] App running or launch command ready
- [ ] Slow path reproducible
- [ ] Trace recorded during problematic behavior
- [ ] Load address captured for symbolication
- [ ] Results analyzed for hotspots
