#!/usr/bin/env python3

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

import changelog


def write_dir(files: dict[str, str]) -> Path:
	root = Path(tempfile.mkdtemp())
	for name, text in files.items():
		(root / name).write_text(text, encoding="utf-8")
	return root


VALID_FRAGMENT = """---
section: Fixed
area: cloudformation
---

- [cloudformation] update replacements no longer destroy same-ID resources
"""


class ParseFragmentTest(unittest.TestCase):
	def test_accepts_valid_fragment(self) -> None:
		root = write_dir({"20260730-cfn-update.md": VALID_FRAGMENT})

		fragment, errors = changelog.parse_fragment(root / "20260730-cfn-update.md")

		self.assertEqual([], errors)
		assert fragment is not None
		self.assertEqual("Fixed", fragment.section)
		self.assertEqual("cloudformation", fragment.area)
		self.assertTrue(fragment.body.startswith("- [cloudformation]"))

	def test_accepts_trailing_comment_and_missing_area(self) -> None:
		root = write_dir(
			{
				"20260730-tls.md": (
					"---\n"
					"section: Added        # Added | Changed | Fixed\n"
					"---\n"
					"\n"
					"- HTTPS support\n"
				)
			}
		)

		fragment, errors = changelog.parse_fragment(root / "20260730-tls.md")

		self.assertEqual([], errors)
		assert fragment is not None
		self.assertEqual("Added", fragment.section)
		self.assertEqual("", fragment.area)

	def test_rejects_bad_filename(self) -> None:
		root = write_dir({"MyFix.md": VALID_FRAGMENT})

		_, errors = changelog.parse_fragment(root / "MyFix.md")

		self.assertTrue(any("YYYYMMDD-<slug>.md" in e for e in errors))

	def test_rejects_unknown_section(self) -> None:
		root = write_dir(
			{"20260730-x.md": "---\nsection: Improved\n---\n\n- entry\n"}
		)

		_, errors = changelog.parse_fragment(root / "20260730-x.md")

		self.assertTrue(any("'section' must be one of" in e for e in errors))

	def test_rejects_unknown_key(self) -> None:
		root = write_dir(
			{"20260730-x.md": "---\nsection: Fixed\npr: 42\n---\n\n- entry\n"}
		)

		_, errors = changelog.parse_fragment(root / "20260730-x.md")

		self.assertTrue(any("unknown frontmatter key 'pr'" in e for e in errors))

	def test_rejects_missing_frontmatter(self) -> None:
		root = write_dir({"20260730-x.md": "- entry without frontmatter\n"})

		_, errors = changelog.parse_fragment(root / "20260730-x.md")

		self.assertTrue(any("frontmatter" in e for e in errors))

	def test_rejects_empty_body(self) -> None:
		root = write_dir({"20260730-x.md": "---\nsection: Fixed\n---\n\n"})

		_, errors = changelog.parse_fragment(root / "20260730-x.md")

		self.assertTrue(any("body is empty" in e for e in errors))

	def test_rejects_non_list_body(self) -> None:
		root = write_dir(
			{"20260730-x.md": "---\nsection: Fixed\n---\n\nplain prose entry\n"}
		)

		_, errors = changelog.parse_fragment(root / "20260730-x.md")

		self.assertTrue(any("markdown list items" in e for e in errors))

	def test_rejects_headings_in_body(self) -> None:
		root = write_dir(
			{"20260730-x.md": "---\nsection: Fixed\n---\n\n- entry\n\n### Fixed\n"}
		)

		_, errors = changelog.parse_fragment(root / "20260730-x.md")

		self.assertTrue(any("must not contain headings" in e for e in errors))


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

		_, errors = changelog.load_fragments(root / "does-not-exist")

		self.assertTrue(any("directory is missing" in e for e in errors))


class UnreleasedErrorsTest(unittest.TestCase):
	def test_accepts_empty_unreleased_with_comments_and_headings(self) -> None:
		root = write_dir(
			{
				"CHANGELOG.md": (
					"# Changelog\n\n"
					"<!-- do not edit [Unreleased]; see .changelog/README.md -->\n\n"
					"## [Unreleased]\n\n### Fixed\n\n"
					"## [0.0.1] - 2026-07-30\n\n- shipped\n"
				)
			}
		)

		self.assertEqual([], changelog.unreleased_errors(root / "CHANGELOG.md"))

	def test_rejects_content_in_unreleased(self) -> None:
		root = write_dir(
			{
				"CHANGELOG.md": (
					"# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n- sneaky entry\n\n"
					"## [0.0.1] - 2026-07-30\n\n- shipped\n"
				)
			}
		)

		errors = changelog.unreleased_errors(root / "CHANGELOG.md")

		self.assertTrue(any("must stay empty between releases" in e for e in errors))

	def test_rejects_missing_unreleased_section(self) -> None:
		root = write_dir({"CHANGELOG.md": "# Changelog\n\n## [0.0.1] - 2026-07-30\n"})

		errors = changelog.unreleased_errors(root / "CHANGELOG.md")

		self.assertTrue(any("missing a '## [Unreleased]'" in e for e in errors))


class AssembleTest(unittest.TestCase):
	def test_groups_by_section_then_area(self) -> None:
		root = write_dir(
			{
				"20260730-zeta.md": "---\nsection: Fixed\n---\n\n- no-area fix\n",
				"20260729-sqs.md": "---\nsection: Fixed\narea: sqs\n---\n\n- [sqs] fix one\n",
				"20260731-sqs.md": "---\nsection: Fixed\narea: sqs\n---\n\n- [sqs] fix two\n",
				"20260730-glue.md": "---\nsection: Added\narea: glue\n---\n\n- **Glue** — new service\n",
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
			"- **Glue** — new service\n"
			"\n"
			"### Fixed\n"
			"\n"
			"- [sqs] fix one\n"
			"\n"
			"- [sqs] fix two\n"
			"\n"
			"- no-area fix\n",
			output,
		)


if __name__ == "__main__":
	unittest.main()
