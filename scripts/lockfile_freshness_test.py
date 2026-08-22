#!/usr/bin/env python3
"""Tests for scripts/lockfile-freshness.py.

The decision table is tested on its own, and then against real git history in
a throwaway repository — the merge-base and blob plumbing is the part that
could be subtly wrong (#813 was a changed-file list computed from the wrong
base), and it is cheap to exercise for real.

Run: python scripts/lockfile_freshness_test.py   (CI runs every scripts/*_test.py)
"""

from __future__ import annotations

import importlib.util
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("lockfile-freshness.py")
SPEC = importlib.util.spec_from_file_location("lockfile_freshness", SCRIPT)
assert SPEC is not None
lf = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
# Registered before exec, as changelog_required_test does: a hyphenated
# filename cannot be imported any other way.
sys.modules["lockfile_freshness"] = lf
SPEC.loader.exec_module(lf)

LOCK = "web/pnpm-lock.yaml"


class Verdict(unittest.TestCase):
	"""The decision table for one path."""

	def test_untouched_is_never_stale(self) -> None:
		# Whatever main did to the file, a PR that does not touch it merges
		# main's copy, which is consistent with main by construction.
		self.assertEqual(lf.verdict(False, "aaa", "bbb"), lf.VERDICT_NOT_APPLICABLE)
		self.assertEqual(lf.verdict(False, "aaa", "aaa"), lf.VERDICT_NOT_APPLICABLE)
		self.assertEqual(lf.verdict(False, None, "aaa"), lf.VERDICT_NOT_APPLICABLE)

	def test_touched_and_main_unchanged_is_fresh(self) -> None:
		self.assertEqual(lf.verdict(True, "aaa", "aaa"), lf.VERDICT_FRESH)

	def test_touched_and_main_moved_is_stale(self) -> None:
		self.assertEqual(lf.verdict(True, "bbb", "aaa"), lf.VERDICT_STALE)

	def test_file_new_on_main_since_branching_is_stale(self) -> None:
		# The PR adds the file and so did main, independently: two generated
		# copies, the case this check exists for.
		self.assertEqual(lf.verdict(True, "bbb", None), lf.VERDICT_STALE)

	def test_file_absent_on_both_is_fresh(self) -> None:
		# Only the PR adds it; main never had one to disagree with.
		self.assertEqual(lf.verdict(True, None, None), lf.VERDICT_FRESH)

	def test_combine_is_worst_of(self) -> None:
		na, fresh, stale = lf.VERDICT_NOT_APPLICABLE, lf.VERDICT_FRESH, lf.VERDICT_STALE
		self.assertEqual(lf.combine([na, na]), na)
		self.assertEqual(lf.combine([na, fresh]), fresh)
		self.assertEqual(lf.combine([fresh, stale]), stale)
		self.assertEqual(lf.combine([stale, na]), stale)
		self.assertEqual(lf.combine([]), na)


@unittest.skipUnless(shutil.which("git"), "git is not on PATH")
class AgainstGit(unittest.TestCase):
	"""The plumbing, against a real repository."""

	def setUp(self) -> None:
		self.tmp = tempfile.mkdtemp(prefix="lockfile-freshness-")
		self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
		self.git("init", "-q", "-b", "main")
		self.git("config", "user.email", "t@example.invalid")
		self.git("config", "user.name", "test")
		self.git("config", "commit.gpgsign", "false")
		self.write("README.md", "hello\n")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 1.0.0\n")
		self.commit("initial")

	# -- helpers -----------------------------------------------------------

	def git(self, *args: str) -> str:
		return subprocess.run(
			["git", *args], cwd=self.tmp, check=True, capture_output=True, text=True
		).stdout.strip()

	def write(self, rel: str, text: str) -> None:
		path = Path(self.tmp, rel)
		path.parent.mkdir(parents=True, exist_ok=True)
		path.write_text(text, encoding="utf-8")

	def commit(self, msg: str) -> str:
		self.git("add", "-A")
		self.git("commit", "-q", "-m", msg)
		return self.git("rev-parse", "HEAD")

	def branch(self, name: str, start: str = "main") -> None:
		self.git("switch", "-q", "-c", name, start)

	def checkout(self, name: str) -> None:
		self.git("switch", "-q", name)

	def judge(self, head: str, base: str = "main") -> str:
		overall, _per_path, _mb = lf.judge(head, base, (LOCK,), cwd=self.tmp)
		return overall

	def run_cli(self, *args: str) -> subprocess.CompletedProcess[str]:
		return subprocess.run(
			[sys.executable, str(SCRIPT), "check", "-C", self.tmp, *args],
			capture_output=True,
			text=True,
		)

	# -- cases -------------------------------------------------------------

	def test_pr_not_touching_lockfile_is_not_applicable_even_when_main_moved(self) -> None:
		self.branch("pr")
		self.write("README.md", "changed\n")
		self.commit("docs")
		self.checkout("main")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 2.0.0\n")
		self.commit("bump a on main")
		self.assertEqual(self.judge("pr"), lf.VERDICT_NOT_APPLICABLE)

	def test_pr_touching_lockfile_with_main_unchanged_is_fresh(self) -> None:
		self.branch("pr")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 1.0.0\n    b: 1.0.0\n")
		self.commit("add b")
		self.checkout("main")
		self.write("README.md", "main moved, but not the lock\n")
		self.commit("docs on main")
		self.assertEqual(self.judge("pr"), lf.VERDICT_FRESH)

	def test_pr_touching_lockfile_after_main_regenerated_it_is_stale(self) -> None:
		# The 2026-08-22 shape: two branches each regenerate the lock from the
		# same starting point; the first lands, the second is now stale. Git
		# would merge these two texts cleanly, which is exactly why the check
		# cannot be "does it conflict".
		self.branch("pr")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 1.0.0\n    b: 1.0.0\n")
		self.commit("add b")
		self.checkout("main")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 2.0.0\n")
		self.commit("bump a on main")
		self.assertEqual(self.judge("pr"), lf.VERDICT_STALE)

	def test_rebased_pr_is_fresh_again(self) -> None:
		self.branch("pr")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 1.0.0\n    b: 1.0.0\n")
		self.commit("add b")
		self.checkout("main")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 2.0.0\n")
		self.commit("bump a on main")
		self.assertEqual(self.judge("pr"), lf.VERDICT_STALE)
		# Rebase and regenerate: the branch's merge-base is now main's tip.
		self.checkout("pr")
		self.git("rebase", "-q", "-X", "theirs", "main")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 2.0.0\n    b: 1.0.0\n")
		self.commit("regenerate")
		self.assertEqual(self.judge("pr"), lf.VERDICT_FRESH)

	def test_merging_main_in_is_as_good_as_rebasing(self) -> None:
		self.branch("pr")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 1.0.0\n    b: 1.0.0\n")
		self.commit("add b")
		self.checkout("main")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 2.0.0\n")
		self.commit("bump a on main")
		self.checkout("pr")
		self.git("merge", "-q", "-X", "ours", "--no-edit", "main")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 2.0.0\n    b: 1.0.0\n")
		self.commit("regenerate after merge")
		self.assertEqual(self.judge("pr"), lf.VERDICT_FRESH)

	def test_main_changing_the_lock_and_changing_it_back_is_fresh(self) -> None:
		# Judged by blob, not by history: the bytes the PR generated against
		# are the bytes main has again.
		self.branch("pr")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 1.0.0\n    b: 1.0.0\n")
		self.commit("add b")
		self.checkout("main")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 2.0.0\n")
		self.commit("bump a on main")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 1.0.0\n")
		self.commit("revert a on main")
		self.assertEqual(self.judge("pr"), lf.VERDICT_FRESH)

	def test_cli_prints_only_the_verdict_on_stdout_and_exits_zero(self) -> None:
		self.branch("pr")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 1.0.0\n    b: 1.0.0\n")
		self.commit("add b")
		self.checkout("main")
		self.write(LOCK, "lockfileVersion: '9.0'\nimporters:\n  .:\n    a: 2.0.0\n")
		self.commit("bump a on main")
		details = os.path.join(self.tmp, "details.md")
		proc = self.run_cli("--head", "pr", "--base", "main", "--details-file", details)
		self.assertEqual(proc.returncode, 0, proc.stderr)
		self.assertEqual(proc.stdout.strip(), lf.VERDICT_STALE)
		text = Path(details).read_text(encoding="utf-8")
		self.assertIn("**stale**", text)
		self.assertIn("pnpm install", text)
		self.assertIn(LOCK, text)

	def test_cli_not_applicable_details_say_so(self) -> None:
		self.branch("pr")
		self.write("README.md", "changed\n")
		self.commit("docs")
		details = os.path.join(self.tmp, "details.md")
		proc = self.run_cli("--head", "pr", "--base", "main", "--details-file", details)
		self.assertEqual(proc.returncode, 0, proc.stderr)
		self.assertEqual(proc.stdout.strip(), lf.VERDICT_NOT_APPLICABLE)
		self.assertIn("does not change a lockfile", Path(details).read_text(encoding="utf-8"))

	def test_cli_git_failure_is_exit_two_not_a_verdict(self) -> None:
		proc = self.run_cli("--head", "no-such-ref", "--base", "main")
		self.assertEqual(proc.returncode, 2)
		self.assertEqual(proc.stdout.strip(), "")
		self.assertIn("merge-base", proc.stderr)

	def test_extra_paths_are_judged_too(self) -> None:
		other = "web/api/pnpm-lock.yaml"
		self.write(other, "x: 1\n")
		self.commit("second lock on main")
		self.branch("pr")
		self.write(other, "x: 2\n")
		self.commit("pr regenerates the second lock")
		self.checkout("main")
		self.write(other, "x: 3\n")
		self.commit("main regenerates the second lock")
		overall, per_path, _ = lf.judge("pr", "main", (LOCK, other), cwd=self.tmp)
		self.assertEqual(overall, lf.VERDICT_STALE)
		self.assertEqual(dict(per_path)[LOCK], lf.VERDICT_NOT_APPLICABLE)
		self.assertEqual(dict(per_path)[other], lf.VERDICT_STALE)


if __name__ == "__main__":
	unittest.main()
