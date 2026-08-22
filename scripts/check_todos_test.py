"""Unit tests for check_todos.py — the comment-marker gate.

Run directly: python3 scripts/check_todos_test.py
CI runs every scripts/*_test.py in the script-tests job.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from check_todos import (  # noqa: E402
    load_ignores,
    load_languages,
    scan_text,
)

# Fixtures are assembled rather than written out, so this file never trips its
# own gate: a hash followed by a marker, on one line of a .py file, is exactly
# what the gate is looking for.
MARKER = "TODO"
HASH = "#"

GO = [
    {"type": "line", "pattern": "//"},
    {"type": "block", "pattern": {"start": "/\\*", "end": "\\*/"}},
]
PY = [{"type": "line", "pattern": HASH}]


class CanonicalTest(unittest.TestCase):
    def test_canonical_marker_passes(self):
        text = f"\t// {MARKER}(priority:P2): implement authorizer CRUD\n"
        self.assertEqual(scan_text(text, GO), [])

    def test_every_priority_digit_passes(self):
        for n in (0, 1, 2, 3):
            with self.subTest(priority=n):
                text = f"// {MARKER}(priority:P{n}): do the thing\n"
                self.assertEqual(scan_text(text, GO), [])

    def test_marker_with_no_description_files_nothing(self):
        findings = scan_text(f"// {MARKER}(priority:P2):\n", GO)
        self.assertEqual(findings, [])  # no title to take, so nothing is filed

    def test_marker_without_a_priority_is_flagged(self):
        findings = scan_text(f"// {MARKER}(perf): share a Docker client\n", GO)
        self.assertEqual(findings, [(1, "share a Docker client")])

    def test_bare_marker_is_flagged(self):
        findings = scan_text(f"// {MARKER}: set to true once implemented\n", GO)
        self.assertEqual(findings, [(1, "set to true once implemented")])


class ProseTest(unittest.TestCase):
    def test_marker_named_mid_sentence_is_flagged(self):
        # Issue #1138, verbatim: a reference to a marker elsewhere, which the
        # action read as a marker of its own.
        text = (
            "// are accepted by CDK/CFN but the service has no field for any of them\n"
            f"// (see the {MARKER}(priority:P3) on apigateway.Method) — intentionally not\n"
            "// forwarded rather than silently claimed as honoured.\n"
        )
        self.assertEqual(
            scan_text(text, GO), [(2, "on apigateway.Method) — intentionally not")]
        )

    def test_lowercase_marker_in_prose_is_flagged(self):
        findings = scan_text("// the todo list here is long\n", GO)
        self.assertEqual(findings, [(1, "list here is long")])

    def test_context_todo_is_not_a_marker(self):
        # context.TODO() cannot match: the action needs a colon or a space
        # after the marker, and an empty pair of parentheses is neither.
        text = f"// pass context.{MARKER}() when there is no parent context\n"
        self.assertEqual(scan_text(text, GO), [])

    def test_marker_in_code_without_a_comment_is_ignored(self):
        text = f'\tconst label = "{MARKER}: not a comment"\n'
        self.assertEqual(scan_text(text, PY), [])


class BlockCommentTest(unittest.TestCase):
    def test_block_comment_interior_is_flagged(self):
        text = f"/*\n * {MARKER}: wire this up\n */\n"
        self.assertEqual(scan_text(text, GO), [(2, "wire this up")])

    def test_inline_block_comment_is_flagged(self):
        self.assertEqual(
            scan_text(f"/* {MARKER}: wire this up */\n", GO), [(1, "wire this up")]
        )

    def test_star_in_a_string_does_not_open_a_block(self):
        # A route literal contains "/*". Treating it as a block start turned
        # the rest of the file into comment, and the canonical marker below it
        # into a finding.
        text = (
            '\tr.Get("/v2/tags/*", h.GetV2Tags)\n'
            f"\t// {MARKER}(priority:P2): implement v2 execution routes\n"
        )
        self.assertEqual(scan_text(text, GO), [])

    def test_block_closes_on_its_end_marker(self):
        text = f"/* opening */\n// {MARKER}(priority:P1): still canonical here\n"
        self.assertEqual(scan_text(text, GO), [])


class ConfigTest(unittest.TestCase):
    def test_languages_come_from_the_action_config(self):
        markers = load_languages()
        self.assertIn(".go", markers)
        self.assertIn(".py", markers)
        self.assertEqual(markers[".go"][0]["pattern"], "//")
        # Markdown is block-only, so prose in a doc is not scanned.
        self.assertTrue(all(m["type"] == "block" for m in markers[".md"]))

    def test_ignores_come_from_the_workflow(self):
        ignores = load_ignores()
        self.assertTrue(
            any(i.match("internal/mcp/providers/repo_provider_test.go") for i in ignores),
            "the workflow's IGNORE should cover the MCP fixture file",
        )
        self.assertFalse(any(i.match("internal/services/sqs/handler.go") for i in ignores))


if __name__ == "__main__":
    unittest.main()
