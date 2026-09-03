#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("ci-scope.py")
SPEC = importlib.util.spec_from_file_location("ci_scope", SCRIPT)
assert SPEC is not None
scope = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
# Registered before exec, as changelog_required_test does: a hyphenated
# filename cannot be imported any other way.
sys.modules["ci_scope"] = scope
SPEC.loader.exec_module(scope)


class NeedsCodeJobs(unittest.TestCase):
	"""The decision this file exists to protect.

	A wrong `True` costs a CI run. A wrong `False` skips the suite on a change
	that needed it — a green tick over untested code — so every ambiguous case
	below asserts `True`.
	"""

	def test_prose_only_skips(self) -> None:
		for files in (
			["AGENTS.md"],
			["docs/plans/ci-streamlining.md"],
			["docs/dev/development-setup.md"],
			[".changelog/20260804-something.md"],
			["README.md", "CONTRIBUTING.md", ".changelog/x.md"],
		):
			with self.subTest(files=files):
				self.assertFalse(scope.needs_code_jobs(files))

	def test_any_code_file_runs_everything(self) -> None:
		for files in (
			["internal/services/rds/password.go"],
			["Dockerfile"],
			[".github/workflows/test.yml"],
			["scripts/verify-changed.sh"],
			["web/src/main.tsx"],
			["go.mod"],
		):
			with self.subTest(files=files):
				self.assertTrue(scope.needs_code_jobs(files))

	def test_a_mixed_change_runs_everything(self) -> None:
		# The case that breaks the twin-workflow approach, and the reason this
		# is a classifier: one doc plus one handler is a code change.
		self.assertTrue(scope.needs_code_jobs(["docs/services/rds.md", "internal/services/rds/password.go"]))

	def test_a_published_doc_is_code(self) -> None:
		# embed.go compiles published docs into the binary and
		# internal/docsindex indexes them, so a published doc is a build input
		# and the corpus tests assert on it. Treating one as prose would skip
		# the suite that has to accept it.
		for files in (
			["docs/services/rds.md"],
			["docs/README.md"],
			["docs/cdk/local-vpc.md"],
			["docs/generated/service-support.json"],
		):
			with self.subTest(files=files):
				self.assertTrue(scope.needs_code_jobs(files))

	def test_a_contributor_doc_is_not(self) -> None:
		# docs/plans/ and docs/dev/ are excluded from the embed pattern and the
		# index, so nothing reads them.
		self.assertFalse(scope.needs_code_jobs(["docs/dev/content-charter.md", "docs/plans/x.md"]))

	def test_the_tells_allowlist_is_code(self) -> None:
		# It lives under docs/dev/, but `make docs-lint` reads it and fails on an
		# entry that no longer matches, so it has to run the docs gate.
		self.assertFalse(scope.is_prose("docs/dev/llm-tells-allowlist.txt"))
		self.assertTrue(scope.needs_code_jobs(["docs/dev/llm-tells-allowlist.txt"]))

	def test_markdown_outside_docs_is_still_prose(self) -> None:
		self.assertFalse(scope.needs_code_jobs(["compat/AGENTS.md", "tests/AGENTS.md"]))

	def test_empty_change_set_runs_everything(self) -> None:
		# Not knowing what changed is not the same as knowing nothing did.
		self.assertTrue(scope.needs_code_jobs([]))

	def test_a_go_file_named_like_docs_is_code(self) -> None:
		# docs/ is a prefix match, not a substring one.
		self.assertFalse(scope.is_prose("docs_search.go"))
		self.assertFalse(scope.is_prose("internal/docssearch/index.gen.go"))


class IsProse(unittest.TestCase):
	def test_suffix_and_prefix(self) -> None:
		self.assertTrue(scope.is_prose("CONTRIBUTING.md"))
		self.assertTrue(scope.is_prose("docs/dev/testing.md"))
		self.assertFalse(scope.is_prose("docs/generated/service-support.json"))
		self.assertFalse(scope.is_prose("internal/router/router.go"))
		self.assertFalse(scope.is_prose("Makefile"))


if __name__ == "__main__":
	unittest.main()
