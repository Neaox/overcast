#!/usr/bin/env python3
"""Tests for the optional rich prompt.

Skipped wholesale when prompt_toolkit is absent, which is the case in CI: the
module is a local convenience and must never become a CI dependency.
"""

from __future__ import annotations

import unittest

try:
	import changelog_prompt

	HAVE_PROMPT_TOOLKIT = True
except ImportError:  # pragma: no cover - exercised by the CI interpreter
	HAVE_PROMPT_TOOLKIT = False


@unittest.skipUnless(HAVE_PROMPT_TOOLKIT, "prompt_toolkit is not installed")
class AreaVocabularyTest(unittest.TestCase):
	def test_discovers_services_and_concept_areas(self) -> None:
		areas = changelog_prompt.area_vocabulary()

		self.assertEqual("service", areas.get("sqs"))
		self.assertEqual("service", areas.get("cloudformation"))
		self.assertEqual("area", areas.get("release"))
		self.assertNotIn("SQS", areas)


@unittest.skipUnless(HAVE_PROMPT_TOOLKIT, "prompt_toolkit is not installed")
class EntryLexerTest(unittest.TestCase):
	def tokens(self, text: str) -> list[tuple[str, str]]:
		lexer = changelog_prompt.EntryLexer({"sqs": "service", "efs": "service"})
		return list(lexer.tokens(text))

	def test_styles_kind_marker_and_areas(self) -> None:
		tokens = self.tokens("+! [sqs] a thing")

		self.assertIn(("class:kind.added", "+"), tokens)
		self.assertIn(("class:marker.breaking", "!"), tokens)
		self.assertIn(("class:area.known", "sqs"), tokens)

	def test_flags_an_unknown_area(self) -> None:
		tokens = self.tokens("+ [lamda] a typo")

		self.assertIn(("class:area.unknown", "lamda"), tokens)

	def test_highlights_code_spans_and_migration_notes(self) -> None:
		self.assertIn(("class:code", "`ReceiveMessage`"), self.tokens("+ `ReceiveMessage` polls"))
		self.assertIn(("class:migration", "migration:"), self.tokens("  migration: do a thing"))

	def test_preserves_every_character(self) -> None:
		# A lexer that drops or invents text would corrupt what the user sees.
		for line in (
			"+! [sqs/efs] mounts `Foo` now",
			"  migration: export first",
			"   plain continuation",
			"~ no area at all",
		):
			self.assertEqual(line, "".join(text for _, text in self.tokens(line)))


@unittest.skipUnless(HAVE_PROMPT_TOOLKIT, "prompt_toolkit is not installed")
class EntryCompleterTest(unittest.TestCase):
	def complete(self, text: str) -> list[str]:
		from prompt_toolkit.document import Document

		completer = changelog_prompt.EntryCompleter(
			{"sqs": "service", "sns": "service", "efs": "service"}
		)
		return [
			completion.text
			for completion in completer.get_completions(Document(text, len(text)), None)
		]

	def test_completes_area_slugs_inside_brackets(self) -> None:
		self.assertEqual(["sqs", "sns"], self.complete("+ [s"))

	def test_completes_the_second_area_after_a_slash(self) -> None:
		self.assertEqual(["efs"], self.complete("+ [sqs/e"))

	def test_offers_nothing_once_the_bracket_closes(self) -> None:
		self.assertEqual([], self.complete("+ [sqs] some prose s"))

	def test_completes_spelled_out_sections_at_the_start(self) -> None:
		self.assertEqual(["security"], self.complete("sec"))


@unittest.skipUnless(HAVE_PROMPT_TOOLKIT, "prompt_toolkit is not installed")
class EntryValidatorTest(unittest.TestCase):
	def error_for(self, text: str) -> str:
		from prompt_toolkit.document import Document
		from prompt_toolkit.validation import ValidationError

		try:
			changelog_prompt.EntryValidator().validate(Document(text, len(text)))
		except ValidationError as exc:
			return exc.message
		return ""

	def test_accepts_a_good_entry(self) -> None:
		self.assertEqual("", self.error_for("+ [sqs] long polling"))

	def test_rejects_an_unknown_kind(self) -> None:
		self.assertIn("unknown kind", self.error_for("improved [sqs] thing"))

	def test_does_not_demand_a_migration_note_mid_entry(self) -> None:
		# The note arrives on the next line; blocking here would reject input
		# that is about to become correct.
		self.assertEqual("", self.error_for("+! [sqs] a required field"))

	def test_ignores_continuations_and_blanks(self) -> None:
		self.assertEqual("", self.error_for("  migration: do a thing"))
		self.assertEqual("", self.error_for(""))


@unittest.skipUnless(HAVE_PROMPT_TOOLKIT, "prompt_toolkit is not installed")
class DescribeTest(unittest.TestCase):
	def test_reads_back_section_area_and_compatibility(self) -> None:
		self.assertEqual(
			"Added | sqs | compatible", changelog_prompt.describe("+ [sqs] long polling")
		)
		self.assertEqual(
			"Removed | state | BREAKING", changelog_prompt.describe("- [state] v1 layout")
		)

	def test_describes_continuations_and_blanks(self) -> None:
		self.assertIn("migration note", changelog_prompt.describe("  migration: x"))
		self.assertIn("continuation", changelog_prompt.describe("  more prose"))
		self.assertIn("blank line finishes", changelog_prompt.describe(""))


if __name__ == "__main__":
	unittest.main()
