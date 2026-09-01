#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import subprocess
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("release-channel-tags.py")
SPEC = importlib.util.spec_from_file_location("release_channel_tags", SCRIPT)
assert SPEC is not None
tags = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
# Registered before exec: @dataclass resolves its own module through
# sys.modules, and a hyphenated filename cannot be imported any other way.
sys.modules["release_channel_tags"] = tags
SPEC.loader.exec_module(tags)


def moved(version: str, released: list[str]) -> list[str]:
	parsed = tags.parse(version)
	assert parsed is not None, version
	return tags.floating_tags(parsed, tags.parse_released(released))


class ParseTest(unittest.TestCase):
	def test_leading_v_is_optional(self) -> None:
		self.assertEqual(tags.parse("v1.2.3"), tags.parse("1.2.3"))

	def test_prerelease_identifiers_are_split(self) -> None:
		version = tags.parse("0.0.1-alpha.31")
		assert version is not None
		self.assertEqual(("alpha", "31"), version.prerelease)
		self.assertEqual("alpha", version.channel)

	# Build metadata takes no part in precedence (SemVer §11), so two releases
	# differing only in it are the same release as far as a tag is concerned.
	def test_build_metadata_is_ignored(self) -> None:
		self.assertEqual(tags.parse("1.2.3"), tags.parse("1.2.3+build.7"))

	def test_non_versions_are_rejected(self) -> None:
		for text in ["", "v1.2", "1.2.3.4", "latest", "1.2.3-"]:
			self.assertIsNone(tags.parse(text), text)

	# alpha.9 precedes alpha.10: numeric identifiers compare numerically. The
	# alpha counter is already past 9, so string comparison would be wrong now,
	# not eventually.
	def test_numeric_prerelease_identifiers_compare_numerically(self) -> None:
		nine = tags.parse("0.0.1-alpha.9")
		ten = tags.parse("0.0.1-alpha.10")
		assert nine is not None and ten is not None
		self.assertLess(nine.precedence(), ten.precedence())

	def test_stable_outranks_its_own_prerelease(self) -> None:
		stable = tags.parse("1.0.0")
		rc = tags.parse("1.0.0-rc.1")
		assert stable is not None and rc is not None
		self.assertLess(rc.precedence(), stable.precedence())


class FloatingTagsTest(unittest.TestCase):
	# The ordinary case: the newest release, so every pointer it owns follows.
	def test_forward_stable_release_moves_everything(self) -> None:
		self.assertEqual(
			["latest", "1.3", "1"],
			moved("1.3.0", ["1.2.3", "1.2.0", "1.1.0"]),
		)

	# The case this file exists for. 1.2.4 ships after 1.3.0, so :latest and
	# :1 stay where they are and only the 1.2 line follows it.
	def test_maintenance_release_moves_only_its_own_line(self) -> None:
		self.assertEqual(["1.2"], moved("1.2.4", ["1.3.0", "1.2.3", "1.2.0"]))

	# ... and the release after it on the newer line still moves everything,
	# because 1.3.1 is genuinely the highest thing released.
	def test_next_mainline_release_recovers_every_tag(self) -> None:
		self.assertEqual(
			["latest", "1.3", "1"],
			moved("1.3.1", ["1.3.0", "1.2.4", "1.2.3"]),
		)

	def test_maintenance_release_of_an_older_major_keeps_off_latest(self) -> None:
		self.assertEqual(["1.9", "1"], moved("1.9.1", ["2.0.0", "1.9.0"]))

	# Nothing released yet under this pointer: the comparison is vacuous and
	# the tag is claimed. No stable exists in either case, so the newest
	# prerelease takes :latest along with its channel.
	def test_first_release_in_a_channel(self) -> None:
		self.assertEqual(
			["beta", "latest"], moved("1.0.0-beta.0", ["0.0.1-alpha.31"])
		)
		self.assertEqual(["alpha", "latest"], moved("0.0.1-alpha.0", []))

	def test_first_stable_release_claims_every_line(self) -> None:
		self.assertEqual(
			["latest", "1.0", "1"],
			moved("1.0.0", ["0.0.1-alpha.31", "1.0.0-rc.1"]),
		)

	# The channels do not gate each other, and neither gates :latest. A
	# prerelease of the next minor must not stop the current one patching.
	def test_prerelease_and_stable_are_independent(self) -> None:
		self.assertEqual(["alpha"], moved("1.4.0-alpha.0", ["1.3.0", "1.2.4"]))
		self.assertEqual(
			["latest", "1.3", "1"],
			moved("1.3.1", ["1.4.0-alpha.0", "1.3.0"]),
		)

	# The *channel* tags do not gate each other; pre-stable :latest is decided
	# across all channels, and both of these are the newest thing shipped.
	def test_channels_do_not_gate_each_other(self) -> None:
		self.assertEqual(
			["beta", "latest"], moved("1.4.0-beta.0", ["1.4.0-alpha.9"])
		)
		self.assertEqual(
			["alpha", "latest"], moved("1.5.0-alpha.0", ["1.4.0-beta.0"])
		)

	# A prerelease never claims a line tag: :1.4 must mean the shipped 1.4
	# line, not whatever is being tried out on it.
	def test_prerelease_claims_no_line_tags(self) -> None:
		self.assertEqual(["alpha", "latest"], moved("1.4.0-alpha.0", []))

	# Re-running a release republishes its images; the tag already points at
	# this version, so writing it again is a no-op and refusing would silently
	# drop :latest from an otherwise successful re-run.
	def test_republishing_the_newest_release_still_moves_its_tags(self) -> None:
		self.assertEqual(
			["latest", "1.3", "1"],
			moved("1.3.0", ["1.3.0", "1.2.3"]),
		)

	def test_republishing_a_maintenance_release_still_moves_its_line(self) -> None:
		self.assertEqual(["1.2"], moved("1.2.4", ["1.3.0", "1.2.4", "1.2.3"]))

	def test_republishing_an_alpha_still_moves_the_channel(self) -> None:
		self.assertEqual(
			["alpha", "latest"],
			moved("0.0.1-alpha.31", ["0.0.1-alpha.31", "0.0.1-alpha.30"]),
		)

	# Today's shape, and the one regression the guard buys immediately:
	# re-publishing alpha.30 after alpha.31 must not take :alpha backwards —
	# nor :latest, which pre-stable points at the same alphas.
	def test_older_alpha_does_not_take_the_channel_back(self) -> None:
		self.assertEqual([], moved("0.0.1-alpha.30", ["0.0.1-alpha.31"]))

	# Today's shape: every release is an alpha and no stable exists, so the
	# newest alpha carries :latest alongside :alpha. `docker pull` with no tag
	# asks for :latest, and pre-stable the honest answer is the current alpha.
	def test_current_alpha_flow_claims_latest_pre_stable(self) -> None:
		self.assertEqual(
			["alpha", "latest"],
			moved("0.0.1-alpha.32", ["0.0.1-alpha.31", "0.0.1-alpha.30"]),
		)

	# The first stable release ends prerelease ownership of :latest for good:
	# afterwards a prerelease keeps its channel tag only, even though
	# 1.1.0-alpha.0 outranks 1.0.0 by SemVer precedence.
	def test_stable_release_ends_prerelease_claim_on_latest(self) -> None:
		self.assertEqual(["alpha"], moved("1.1.0-alpha.0", ["1.0.0"]))

	# Pre-stable, :latest is compared across every channel: an alpha shipped
	# while a newer beta exists keeps its own channel moving but must not drag
	# :latest back past the beta.
	def test_older_prerelease_cannot_take_latest_past_a_newer_channel(self) -> None:
		self.assertEqual(
			["alpha"], moved("0.0.2-alpha.0", ["1.0.0-beta.1", "0.0.1-alpha.38"])
		)

	# The input is `git tag --list`, which holds whatever the repository has
	# accumulated. Junk in it must not be able to stop a release tagging.
	def test_unparseable_released_versions_are_ignored(self) -> None:
		self.assertEqual(
			["latest", "1.3", "1"],
			moved("1.3.0", ["nightly", "", "v1.2", "1.2.3"]),
		)


def run(*args: str, stdin: str = "") -> subprocess.CompletedProcess[str]:
	return subprocess.run(
		[sys.executable, str(SCRIPT), *args],
		input=stdin,
		capture_output=True,
		text=True,
		encoding="utf-8",
	)


class CommandTest(unittest.TestCase):
	def test_plain_output_is_one_tag_per_line(self) -> None:
		result = run("1.3.0", stdin="v1.2.3\nv1.2.0\n")
		self.assertEqual(0, result.returncode, result.stderr)
		self.assertEqual(["latest", "1.3", "1"], result.stdout.split())

	def test_metadata_output_is_the_action_input(self) -> None:
		result = run("1.2.4", "--format", "metadata", stdin="v1.3.0\nv1.2.3\n")
		self.assertEqual("type=raw,value=1.2\n", result.stdout)

	def test_no_tags_move_is_empty_output_and_success(self) -> None:
		result = run("0.0.1-alpha.30", stdin="v0.0.1-alpha.31\n")
		self.assertEqual(0, result.returncode, result.stderr)
		self.assertEqual("", result.stdout)

	# Fatal, unlike an unparseable tag in the released list: publishing with a
	# silently empty answer would look like a successful release.
	def test_unparseable_release_version_fails(self) -> None:
		result = run("not-a-version", stdin="")
		self.assertEqual(1, result.returncode)
		self.assertIn("not a semantic version", result.stderr)


if __name__ == "__main__":
	unittest.main()
