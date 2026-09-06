#!/usr/bin/env python3
"""Validate compat/suites/registry.json against its JSON Schema.

The registry is the join key for every compat suite: group and test names are
matched verbatim across seven suite implementations, the dashboard, and
compat/baseline/. Nothing enforced the schema until now, so names could
drift from the documented PascalCase convention without CI noticing.

Beyond the schema, this checks referential invariants JSON Schema cannot
express:

  - names must be unique, and every `depends` entry must name a test in the
    same group;
  - `suites` scoping on a hand-written group is reserved for the small
    allowed set (today just `cdk-lifecycle`) -- see compat/AGENTS.md's
    suites-scoping amendment (docs/plans/compat-coverage-modelgen.md §3.6);
  - a *generated* group (loaded from the sibling
    compat/suites/registry.generated.json, which cmd/compatgen owns) must
    always declare `suites`, since it is mechanically derived from backend
    availability;
  - every group's `service` must be a known Overcast capability service key,
    with the deliberate, narrow exception of `cdk` on `cdk-lifecycle` (see
    docs/plans/compat-coverage-modelgen.md §7.7). A *generated* group may
    instead name a service the pruned shape snapshot covers and the emulator
    has no capability row for at all -- a Tier 0 service whose recipe lands
    before its implementation does, which is what G4 is (see
    service_key_errors).

The generated-registry file is checked in but empty until cmd/compatgen
populates it. Its absence is still tolerated -- a suite image, a CI artifact,
or a maintenance branch cut before it existed all read this path without it --
and its half of the checks above is then skipped cleanly, with a note.

Usage:
    python3 scripts/validate-compat-registry.py
    python3 scripts/validate-compat-registry.py --registry path --schema path

Exit codes:
    0  registry is valid
    1  registry is invalid
    2  bad usage, or jsonschema is not installed
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import NoReturn

REPO_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_REGISTRY = REPO_ROOT / "compat" / "suites" / "registry.json"
DEFAULT_SCHEMA = REPO_ROOT / "compat" / "suites" / "registry.schema.json"
DEFAULT_GENERATED_REGISTRY = REPO_ROOT / "compat" / "suites" / "registry.generated.json"
DEFAULT_CAPABILITIES = REPO_ROOT / "internal" / "capabilities" / "all.gen.go"
DEFAULT_SHAPES_SERVICES = REPO_ROOT / "models" / "aws" / "shapes-services.txt"

# Hand-written groups (entries in compat/suites/registry.json) allowed to
# declare "suites". Discovered by inspection, not assumed: as of this check's
# introduction, cdk-lifecycle is the *only* hand-written group that scopes
# itself, and it does so because cmd/compat/parity.go's `nonUniformSuites`
# exempts the "cdk" suite from registry-wide uniformity -- CDK deploys whole
# stacks rather than calling operations one at a time, so "implement every
# registry group" is meaningless for it. If a future hand-written group needs
# `suites` scoping, it must correspond to a suite `nonUniformSuites` also
# exempts, and belongs in this set with its own comment explaining why -- not
# added silently, and never used to route around registry-wide uniformity for
# an SDK/CLI suite.
HAND_WRITTEN_SUITES_SCOPE_ALLOWLIST = {"cdk-lifecycle"}

# service values that are deliberately not an AWS/Overcast capability key,
# mapped to the group name(s) allowed to use them. "cdk" names the IaC tool
# cdk-lifecycle drives (CDK deploys/destroys), not an AWS service -- it has no
# entry in internal/capabilities/all.gen.go and never will. Keep this narrow:
# a new non-AWS "service" value needs its own entry and its own reasoning,
# not a blanket carve-out.
NON_AWS_SERVICE_VALUES = {"cdk": {"cdk-lifecycle"}}


def die(message: str) -> NoReturn:
    """Exit 2 — a broken environment or bad input, distinct from an invalid registry."""
    print(message, file=sys.stderr)
    sys.exit(2)


def load_json(path: Path) -> object:
    try:
        with path.open(encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        die(f"ERROR: {path} not found")
    except json.JSONDecodeError as e:
        die(f"ERROR: {path} is not valid JSON: {e}")


def load_json_optional(path: Path) -> object | None:
    """Like load_json, but a missing file is not an error -- it returns None.

    Used for compat/suites/registry.generated.json, which is checked in but
    need not be present everywhere this script runs -- a suite image or a
    branch cut before it existed has no copy. A malformed *existing* file is
    still a hard error: only absence is tolerated.
    """
    try:
        with path.open(encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return None
    except json.JSONDecodeError as e:
        die(f"ERROR: {path} is not valid JSON: {e}")


def pointer(error) -> str:
    """Render a validation error path as a readable JSON pointer."""
    if not error.absolute_path:
        return "(document root)"
    return "/" + "/".join(str(part) for part in error.absolute_path)


def schema_errors(registry: object, schema: object) -> list[str]:
    try:
        from jsonschema import Draft7Validator
    except ImportError:
        die(
            "ERROR: the 'jsonschema' package is required.\n"
            "       Install it with: pip install jsonschema"
        )

    validator = Draft7Validator(schema)
    return [
        f"{pointer(e)}: {e.message}"
        for e in sorted(validator.iter_errors(registry), key=pointer)
    ]


def reference_errors(registry: object) -> list[str]:
    """Check invariants the schema cannot express: uniqueness and `depends`."""
    if not isinstance(registry, dict):
        return []

    errors: list[str] = []
    seen_groups: set[str] = set()

    for group in registry.get("groups", []):
        if not isinstance(group, dict):
            continue
        group_name = group.get("name", "(unnamed)")
        if group_name in seen_groups:
            errors.append(f"group {group_name!r}: duplicate group name")
        seen_groups.add(group_name)

        tests = [t for t in group.get("tests", []) if isinstance(t, dict)]
        names = [t.get("name") for t in tests]
        seen_tests: set[str] = set()
        for name in names:
            if name in seen_tests:
                errors.append(
                    f"group {group_name!r}: duplicate test name {name!r}"
                )
            seen_tests.add(name)

        for test in tests:
            test_name = test.get("name", "(unnamed)")
            for dep in test.get("depends", []):
                if dep == test_name:
                    errors.append(
                        f"group {group_name!r}, test {test_name!r}: "
                        "depends on itself"
                    )
                elif dep not in seen_tests:
                    errors.append(
                        f"group {group_name!r}, test {test_name!r}: "
                        f"depends on {dep!r}, which is not a test in this group"
                    )

    return errors


def suites_scope_errors(registry: object) -> list[str]:
    """Hand-written half of the suites-scoping rule.

    compat/AGENTS.md's suites-scoping section (amended alongside this lint,
    docs/plans/compat-coverage-modelgen.md §3.6): on a hand-written
    registry.json group, `suites` remains reserved for the groups in
    HAND_WRITTEN_SUITES_SCOPE_ALLOWLIST. An SDK/CLI suite is never a
    legitimate `suites` scope on a hand-written group -- that would let a
    group quietly opt out of registry-wide uniformity instead of being
    implemented (or `na`'d, or debt-declared) everywhere.
    """
    if not isinstance(registry, dict):
        return []

    errors: list[str] = []
    for group in registry.get("groups", []):
        if not isinstance(group, dict):
            continue
        name = group.get("name", "(unnamed)")
        if group.get("suites") and name not in HAND_WRITTEN_SUITES_SCOPE_ALLOWLIST:
            errors.append(
                f"group {name!r}: hand-written groups may not declare \"suites\" "
                f"(reserved for {sorted(HAND_WRITTEN_SUITES_SCOPE_ALLOWLIST)!r}); "
                "see compat/AGENTS.md's suites-scoping amendment"
            )
    return errors


def generated_group_errors(
    generated: object, capability_keys: set[str], snapshot_keys: set[str]
) -> list[str]:
    """Generated half of the suites-scoping rule, plus the service-key check.

    Every group in compat/suites/registry.generated.json is generated by
    construction, so it must declare `suites` -- mechanically derived from
    which suites have a backend for that recipe, never hand-edited (§3.6).

    `snapshot_keys` widens the service-key check for generated groups only;
    service_key_errors says why.
    """
    if not isinstance(generated, dict):
        return []

    errors: list[str] = []
    for group in generated.get("groups", []):
        if not isinstance(group, dict):
            continue
        name = group.get("name", "(unnamed)")
        if not group.get("suites"):
            errors.append(
                f"generated group {name!r}: must declare \"suites\" "
                "(generated groups are mechanically scoped to the backends "
                "that implement them)"
            )
    errors.extend(
        service_key_errors(
            generated,
            capability_keys,
            label="registry.generated.json",
            tier0_keys=snapshot_keys,
        )
    )
    return errors


def load_capability_service_keys(path: Path) -> set[str]:
    """Extract the set of Overcast capability service keys.

    Source of truth: internal/capabilities/all.gen.go, a checked-in artifact
    generated by cmd/capgen (`make generate-caps`) that already lists every
    service with a registered capability -- exactly "the capability key" that
    generated registry groups use by construction (§7.7). This is a *repo*
    script, not a compat/ suite, so reading a generated Go artifact does not
    cross the "compat/ never imports emulator Go code" boundary
    (compat/AGENTS.md); it never imports the package, only reads its text.

    Parsed with a regex rather than a Go AST, deliberately: the file is a
    single flat `[]Capability{ {Service: "...", ...}, ... }` literal with
    `Service` always keyed and first, so the field only breaks if `cmd/capgen`
    stops emitting a `Service` field at all -- at which point every consumer
    of AllCapabilities breaks, not just this lint. A Go-side check (e.g. in
    cmd/capgen) was considered and rejected: it would need its own new lint
    entry point and CI wiring for no accuracy gain over the regex here.
    """
    try:
        text = path.read_text(encoding="utf-8")
    except FileNotFoundError:
        die(f"ERROR: {path} not found (run `make generate-caps`?)")

    keys = set(re.findall(r'Service:\s*"([a-z0-9][a-z0-9-]*)"', text))
    if not keys:
        die(
            f"ERROR: parsed zero capability services from {path} -- "
            "its format may have changed; update the regex in "
            "load_capability_service_keys()"
        )
    return keys


def load_shape_snapshot_service_keys(path: Path) -> set[str]:
    """The services the pruned AWS shape snapshot covers.

    models/aws/shapes-services.txt is reviewed data with one canonical service
    key per line and `#` comments -- the same key the routing manifest uses,
    which is the key a recipe for a service Overcast has not implemented yet
    carries. It is the narrow widening the Tier 0 branch of
    service_key_errors needs, and nothing else reads it here.

    A missing file is not fatal: the widening simply does not apply, and every
    generated group is then held to the capability-key rule alone.
    """
    try:
        text = path.read_text(encoding="utf-8")
    except FileNotFoundError:
        return set()
    keys: set[str] = set()
    for line in text.splitlines():
        line = line.split("#", 1)[0].strip()
        if line:
            keys.add(line)
    return keys


def service_key_errors(
    registry: object,
    capability_keys: set[str],
    label: str = "registry.json",
    tier0_keys: set[str] | None = None,
) -> list[str]:
    """Every group's `service` must be a known Overcast capability key.

    docs/plans/compat-coverage-modelgen.md §7.7: nothing previously asserted
    this, and the `cognito` case only worked because
    internal/awsapi/registry_data.go aliases `cognito-identity-provider` to
    the capability key `cognito` -- a hand-written group using the *model*
    service name instead of the capability key would have gone unnoticed.
    `cdk` is a deliberate, narrow exception (NON_AWS_SERVICE_VALUES): it is
    not an AWS service and never will be a capability key.

    `tier0_keys` is the second exception, and it is passed only for the
    generated registry. §7.7 assumed a generated group "will use the
    capability key by construction", which holds only while every generated
    service has a capability row. G4 breaks that assumption on purpose: a
    recipe for a Tier 0 service lands *before* the emulator implements it, so
    the service has no row at all and every one of its tests records
    `unimplemented` or `skip` until the inert tier flips it. Such a service
    still has one canonical key -- the one models/aws/shapes-services.txt
    lists and cmd/compatgen has already resolved against the routing manifest
    (clientInfoFor refuses to generate otherwise).

    So the widening is deliberately conditioned on the emulator having *no*
    row for the service: the moment it implements one operation, the group
    goes back to being held to the capability key. What that does not catch is
    a recipe naming the model service of a service Overcast aliases (a
    hypothetical `cognito-identity-provider` recipe, were its shapes ever
    snapshotted) -- for that case the alias lives in the recipe's own `model`
    field and the check is cmd/compatgen's, not this one's.
    """
    if not isinstance(registry, dict):
        return []

    errors: list[str] = []
    for group in registry.get("groups", []):
        if not isinstance(group, dict):
            continue
        name = group.get("name", "(unnamed)")
        service = group.get("service")
        if service in capability_keys:
            continue
        if service in NON_AWS_SERVICE_VALUES and name in NON_AWS_SERVICE_VALUES[service]:
            continue
        if tier0_keys and service in tier0_keys:
            continue
        errors.append(
            f"{label} group {name!r}: service {service!r} is not a known "
            "Overcast capability service key (internal/capabilities/all.gen.go); "
            "use the capability key, not the Smithy/model service name "
            "(see internal/awsapi/registry_data.go's alias table if they differ)"
        )
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--registry", type=Path, default=DEFAULT_REGISTRY)
    parser.add_argument("--schema", type=Path, default=DEFAULT_SCHEMA)
    parser.add_argument(
        "--generated-registry",
        type=Path,
        default=DEFAULT_GENERATED_REGISTRY,
        help="compat/suites/registry.generated.json. Optional: where the file "
        "is absent its checks are skipped rather than failing.",
    )
    parser.add_argument("--capabilities-file", type=Path, default=DEFAULT_CAPABILITIES)
    parser.add_argument(
        "--shapes-services-file",
        type=Path,
        default=DEFAULT_SHAPES_SERVICES,
        help="models/aws/shapes-services.txt, which names the Tier 0 services a "
        "generated group may carry. Optional: where the file is absent every "
        "generated group is held to the capability-key rule alone.",
    )
    args = parser.parse_args(argv)

    registry = load_json(args.registry)
    schema = load_json(args.schema)
    capability_keys = load_capability_service_keys(args.capabilities_file)
    snapshot_keys = load_shape_snapshot_service_keys(args.shapes_services_file)

    errors = (
        schema_errors(registry, schema)
        + reference_errors(registry)
        + suites_scope_errors(registry)
        + service_key_errors(registry, capability_keys)
    )

    generated = load_json_optional(args.generated_registry)
    if generated is None:
        print(
            f"(no generated registry at {args.generated_registry} -- "
            "generated-group checks skipped)"
        )
    else:
        errors += generated_group_errors(generated, capability_keys, snapshot_keys)

    if errors:
        rel = args.registry.relative_to(REPO_ROOT) if args.registry.is_relative_to(REPO_ROOT) else args.registry
        print(f"ERROR: {rel} is invalid ({len(errors)} problem(s)):", file=sys.stderr)
        for err in errors:
            print(f"  {err}", file=sys.stderr)
        print(
            "\nTest names must be PascalCase (^[A-Z][A-Za-z0-9]+$) and are the "
            "join key\nacross every suite implementation, so renaming one means "
            "updating them all.",
            file=sys.stderr,
        )
        return 1

    group_count = len(registry.get("groups", [])) if isinstance(registry, dict) else 0
    test_count = sum(
        len(g.get("tests", []))
        for g in (registry.get("groups", []) if isinstance(registry, dict) else [])
        if isinstance(g, dict)
    )
    print(f"registry OK — {group_count} groups, {test_count} tests")
    return 0


if __name__ == "__main__":
    sys.exit(main())
