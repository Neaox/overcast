#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("release-hold.py")
SPEC = importlib.util.spec_from_file_location("release_hold", SCRIPT)
assert SPEC is not None
hold = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
# Registered before exec: @dataclass resolves its own module through
# sys.modules, and a hyphenated filename cannot be imported any other way.
sys.modules["release_hold"] = hold
SPEC.loader.exec_module(hold)


def write_fragments(files: dict[str, str]) -> Path:
	root = Path(tempfile.mkdtemp())
	for name, text in files.items():
		(root / name).write_text(text, encoding="utf-8")
	return root


BREAKING = (
	"~! [state] the on-disk layout moved to v2\n"
	"  migration: run `overcast state export` before upgrading, then import after\n"
)
COMPATIBLE = "+ [sqs] long polling on `ReceiveMessage`\n"


class HoldsTest(unittest.TestCase):
	def test_minor_release_holds(self) -> None:
		decision = hold.holds("1.2.0")
		self.assertTrue(decision.holds)
		self.assertEqual("minor", decision.kind)
		self.assertEqual("2.0.0", decision.next_major)

	def test_patch_release_holds(self) -> None:
		decision = hold.holds("1.2.3")
		self.assertTrue(decision.holds)
		self.assertEqual("patch", decision.kind)

	def test_major_release_does_not_hold(self) -> None:
		self.assertFalse(hold.holds("2.0.0").holds)

	# A release candidate for a minor is still a minor: it ships under 1.2.0
	# and carries the same promise.
	def test_prerelease_of_a_minor_holds(self) -> None:
		decision = hold.holds("1.2.0-rc.1")
		self.assertTrue(decision.holds)
		self.assertEqual("minor", decision.kind)

	def test_prerelease_of_a_major_does_not_hold(self) -> None:
		self.assertFalse(hold.holds("2.0.0-rc.1").holds)

	# The whole alpha lives here: 0.x promises nothing, so holding on it would
	# fire on every release without protecting a promise anyone was given.
	def test_zero_major_never_holds(self) -> None:
		self.assertFalse(hold.holds("0.0.1-alpha.29").holds)
		self.assertFalse(hold.holds("0.4.2").holds)

	# Degrades to "not holding" rather than wedging every PR in the repo
	# behind a VERSION file nobody can parse.
	def test_unparseable_version_does_not_hold(self) -> None:
		decision = hold.holds("not-a-version")
		self.assertFalse(decision.holds)
		self.assertIn("not a semantic version", decision.reason)


class BreakingEntriesTest(unittest.TestCase):
	def test_finds_marked_breaking_entries(self) -> None:
		root = write_fragments({"20260803-state.md": BREAKING})
		found = hold.breaking_entries([root / "20260803-state.md"])
		self.assertEqual(1, len(found))
		self.assertEqual(1, len(found[0][1]))
		self.assertIn("on-disk layout", found[0][1][0].prose)

	def test_ignores_compatible_entries(self) -> None:
		root = write_fragments({"20260803-sqs.md": COMPATIBLE})
		self.assertEqual([], hold.breaking_entries([root / "20260803-sqs.md"]))

	# Removed is breaking unless the entry says otherwise — the one category
	# whose default inverts. A hold must honour that, not just look for '!'.
	def test_removed_is_breaking_by_default(self) -> None:
		root = write_fragments(
			{
				"20260803-drop.md": (
					"- [config] the OVERCAST_LEGACY_PORT variable\n"
					"  migration: set OVERCAST_PORT instead\n"
				)
			}
		)
		self.assertEqual(1, len(hold.breaking_entries([root / "20260803-drop.md"])))

	def test_removed_marked_not_breaking_is_not_held(self) -> None:
		root = write_fragments(
			{"20260803-drop.md": "-. [web] a button that never shipped\n"}
		)
		self.assertEqual([], hold.breaking_entries([root / "20260803-drop.md"]))

	# The 'Changelog fragments' check reports malformed fragments already;
	# failing twice for one file buries the message that says what is wrong.
	def test_unparseable_fragment_is_skipped(self) -> None:
		root = write_fragments({"20260803-bad.md": "this is not an entry line\n"})
		self.assertEqual([], hold.breaking_entries([root / "20260803-bad.md"]))

	def test_missing_file_is_skipped(self) -> None:
		self.assertEqual([], hold.breaking_entries([Path("no-such-fragment.md")]))


class RenderEntriesTest(unittest.TestCase):
	def test_groups_under_the_file_to_edit(self) -> None:
		root = write_fragments({"20260803-state.md": BREAKING})
		markdown = hold.render_entries(
			hold.breaking_entries([root / "20260803-state.md"])
		)
		self.assertIn("20260803-state.md`", markdown)
		self.assertIn("- **BREAKING** [state] the on-disk layout moved to v2", markdown)
		self.assertIn("  migration: run `overcast state export`", markdown)


def run(*args: str) -> subprocess.CompletedProcess[str]:
	return subprocess.run(
		[sys.executable, str(SCRIPT), *args],
		capture_output=True,
		text=True,
		encoding="utf-8",
	)


class CheckCommandTest(unittest.TestCase):
	def test_no_release_in_flight(self) -> None:
		result = run("check", "--release-version", "")
		self.assertEqual(0, result.returncode, result.stderr)
		self.assertEqual(hold.VERDICT_NO_RELEASE, result.stdout.strip())

	def test_major_release_in_flight(self) -> None:
		result = run("check", "--release-version", "2.0.0")
		self.assertEqual(hold.VERDICT_NOT_HOLDING, result.stdout.strip())

	def test_holding_release_with_no_breaking_entries(self) -> None:
		root = write_fragments({"20260803-sqs.md": COMPATIBLE})
		result = run(
			"check",
			"--release-version",
			"1.2.0",
			str(root / "20260803-sqs.md"),
		)
		self.assertEqual(hold.VERDICT_CLEAR, result.stdout.strip())

	def test_holding_release_with_a_breaking_entry(self) -> None:
		root = write_fragments(
			{"20260803-state.md": BREAKING, "20260803-sqs.md": COMPATIBLE}
		)
		entries = root / "entries.md"
		result = run(
			"check",
			"--release-version",
			"1.2.0",
			"--entries-file",
			str(entries),
			str(root / "20260803-state.md"),
			str(root / "20260803-sqs.md"),
		)
		self.assertEqual(hold.VERDICT_HELD, result.stdout.strip())
		written = entries.read_text(encoding="utf-8")
		self.assertIn("the on-disk layout moved to v2", written)
		self.assertNotIn("long polling", written)

	# The entries file is the comment body: an empty one must not leave a
	# stale list from an earlier run behind it.
	def test_entries_file_is_emptied_when_nothing_is_breaking(self) -> None:
		root = write_fragments({"20260803-sqs.md": COMPATIBLE})
		entries = root / "entries.md"
		entries.write_text("stale content\n", encoding="utf-8")
		run(
			"check",
			"--release-version",
			"1.2.0",
			"--entries-file",
			str(entries),
			str(root / "20260803-sqs.md"),
		)
		self.assertEqual("", entries.read_text(encoding="utf-8"))


class DescribeCommandTest(unittest.TestCase):
	def test_kind_and_next_major(self) -> None:
		self.assertEqual("minor", run("describe", "1.2.0", "kind").stdout.strip())
		self.assertEqual(
			"2.0.0", run("describe", "1.2.0", "next-major").stdout.strip()
		)


if __name__ == "__main__":
	unittest.main()
