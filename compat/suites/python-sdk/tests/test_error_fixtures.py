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
from typing import Any

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
    "cliBanner",
}

# What boto3 puts in front of this suite: the exception class botocore minted,
# the parsed error body and the response headers. `bodyType` and `bodyCode` are
# in here because botocore folds the body member into `Error.Code` rather than
# leaving it where it found it — the code the body states is readable, the
# member is not. A query-compatible response is the case where that stops being
# true: the header replaces the body's code, so the body no longer carries the
# shape at all. A fixture says which carrier each expectation is reachable
# through with `via`, so this set is the suite's general answer and not the last
# word on any one wire. The AWS CLI's stderr banner belongs to another suite.
OBSERVED_CARRIERS = {"exceptionName", "bodyType", "bodyCode", "queryErrorHeader"}

WHAT_THIS_SUITE_SEES = (
    "boto3 hands the interpreter an exception, a parsed error body and the "
    "response headers, never a process's stderr"
)

# The strict reader. The cli suite decodes a fixture with DisallowUnknownFields,
# so a key none of the three recognises has to be an error here too: a field
# added to a fixture and silently ignored by two of the backends is the drift
# these documents exist to prevent.
FIXTURE_KEYS = {"id", "title", "why", "carriers", "wire", "expect"}
WIRE_KEYS = {"status", "exceptionName", "headers", "body", "stderr"}
CASE_KEYS = {"name", "error", "matches", "via"}
ERROR_KEYS = {"shape", "code"}


def _strict(obj: dict, allowed: set, where: str) -> dict:
    unknown = sorted(set(obj) - allowed)
    if unknown:
        raise AssertionError(
            f"{where}: unknown key(s) {unknown}; the fixture format is fixed "
            "by compat/model/README.md § Errors"
        )
    return obj


def load_fixtures() -> list[dict]:
    """Every fixture, in file-name order so a failure is reported the same way
    on every run."""
    fixtures = []
    for path in sorted(glob.glob(os.path.join(FIXTURE_DIR, "*.json"))):
        with open(path, encoding="utf-8") as f:
            fixture = json.load(f)
        name = os.path.basename(path)
        _strict(fixture, FIXTURE_KEYS, name)
        _strict(fixture["wire"], WIRE_KEYS, f"{name}: wire")
        for case in fixture["expect"]:
            where = f"{name}: expect[{case.get('name')!r}]"
            _strict(case, CASE_KEYS, where)
            _strict(case["error"], ERROR_KEYS, f"{where}.error")
        fixtures.append(fixture)
    return fixtures


def as_client_error(wire: dict) -> ClientError:
    """The fixture as botocore would have handed it to this suite.

    This mirrors ``botocore.parsers``: ``BaseJSONParser._do_error_parse``
    resolves one ``Error.Code`` out of the body's ``__type`` (cut at the first
    ``:``, then after the last ``#``); ``_do_query_compatible_error_parse``
    replaces it with the ``x-amzn-query-error`` code when that header is there,
    keeping the displaced one as ``QueryErrorCode`` and the fault as ``Type``;
    and ``RestJSONParser._inject_error_code`` then prefers ``x-amzn-errortype``
    and falls back to the body's ``code``/``Code`` member. Applying all three in
    that order renders every fixture exactly as its own protocol's parser
    would, because no fixture carries two protocols' surfaces at once.

    Nothing here invents a key botocore does not set: there is no
    ``Error.__type`` and no top-level ``__type``, which is why ``error_names``
    no longer looks for either. Verified against botocore 1.43.67 by driving a
    real client with a stubbed transport for each of these wires."""
    body = dict(wire.get("body") or {})
    headers = dict(wire.get("headers") or {})
    status = wire.get("status", 400)

    error: dict[str, Any] = {
        "Message": body.get("message", body.get("Message", "")),
        "Code": "",
    }

    code = body.get("__type", str(status) if status is not None else None)
    if code is not None:
        if ":" in code:
            code = code.split(":", 1)[0]
        if "#" in code:
            code = code.rsplit("#", 1)[1]
        if "x-amzn-query-error" in headers:
            parts = headers["x-amzn-query-error"].split(";")
            if len(parts) == 2 and parts[0]:
                error["QueryErrorCode"] = code
                error["Type"] = parts[1]
                code = parts[0]
        error["Code"] = code

    injected = None
    if "x-amzn-errortype" in headers:
        injected = headers["x-amzn-errortype"]
    elif "code" in body or "Code" in body:
        injected = body.get("code", body.get("Code", ""))
    if isinstance(injected, str):
        error["Code"] = injected.split(":", 1)[0].rsplit("#", 1)[-1]
    elif injected is not None:
        error["Code"] = injected

    response = {
        "Error": error,
        "ResponseMetadata": {
            "HTTPStatusCode": status,
            "HTTPHeaders": headers,
            "RetryAttempts": 0,
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
                    # A fixture stating no carrier states no code anywhere:
                    # every suite runs it, because there is nothing to miss and
                    # every expectation on it is negative.
                    if carriers and not OBSERVED_CARRIERS.intersection(carriers):
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
