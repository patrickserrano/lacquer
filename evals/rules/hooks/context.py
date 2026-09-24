#!/usr/bin/env python3
"""Deliver the case profile's real CLAUDE render, never the AGENTS inventory."""
import json
import os
from pathlib import Path
import sys

root = Path(os.environ["CLAUDE_PLUGIN_ROOT"])
session = json.load(sys.stdin)
profile_file = Path(session["cwd"]) / ".fixture/profile"
profile = profile_file.read_text().strip() if profile_file.exists() else "core"
if profile not in {"core", "ios", "web", "supabase", "marketing"}:
    raise SystemExit("unknown eval fixture profile: " + profile)
print(json.dumps({"hookSpecificOutput": {
    "hookEventName": "SessionStart",
    "additionalContext": (root / "contexts" / profile / "CLAUDE.md").read_text(),
}}))
