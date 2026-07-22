#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
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


if __name__ == "__main__":
	unittest.main()
