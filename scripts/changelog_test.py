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


CHANGELOG_FIXTURE = """# Changelog

## [Unreleased]

## [0.0.1-alpha.1] - 2026-07-01

### Fixed

- [sqs] an older fix

[Unreleased]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.1...HEAD
[0.0.1-alpha.1]: https://github.com/Neaox/overcast/releases/tag/v0.0.1-alpha.1
"""


class NextVersionTest(unittest.TestCase):
	def test_increments_the_prerelease_counter(self) -> None:
		self.assertEqual("0.0.1-alpha.28", changelog.next_version("0.0.1-alpha.27"))
		self.assertEqual("1.2.3-beta.1", changelog.next_version("1.2.3-beta.0\n"))

	def test_refuses_a_stable_version_rather_than_guessing(self) -> None:
		# The breaking/added -> major/minor mapping is an open policy decision
		# that takes effect at 1.0; guessing it here would be worse than a gap.
		with self.assertRaises(ValueError) as caught:
			changelog.next_version("1.0.0")

		self.assertIn("unimplemented until", str(caught.exception))


class ChangelogEditTest(unittest.TestCase):
	def test_inserts_the_section_below_unreleased(self) -> None:
		result = changelog.insert_release_section(
			CHANGELOG_FIXTURE, "## [0.0.1-alpha.2] - 2026-08-01\n\n### Added\n\n- a thing\n"
		)

		self.assertIn("## [Unreleased]\n\n## [0.0.1-alpha.2] - 2026-08-01\n", result)
		self.assertIn("- a thing\n\n## [0.0.1-alpha.1] - 2026-07-01", result)

	def test_rejects_a_changelog_without_unreleased(self) -> None:
		with self.assertRaises(ValueError):
			changelog.insert_release_section("# Changelog\n", "## [x] - y\n")

	def test_repoints_unreleased_and_adds_the_new_reference(self) -> None:
		result = changelog.update_links(CHANGELOG_FIXTURE, "0.0.1-alpha.2", "0.0.1-alpha.1")

		self.assertIn(
			"[Unreleased]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.2...HEAD",
			result,
		)
		self.assertIn(
			"[0.0.1-alpha.2]: https://github.com/Neaox/overcast/compare/"
			"v0.0.1-alpha.1...v0.0.1-alpha.2",
			result,
		)
		self.assertIn("[0.0.1-alpha.1]: https://github.com/Neaox/overcast/releases/tag/", result)

	def test_first_release_links_to_the_tag(self) -> None:
		result = changelog.update_links(CHANGELOG_FIXTURE, "0.0.1-alpha.0", None)

		self.assertIn(
			"[0.0.1-alpha.0]: https://github.com/Neaox/overcast/releases/tag/v0.0.1-alpha.0",
			result,
		)


class ReleaseSummaryTest(unittest.TestCase):
	def summary(self, fragment_text: str) -> str:
		root = write_dir({"20260801-x.md": fragment_text})
		fragments, errors = changelog.load_fragments(root)
		self.assertEqual([], errors)
		return changelog.release_summary(fragments, "0.0.1-alpha.2")

	def test_counts_entries_by_section(self) -> None:
		text = self.summary("+ [sqs] one\n* [sqs] two\n* [efs] three\n")

		self.assertIn("3 entries (1 Added, 2 Fixed)", text)
		self.assertIn("No breaking changes.", text)

	def test_renders_the_entries_as_they_will_appear_in_the_changelog(self) -> None:
		# A count tells a reader nothing they can act on; the comment exists to
		# show what the release notes will say.
		text = self.summary("+ [sqs] long polling\n* [efs] a mount fix\n")

		self.assertIn("### Added\n\n- [sqs] long polling", text)
		self.assertIn("### Fixed\n\n- [efs] a mount fix", text)
		self.assertNotIn("## [0.0.1-alpha.2]", text)

	def test_heading_can_be_replaced(self) -> None:
		root = write_dir({"20260801-x.md": "+ [sqs] a thing\n"})
		fragments, _ = changelog.load_fragments(root)

		text = changelog.release_summary(fragments, "1.0.0", heading="### Custom")

		self.assertTrue(text.startswith("### Custom\n"))
		self.assertNotIn("1 entries", text)

	def test_lists_breaking_changes_with_their_migrations(self) -> None:
		text = self.summary(
			"+ [sqs] a feature\n"
			"-! [state] the v1 layout\n"
			"  migration: export before upgrading\n"
		)

		self.assertIn("### Breaking changes (1)", text)
		self.assertIn("- [state] the v1 layout", text)
		self.assertIn("**migration:** export before upgrading", text)


class CommandSummaryTest(unittest.TestCase):
	def run_summary(self, root: Path, *args: str) -> subprocess.CompletedProcess[bytes]:
		return subprocess.run(
			[
				sys.executable,
				str(Path(__file__).with_name("changelog.py")),
				"--fragments-dir",
				str(root),
				"summary",
				"0.0.1-alpha.2",
				*args,
			],
			capture_output=True,
		)

	def test_prints_the_entries(self) -> None:
		root = write_dir({"20260801-x.md": "+ [sqs] long polling\n"})

		result = self.run_summary(root)

		self.assertEqual(0, result.returncode, result.stderr.decode("utf-8", "replace"))
		self.assertIn("- [sqs] long polling", result.stdout.decode("utf-8"))

	def test_prints_the_empty_text_when_no_fragments_remain(self) -> None:
		# The release-prep workflow uses this to say "nothing to curate"
		# rather than posting an empty comment.
		root = write_dir({"README.md": "# not a fragment"})

		result = self.run_summary(root, "--empty", "Nothing to curate.")

		self.assertEqual(0, result.returncode, result.stderr.decode("utf-8", "replace"))
		self.assertEqual("Nothing to curate.", result.stdout.decode("utf-8").strip())


class CommandReleaseTest(unittest.TestCase):
	def prepare(self) -> Path:
		root = Path(tempfile.mkdtemp())
		(root / ".changelog").mkdir()
		(root / ".changelog" / "20260801-a.md").write_text(
			"+ [sqs] long polling\n"
			"-! [state] the v1 layout\n"
			"  migration: export before upgrading\n",
			encoding="utf-8",
		)
		(root / "CHANGELOG.md").write_text(CHANGELOG_FIXTURE, encoding="utf-8")
		(root / "VERSION").write_text("0.0.1-alpha.1\n", encoding="utf-8")
		return root

	def run_release(self, root: Path, *args: str) -> subprocess.CompletedProcess[bytes]:
		return subprocess.run(
			[
				sys.executable,
				str(Path(__file__).with_name("changelog.py")),
				"--fragments-dir",
				str(root / ".changelog"),
				"--changelog",
				str(root / "CHANGELOG.md"),
				"release",
				"--version-file",
				str(root / "VERSION"),
				"--date",
				"2026-08-01",
				*args,
			],
			capture_output=True,
		)

	def test_writes_version_section_links_and_removes_fragments(self) -> None:
		root = self.prepare()

		result = self.run_release(root)

		self.assertEqual(0, result.returncode, result.stderr.decode("utf-8", "replace"))
		self.assertEqual("0.0.1-alpha.2\n", (root / "VERSION").read_text(encoding="utf-8"))
		text = (root / "CHANGELOG.md").read_text(encoding="utf-8")
		self.assertIn("## [0.0.1-alpha.2] - 2026-08-01", text)
		self.assertIn("- **BREAKING** [state] the v1 layout", text)
		self.assertIn("compare/v0.0.1-alpha.2...HEAD", text)
		self.assertEqual([], list((root / ".changelog").iterdir()))

	def test_result_satisfies_the_release_validator(self) -> None:
		# The workflow gates on this script, so the generated tree has to pass
		# it — that is the whole point of doing the edit mechanically.
		root = self.prepare()
		self.run_release(root)

		validated = subprocess.run(
			[
				sys.executable,
				str(Path(__file__).with_name("check-release-changelog.py")),
				"0.0.1-alpha.2",
				"--changelog",
				str(root / "CHANGELOG.md"),
				"--fragments-dir",
				str(root / ".changelog"),
			],
			capture_output=True,
		)

		self.assertEqual(0, validated.returncode, validated.stdout.decode("utf-8", "replace"))

	def test_writes_a_summary_file(self) -> None:
		root = self.prepare()

		self.run_release(root, "--summary-file", str(root / "summary.md"))

		summary = (root / "summary.md").read_text(encoding="utf-8")
		self.assertIn("### Breaking changes (1)", summary)

	def test_dry_run_changes_nothing(self) -> None:
		root = self.prepare()

		result = self.run_release(root, "--dry-run")

		self.assertEqual(0, result.returncode, result.stderr.decode("utf-8", "replace"))
		self.assertEqual("0.0.1-alpha.1\n", (root / "VERSION").read_text(encoding="utf-8"))
		self.assertEqual(1, len(list((root / ".changelog").iterdir())))

	def test_refuses_to_release_a_version_already_in_the_changelog(self) -> None:
		root = self.prepare()

		result = self.run_release(root, "0.0.1-alpha.1")

		self.assertEqual(1, result.returncode)
		self.assertIn("already has a", result.stderr.decode("utf-8"))


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
