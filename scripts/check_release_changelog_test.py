#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("check-release-changelog.py")
SPEC = importlib.util.spec_from_file_location("check_release_changelog", SCRIPT)
assert SPEC is not None
checker = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(checker)


def write_changelog(text: str) -> Path:
	path = Path(tempfile.mkdtemp()) / "CHANGELOG.md"
	path.write_text(text, encoding="utf-8")
	return path


class CheckReleaseChangelogTest(unittest.TestCase):
	def test_validate_accepts_exhaustive_compare_links(self) -> None:
		path = write_changelog(
			"""
# Changelog

## [Unreleased]

### Fixed

## [0.0.1-alpha.2] - 2026-07-22

### Fixed

- Fix.

## [0.0.1-alpha.1] - 2026-07-21

### Fixed

- Fix.

## [0.0.1-alpha.0] - 2026-07-20

### Added

- Initial.

[Unreleased]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.2...HEAD
[0.0.1-alpha.2]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.1...v0.0.1-alpha.2
[0.0.1-alpha.1]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.0...v0.0.1-alpha.1
[0.0.1-alpha.0]: https://github.com/Neaox/overcast/releases/tag/v0.0.1-alpha.0
"""
		)

		errors = checker.validate(path, "0.0.1-alpha.2")

		self.assertEqual([], errors)

	def test_validate_rejects_stale_unreleased_compare_link(self) -> None:
		path = write_changelog(
			"""
# Changelog

## [Unreleased]

### Fixed

## [0.0.1-alpha.2] - 2026-07-22

### Fixed

- Fix.

## [0.0.1-alpha.1] - 2026-07-21

### Fixed

- Fix.

[Unreleased]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.1...HEAD
[0.0.1-alpha.2]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.1...v0.0.1-alpha.2
[0.0.1-alpha.1]: https://github.com/Neaox/overcast/releases/tag/v0.0.1-alpha.1
"""
		)

		errors = checker.validate(path, "0.0.1-alpha.2")

		self.assertIn(
			"CHANGELOG.md link [Unreleased] must be "
			"https://github.com/Neaox/overcast/compare/v0.0.1-alpha.2...HEAD.",
			errors,
		)

	def test_validate_rejects_missing_release_compare_link(self) -> None:
		path = write_changelog(
			"""
# Changelog

## [Unreleased]

### Fixed

## [0.0.1-alpha.2] - 2026-07-22

### Fixed

- Fix.

## [0.0.1-alpha.1] - 2026-07-21

### Fixed

- Fix.

[Unreleased]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.2...HEAD
[0.0.1-alpha.1]: https://github.com/Neaox/overcast/releases/tag/v0.0.1-alpha.1
"""
		)

		errors = checker.validate(path, "0.0.1-alpha.2")

		self.assertIn(
			"CHANGELOG.md is missing link reference [0.0.1-alpha.2].",
			errors,
		)

	def test_validate_rejects_unconsumed_fragments(self) -> None:
		path = write_changelog(
			"""
# Changelog

## [Unreleased]

## [0.0.1-alpha.2] - 2026-07-22

### Fixed

- Fix.

[Unreleased]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.2...HEAD
[0.0.1-alpha.2]: https://github.com/Neaox/overcast/releases/tag/v0.0.1-alpha.2
"""
		)
		fragments_dir = path.parent / ".changelog"
		fragments_dir.mkdir()
		(fragments_dir / "README.md").write_text("docs", encoding="utf-8")
		(fragments_dir / "20260730-left-behind.md").write_text(
			"---\nsection: Fixed\n---\n\n- forgotten\n", encoding="utf-8"
		)

		errors = checker.validate(path, "0.0.1-alpha.2", fragments_dir)

		self.assertTrue(
			any("20260730-left-behind.md" in error for error in errors),
			errors,
		)

	def test_validate_accepts_consumed_fragments_dir(self) -> None:
		path = write_changelog(
			"""
# Changelog

## [Unreleased]

## [0.0.1-alpha.2] - 2026-07-22

### Fixed

- Fix.

[Unreleased]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.2...HEAD
[0.0.1-alpha.2]: https://github.com/Neaox/overcast/releases/tag/v0.0.1-alpha.2
"""
		)
		fragments_dir = path.parent / ".changelog"
		fragments_dir.mkdir()
		(fragments_dir / "README.md").write_text("docs", encoding="utf-8")

		errors = checker.validate(path, "0.0.1-alpha.2", fragments_dir)

		self.assertEqual([], errors)

		# A missing fragments dir must not fail older checkouts.
		errors = checker.validate(
			path, "0.0.1-alpha.2", path.parent / "no-such-dir"
		)

		self.assertEqual([], errors)


class StdioEncodingTest(unittest.TestCase):
	# Same reason as scripts/changelog_test.py: error lines echo fragment file
	# names, which are not guaranteed to be cp1252-encodable, and the Windows
	# console default is cp1252. PYTHONIOENCODING reproduces it on any platform.
	def test_reports_non_cp1252_fragment_name_to_cp1252_stdout(self) -> None:
		path = write_changelog(
			"""
# Changelog

## [Unreleased]

## [0.0.1-alpha.2] - 2026-07-22

### Fixed

- Fix.

[Unreleased]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.2...HEAD
[0.0.1-alpha.2]: https://github.com/Neaox/overcast/releases/tag/v0.0.1-alpha.2
"""
		)
		fragments_dir = path.parent / ".changelog"
		fragments_dir.mkdir()
		(fragments_dir / "20260731-uni→code.md").write_text(
			"---\nsection: Fixed\n---\n\n- forgotten\n", encoding="utf-8"
		)
		env = dict(os.environ, PYTHONIOENCODING="cp1252")

		result = subprocess.run(
			[
				sys.executable,
				str(SCRIPT),
				"0.0.1-alpha.2",
				"--changelog",
				str(path),
				"--fragments-dir",
				str(fragments_dir),
			],
			capture_output=True,
			env=env,
		)

		self.assertIn("uni→code.md", result.stdout.decode("utf-8"))
		self.assertNotIn(b"UnicodeEncodeError", result.stderr)


if __name__ == "__main__":
	unittest.main()
