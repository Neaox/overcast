#!/usr/bin/env python3

"""Tests for verify-changed.sh — the pre-push gate wired up in .claude/settings.json.

The interesting part is the result cache: it decides whether a required CI check
gets skipped, so a fingerprint that is too eager silently stops linting a change.
These drive the real script in a throwaway git repo with stub linters on PATH,
and assert on whether the stubs were actually invoked.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("verify-changed.sh")

# Everything the stubs need travels through files beside the stub itself, keyed
# off $0, rather than through the environment or an absolute path. On a Windows
# dev box `bash` may be WSL, which cannot open a C:\\... path handed to it and
# drops environment variables that WSLENV does not name; both would silently
# turn "the linter failed" into "the linter passed".
STUB = """#!/bin/sh
d=$(dirname "$0")
echo "$0 $*" >>"$d/invocations.log"
if [ -f "$d/exit-%s" ]; then exit "$(cat "$d/exit-%s")"; fi
exit 0
"""


class Repo:
	"""A throwaway git repo laid out the way verify-changed.sh expects."""

	def __init__(self) -> None:
		self.root = Path(tempfile.mkdtemp())
		self.bin = self.root / "stub-bin"
		self.work = self.root / "repo"
		self.log = self.bin / "invocations.log"
		self.stderr = ""
		self.bin.mkdir()
		self.work.mkdir()

		for name in ("golangci-lint", "pnpm"):
			self.install_stub(name)

		self.git("init", "-q", "-b", "main")
		self.git("config", "user.email", "test@example.com")
		self.git("config", "user.name", "Test")

		# Commit 1 is what origin/main points at, so everything added in commit 2
		# shows up as "changed on this branch".
		self.write(".gitignore", "node_modules/\nignored-scratch/\n")
		self.write("Makefile", "GOLANGCI_LINT_VERSION := v2.8.0\n")
		self.write(".golangci.yml", "version: \"2\"\n")
		self.write("go.mod", "module example.com/probe\n\ngo 1.24\n")
		self.write("web/package.json", '{"name":"web"}\n')
		self.write("scripts/verify-changed.sh", SCRIPT.read_text(encoding="utf-8"))
		self.commit("base")
		self.git("update-ref", "refs/remotes/origin/main", "HEAD")

		self.write("app.go", "package app\n")
		self.write("web/src/app.ts", "export const a = 1\n")
		self.commit("work")

		# Gitignored, but has to exist for the web branch to run at all.
		(self.work / "web" / "node_modules").mkdir()

	def install_stub(self, name: str) -> None:
		stub = self.bin / name
		with stub.open("w", encoding="utf-8", newline="") as fh:
			fh.write(STUB % (name, name))
		stub.chmod(0o755)

	def fake_uname(self, sysname_release: str) -> None:
		"""Answer `uname -sr` with a canned string, so the script's platform
		sniff is deterministic regardless of the host running the tests."""
		stub = self.bin / "uname"
		with stub.open("w", encoding="utf-8", newline="") as fh:
			fh.write('#!/bin/sh\necho "%s"\n' % sysname_release)
		stub.chmod(0o755)

	def git(self, *args: str) -> str:
		return subprocess.run(
			["git", *args], cwd=self.work, check=True, capture_output=True, text=True
		).stdout

	def write(self, rel: str, text: str) -> None:
		path = self.work / rel
		path.parent.mkdir(parents=True, exist_ok=True)
		# newline="" so a Windows checkout does not CRLF-mangle the shell scripts.
		with path.open("w", encoding="utf-8", newline="") as fh:
			fh.write(text)

	def commit(self, message: str) -> None:
		self.git("add", "-A")
		self.git("commit", "-q", "-m", message)

	def run(self, *args: str, golangci_exit: int = 0, pnpm_exit: int = 0, script_env: dict | None = None):
		"""Run the script; returns (exit code, list of stub invocations since last run).

		The script is launched through a wrapper file rather than `bash -c` or
		subprocess env: a WSL bash drops environment variables WSLENV does not
		name, appends the (translated) Windows PATH *after* the Linux one so
		/usr/bin shadows same-named stubs, and the System32 launcher mangles
		quoted -c payloads. A file sidesteps all three — the wrapper prepends
		stub-bin inside bash, where it wins on every host, and script_env
		entries become assignments in the same file.
		"""
		self.log.write_text("", encoding="utf-8")
		(self.bin / "exit-golangci-lint").write_text(f"{golangci_exit}\n", encoding="utf-8")
		(self.bin / "exit-pnpm").write_text(f"{pnpm_exit}\n", encoding="utf-8")
		(self.bin / "exit-go").write_text("0\n", encoding="utf-8")
		wrapper_lines = [
			"#!/bin/sh",
			'PATH="$PWD/../stub-bin:$PATH"',
			"export PATH",
			# A dev box may export the parallelism knobs; the tests set them
			# via script_env when they are the thing under test.
			"unset OVERCAST_GO_TEST_P OVERCAST_GO_CPUS",
		]
		for name, value in (script_env or {}).items():
			wrapper_lines += [f'{name}="{value}"', f"export {name}"]
		wrapper_lines.append('exec bash scripts/verify-changed.sh "$@"')
		wrapper = self.root / "run-wrapper.sh"
		with wrapper.open("w", encoding="utf-8", newline="") as fh:
			fh.write("\n".join(wrapper_lines) + "\n")
		env = {**os.environ, "PATH": f"{self.bin}{os.pathsep}{os.environ['PATH']}"}
		proc = subprocess.run(
			["bash", "../run-wrapper.sh", *args],
			cwd=self.work,
			env=env,
			stdin=subprocess.DEVNULL,
			capture_output=True,
			text=True,
		)
		self.stderr = proc.stderr
		calls = [ln for ln in self.log.read_text(encoding="utf-8").splitlines() if ln.strip()]
		return proc.returncode, calls

	def ran_go(self, calls: list[str]) -> bool:
		return any("golangci-lint" in c for c in calls)

	def ran_web(self, calls: list[str]) -> bool:
		return any("pnpm" in c for c in calls)

	def cleanup(self) -> None:
		shutil.rmtree(self.root, ignore_errors=True)


class VerifyChangedTest(unittest.TestCase):
	def setUp(self) -> None:
		self.repo = Repo()
		self.addCleanup(self.repo.cleanup)

	def test_first_run_checks_and_second_run_is_cached(self) -> None:
		code, calls = self.repo.run()
		self.assertEqual(0, code)
		self.assertTrue(self.repo.ran_go(calls))
		self.assertTrue(self.repo.ran_web(calls))

		code, calls = self.repo.run()
		self.assertEqual(0, code)
		self.assertEqual([], calls, "nothing changed, so neither check should re-run")
		# The skip has to be visible, or a stale cache looks like a fast lint.
		self.assertIn("already green", self.repo.stderr)
		self.assertIn("--force", self.repo.stderr)

	def test_editing_a_go_file_invalidates_only_the_go_check(self) -> None:
		self.repo.run()
		self.repo.write("app.go", "package app\n\nvar X = 1\n")

		code, calls = self.repo.run()
		self.assertEqual(0, code)
		self.assertTrue(self.repo.ran_go(calls))
		self.assertFalse(self.repo.ran_web(calls), "a Go edit must not invalidate web")

	def test_editing_a_web_file_invalidates_only_the_web_check(self) -> None:
		self.repo.run()
		self.repo.write("web/src/app.ts", "export const a = 2\n")

		code, calls = self.repo.run()
		self.assertEqual(0, code)
		self.assertTrue(self.repo.ran_web(calls))
		self.assertFalse(self.repo.ran_go(calls), "a web edit must not invalidate Go")

	def test_gitignored_file_does_not_invalidate(self) -> None:
		self.repo.run()
		(self.repo.work / "web" / "node_modules" / "probe").write_text("x", encoding="utf-8")
		(self.repo.work / "ignored-scratch").mkdir()
		(self.repo.work / "ignored-scratch" / "app.go").write_text("package x\n", encoding="utf-8")

		_, calls = self.repo.run()
		self.assertEqual([], calls, "ignored files are not inputs to either check")

	def test_new_untracked_go_file_invalidates(self) -> None:
		self.repo.run()
		self.repo.write("added.go", "package app\n")

		_, calls = self.repo.run()
		self.assertTrue(self.repo.ran_go(calls))

	def test_staging_does_not_invalidate(self) -> None:
		self.repo.write("added.go", "package app\n")
		self.repo.run()
		self.repo.git("add", "added.go")

		_, calls = self.repo.run()
		self.assertEqual([], calls, "the fingerprint is worktree content, not the index")

	def test_bumping_the_pinned_tool_version_invalidates_go(self) -> None:
		self.repo.run()
		self.repo.write("Makefile", "GOLANGCI_LINT_VERSION := v2.9.0\n")

		_, calls = self.repo.run()
		self.assertTrue(self.repo.ran_go(calls), "a different linter version is not a like-for-like run")

	def test_failure_blocks_and_is_not_cached(self) -> None:
		code, calls = self.repo.run(golangci_exit=1)
		self.assertEqual(2, code, "a failed check must block the push")
		self.assertTrue(self.repo.ran_go(calls))

		code, calls = self.repo.run(golangci_exit=1)
		self.assertEqual(2, code)
		self.assertTrue(self.repo.ran_go(calls), "a failure must never be cached as a pass")

	def test_pass_after_failure_is_cached(self) -> None:
		self.repo.run(golangci_exit=1)
		code, _ = self.repo.run()
		self.assertEqual(0, code)

		_, calls = self.repo.run()
		self.assertEqual([], calls)

	def test_force_ignores_the_cache(self) -> None:
		self.repo.run()
		_, calls = self.repo.run("--force")
		self.assertTrue(self.repo.ran_go(calls))
		self.assertTrue(self.repo.ran_web(calls))

	def test_record_seeds_the_cache_without_running_anything(self) -> None:
		code, calls = self.repo.run("--record", "go")
		self.assertEqual(0, code)
		self.assertEqual([], calls)

		_, calls = self.repo.run()
		self.assertFalse(self.repo.ran_go(calls), "a recorded pass counts as a run")
		self.assertTrue(self.repo.ran_web(calls), "web was never recorded")

	def test_record_is_rejected_without_a_valid_check_name(self) -> None:
		for args in (("--record",), ("--record", "bogus"), ("--bogus",)):
			with self.subTest(args=args):
				code, _ = self.repo.run(*args)
				self.assertEqual(2, code)

	def test_cache_lives_outside_the_worktree(self) -> None:
		self.repo.run()
		self.assertEqual("", self.repo.git("status", "--porcelain").strip())

	# ---- go test parallelism ------------------------------------------------
	# On a Windows-flavoured host the scoped test run must default to `-p 2`:
	# uncapped it starts NumCPU test binaries at once and the network-heavy
	# tests deterministically exhaust the ephemeral-port range (ADDRINUSE on a
	# green tree). The compile-only tag pass must stay uncapped — it binds no
	# sockets, and -p bounds build actions, so a cap there only slows it down.

	def go_test_calls(self, calls: list[str]) -> list[str]:
		return [c for c in calls if "/go " in c and " test " in c]

	def test_windows_flavoured_host_caps_the_scoped_test_run(self) -> None:
		self.repo.install_stub("go")
		for kernel in (
			"MINGW64_NT-10.0-26100 3.6.4.x86_64",
			"MSYS_NT-10.0-26100 3.6.4.x86_64",
			"CYGWIN_NT-10.0-26100 3.6.4-390.x86_64",
			"Linux 5.15.167.4-microsoft-standard-WSL2",
		):
			with self.subTest(kernel=kernel):
				self.repo.fake_uname(kernel)
				code, calls = self.repo.run("--force")
				self.assertEqual(0, code)
				scoped = [c for c in calls if " -C . test " in c]
				compiles = [c for c in calls if " test -run=^$ " in c]
				self.assertEqual(1, len(scoped))
				self.assertEqual(3, len(compiles), "one compile per CI build-tag set")
				self.assertIn(" -p 2 ", scoped[0])
				for c in compiles:
					self.assertNotIn(" -p ", c)

	def test_non_windows_host_stays_uncapped(self) -> None:
		self.repo.install_stub("go")
		self.repo.fake_uname("Linux 6.8.0-45-generic")
		code, calls = self.repo.run()
		self.assertEqual(0, code)
		go_tests = self.go_test_calls(calls)
		self.assertEqual(4, len(go_tests))
		for c in go_tests:
			self.assertNotIn(" -p ", c)

	def test_explicit_parallelism_beats_the_windows_default(self) -> None:
		self.repo.install_stub("go")
		self.repo.fake_uname("MINGW64_NT-10.0-26100 3.6.4.x86_64")

		# An explicit value keeps its old meaning: both the compiles and the run.
		_, calls = self.repo.run(script_env={"OVERCAST_GO_TEST_P": "5"})
		go_tests = self.go_test_calls(calls)
		self.assertEqual(4, len(go_tests))
		for c in go_tests:
			self.assertIn(" -p 5 ", c)

		# "0" is the explicit no-cap spelling, even on Windows.
		_, calls = self.repo.run("--force", script_env={"OVERCAST_GO_TEST_P": "0"})
		go_tests = self.go_test_calls(calls)
		self.assertEqual(4, len(go_tests))
		for c in go_tests:
			self.assertNotIn(" -p ", c)


if __name__ == "__main__":
	unittest.main()
