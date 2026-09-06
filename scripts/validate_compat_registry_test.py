#!/usr/bin/env python3
"""Tests for scripts/validate-compat-registry.py.

Two lints added for issue #1113 Phase G0 (docs/plans/compat-coverage-modelgen.md
§3.6, open questions §7.2 and §7.7):

1. Suites-scoping: a hand-written `compat/suites/registry.json` group may not
   declare `"suites"` outside the small allowed set (today just
   `cdk-lifecycle`); a *generated* group (one loaded from the sibling
   `compat/suites/registry.generated.json`, which cmd/compatgen owns) must
   always declare `"suites"`.
2. Service-key validation: every group's `service` must be a known Overcast
   capability service key (from `internal/capabilities/all.gen.go`), except
   the deliberate non-AWS `"cdk"` value used by `cdk-lifecycle`, and except a
   *generated* group naming a Tier 0 service the pruned shape snapshot covers
   (`models/aws/shapes-services.txt`) -- a G4 recipe that lands before the
   emulator implements the service, so no capability row exists for it yet.

The generated-registry half of lint 1 (and the generated-file leg of lint 2)
must tolerate `compat/suites/registry.generated.json` being absent: the file is
checked in, but a suite image or a branch cut before it existed has no copy.

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

# The schema check needs jsonschema; the two lints under test do not. See
# MainIntegrationTest for why that distinction is load-bearing here.
#
# find_spec returns None for a module that is simply absent, which is the CI
# case, but it propagates ImportError from a finder that refuses the name --
# so both mean "not usable here" and neither should be an error in a test file.
try:
    HAS_JSONSCHEMA = importlib.util.find_spec("jsonschema") is not None
except ImportError:  # pragma: no cover - depends on the environment
    HAS_JSONSCHEMA = False


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


class GeneratedRegistryTest(unittest.TestCase):
    """Both halves of the generated registry's presence.

    Absence still has to be tolerated -- a suite image, a CI artifact, or a
    maintenance branch cut before the file existed all read this lint's path
    without it. Presence is now the ordinary case: the sibling PR landed
    compat/suites/registry.generated.json, empty, and it is what
    cmd/compatgen will rewrite wholly.
    """

    def test_missing_generated_registry_is_not_an_error(self):
        with tempfile.TemporaryDirectory() as d:
            missing = Path(d) / "registry.generated.json"
            self.assertFalse(missing.exists())
            self.assertEqual(vcr.load_json_optional(missing), None)

    def test_real_generated_registry_loads_and_passes_its_checks(self):
        # This replaces an assertion that the file did not exist yet, which
        # was true only until the sibling PR merged and then failed on main.
        # A test may not pin a fact that another in-flight branch is about to
        # change; what is durable is that the checked-in file loads and is
        # clean, which is the invariant regeneration must preserve.
        loaded = vcr.load_json_optional(vcr.DEFAULT_GENERATED_REGISTRY)
        self.assertIsNotNone(
            loaded, f"{vcr.DEFAULT_GENERATED_REGISTRY} should be checked in"
        )
        keys = vcr.load_capability_service_keys(vcr.DEFAULT_CAPABILITIES)
        snapshot = vcr.load_shape_snapshot_service_keys(vcr.DEFAULT_SHAPES_SERVICES)
        self.assertEqual(vcr.generated_group_errors(loaded, keys, snapshot), [])


class GeneratedGroupMustDeclareSuitesTest(unittest.TestCase):
    def test_generated_group_without_suites_is_rejected(self):
        generated = {"groups": [group("dynamodb-generated-0001", generated=True)]}
        errors = vcr.generated_group_errors(
            generated, capability_keys={"dynamodb"}, snapshot_keys=set()
        )
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
        errors = vcr.generated_group_errors(
            generated, capability_keys={"dynamodb"}, snapshot_keys=set()
        )
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


class Tier0GeneratedServiceKeyTest(unittest.TestCase):
    """The G4 widening: a generated group may name a Tier 0 service.

    A recipe for a service Overcast has not implemented lands before the
    emulator does -- that is what G4 is -- so the service has no capability
    row and the §7.7 assumption ("generated groups use the capability key by
    construction") has nothing to check against. The widening is conditioned
    on the snapshot's own reviewed service list, and it does not reach
    hand-written groups.
    """

    def test_tier0_service_passes_for_a_generated_group(self):
        generated = {
            "groups": [
                group(
                    "batch-gen-jobqueue",
                    service="batch",
                    suites=["go-sdk", "python-sdk", "cli"],
                    generated=True,
                )
            ]
        }
        errors = vcr.generated_group_errors(
            generated, capability_keys={"s3", "sqs"}, snapshot_keys={"batch"}
        )
        self.assertEqual(errors, [])

    def test_service_outside_the_snapshot_is_still_rejected(self):
        generated = {
            "groups": [
                group(
                    "bogus-gen-thing",
                    service="not-a-real-service",
                    suites=["cli"],
                    generated=True,
                )
            ]
        }
        errors = vcr.generated_group_errors(
            generated, capability_keys={"s3", "sqs"}, snapshot_keys={"batch"}
        )
        self.assertEqual(len(errors), 1)
        self.assertIn("not-a-real-service", errors[0])

    def test_hand_written_group_gets_no_tier0_widening(self):
        # service_key_errors is called without tier0_keys for registry.json,
        # so a hand-written group naming a Tier 0 service is still an error:
        # there is nothing for a hand-written group to test against a service
        # the emulator does not implement.
        registry = {"groups": [group("batch-crud", service="batch")]}
        errors = vcr.service_key_errors(registry, capability_keys={"s3", "sqs"})
        self.assertEqual(len(errors), 1)


class ShapeSnapshotServiceKeyParsingTest(unittest.TestCase):
    def test_parses_keys_and_ignores_comments_and_blanks(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "shapes-services.txt"
            path.write_text(
                "\n".join(
                    [
                        "# a comment",
                        "",
                        "batch                  # REST-JSON",
                        "elastic-load-balancing # AWS Query",
                        "sqs",
                        "",
                    ]
                ),
                encoding="utf-8",
            )
            self.assertEqual(
                vcr.load_shape_snapshot_service_keys(path),
                {"batch", "elastic-load-balancing", "sqs"},
            )

    def test_missing_file_is_an_empty_set_rather_than_a_failure(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(
                vcr.load_shape_snapshot_service_keys(Path(d) / "nope.txt"), set()
            )

    def test_real_file_parses_to_a_nonempty_set(self):
        keys = vcr.load_shape_snapshot_service_keys(vcr.DEFAULT_SHAPES_SERVICES)
        self.assertIn("sqs", keys)
        self.assertIn("organizations", keys)


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


@unittest.skipUnless(HAS_JSONSCHEMA, "jsonschema is not installed")
class MainIntegrationTest(unittest.TestCase):
    """main() wires both new lints in alongside the existing schema check.

    Skipped without jsonschema. These are the only tests here that reach the
    schema check, and the CI job that runs scripts/*_test.py installs no pip
    dependencies on purpose -- the script tests are meant to stay cheap. The
    lints themselves are pure Python and their unit tests above run
    unconditionally; end-to-end coverage of main() against the real registry
    is what the `Script tests` job already does, with jsonschema installed.
    """

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
