#!/usr/bin/env python3

"""Tests for image-tag.{sh,ps1} — the per-branch Docker tag.

The tag decides which image `make docker-console` writes and which one the test
instance then runs, so a sanitiser that produces the same string for two
different branches reintroduces exactly the collision it was added to remove —
and does it silently, because the container starts either way.

The cases that matter are the ones a branch name really contains: a slash,
uppercase, and no branch at all. Each is driven against a throwaway git repo so
the assertion is about git's real output rather than a guess at it, and both
halves of the pair are checked to agree.

Run: python scripts/image_tag_test.py   (CI runs every scripts/*_test.py)
"""

from __future__ import annotations

import importlib.util
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent
SH = SCRIPTS / "image-tag.sh"
PS1 = SCRIPTS / "image-tag.ps1"


def _load(path: Path, name: str):
	spec = importlib.util.spec_from_file_location(name, path)
	assert spec is not None and spec.loader is not None
	module = importlib.util.module_from_spec(spec)
	spec.loader.exec_module(module)
	return module


BASH = _load(SCRIPTS / "pr-wait_test.py", "pr_wait_test").BASH
POWERSHELL = shutil.which("powershell") or shutil.which("pwsh")

needs_powershell = unittest.skipUnless(POWERSHELL, "no powershell on PATH")


class Repo:
	"""A throwaway git repo with one commit, so HEAD resolves."""

	def __init__(self, branch: str = "main"):
		self.root = Path(tempfile.mkdtemp())
		self.git("init", "--quiet", f"--initial-branch={branch}")
		self.git("config", "user.email", "test@example.com")
		self.git("config", "user.name", "Test")
		self.git("commit", "--quiet", "--allow-empty", "-m", "root")

	def git(self, *args: str) -> str:
		out = subprocess.run(
			["git", *args], cwd=str(self.root), capture_output=True, text=True, check=True
		)
		return out.stdout.strip()

	def head_sha12(self) -> str:
		return self.git("rev-parse", "--short=12", "HEAD")

	def cleanup(self) -> None:
		shutil.rmtree(self.root, ignore_errors=True)


def tag_sh(cwd: Path, override: str | None = None) -> subprocess.CompletedProcess[str]:
	env = dict(os.environ)
	env.pop("OVERCAST_IMAGE_TAG", None)
	if override is not None:
		env["OVERCAST_IMAGE_TAG"] = override
	# as_posix(): Git Bash reads backslashes in a native Windows path as escapes.
	return subprocess.run(
		[BASH, SH.as_posix()], cwd=str(cwd), capture_output=True, text=True, env=env
	)


def tag_ps1(cwd: Path, override: str | None = None) -> subprocess.CompletedProcess[str]:
	env = dict(os.environ)
	env.pop("OVERCAST_IMAGE_TAG", None)
	if override is not None:
		env["OVERCAST_IMAGE_TAG"] = override
	return subprocess.run(
		[POWERSHELL, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(PS1)],
		cwd=str(cwd),
		capture_output=True,
		text=True,
		env=env,
	)


class Sanitising(unittest.TestCase):
	"""What a branch name becomes, for the shapes branch names really take."""

	def setUp(self) -> None:
		self.repos: list[Repo] = []

	def tearDown(self) -> None:
		for repo in self.repos:
			repo.cleanup()

	def repo(self, branch: str = "main") -> Repo:
		created = Repo(branch)
		self.repos.append(created)
		return created

	def assert_tag(self, repo: Repo, expected: str) -> None:
		result = tag_sh(repo.root)
		self.assertEqual(result.returncode, 0, result.stderr)
		self.assertEqual(result.stdout.strip(), expected)
		if POWERSHELL:
			twin = tag_ps1(repo.root)
			self.assertEqual(twin.returncode, 0, twin.stderr)
			self.assertEqual(twin.stdout.strip(), expected, "the .ps1 twin disagreed")

	def test_a_plain_branch_is_left_alone(self):
		self.assert_tag(self.repo("main"), "main")

	def test_a_slash_becomes_a_dash(self):
		# The shape every agent branch in this repo has. A tag may not contain
		# '/' at all: docker would read overcast:claude/foo as a registry path.
		self.assert_tag(
			self.repo("claude/handover-documentation-b7930d"),
			"claude-handover-documentation-b7930d",
		)

	def test_uppercase_is_lowered(self):
		self.assert_tag(self.repo("Feature/ADD-Thing"), "feature-add-thing")

	def test_a_leading_character_docker_forbids_is_dropped(self):
		# '+' is legal in a git ref and illegal in a docker tag, so it maps to
		# '-' — and a tag may not *start* with '-', so the dash goes too.
		self.assert_tag(self.repo("+plus/Thing"), "plus-thing")

	def test_dots_and_underscores_survive(self):
		self.assert_tag(self.repo("release/1.2.3_rc"), "release-1.2.3_rc")

	def test_detached_head_uses_the_commit_not_the_word_HEAD(self):
		# `git rev-parse --abbrev-ref HEAD` prints the literal "HEAD" when
		# detached, which sanitises to a valid tag that every detached worktree
		# on the machine would share — the collision this script exists to
		# prevent, wearing a disguise.
		repo = self.repo("main")
		sha = repo.head_sha12()
		repo.git("checkout", "--quiet", "--detach", "HEAD")
		self.assert_tag(repo, f"detached-{sha}")

	def test_outside_a_git_repo_falls_back_rather_than_failing(self):
		# A tarball export or a docker build context with no .git. Printing
		# nothing would make the caller build `overcast:` and fail obscurely.
		outside = Path(tempfile.mkdtemp())
		try:
			result = tag_sh(outside)
			self.assertEqual(result.returncode, 0, result.stderr)
			self.assertEqual(result.stdout.strip(), "local")
			if POWERSHELL:
				twin = tag_ps1(outside)
				self.assertEqual(twin.returncode, 0, twin.stderr)
				self.assertEqual(twin.stdout.strip(), "local")
		finally:
			shutil.rmtree(outside, ignore_errors=True)

	def test_the_result_is_always_a_legal_docker_tag(self):
		for branch in ("main", "Feature/ADD-Thing", "+plus/Thing", "release/1.2.3_rc"):
			with self.subTest(branch=branch):
				tag = tag_sh(self.repo(branch).root).stdout.strip()
				self.assertRegex(tag, r"^[a-z0-9_][a-z0-9._-]{0,127}$")


class ExplicitOverride(unittest.TestCase):
	"""OVERCAST_IMAGE_TAG is honoured, or refused — never quietly rewritten."""

	def setUp(self) -> None:
		self.repo = Repo("main")

	def tearDown(self) -> None:
		self.repo.cleanup()

	def test_a_valid_override_wins_over_the_branch(self):
		result = tag_sh(self.repo.root, "scratch")
		self.assertEqual(result.returncode, 0, result.stderr)
		self.assertEqual(result.stdout.strip(), "scratch")

	def test_an_illegal_override_is_refused_not_sanitised(self):
		# Sanitising it would hand back a tag the caller did not ask for, and
		# they would have no way to notice: the build would succeed under a
		# name they never chose.
		for bad in ("has/slash", "-leading-dash", ".leading-dot", "has space"):
			with self.subTest(tag=bad):
				result = tag_sh(self.repo.root, bad)
				self.assertEqual(result.returncode, 2, result.stdout)
				self.assertIn("not a valid Docker tag", result.stderr)
				self.assertEqual(result.stdout.strip(), "")

	@needs_powershell
	def test_the_twin_agrees_about_overrides(self):
		self.assertEqual(tag_ps1(self.repo.root, "scratch").stdout.strip(), "scratch")
		refused = tag_ps1(self.repo.root, "has/slash")
		self.assertEqual(refused.returncode, 2, refused.stdout)
		self.assertIn("not a valid Docker tag", refused.stderr)


if __name__ == "__main__":
	unittest.main(verbosity=2)
