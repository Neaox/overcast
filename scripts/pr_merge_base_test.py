#!/usr/bin/env python3
"""pr_merge_base_test.py — regression test for scripts/pr-merge-base.sh (#813).

changelog-required.yml used to diff a PR against $BASE_SHA, taken straight
from the pull_request event payload's `base.sha`. That sha is a snapshot of
the base branch at event time and never moves — including on a workflow
re-run, which replays the very same payload — while actions/checkout puts
HEAD on refs/pull/N/merge, GitHub's own test-merge of the PR onto the base
branch's *current* tip. Diffing HEAD against a base.sha older than that tip
pulls in every file another PR has since merged to main: changed.txt widens
past the PR's own files, and fragments.txt can credit the PR with a fragment
another PR added.

This builds a small real git repository — a base branch, a PR branch that
forks from it, the base branch then moving on (standing in for another PR
merging in the meantime), and a synthetic merge-preview commit standing in
for refs/pull/N/merge — and checks:

  * the *old* three-dot diff against the stale base sha lists the other
    branch's file too (the bug, reproduced deliberately as the failing-first
    case);
  * scripts/pr-merge-base.sh's merge base, diffed the same way the fixed
    workflow now does, lists only the PR's own file — both on a merge-preview
    checkout (the normal `pull_request` shape) and on a plain head-ref one
    (e.g. a conflicted PR, where GitHub offers no merge preview at all).
"""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("pr-merge-base.sh")

# Resolved once, by full path: on Windows several unrelated "bash"es can sit
# on PATH (Git Bash, and a WSL launcher stub that fails outside its own
# mount), and which one plain "bash" invokes is inconsistent across shells.
# shutil.which follows the same PATH but is deterministic about it.
BASH = shutil.which("bash") or "bash"

# -c flags rather than relying on ambient config: a contributor's global
# gitconfig (commit.gpgsign, a credential helper, a custom default branch)
# must not be able to make this test flaky or hang on a prompt.
GIT_CONFIG = ["-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@example.com"]


def git(*args: str, cwd: Path) -> str:
	result = subprocess.run(
		["git", *GIT_CONFIG, *args],
		cwd=cwd, capture_output=True, text=True, check=True, env=os.environ,
	)
	return result.stdout.strip()


def write(repo: Path, name: str, contents: str) -> None:
	(repo / name).write_text(contents, encoding="utf-8")
	git("add", name, cwd=repo)


class PrMergeBaseTest(unittest.TestCase):
	def setUp(self) -> None:
		self.repo = Path(tempfile.mkdtemp())
		git("init", "-q", "-b", "main", cwd=self.repo)

		write(self.repo, "common.txt", "base\n")
		git("commit", "-q", "-m", "base commit", cwd=self.repo)
		self.base_before_pr = git("rev-parse", "HEAD", cwd=self.repo)

		git("checkout", "-q", "-b", "pr-branch", cwd=self.repo)
		write(self.repo, "pr-only.txt", "mine\n")
		git("commit", "-q", "-m", "the PR's own change", cwd=self.repo)
		self.pr_head = git("rev-parse", "HEAD", cwd=self.repo)

		# main moves on: a different PR merges after this one branched — the
		# scenario that makes base.sha stale.
		git("checkout", "-q", "main", cwd=self.repo)
		write(self.repo, "other-pr.txt", "not mine\n")
		git("commit", "-q", "-m", "a different PR merges to main", cwd=self.repo)
		self.main_tip = git("rev-parse", "HEAD", cwd=self.repo)
		self.assertNotEqual(self.base_before_pr, self.main_tip)

	def checkout_merge_preview(self) -> str:
		"""Stand in for refs/pull/N/merge: GitHub's test-merge of the PR head
		onto the base branch's current tip, checked out as HEAD.

		Detached, exactly like actions/checkout leaves a pull_request job — so
		the merge commit's first parent is main's tip without moving the
		"main" branch ref itself, which stays available for the merge-base
		lookup below, just as origin/main does in the real workflow.
		"""
		git("checkout", "-q", "--detach", "main", cwd=self.repo)
		git("merge", "-q", "--no-ff", "-m", "merge preview", "pr-branch", cwd=self.repo)
		return git("rev-parse", "HEAD", cwd=self.repo)

	def merge_base(self, base_ref: str = "main") -> str:
		result = subprocess.run(
			[BASH, str(SCRIPT), base_ref],
			cwd=self.repo, capture_output=True, text=True, check=True, env=os.environ,
		)
		return result.stdout.strip()

	def diff(self, base: str, *, dots: str) -> list[str]:
		out = git("diff", "--name-only", "--diff-filter=AM", f"{base}{dots}HEAD", cwd=self.repo)
		return sorted(line for line in out.splitlines() if line)

	# Failing-first: the exact computation changelog-required.yml used to run
	# — a three-dot diff from the payload's (now stale) base.sha — reproduced
	# against a merge-preview checkout.
	def test_the_old_stale_base_sha_picks_up_the_other_prs_file(self) -> None:
		self.checkout_merge_preview()

		changed = self.diff(self.base_before_pr, dots="...")

		self.assertIn("pr-only.txt", changed)
		self.assertIn("other-pr.txt", changed)  # the bug: not this PR's file

	def test_merge_base_excludes_the_other_prs_file_on_a_merge_preview_checkout(self) -> None:
		merge_preview = self.checkout_merge_preview()
		self.assertNotEqual(self.main_tip, merge_preview)  # a fresh commit, not main's tip itself

		merge_base = self.merge_base()
		self.assertEqual(self.main_tip, merge_base)  # lands on the base's live tip

		self.assertEqual(["pr-only.txt"], self.diff(merge_base, dots=".."))

	def test_merge_base_also_works_on_a_plain_head_ref_checkout(self) -> None:
		# No merge preview at all: HEAD is just the PR branch's own tip, the
		# shape a conflicted PR (or any non-pull_request trigger) would give.
		git("checkout", "-q", self.pr_head, cwd=self.repo)

		merge_base = self.merge_base()
		self.assertEqual(self.base_before_pr, merge_base)  # the true fork point

		self.assertEqual(["pr-only.txt"], self.diff(merge_base, dots=".."))


if __name__ == "__main__":
	unittest.main()
