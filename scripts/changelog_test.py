#!/usr/bin/env python3

from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import changelog


def write_dir(files: dict[str, str]) -> Path:
	root = Path(tempfile.mkdtemp())
	for name, text in files.items():
		(root / name).write_text(text, encoding="utf-8")
	return root


VALID_FRAGMENT = (
	"* [cloudformation] update replacements keep same-ID resources alive\n"
)


class ParseEntriesTest(unittest.TestCase):
	def test_symbols_map_to_sections(self) -> None:
		entries, errors = changelog.parse_entries(
			"+ [sqs] added thing\n"
			"~ [sqs] changed thing\n"
			"* [sqs] fixed thing\n"
		)

		self.assertEqual([], errors)
		self.assertEqual(["Added", "Changed", "Fixed"], [e.section for e in entries])

	def test_accepts_spelled_out_sections(self) -> None:
		entries, errors = changelog.parse_entries(
			"security [iam] policy parsing tightened\ndeprecated [ec2] old flag\n"
		)

		self.assertEqual([], errors)
		self.assertEqual(["Security", "Deprecated"], [e.section for e in entries])

	def test_common_sections_default_to_not_breaking(self) -> None:
		entries, errors = changelog.parse_entries(
			"+ [sqs] long polling\n~ [web] toast redesign\n* [lambda] a fix\n"
		)

		self.assertEqual([], errors)
		self.assertEqual([False, False, False], [e.breaking for e in entries])

	def test_removed_defaults_to_breaking(self) -> None:
		entries, errors = changelog.parse_entries(
			"- [sqs] the FooBar operation\n  migration: stop calling it\n"
		)

		self.assertEqual([], errors)
		self.assertTrue(entries[0].breaking)

	def test_removed_can_opt_out_of_breaking(self) -> None:
		entries, errors = changelog.parse_entries("-. [sqs] an unreleased debug flag\n")

		self.assertEqual([], errors)
		self.assertFalse(entries[0].breaking)

	def test_breaking_marker_is_independent_of_section(self) -> None:
		entries, errors = changelog.parse_entries(
			"+! [sqs] SendMessage takes MessageGroupId on FIFO queues\n"
			"  migration: pass a group id on every FIFO send\n"
		)

		self.assertEqual([], errors)
		self.assertEqual("Added", entries[0].section)
		self.assertTrue(entries[0].breaking)
		self.assertEqual("pass a group id on every FIFO send", entries[0].migration)

	def test_breaking_entry_needs_a_migration_note(self) -> None:
		_, errors = changelog.parse_entries("+! [sqs] a required field\n")

		self.assertTrue(any("needs an indented 'migration:' line" in e for e in errors))

	def test_hint_phrase_forces_an_explicit_marker(self) -> None:
		_, errors = changelog.parse_entries(
			"* [s3] PutObject now rejects a malformed checksum\n"
		)

		self.assertTrue(any("reads like a compatibility break" in e for e in errors))

	def test_hint_phrase_is_satisfied_by_an_explicit_marker(self) -> None:
		entries, errors = changelog.parse_entries(
			"*. [s3] PutObject now rejects a malformed checksum\n"
		)

		self.assertEqual([], errors)
		self.assertFalse(entries[0].breaking)

	def test_splits_areas_on_slash(self) -> None:
		entries, errors = changelog.parse_entries(
			"+ [efs/ecs] mounts honour root directories\n"
		)

		self.assertEqual([], errors)
		self.assertEqual(["efs", "ecs"], entries[0].areas)
		self.assertEqual("efs", entries[0].area)

	def test_accepts_entry_without_area(self) -> None:
		entries, errors = changelog.parse_entries("* build from source works again\n")

		self.assertEqual([], errors)
		self.assertEqual([], entries[0].areas)
		self.assertEqual("build from source works again", entries[0].prose)

	def test_indented_lines_continue_the_previous_entry(self) -> None:
		entries, errors = changelog.parse_entries(
			"+ [sqs] long polling\n"
			"    honours WaitTimeSeconds\n"
			"    and caps it at 20\n"
		)

		self.assertEqual([], errors)
		self.assertEqual(1, len(entries))
		self.assertEqual(
			"long polling honours WaitTimeSeconds and caps it at 20", entries[0].prose
		)

	def test_migration_continues_onto_following_lines(self) -> None:
		entries, errors = changelog.parse_entries(
			"-! [state] the v1 on-disk layout\n"
			"  migration: export before upgrading,\n"
			"  then import afterwards\n"
		)

		self.assertEqual([], errors)
		self.assertEqual(
			"export before upgrading, then import afterwards", entries[0].migration
		)

	def test_ignores_blank_lines_and_comments(self) -> None:
		entries, errors = changelog.parse_entries("# a note\n\n+ [sqs] thing\n\n")

		self.assertEqual([], errors)
		self.assertEqual(1, len(entries))

	def test_rejects_unknown_kind(self) -> None:
		_, errors = changelog.parse_entries("improved [sqs] thing\n")

		self.assertTrue(any("unknown kind 'improved'" in e for e in errors))

	def test_rejects_malformed_line(self) -> None:
		_, errors = changelog.parse_entries("+\n")

		self.assertTrue(any("expected" in e for e in errors))

	def test_rejects_continuation_without_an_entry(self) -> None:
		_, errors = changelog.parse_entries("    orphaned continuation\n")

		self.assertTrue(any("no entry above it" in e for e in errors))

	def test_rejects_empty_migration_note(self) -> None:
		_, errors = changelog.parse_entries("+! [sqs] thing\n  migration:\n")

		self.assertTrue(any("needs the upgrade instructions" in e for e in errors))

	def test_rejects_bad_area_slug(self) -> None:
		_, errors = changelog.parse_entries("+ [SQS] shouting\n")

		self.assertTrue(any("lowercase slugs" in e for e in errors))

	def test_rejects_repeated_area(self) -> None:
		_, errors = changelog.parse_entries("+ [sqs/sqs] doubled\n")

		self.assertTrue(any("repeats 'sqs'" in e for e in errors))


class RenderEntryTest(unittest.TestCase):
	def test_round_trips_through_the_parser(self) -> None:
		source = (
			"+! [efs/ecs] mounts take an access point with a root directory\n"
			"  migration: declare a RootDirectory on the access point\n"
			"-. [sqs] an unreleased debug flag\n"
			"~ [web] toast redesign\n"
		)
		entries, errors = changelog.parse_entries(source)
		self.assertEqual([], errors)

		rendered = "".join(changelog.render_entry(entry) for entry in entries)

		self.assertEqual(source, rendered)

	def test_omits_the_marker_when_it_matches_the_default(self) -> None:
		entries, _ = changelog.parse_entries("+ [sqs] long polling\n")

		self.assertEqual("+ [sqs] long polling\n", changelog.render_entry(entries[0]))

	def test_keeps_the_marker_a_hint_phrase_forced(self) -> None:
		entries, _ = changelog.parse_entries("*. [s3] now rejects bad checksums\n")

		self.assertEqual(
			"*. [s3] now rejects bad checksums\n", changelog.render_entry(entries[0])
		)


class ParseFragmentTest(unittest.TestCase):
	def test_accepts_a_valid_fragment(self) -> None:
		root = write_dir({"20260730-cfn-update.md": VALID_FRAGMENT})

		fragment, errors = changelog.parse_fragment(root / "20260730-cfn-update.md")

		self.assertEqual([], errors)
		assert fragment is not None
		self.assertEqual(1, len(fragment.entries))
		self.assertEqual("Fixed", fragment.entries[0].section)
		self.assertEqual("20260730-cfn-update.md", fragment.entries[0].source)

	def test_rejects_the_old_frontmatter_format(self) -> None:
		root = write_dir(
			{"20260730-x.md": "---\nsection: Fixed\n---\n\n- [sqs] a fix\n"}
		)

		_, errors = changelog.parse_fragment(root / "20260730-x.md")

		self.assertTrue(any("no longer use frontmatter" in e for e in errors))

	def test_rejects_bad_filename(self) -> None:
		root = write_dir({"MyFix.md": VALID_FRAGMENT})

		_, errors = changelog.parse_fragment(root / "MyFix.md")

		self.assertTrue(any("YYYYMMDD-<slug>.md" in e for e in errors))

	def test_rejects_an_empty_fragment(self) -> None:
		root = write_dir({"20260730-x.md": "\n# only a comment\n"})

		_, errors = changelog.parse_fragment(root / "20260730-x.md")

		self.assertTrue(any("no entries" in e for e in errors))


class LoadFragmentsTest(unittest.TestCase):
	def test_skips_readme_and_reports_missing_dir(self) -> None:
		root = write_dir(
			{
				"README.md": "# docs, not a fragment",
				"20260730-cfn-update.md": VALID_FRAGMENT,
			}
		)

		fragments, errors = changelog.load_fragments(root)
		self.assertEqual([], errors)
		self.assertEqual(1, len(fragments))

		_, missing = changelog.load_fragments(root / "nope")
		self.assertTrue(any("directory is missing" in e for e in missing))


class UnreleasedErrorsTest(unittest.TestCase):
	def test_accepts_empty_unreleased_with_comments_and_headings(self) -> None:
		root = write_dir(
			{
				"CHANGELOG.md": (
					"# Changelog\n\n## [Unreleased]\n\n### Added\n\n## [0.0.1] - x\n"
					"\n- something shipped\n"
				)
			}
		)

		self.assertEqual([], changelog.unreleased_errors(root / "CHANGELOG.md"))

	def test_rejects_content_in_unreleased(self) -> None:
		root = write_dir(
			{"CHANGELOG.md": "# Changelog\n\n## [Unreleased]\n\n- a leaked entry\n"}
		)

		errors = changelog.unreleased_errors(root / "CHANGELOG.md")

		self.assertTrue(any("must stay empty" in e for e in errors))

	def test_rejects_missing_unreleased_section(self) -> None:
		root = write_dir({"CHANGELOG.md": "# Changelog\n"})

		errors = changelog.unreleased_errors(root / "CHANGELOG.md")

		self.assertTrue(any("missing a '## [Unreleased]' section" in e for e in errors))


class AssembleTest(unittest.TestCase):
	def test_groups_by_section_then_area_across_files(self) -> None:
		root = write_dir(
			{
				"20260730-zeta.md": "* a fix with no area\n",
				"20260729-one.md": "* [sqs] fix one\n+ [glue] new service\n",
				"20260731-two.md": "* [sqs] fix two\n",
			}
		)
		fragments, errors = changelog.load_fragments(root)
		self.assertEqual([], errors)

		output = changelog.assemble(fragments, "0.0.1-alpha.27", "2026-07-31")

		self.assertEqual(
			"## [0.0.1-alpha.27] - 2026-07-31\n"
			"\n"
			"### Added\n"
			"\n"
			"- [glue] new service\n"
			"\n"
			"### Fixed\n"
			"\n"
			"- [sqs] fix one\n"
			"\n"
			"- [sqs] fix two\n"
			"\n"
			"- a fix with no area\n",
			output,
		)

	def test_flags_breaking_entries_with_their_migration(self) -> None:
		root = write_dir(
			{
				"20260801-state.md": (
					"-! [state] the v1 on-disk layout\n"
					"  migration: export before upgrading\n"
				)
			}
		)
		fragments, errors = changelog.load_fragments(root)
		self.assertEqual([], errors)

		output = changelog.assemble(fragments, "0.1.0", "2026-08-01")

		self.assertIn("- **BREAKING** [state] the v1 on-disk layout\n", output)
		self.assertIn("  migration: export before upgrading\n", output)


class CommandNewTest(unittest.TestCase):
	def run_new(self, root: Path, *args: str) -> subprocess.CompletedProcess[bytes]:
		return subprocess.run(
			[
				sys.executable,
				str(Path(__file__).with_name("changelog.py")),
				"--fragments-dir",
				str(root),
				"new",
				"--date",
				"20260801",
				"--name",
				"my-branch",
				*args,
			],
			capture_output=True,
		)

	def test_writes_every_entry_to_one_file(self) -> None:
		root = write_dir({})

		result = self.run_new(
			root,
			"-e",
			"+ [sqs] long polling on ReceiveMessage",
			"-e",
			"~ [web] toast redesign",
		)

		self.assertEqual(0, result.returncode, result.stderr.decode("utf-8", "replace"))
		self.assertEqual(
			["20260801-my-branch.md"], [path.name for path in root.iterdir()]
		)
		fragments, errors = changelog.load_fragments(root)
		self.assertEqual([], errors)
		self.assertEqual(2, len(fragments[0].entries))

	def test_appends_to_an_existing_fragment(self) -> None:
		root = write_dir({})

		self.run_new(root, "-e", "+ [sqs] first change")
		result = self.run_new(root, "-e", "* [sqs] second change")

		self.assertEqual(0, result.returncode, result.stderr.decode("utf-8", "replace"))
		fragments, errors = changelog.load_fragments(root)
		self.assertEqual([], errors)
		self.assertEqual(1, len(fragments))
		self.assertEqual(2, len(fragments[0].entries))

	def test_writes_lf_line_endings_on_every_platform(self) -> None:
		root = write_dir({})

		self.run_new(root, "-e", "+ [sqs] first", "-e", "* [sqs] second")

		written = (root / "20260801-my-branch.md").read_bytes()
		self.assertNotIn(b"\r\n", written)

	def test_dry_run_writes_nothing(self) -> None:
		root = write_dir({})

		result = self.run_new(root, "--dry-run", "-e", "* [sqs] a fix")

		self.assertEqual(0, result.returncode, result.stderr.decode("utf-8", "replace"))
		self.assertEqual([], list(root.iterdir()))
		self.assertIn("* [sqs] a fix", result.stdout.decode("utf-8"))

	def test_reports_a_bad_entry_and_writes_nothing(self) -> None:
		root = write_dir({})

		result = self.run_new(root, "-e", "improved [sqs] thing")

		self.assertEqual(1, result.returncode)
		self.assertIn("unknown kind 'improved'", result.stderr.decode("utf-8"))
		self.assertEqual([], list(root.iterdir()))

	def test_reports_an_unmarked_hint_phrase(self) -> None:
		root = write_dir({})

		result = self.run_new(root, "-e", "* [s3] PutObject now rejects bad checksums")

		self.assertEqual(1, result.returncode)
		self.assertIn("reads like a compatibility break", result.stderr.decode("utf-8"))
		self.assertEqual([], list(root.iterdir()))


class StdioEncodingTest(unittest.TestCase):
	# Fragment bodies routinely contain non-cp1252 characters ('→' in perf
	# entries, '—' in feature entries). Windows consoles default to cp1252, so
	# the script must not inherit the ambient stdout encoding. PYTHONIOENCODING
	# reproduces that default on any platform.
	def test_assemble_writes_non_cp1252_body_to_cp1252_stdout(self) -> None:
		root = write_dir(
			{"20260731-perf.md": "~ [lambda] cold start p50 ~355 ms → ~300 ms\n"}
		)
		env = dict(os.environ, PYTHONIOENCODING="cp1252")

		result = subprocess.run(
			[
				sys.executable,
				str(Path(__file__).with_name("changelog.py")),
				"--fragments-dir",
				str(root),
				"assemble",
				"0.0.1-alpha.28",
				"--date",
				"2026-07-31",
			],
			capture_output=True,
			env=env,
		)

		self.assertEqual(0, result.returncode, result.stderr.decode("utf-8", "replace"))
		self.assertIn("~355 ms → ~300 ms", result.stdout.decode("utf-8"))

	def test_check_reports_non_cp1252_path_to_cp1252_stdout(self) -> None:
		root = write_dir({"20260731-uni→code.md": VALID_FRAGMENT})
		changelog_path = write_dir({"CHANGELOG.md": "# Changelog\n\n## [Unreleased]\n"})
		env = dict(os.environ, PYTHONIOENCODING="cp1252")

		result = subprocess.run(
			[
				sys.executable,
				str(Path(__file__).with_name("changelog.py")),
				"--fragments-dir",
				str(root),
				"--changelog",
				str(changelog_path / "CHANGELOG.md"),
				"check",
			],
			capture_output=True,
			env=env,
		)

		self.assertIn("uni→code.md", result.stdout.decode("utf-8"))
		self.assertNotIn(b"UnicodeEncodeError", result.stderr)


if __name__ == "__main__":
	unittest.main()
