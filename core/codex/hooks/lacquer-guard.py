#!/usr/bin/env python3
"""Codex PreToolUse guard. Input/output contract: learn.chatgpt.com/docs/hooks.

This is a guardrail for visible shell commands and apply_patch paths, not a
sandbox: scripts, aliases, MCP edits and later terminal input need AGENTS.md.
"""

import json
import os
import re
import shlex
import sys


def deny(reason):
    print(json.dumps({"hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": "deny",
        "permissionDecisionReason": reason,
    }}))


def check(event):
    tool = event.get("tool_name")
    command = event.get("tool_input", {}).get("command")
    if not isinstance(command, str):
        return "Cannot inspect tool input; expected tool_input.command."
    if tool == "Bash":
        try:
            words = shlex.split(command.replace("\\\n", ""))
        except ValueError:
            return "Cannot inspect malformed shell quoting."
        # Conservative lexical parity with the existing command guard: flags
        # may follow global options or occur inside a shell wrapper.
        invokes_git = any(re.search(r"\b(?:git|gh)\b", w) for w in words)
        forbidden = re.compile(r"--(?:force-with-lease|force|no-verify|admin)\b")
        if invokes_git and (forbidden.search(command) or "-f" in words):
            return "Do not bypass git/gh safety: --force, --force-with-lease, -f, --no-verify or --admin."
    elif tool == "apply_patch":
        for line in command.splitlines():
            match = re.match(r"^\*\*\* (?:Add File|Update File|Delete File|Move to): (.+)$", line)
            if not match:
                continue
            path = os.path.normpath(match.group(1).strip())
            if path.endswith(".entitlements"):
                return "Do not modify .entitlements without explicit operator permission."
            if re.search(r"\.(?:pbxproj|xcworkspace|xib|storyboard)$|\.(?:xcodeproj|xcworkspace)/", path):
                return ("Do not hand-edit project files. Use XcodeGen or ask the operator for structural changes. "
                        "Find the version source first; use scripts/bump-marketing-version.sh only for "
                        "MARKETING_VERSION lines in a hand-maintained pbxproj.")
    return None


if __name__ == "__main__":
    try:
        reason = check(json.load(sys.stdin))
    except (ValueError, TypeError, AttributeError):
        reason = "Cannot inspect malformed safety-check input."
    if reason:
        deny(reason)
