#!/usr/bin/env python3
"""Tests for scripts/validate-compat-registry.py.

Two lints added for issue #1113 Phase G0 (docs/plans/compat-coverage-modelgen.md
§3.6, open questions §7.2 and §7.7):

1. Suites-scoping: a hand-written `compat/suites/registry.json` group may not
   declare `"suites"` outside the small allowed set (today just
   `cdk-lifecycle`); a *generated* group (one loaded from the sibling
   `compat/suites/registry.generated.json`, which a separate in-flight PR
   introduces) must always declare `"suites"`.
2. Service-key validation: every group's `service` must be a known Overcast
   capability service key (from `internal/capabilities/all.gen.go`), except
   the deliberate non-AWS `"cdk"` value used by `cdk-lifecycle`.

The generated-registry half of lint 1 (and the generated-file leg of lint 2)
must tolerate `compat/suites/registry.generated.json` being absent -- a
sibling PR introduces that file. Until it lands, those checks are inert.

Run: python3 scripts/validate_compat_registry_test.py
"""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("validate-compat-registry.py")
SPEC = importlib.util.spec_from_file_location("validate_compat_registry", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
vcr = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = vcr
SPEC.loader.exec_module(vcr)


def group(name, service="s3", suites=None, generated=None):
    g = {"service": service, "name": name, "tests": [{"name": "Get"}]}
    if suites is not None:
        g["suites"] = suites
    if generated is not None:
        g["generated"] = generated
    return g


class SuitesScopeHandWrittenTest(unittest.TestCase):
    """Hand-written registry.json groups: `suites` is reserved."""

    def test_unscoped_group_is_fine(self):
        registry = {"groups": [group("s3-crud")]}
        self.assertEqual(vcr.suites_scope_errors(registry), [])

    def test_cdk_lifecycle_is_the_allowed_exception(self):
        registry = {"groups": [group("cdk-lifecycle", service="cdk", suites=["cdk"])]}
        self.assertEqual(vcr.suites_scope_errors(registry), [])

    def test_sdk_suite_scoping_a_hand_written_group_is_rejected(self):
        # This is exactly the case compat/AGENTS.md's absolute rule existed to
        # forbid: an SDK suite is never a legitimate `suites` scope on a
        # hand-written group.
        registry = {"groups": [group("s3-crud", suites=["node-js-sdk"])]}
        errors = vcr.suites_scope_errors(registry)
        self.assertEqual(len(errors), 1)
        self.assertIn("s3-crud", errors[0])

    def test_real_registry_has_no_suites_scope_violations(self):
        registry = vcr.load_json(vcr.DEFAULT_REGISTRY)
        self.assertEqual(vcr.suites_scope_errors(registry), [])


class GeneratedRegistryAbsentTest(unittest.TestCase):
    """The generated registry does not exist yet -- a sibling PR adds it."""

    def test_missing_generated_registry_is_not_an_error(self):
        with tempfile.TemporaryDirectory() as d:
            missing = Path(d) / "registry.generated.json"
            self.assertFalse(missing.exists())
            self.assertEqual(vcr.load_json_optional(missing), None)

    def test_real_repo_has_no_generated_registry_yet(self):
        # Documents the current state this lint must tolerate. When the
        # sibling PR lands, this test starts exercising the loaded-file path
        # instead and should be revisited, not deleted.
        self.assertFalse(vcr.DEFAULT_GENERATED_REGISTRY.exists())


class GeneratedGroupMustDeclareSuitesTest(unittest.TestCase):
    def test_generated_group_without_suites_is_rejected(self):
        generated = {"groups": [group("dynamodb-generated-0001", generated=True)]}
        errors = vcr.generated_group_errors(generated, capability_keys={"dynamodb"})
        self.assertTrue(any("suites" in e for e in errors))

    def test_generated_group_with_suites_passes(self):
        generated = {
            "groups": [
                group(
                    "dynamodb-generated-0001",
                    service="dynamodb",
                    suites=["go-sdk", "python-sdk", "cli"],
                    generated=True,
                )
            ]
        }
        errors = vcr.generated_group_errors(generated, capability_keys={"dynamodb"})
        self.assertEqual(errors, [])


class ServiceKeyValidationTest(unittest.TestCase):
    def test_known_capability_key_passes(self):
        registry = {"groups": [group("s3-crud", service="s3")]}
        errors = vcr.service_key_errors(registry, capability_keys={"s3", "sqs"})
        self.assertEqual(errors, [])

    def test_unknown_service_key_is_rejected(self):
        registry = {"groups": [group("bogus-crud", service="not-a-real-service")]}
        errors = vcr.service_key_errors(registry, capability_keys={"s3", "sqs"})
        self.assertEqual(len(errors), 1)
        self.assertIn("not-a-real-service", errors[0])

    def test_cdk_service_on_cdk_lifecycle_is_the_deliberate_exception(self):
        registry = {"groups": [group("cdk-lifecycle", service="cdk", suites=["cdk"])]}
        errors = vcr.service_key_errors(registry, capability_keys={"s3", "sqs"})
        self.assertEqual(errors, [])

    def test_cdk_service_on_any_other_group_is_still_rejected(self):
        # "cdk" is a deliberate, narrow exception for cdk-lifecycle -- not a
        # general escape hatch for "not an AWS service".
        registry = {"groups": [group("some-other-group", service="cdk")]}
        errors = vcr.service_key_errors(registry, capability_keys={"s3", "sqs"})
        self.assertEqual(len(errors), 1)

    def test_real_registry_has_no_service_key_violations(self):
        registry = vcr.load_json(vcr.DEFAULT_REGISTRY)
        capability_keys = vcr.load_capability_service_keys(vcr.DEFAULT_CAPABILITIES)
        self.assertEqual(vcr.service_key_errors(registry, capability_keys), [])


class CapabilityServiceKeyParsingTest(unittest.TestCase):
    def test_parses_service_keys_from_generated_capabilities_snapshot(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "all.gen.go"
            path.write_text(
                'package capabilities\n\n'
                'var AllCapabilities = []Capability{\n'
                '\t{Service: "s3", Operation: "CreateBucket", Category: "Buckets"},\n'
                '\t{Service: "s3", Operation: "DeleteBucket", Category: "Buckets"},\n'
                '\t{Service: "sqs", Operation: "CreateQueue", Category: "Queues"},\n'
                '}\n',
                encoding="utf-8",
            )
            keys = vcr.load_capability_service_keys(path)
            self.assertEqual(keys, {"s3", "sqs"})

    def test_real_capabilities_file_parses_to_a_nonempty_set(self):
        keys = vcr.load_capability_service_keys(vcr.DEFAULT_CAPABILITIES)
        self.assertIn("s3", keys)
        self.assertIn("dynamodb", keys)
        self.assertGreater(len(keys), 20)


class MainIntegrationTest(unittest.TestCase):
    """main() wires both new lints in alongside the existing schema check."""

    def test_main_still_passes_on_the_real_registry(self):
        self.assertEqual(vcr.main([]), 0)

    def test_main_fails_on_a_bad_suites_scope(self):
        with tempfile.TemporaryDirectory() as d:
            registry_path = Path(d) / "registry.json"
            registry_path.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "groups": [
                            {
                                "service": "s3",
                                "name": "s3-crud",
                                "suites": ["node-js-sdk"],
                                "tests": [{"name": "Get"}],
                            }
                        ],
                    }
                ),
                encoding="utf-8",
            )
            rc = vcr.main(
                [
                    "--registry",
                    str(registry_path),
                    "--schema",
                    str(vcr.DEFAULT_SCHEMA),
                ]
            )
            self.assertEqual(rc, 1)


if __name__ == "__main__":
    unittest.main()
