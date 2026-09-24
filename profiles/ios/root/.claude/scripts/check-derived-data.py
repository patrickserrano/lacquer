#!/usr/bin/env python3
"""Require explicit DerivedData on literal build-tool commands in Bash hooks.

Inspect each shell command separately: a flag on echo or on a previous build
must not authorize the next one. This is an invocation guard, not a shell
sandbox: commands hidden inside scripts or dynamically constructed names are
outside PreToolUse's view. Never execute the submitted command to inspect it.
"""

import json
import os
import re
import shlex
import sys


def missing_path(words):
    """Recognize direct invocations and common launchers, not echoed examples."""
    while words:
        name = os.path.basename(words[0])
        if re.match(r"^[A-Za-z_][A-Za-z_0-9]*=", words[0]):
            words = words[1:]
        elif name in ("env", "command", "exec", "sudo", "xcrun", "if", "then", "elif", "while", "until", "do", "!"):
            words = words[1:]
            # Common launcher options that take an argument.
            while words and words[0].startswith("-"):
                takes_value = words[0] in ("-u", "--unset", "--sdk", "--toolchain", "-user")
                words = words[2 if takes_value else 1:]
        else:
            break
    if not words:
        return False
    name = os.path.basename(words[0])
    if name in ("bash", "sh", "zsh"):
        for i, word in enumerate(words[1:], 1):
            if word.startswith("-") and "c" in word and i + 1 < len(words):
                return denied(words[i + 1])
        return False
    if name == "flowdeck":
        subcommand = next((word for word in words[1:] if not word.startswith("-")), "")
        if subcommand not in ("build", "run", "test", "clean"):
            return False
        flags = ("-d", "--derived-data-path")
    elif name == "xcodebuild":
        flags = ("-derivedDataPath",)
    else:
        return False
    for i, word in enumerate(words[1:], 1):
        if word in flags and i + 1 < len(words):
            if words[i + 1] and not words[i + 1].startswith("-"):
                return False
        if any(word.startswith(flag + "=") and word[len(flag) + 1:] for flag in flags):
            return False
    return True


def denied(command):
    lexer = shlex.shlex(command.replace("\\\n", ""), posix=True, punctuation_chars=";&|()<>\n")
    lexer.whitespace = " \t\r"
    words = []
    for word in lexer:
        if word and all(c in ";&|()<>\n" for c in word):
            if missing_path(words):
                return True
            words = []
        else:
            words.append(word)
    return missing_path(words)


def main():
    try:
        command = json.load(sys.stdin).get("tool_input", {}).get("command", "")
        blocked = denied(command)
    except (ValueError, TypeError, AttributeError):
        print("BLOCKED: cannot inspect Bash command for an explicit DerivedData path.", file=sys.stderr)
        return 2
    if blocked:
        print('BLOCKED: build data must stay in the worktree. Add '
              '-d "$(git rev-parse --show-toplevel)/DerivedData" to flowdeck '
              'build/run/test/clean, or -derivedDataPath '
              '"$(git rev-parse --show-toplevel)/DerivedData" to xcodebuild '
              '(including -showBuildSettings, -list, and -resolvePackageDependencies).',
              file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
