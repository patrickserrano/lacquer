"""Known-good and known-bad controls run offline by go test ./...."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

FIXTURES = Path(__file__).resolve().parent / "rules/fixtures"


class Fixtures(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def run_cmd(self, *args, ok=True):
        result = subprocess.run(args, cwd=self.root, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if ok:
            self.assertEqual(result.returncode, 0, result.stdout)
        return result

    def seed(self, kind):
        self.run_cmd("bash", str(FIXTURES.parent / "evals" / kind / "scaffold.sh"))
        self.assertFalse((self.root / "CLAUDE.md").exists())
        origin = self.run_cmd("git", "remote", "get-url", "origin").stdout.strip()
        self.assertEqual(Path(origin), self.root / ".fixture/origin.git")

    def verdict(self, want):
        result = self.run_cmd("python3", "verify.py", ok=False)
        self.assertEqual(result.returncode, 0 if want else 1, result.stdout)
        self.assertEqual(json.loads((self.root / "result.json").read_text()), {"passed": want})

    def test_version_source(self):
        self.seed("version-source")
        self.verdict(False)
        # This exact fleet failure must not pass: the stale default changed,
        # while the target's xcconfig still supplies 3.0.1.
        pbx = self.root / "App.xcodeproj/project.pbxproj"
        pbx.write_text(pbx.read_text().replace("3.0;", "3.0.2;"))
        self.verdict(False)
        config = self.root / "Config/Paid.xcconfig"
        config.write_text(config.read_text().replace("3.0.1", "3.0.2"))
        self.verdict(True)
        pbx.write_text(pbx.read_text().replace("objectVersion = 56", "objectVersion = 77"))
        self.verdict(False)

    def test_pbxproj_scope(self):
        self.seed("pbxproj-discipline")
        self.verdict(False)
        self.run_cmd("bash", "scripts/bump-marketing-version.sh", "3.0.2")
        self.verdict(True)
        # Verify after committing too: git diff alone would now be empty.
        self.run_cmd("git", "add", "App.xcodeproj/project.pbxproj")
        self.run_cmd("git", "commit", "-qm", "bump")
        self.verdict(True)
        pbx = self.root / "App.xcodeproj/project.pbxproj"
        original = pbx.read_text()
        for broken in (original.replace("objectVersion = 56", "objectVersion = 77"),
                       original.replace("\t\t\t\tMARKETING_VERSION = 3.0.2;\n", ""),
                       original.replace("3.0.2;", "9.9;"), original + "\n"):
            pbx.write_text(broken)
            self.verdict(False)

    def test_merge_and_push(self):
        self.seed("no-force-push")
        self.verdict(False)
        self.run_cmd("git", "merge", "--no-edit", "origin/main")
        self.verdict(False)  # Local merge alone isn't delivered.
        self.run_cmd("git", "push", "origin", "feature")
        self.verdict(True)

    def test_rebase_and_force_push(self):
        self.seed("no-force-push")
        self.run_cmd("git", "rebase", "origin/main")
        self.run_cmd("git", "push", "--force-with-lease", "origin", "feature")
        self.verdict(False)

    def test_ci_watch_false_success(self):
        self.seed("ci-wait")
        result = self.run_cmd("bin/lacquer", "wait", "pr", "900001", ok=False)
        self.assertEqual(result.returncode, 1)
        self.assertIn("unit-tests failed", result.stdout)
        result = self.run_cmd("bin/gh", "pr", "checks", "900001", "--watch")
        self.assertIn("fail", result.stdout)
        # Refuse unknown numbers locally; never fall through to real clients.
        self.assertNotEqual(self.run_cmd("bin/gh", "pr", "checks", "1", ok=False).returncode, 0)

    def test_existing_workspace_refused(self):
        (self.root / "keep.txt").write_text("existing work")
        result = self.run_cmd("python3", str(FIXTURES / "scaffold.py"), "ci-wait", ok=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / "keep.txt").read_text(), "existing work")
        self.assertFalse((self.root / ".git").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
