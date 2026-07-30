#!/usr/bin/env python3
"""Tests for scripts/compat-flake-detect.py.

Run: python3 scripts/compat_flake_detect_test.py
"""

from __future__ import annotations

import importlib.util
import json
import os
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("compat-flake-detect.py")


def load_module(flaky_path: Path | None = None):
    """Import the script fresh, with FLAKY_FILE pointed where we want it."""
    os.environ["FLAKY_FILE"] = str(flaky_path) if flaky_path else "does-not-exist.json"
    os.environ.pop("GITHUB_STEP_SUMMARY", None)
    spec = importlib.util.spec_from_file_location("compat_flake_detect", SCRIPT)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def write_run(dirpath: Path, name: str, tests: dict[str, str]) -> Path:
    """tests maps 'suite/group/test' -> status."""
    suites: dict[str, dict] = {}
    for key, status in tests.items():
        suite, group, test = key.split("/")
        s = suites.setdefault(suite, {"Suite": suite, "Groups": {}})
        g = s["Groups"].setdefault(group, {"Name": group, "Service": "svc", "Tests": []})
        g["Tests"].append({"group": group, "test": test, "status": status, "error": ""})
    doc = {"Suites": [{**s, "Groups": list(s["Groups"].values())} for s in suites.values()]}
    path = dirpath / name
    path.write_text(json.dumps(doc), encoding="utf-8")
    return path


class FlakeDetectionTest(unittest.TestCase):
    def test_consistent_runs_pass(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            a = write_run(d, "a.json", {"go-sdk/s3-crud/CreateBucket": "pass"})
            b = write_run(d, "b.json", {"go-sdk/s3-crud/CreateBucket": "pass"})
            mod = load_module()
            self.assertEqual(mod.main(["prog", str(a), str(b)]), 0)

    def test_varying_status_is_reported(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            a = write_run(d, "a.json", {"cli/eventbridge-buses/DeleteEventBus": "skip"})
            b = write_run(d, "b.json", {"cli/eventbridge-buses/DeleteEventBus": "fail"})
            mod = load_module()
            # A test that answers differently on an unchanged tree is the whole
            # point of the check.
            self.assertEqual(mod.main(["prog", str(a), str(b)]), 1)

    def test_quarantined_variance_is_not_news(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            flaky = d / "flaky.json"
            flaky.write_text(json.dumps({"version": 1, "flaky": [
                {"suite": "cli", "group": "eventbridge-buses", "test": "DeleteEventBus", "reason": "known"}
            ]}), encoding="utf-8")
            a = write_run(d, "a.json", {"cli/eventbridge-buses/DeleteEventBus": "skip"})
            b = write_run(d, "b.json", {"cli/eventbridge-buses/DeleteEventBus": "fail"})
            mod = load_module(flaky)
            # Already known and argued for — it must not fail the nightly run,
            # or the signal drowns in what we already decided to tolerate.
            self.assertEqual(mod.main(["prog", str(a), str(b)]), 0)

    def test_quarantine_does_not_hide_a_different_test(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            flaky = d / "flaky.json"
            flaky.write_text(json.dumps({"version": 1, "flaky": [
                {"suite": "cli", "group": "eventbridge-buses", "test": "DeleteEventBus", "reason": "known"}
            ]}), encoding="utf-8")
            a = write_run(d, "a.json", {
                "cli/eventbridge-buses/DeleteEventBus": "skip",
                "go-sdk/s3-crud/CreateBucket": "pass",
            })
            b = write_run(d, "b.json", {
                "cli/eventbridge-buses/DeleteEventBus": "fail",
                "go-sdk/s3-crud/CreateBucket": "fail",
            })
            mod = load_module(flaky)
            self.assertEqual(mod.main(["prog", str(a), str(b)]), 1)

    def test_test_missing_from_one_run_is_reported(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            a = write_run(d, "a.json", {"go-sdk/s3-crud/CreateBucket": "pass",
                                        "go-sdk/s3-crud/DeleteBucket": "pass"})
            b = write_run(d, "b.json", {"go-sdk/s3-crud/CreateBucket": "pass"})
            mod = load_module()
            # A test that sometimes is not reported at all is unstable too —
            # a crashed group looks like this.
            self.assertEqual(mod.main(["prog", str(a), str(b)]), 1)

    def test_findings_group_related_tests_into_one_cluster(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            cwd = os.getcwd()
            os.chdir(d)
            try:
                a = write_run(d, "a.json", {
                    "cli/eventbridge-buses/ListEventBuses": "fail",
                    "cli/eventbridge-buses/DeleteEventBus": "skip",
                    "go-sdk/s3-crud/CreateBucket": "pass",
                })
                b = write_run(d, "b.json", {
                    "cli/eventbridge-buses/ListEventBuses": "pass",
                    "cli/eventbridge-buses/DeleteEventBus": "fail",
                    "go-sdk/s3-crud/CreateBucket": "pass",
                })
                mod = load_module()
                self.assertEqual(mod.main(["prog", str(a), str(b)]), 1)

                findings = json.loads((d / "flake-findings.json").read_text(encoding="utf-8"))
                # Two tests in one group are one investigation, not two: the
                # workflow raises a single issue per cluster.
                self.assertEqual(len(findings["clusters"]), 1)
                cluster = findings["clusters"][0]
                self.assertEqual((cluster["suite"], cluster["group"]), ("cli", "eventbridge-buses"))
                self.assertEqual([t["test"] for t in cluster["tests"]],
                                 ["DeleteEventBus", "ListEventBuses"])
                self.assertEqual(cluster["tests"][0]["statuses"], ["fail", "skip"])
            finally:
                os.chdir(cwd)

    def test_requires_at_least_two_runs(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            a = write_run(d, "a.json", {"go-sdk/s3-crud/CreateBucket": "pass"})
            mod = load_module()
            self.assertEqual(mod.main(["prog", str(a)]), 2)

    def test_missing_file_is_an_input_error(self):
        mod = load_module()
        self.assertEqual(mod.main(["prog", "nope-a.json", "nope-b.json"]), 2)


if __name__ == "__main__":
    unittest.main()
