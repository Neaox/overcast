#!/usr/bin/env python3

"""Tests for the shared Claude Code/Codex worktree stash guard."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("reject-worktree-stash.sh")
WINDOWS_LAUNCHER = Path(__file__).with_name("run-git-bash.ps1")


class RejectWorktreeStashTest(unittest.TestCase):
	def setUp(self) -> None:
		self.root = Path(tempfile.mkdtemp())
		self.main = self.root / "repo"
		self.linked = self.root / "linked"
		self.main.mkdir()
		self.git(self.main, "init", "-q", "-b", "main")
		self.git(self.main, "config", "user.email", "test@example.com")
		self.git(self.main, "config", "user.name", "Test")
		(self.main / "README.md").write_text("base\n", encoding="utf-8")
		self.git(self.main, "add", "README.md")
		self.git(self.main, "commit", "-q", "-m", "base")
		self.git(self.main, "worktree", "add", "-q", "-b", "task", str(self.linked))
		self.addCleanup(self.cleanup)

	def git(self, cwd: Path, *args: str) -> None:
		subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True, text=True)

	def run_hook(self, command: str, cwd: Path) -> subprocess.CompletedProcess[str]:
		payload = json.dumps(
			{
				"cwd": str(self.main),
				"tool_input": {"command": command, "workdir": str(cwd)},
			}
		)
		if os.name == "nt":
			runner = [
				"powershell.exe",
				"-NoProfile",
				"-ExecutionPolicy",
				"Bypass",
				"-File",
				str(WINDOWS_LAUNCHER),
				"-ScriptPath",
				str(SCRIPT),
			]
		else:
			runner = [shutil.which("bash") or "bash", str(SCRIPT)]

		return subprocess.run(
			runner,
			input=payload,
			capture_output=True,
			text=True,
		)

	def cleanup(self) -> None:
		subprocess.run(
			["git", "worktree", "remove", str(self.linked)],
			cwd=self.main,
			capture_output=True,
		)
		shutil.rmtree(self.root, ignore_errors=True)

	def test_blocks_stash_in_linked_worktree(self) -> None:
		result = self.run_hook("git stash push", self.linked)

		self.assertEqual(2, result.returncode)
		self.assertIn("stash stack is shared", result.stderr)
		self.assertIn("temporary WIP commit", result.stderr)

	def test_blocks_stash_in_a_chained_command(self) -> None:
		result = self.run_hook("git status; git stash", self.linked)

		self.assertEqual(2, result.returncode)

	def test_blocks_stash_after_a_git_global_option(self) -> None:
		result = self.run_hook(f'git -C "{self.linked}" stash', self.linked)

		self.assertEqual(2, result.returncode)

	def test_allows_stash_in_primary_checkout(self) -> None:
		result = self.run_hook("git stash", self.main)

		self.assertEqual(0, result.returncode)

	def test_allows_non_stash_command_in_linked_worktree(self) -> None:
		result = self.run_hook("git status", self.linked)

		self.assertEqual(0, result.returncode)

	def test_allows_text_that_only_mentions_git_stash(self) -> None:
		result = self.run_hook("echo git stash", self.linked)

		self.assertEqual(0, result.returncode)

	def test_allows_stash_as_an_argument_to_another_git_subcommand(self) -> None:
		result = self.run_hook("git show stash", self.linked)

		self.assertEqual(0, result.returncode)


if __name__ == "__main__":
	unittest.main()
