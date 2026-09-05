"""
The shared error-matching conformance fixtures, compat/model/testdata/errors.

Three interpreters read the same documents and must agree about which clauses
they satisfy. Each suite writes this test once, against its own matcher, so a
rule only one backend implements fails somewhere rather than being discovered
when a generated group disagrees with itself across suites
(compat/model/README.md § Errors).

A fixture whose surfaces this suite cannot see is skipped by name and with a
reason: a silently ignored fixture would look exactly like a passing one.

Run with:  python -m unittest discover -s tests  (from compat/suites/python-sdk/)
"""

from __future__ import annotations

import glob
import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from botocore.exceptions import ClientError  # noqa: E402

# REPO_ROOT is derived from registry.json's own location, which is the rule the
# scenario loader already resolves a group's `scenario` path by — never the
# working directory, which differs between `cmd/compat` and a hand run.
from lib.registry import REPO_ROOT  # noqa: E402
from lib.scenario.executor import error_matches, error_names  # noqa: E402

FIXTURE_DIR = os.path.join(REPO_ROOT, "compat", "model", "testdata", "errors")

# The whole carrier vocabulary. A fixture naming anything else is a typo that
# would otherwise skip quietly in all three suites at once.
KNOWN_CARRIERS = {
    "exceptionName",
    "bodyType",
    "bodyCode",
    "queryErrorHeader",
    "errorTypeHeader",
    "cliBanner",
}

# What boto3 puts in front of this suite: the exception class botocore minted,
# the parsed error body and the response headers. `x-amzn-errortype` is on the
# wire, but botocore has already folded it into the error body by the time the
# interpreter sees anything, so it is not read as a surface of its own; the AWS
# CLI's stderr banner belongs to another suite.
OBSERVED_CARRIERS = {"exceptionName", "bodyType", "bodyCode", "queryErrorHeader"}

WHAT_THIS_SUITE_SEES = (
    "boto3 hands the interpreter an exception, a parsed error body and the "
    "response headers, never a process's stderr"
)


def load_fixtures() -> list[dict]:
    """Every fixture, in file-name order so a failure is reported the same way
    on every run."""
    fixtures = []
    for path in sorted(glob.glob(os.path.join(FIXTURE_DIR, "*.json"))):
        with open(path, encoding="utf-8") as f:
            fixtures.append(json.load(f))
    return fixtures


def as_client_error(wire: dict) -> ClientError:
    """The fixture as this suite would have observed it.

    botocore parses the error body into ``response["Error"]`` and mints an
    exception class named after the modeled shape when it recognises one, so a
    fixture's ``exceptionName`` becomes a ``ClientError`` subclass of that name
    and everything else is left where botocore puts it."""
    body = dict(wire.get("body") or {})
    error: dict = {"Message": body.get("message", "")}
    code = body.get("code") or body.get("Code")
    if code:
        error["Code"] = code
    if body.get("__type"):
        error["__type"] = body["__type"]
    response = {
        "Error": error,
        "ResponseMetadata": {
            "HTTPStatusCode": wire.get("status", 400),
            "HTTPHeaders": dict(wire.get("headers") or {}),
        },
    }
    cls = ClientError
    name = wire.get("exceptionName")
    if name:
        cls = type(name, (ClientError,), {})
    return cls(response, "Op")


class TestSharedErrorFixtures(unittest.TestCase):
    def test_the_matcher_agrees_with_every_fixture(self):
        fixtures = load_fixtures()
        self.assertTrue(
            fixtures,
            f"no fixtures in {FIXTURE_DIR}: the shared conformance set may not "
            "be skipped by deleting it",
        )
        checked = 0
        for fixture in fixtures:
            carriers = fixture["carriers"]
            unknown = set(carriers) - KNOWN_CARRIERS
            self.assertFalse(
                unknown,
                f"{fixture['id']}: unknown carrier(s) {sorted(unknown)}; the "
                "vocabulary is fixed by compat/model/README.md § Errors",
            )
            observed = as_client_error(fixture["wire"])
            for case in fixture["expect"]:
                with self.subTest(fixture=fixture["id"], case=case["name"]):
                    if not OBSERVED_CARRIERS.intersection(carriers):
                        raise unittest.SkipTest(
                            "this suite reads none of the fixture's surfaces "
                            f"({', '.join(carriers)}): {WHAT_THIS_SUITE_SEES}"
                        )
                    if case["matches"] and case["via"] not in OBSERVED_CARRIERS:
                        raise unittest.SkipTest(
                            f"this expectation matches through {case['via']!r}, "
                            f"which this suite does not observe: {WHAT_THIS_SUITE_SEES}"
                        )
                    checked += 1
                    self.assertEqual(
                        error_matches(observed, case["error"]),
                        case["matches"],
                        f"{fixture['id']}: {case['name']}: the error reports "
                        f"{error_names(observed)}, the clause names {case['error']}",
                    )
        self.assertGreater(
            checked,
            0,
            "every fixture was skipped: this suite is asserting nothing about "
            "error matching",
        )


if __name__ == "__main__":
    unittest.main()
