#!/usr/bin/env python3
"""Tests for scripts/compat-report.py.

Run: python3 scripts/compat_report_test.py
"""

from __future__ import annotations

import importlib.util
import unittest
import xml.etree.ElementTree as ET
from pathlib import Path

SCRIPT = Path(__file__).with_name("compat-report.py")
SPEC = importlib.util.spec_from_file_location("compat_report", SCRIPT)
assert SPEC is not None
report = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(report)


def results(*tests: dict) -> dict:
    """Build a merged-results document from flat test dicts."""
    suites: dict[str, dict] = {}
    for t in tests:
        suite = suites.setdefault(t["suite"], {"Suite": t["suite"], "Groups": {}})
        group = suite["Groups"].setdefault(
            t["group"], {"Suite": t["suite"], "Service": t.get("service", "s3"), "Name": t["group"], "Tests": []}
        )
        group["Tests"].append(
            {
                "suite": t["suite"],
                "service": t.get("service", "s3"),
                "group": t["group"],
                "test": t["test"],
                "status": t["status"],
                "error": t.get("error", ""),
                "duration_ms": t.get("duration_ms", 1),
            }
        )
    return {
        "Endpoint": "http://localhost:4566",
        "Suites": [{**s, "Groups": list(s["Groups"].values())} for s in suites.values()],
    }


class RegressionKeysTest(unittest.TestCase):
    def test_extracts_keys_from_compare_output(self):
        lines = [
            "compat baseline regression: go-sdk/sqs-fifo/CreateFifoQueue pass -> fail",
            "compat baseline new failure, not in baseline: rust-sdk/s3-copy/CreateSourceBucket",
            "compat baseline missing result: cli/s3-crud/PutObject expected pass",
        ]
        self.assertEqual(
            report.regression_keys(lines),
            {
                "go-sdk/sqs-fifo/CreateFifoQueue",
                "rust-sdk/s3-copy/CreateSourceBucket",
                "cli/s3-crud/PutObject",
            },
        )

    def test_ignores_unrelated_lines(self):
        self.assertEqual(report.regression_keys(["go: downloading something", ""]), set())


class JUnitMappingTest(unittest.TestCase):
    def junit(self, data, regressions, baseline=None):
        _, _, xml = report.build(data, baseline or {"entries": []}, None, regressions, [])
        return ET.fromstring(xml)

    def test_regression_becomes_failure(self):
        # Given a failing test the baseline did not excuse
        data = results({"suite": "go-sdk", "group": "s3-crud", "test": "CreateBucket", "status": "fail", "error": "boom"})
        root = self.junit(data, ["compat baseline regression: go-sdk/s3-crud/CreateBucket pass -> fail"])
        # Then it is a JUnit failure, so the check run goes red on what changed
        self.assertEqual(len(root.findall(".//failure")), 1)
        self.assertEqual(len(root.findall(".//skipped")), 0)

    def test_known_failure_becomes_skipped(self):
        # Given a failure the baseline already records (burn-down pending)
        data = results({"suite": "rust-sdk", "group": "s3-copy", "test": "CreateSourceBucket", "status": "fail", "error": "service error"})
        root = self.junit(data, [])
        # Then it is skipped, not failed — 800 expected gaps must not drown the
        # signal from an actual regression
        self.assertEqual(len(root.findall(".//failure")), 0)
        skipped = root.findall(".//skipped")
        self.assertEqual(len(skipped), 1)
        self.assertIn("known failure", skipped[0].get("message"))

    def test_passing_test_flagged_by_compare_is_not_failed(self):
        # Given a key named by the compare step whose test currently passes
        data = results({"suite": "go-sdk", "group": "s3-crud", "test": "CreateBucket", "status": "pass"})
        root = self.junit(data, ["compat baseline missing result: go-sdk/s3-crud/CreateBucket expected pass"])
        # Then no failure is fabricated on a green test
        self.assertEqual(len(root.findall(".//failure")), 0)

    def test_expected_gap_statuses_are_skipped_with_reasons(self):
        data = results(
            {"suite": "cli", "group": "s3-crud", "test": "A", "status": "unimplemented", "error": "501"},
            {"suite": "cli", "group": "s3-crud", "test": "B", "status": "na", "error": "no CLI command"},
            {"suite": "cli", "group": "s3-crud", "test": "C", "status": "skip", "error": "requires: docker"},
        )
        root = self.junit(data, [])
        messages = [el.get("message") for el in root.findall(".//skipped")]
        self.assertEqual(len(messages), 3)
        self.assertTrue(any("not implemented by Overcast" in m for m in messages))
        self.assertTrue(any("n/a for this tool" in m for m in messages))

    def test_counts_are_consistent(self):
        data = results(
            {"suite": "go-sdk", "group": "s3-crud", "test": "A", "status": "pass"},
            {"suite": "go-sdk", "group": "s3-crud", "test": "B", "status": "fail", "error": "x"},
            {"suite": "go-sdk", "group": "s3-crud", "test": "C", "status": "skip"},
        )
        root = self.junit(data, ["compat baseline regression: go-sdk/s3-crud/B pass -> fail"])
        suite = root.find("testsuite")
        self.assertEqual(suite.get("tests"), "3")
        self.assertEqual(suite.get("failures"), "1")
        self.assertEqual(suite.get("skipped"), "1")
        self.assertEqual(suite.get("errors"), "0")


class SummaryTest(unittest.TestCase):
    def test_clean_run_says_no_regressions(self):
        data = results({"suite": "go-sdk", "group": "s3-crud", "test": "A", "status": "pass"})
        summary, comment, _ = report.build(data, {"entries": []}, None, [], [])
        self.assertIn("No regressions against the baseline", summary)
        self.assertIn("No regressions against the baseline", comment)

    def test_gate_failures_lead_the_summary(self):
        data = results({"suite": "go-sdk", "group": "s3-crud", "test": "A", "status": "fail", "error": "boom"})
        summary, comment, _ = report.build(
            data,
            {"entries": []},
            None,
            ["compat baseline new failure, not in baseline: go-sdk/s3-crud/A"],
            ["unrecorded parity debt: rust-sdk/iam-users (11 test(s))"],
        )
        # The thing CI enforces is the first thing a reader sees.
        self.assertLess(summary.index("Gate failures"), summary.index("| Suite |"))
        self.assertIn("baseline regression(s)", summary)
        self.assertIn("parity issue(s)", summary)
        self.assertIn("New failures (1)", summary)
        self.assertIn("❌", comment)

    def test_parity_debt_table_rendered(self):
        data = results({"suite": "rust-sdk", "group": "s3-crud", "test": "A", "status": "pass"})
        debt = {"version": 1, "debt": [{"suite": "rust-sdk", "service": "iam", "group": "iam-users", "tests": 11}]}
        summary, _, _ = report.build(data, {"entries": []}, debt, [], [])
        self.assertIn("parity debt", summary)
        self.assertIn("11", summary)


class SuiteBugCandidatesTest(unittest.TestCase):
    def make(self, *statuses: tuple[str, str]) -> list[dict]:
        return [
            {"suite": suite, "group": "sts-assume", "test": "AssumeRole", "status": status, "error": "boom" if status == "fail" else ""}
            for suite, status in statuses
        ]

    def test_flags_a_test_failing_in_exactly_one_of_three_or_more_suites(self):
        tests = self.make(("go-sdk", "pass"), ("python-sdk", "pass"), ("node-js-sdk", "fail"))
        candidates = report.suite_bug_candidates(tests)
        self.assertEqual(len(candidates), 1)
        self.assertEqual(candidates[0]["suite"], "node-js-sdk")
        self.assertEqual(candidates[0]["passing_suites"], ["go-sdk", "python-sdk"])

    def test_does_not_flag_below_the_minimum_implementing_suites(self):
        # Only two suites implement it — one disagreeing pair isn't enough
        # signal to call out as an outlier.
        tests = self.make(("go-sdk", "pass"), ("node-js-sdk", "fail"))
        self.assertEqual(report.suite_bug_candidates(tests), [])

    def test_does_not_flag_when_more_than_one_suite_fails(self):
        # Two suites failing the same test looks like a real emulator gap,
        # not one suite's assertion bug.
        tests = self.make(("go-sdk", "pass"), ("python-sdk", "fail"), ("node-js-sdk", "fail"))
        self.assertEqual(report.suite_bug_candidates(tests), [])

    def test_ignores_suites_that_have_not_implemented_the_test(self):
        tests = self.make(
            ("go-sdk", "pass"), ("python-sdk", "pass"), ("node-js-sdk", "fail"), ("rust-sdk", "skip")
        )
        candidates = report.suite_bug_candidates(tests)
        self.assertEqual(len(candidates), 1)
        self.assertNotIn("rust-sdk", candidates[0]["passing_suites"])

    def test_summary_renders_the_candidate_table(self):
        data = results(
            {"suite": "go-sdk", "group": "sts-assume", "test": "AssumeRole", "status": "pass"},
            {"suite": "python-sdk", "group": "sts-assume", "test": "AssumeRole", "status": "pass"},
            {"suite": "node-js-sdk", "group": "sts-assume", "test": "AssumeRole", "status": "fail", "error": "wrong role"},
        )
        summary, _, _ = report.build(data, {"entries": []}, None, [], [])
        self.assertIn("suite-bug candidate", summary)
        self.assertIn("sts-assume/AssumeRole", summary)
        self.assertIn("node-js-sdk", summary)


if __name__ == "__main__":
    unittest.main()
