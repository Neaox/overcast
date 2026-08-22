#!/usr/bin/env python3
"""Tests for scripts/compat-name-hygiene.py.

Run: python3 scripts/compat_name_hygiene_test.py
"""

from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("compat-name-hygiene.py")
SPEC = importlib.util.spec_from_file_location("compat_name_hygiene", SCRIPT)
assert SPEC is not None
hygiene = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = hygiene
assert SPEC.loader is not None
SPEC.loader.exec_module(hygiene)


def write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


class CommentStrippingTest(unittest.TestCase):
    def test_strips_trailing_line_comment(self):
        self.assertEqual(hygiene.strip_line_comments('x := 1 // note', "go"), "x := 1 ")

    def test_does_not_strip_slashes_inside_a_string(self):
        line = 'p.value("jdbc://localhost")'
        self.assertEqual(hygiene.strip_line_comments(line, "go"), line)

    def test_python_hash_comment(self):
        self.assertEqual(hygiene.strip_line_comments("x = 1  # note", "python"), "x = 1  ")


class ExtractLiteralsTest(unittest.TestCase):
    def test_ts_template_literal_with_runid(self):
        line = "const name = `${ctx.runId}-role`;"
        self.assertEqual(hygiene.extract_interpolated_literals(line, "ts"), ["${ctx.runId}-role"])

    def test_ts_template_literal_without_runid_is_ignored(self):
        line = "const name = `${other}-role`;"
        self.assertEqual(hygiene.extract_interpolated_literals(line, "ts"), [])

    def test_python_fstring_with_run_id(self):
        line = 'name = f"{ctx.run_id}-role"'
        self.assertEqual(hygiene.extract_interpolated_literals(line, "python"), ["{ctx.run_id}-role"])

    def test_go_sprintf_with_runid_arg(self):
        line = 'name := fmt.Sprintf("%s-role", t.RunID)'
        self.assertEqual(hygiene.extract_format_call_literals(line, "go"), ["%s-role"])

    def test_go_sprintf_without_runid_arg_is_ignored(self):
        line = 'name := fmt.Sprintf("%s-role", other)'
        self.assertEqual(hygiene.extract_format_call_literals(line, "go"), [])

    def test_java_concatenation(self):
        line = '        String name = "compat-role-" + ctx.runId();'
        self.assertEqual(hygiene.extract_concat_literals(line, "java"), ["compat-role-"])

    def test_java_text_block_delimiter_is_not_mis_parsed(self):
        line = '                """.formatted(ctx.runId());'
        self.assertEqual(hygiene.extract_concat_literals(line, "java"), [])


class StaticTextTest(unittest.TestCase):
    def test_bare_runid_reduces_to_empty(self):
        self.assertEqual(hygiene.normalize(hygiene.strip_placeholders("%s", "go")), "")

    def test_descriptive_name_keeps_static_text(self):
        self.assertEqual(
            hygiene.normalize(hygiene.strip_placeholders("${ctx.runId}-sts-assume-role", "ts")),
            "sts-assume-role",
        )


class ResourceKindTest(unittest.TestCase):
    def test_tags_role_context(self):
        self.assertEqual(hygiene.resource_kind("RoleArn: `arn:aws:iam::0:role/x`"), "role")

    def test_untagged_when_no_keyword_present(self):
        self.assertIsNone(hygiene.resource_kind("apiName := fmt.Sprintf(\"compat-%s\", t.RunID)"))


class FindViolationsTest(unittest.TestCase):
    def suite_cfg(self, tmp: Path, name: str = "fake-sdk") -> list[dict]:
        return [
            {
                "name": name,
                "dir": name,
                "ext": ".go",
                "lang": "go",
                "exclude": set(),
            }
        ]

    def test_cross_file_same_kind_collision_is_flagged(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            # sts-assume reusing iam-roles' exact role name — the real bug
            # class this check exists to catch.
            write(
                root / "fake-sdk" / "iam.go",
                'func setup() { name := fmt.Sprintf("%s-role", t.RunID) }',
            )
            write(
                root / "fake-sdk" / "sts.go",
                'func assume() { roleArn := fmt.Sprintf("role/%s-role", t.RunID) }',
            )
            v = hygiene.find_violations(root, set(), suites=self.suite_cfg(root))
            self.assertEqual(len(v.collisions), 1)

    def test_same_file_reuse_is_not_flagged(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            write(
                root / "fake-sdk" / "iam.go",
                'func a() { n := fmt.Sprintf("%s-role", t.RunID) }\n'
                'func b() { n := fmt.Sprintf("%s-role", t.RunID) }\n',
            )
            v = hygiene.find_violations(root, set(), suites=self.suite_cfg(root))
            self.assertEqual(v.collisions, [])

    def test_untagged_shared_scaffold_is_not_flagged(self):
        # Two different services both defaulting to a bare "compat-{runId}"
        # name is not a hygiene bug: different AWS services never share a
        # namespace even when the literal string coincides.
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            write(
                root / "fake-sdk" / "apigateway.go",
                'func a() { n := fmt.Sprintf("compat-%s", t.RunID) }',
            )
            write(
                root / "fake-sdk" / "appsync.go",
                'func b() { n := fmt.Sprintf("compat-%s", t.RunID) }',
            )
            v = hygiene.find_violations(root, set(), suites=self.suite_cfg(root))
            self.assertEqual(v.collisions, [])

    def test_allowlist_suppresses_a_known_collision(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            write(
                root / "fake-sdk" / "iam.go",
                'func a() { n := fmt.Sprintf("%s-role", t.RunID) }',
            )
            write(
                root / "fake-sdk" / "sts.go",
                'func b() { n := fmt.Sprintf("%s-role", t.RunID) }',
            )
            allow = {("fake-sdk", "role")}
            v = hygiene.find_violations(root, allow, suites=self.suite_cfg(root))
            self.assertEqual(v.collisions, [])

    def test_bare_runid_name_is_always_flagged(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            write(
                root / "fake-sdk" / "ses.go",
                'func a() { n := fmt.Sprintf("%s", t.RunID) }',
            )
            v = hygiene.find_violations(root, set(), suites=self.suite_cfg(root))
            self.assertEqual(len(v.bare_runid), 1)


class RealRepoTest(unittest.TestCase):
    """Pins the current, already-audited-clean state of the real suites.

    If this starts failing, either a new suite introduced the sts-assume-role
    bug class again, or the new pattern is intentional and belongs in
    compat/suites/name-hygiene-allowlist.json.
    """

    def test_real_suites_have_no_unaccounted_violations(self):
        allowlist = hygiene.load_allowlist(hygiene.DEFAULT_ALLOWLIST)
        v = hygiene.find_violations(hygiene.REPO_ROOT, allowlist)
        self.assertEqual(v.bare_runid, [], "bare run-id name(s) found")
        self.assertEqual(v.collisions, [], "cross-file same-kind name collision(s) found")


if __name__ == "__main__":
    unittest.main()
