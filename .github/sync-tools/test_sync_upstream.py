"""Exercise synchronization against temporary Git remotes, never GitHub."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("sync-upstream-main.sh").resolve()


class SyncUpstreamTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.env = {
            **os.environ,
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_AUTHOR_NAME": "Sync Test",
            "GIT_AUTHOR_EMAIL": "sync-test@example.invalid",
            "GIT_COMMITTER_NAME": "Sync Test",
            "GIT_COMMITTER_EMAIL": "sync-test@example.invalid",
            "GITHUB_STEP_SUMMARY": str(self.root / "summary.md"),
        }
        self.source = self.root / "source"
        self.fork = self.root / "fork.git"
        self.upstream = self.root / "upstream.git"
        self.git("init", "--quiet", "--initial-branch=main", str(self.source))
        self.base = self.commit("base")
        for remote in (self.fork, self.upstream):
            self.git("clone", "--quiet", "--bare", str(self.source), str(remote))
        self.release = self.commit("fork release customization")
        self.git("-C", str(self.source), "push", str(self.fork), "HEAD:refs/heads/release")
        self.git("-C", str(self.source), "reset", "--hard", self.base)

    def git(self, *args):
        return subprocess.check_output(
            ["git", *args], env=self.env, text=True, stderr=subprocess.PIPE
        ).strip()

    def commit(self, text):
        (self.source / "file.txt").write_text(text + "\n")
        self.git("-C", str(self.source), "add", "file.txt")
        self.git("-C", str(self.source), "commit", "--quiet", "-m", text)
        return self.git("-C", str(self.source), "rev-parse", "HEAD")

    def advance(self, remote, text):
        sha = self.commit(text)
        self.git("-C", str(self.source), "push", str(remote), "HEAD:refs/heads/main")
        return sha

    def run_sync(self, success):
        result = subprocess.run(
            ["bash", str(SCRIPT), str(self.fork), str(self.upstream)],
            env=self.env, text=True, capture_output=True,
        )
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        self.assertEqual(self.git("-C", str(self.fork), "rev-parse", "release"), self.release)
        self.assertEqual(
            self.git("-C", str(self.fork), "for-each-ref", "--format=%(refname)"),
            "refs/heads/main\nrefs/heads/release",
        )
        return result

    def test_no_updates(self):
        result = self.run_sync(True)
        self.assertIn("Already up to date", result.stdout)
        self.assertEqual(self.git("-C", str(self.fork), "rev-parse", "main"), self.base)

    def test_fast_forward_preserves_exact_upstream_commit(self):
        upstream_sha = self.advance(self.upstream, "upstream update")
        result = self.run_sync(True)
        self.assertIn("Synchronized 1 upstream commit(s)", result.stdout)
        self.assertEqual(self.git("-C", str(self.fork), "rev-parse", "main"), upstream_sha)

    def test_fork_ahead_is_rejected(self):
        fork_sha = self.advance(self.fork, "fork-only update")
        self.run_sync(False)
        self.assertEqual(self.git("-C", str(self.fork), "rev-parse", "main"), fork_sha)

    def test_diverged_history_is_rejected(self):
        fork_sha = self.advance(self.fork, "fork-only update")
        self.git("-C", str(self.source), "reset", "--hard", self.base)
        self.advance(self.upstream, "upstream update")
        self.run_sync(False)
        self.assertEqual(self.git("-C", str(self.fork), "rev-parse", "main"), fork_sha)

    def test_rejected_push_does_not_change_branches(self):
        self.advance(self.upstream, "upstream update")
        hook = self.fork / "hooks" / "pre-receive"
        hook.write_text("#!/bin/sh\nexit 1\n")
        hook.chmod(0o755)
        result = self.run_sync(False)
        self.assertIn("push was rejected or failed", result.stdout)
        self.assertEqual(self.git("-C", str(self.fork), "rev-parse", "main"), self.base)


if __name__ == "__main__":
    unittest.main()
