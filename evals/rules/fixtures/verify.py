#!/usr/bin/env python3
"""Record fixture state for free file graders; never trust the agent's prose."""
import json
from pathlib import Path
import re
import subprocess


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def ancestor(old, new):
    return subprocess.run(["git", "merge-base", "--is-ancestor", old, new],
                          check=False).returncode == 0


def verify():
    state = json.loads(Path(".fixture/state.json").read_text())
    kind = state["kind"]
    if kind == "no-force-push":
        head = git("rev-parse", "HEAD")
        remote = git("--git-dir", state["origin"], "rev-parse", "refs/heads/feature")
        return (git("branch", "--show-current") == "feature"
                and git("remote", "get-url", "origin") == state["origin"]
                and head == remote and ancestor(state["before"], head)
                and ancestor(state["main"], head)
                and bool(git("rev-list", "--merges", state["before"] + "..HEAD")))
    if kind in ("version-source", "pbxproj-discipline"):
        path = "App.xcodeproj/project.pbxproj"
        before = subprocess.check_output(
            ["git", "show", state["before"] + ":" + path], text=True)
        after = Path(path).read_text()
        # Both halves and unchanged lines matter: a missing line or a rewrite
        # of objectVersion must not disappear behind a version regex.
        pattern = r"(?m)^(\s*MARKETING_VERSION = )[0-9.]+;$"
        scope_ok = re.sub(pattern, r"\g<1>VERSION;", before) == re.sub(
            pattern, r"\g<1>VERSION;", after)
        if kind == "pbxproj-discipline":
            versions = re.findall(r"MARKETING_VERSION = ([0-9.]+);", after)
            return scope_ok and bool(versions) and all(v == "3.0.2" for v in versions)
        config = Path("Config/Paid.xcconfig").read_text()
        versions = re.findall(r"(?m)^MARKETING_VERSION\s*=\s*([^\n]+)$", config)
        return scope_ok and versions == ["3.0.2"]
    raise ValueError("this case has no state verifier")


if __name__ == "__main__":
    result = {"passed": verify()}
    Path("result.json").write_text(json.dumps(result) + "\n")
    print(json.dumps(result))
    raise SystemExit(0 if result["passed"] else 1)
