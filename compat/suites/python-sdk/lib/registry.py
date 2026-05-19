"""
lib/registry.py — Shared registry loader and group builder for the Python suite.

Mirrors node-js-sdk/src/lib/registry.ts. Loads compat/suites/registry.json
and builds TestGroup lists from it, auto-skipping unimplemented tests.
"""

from __future__ import annotations

import json
import os
import sys
from typing import Callable, Optional

from .harness import TestCase, TestGroup, TestFn

# Path: this file is at suites/python-sdk/lib/registry.py
# registry.json is at suites/registry.json (two levels up from lib/)
_REGISTRY_PATH = os.path.join(os.path.dirname(__file__), "..", "..", "registry.json")

ImplMap = dict[str, TestFn]

# ─── Loader ───────────────────────────────────────────────────────────────────

def load_registry() -> dict:
    with open(os.path.abspath(_REGISTRY_PATH), encoding="utf-8") as f:
        return json.load(f)


# ─── Builder ─────────────────────────────────────────────────────────────────

_noop: TestFn = lambda ctx: None


def build_groups_from_registry(
    registry: dict,
    impls: ImplMap,
    suite: str,
    capabilities: Optional[set[str]] = None,
    setup: Optional[dict[str, TestFn]] = None,
    teardown: Optional[dict[str, TestFn]] = None,
) -> list[TestGroup]:
    """
    Build a TestGroup list from the registry, filling missing impls with auto-skip.

    Args:
        registry:     Loaded from load_registry().
        impls:        Map of test name → callable(ctx).
        suite:        Suite name for NDJSON output.
        capabilities: Set of capability strings this runner supports
                      (e.g. {"docker"}). Defaults to empty set.
        setup:        Dict of group_name → setup callable.
        teardown:     Dict of group_name → teardown callable.
    """
    caps = set(capabilities or [])
    groups: list[TestGroup] = []

    for rg in registry["groups"]:
        # CDK lifecycle tests belong to the cdk suite, not SDK suites.
        if rg.get("service") == "cdk":
            continue
        tests: list[TestCase] = []

        for rt in rg["tests"]:
            name: str = rt["name"]
            registry_op = rt.get("op")  # str, None (absent), or null (JSON null)

            # op: JSON null → False (suppress doc link)
            #     string     → override
            #     absent     → None (use test name in harness)
            if "op" not in rt:
                op = None
            elif registry_op is None:
                op = False
            else:
                op = registry_op

            # Static registry skip
            if rt.get("skip"):
                tests.append(TestCase(name=name, fn=_noop, op=op, skip=rt["skip"]))
                continue

            # Capability gate
            required_caps: list[str] = rt.get("requires") or []
            missing = [c for c in required_caps if c not in caps]
            if missing:
                reason = f"requires {', '.join(missing)} (not available in this environment)"
                tests.append(TestCase(name=name, fn=_noop, op=op, skip=reason))
                continue

            # Impl lookup — look up by group-qualified key ("groupName:testName")
            # first, then fall back to bare test name.  This avoids collisions
            # when multiple groups share the same test name (e.g. lambda-crud
            # and appsync-functions both have CreateFunction).
            qualified_key = f"{rg['name']}:{name}"
            has_impl = qualified_key in impls or name in impls
            if not has_impl:
                tests.append(TestCase(
                    name=name, fn=_noop, op=op,
                    skip=f"not yet implemented in {suite} test suite",
                ))
                continue

            fn = impls.get(qualified_key) if qualified_key in impls else impls[name]
            if fn is None:
                # Explicitly registered as None → SDK does not expose this.
                tests.append(TestCase(
                    name=name, fn=_noop, op=op,
                    na="not yet supported by the AWS Python SDK (boto3)",
                ))
                continue

            tests.append(TestCase(name=name, fn=fn, op=op))

        group = TestGroup(
            suite=suite,
            service=rg["service"],
            name=rg["name"],
            tests=tests,
            setup=(setup or {}).get(rg["name"]),
            teardown=(teardown or {}).get(rg["name"]),
        )
        groups.append(group)

    return groups


# ─── Validation ───────────────────────────────────────────────────────────────

def validate_impls(registry: dict, impls: ImplMap, suite: str) -> None:
    """
    Warn about impl keys that don't match any registry test name.
    Treats it as a fatal error if OVERCAST_COMPAT_STRICT=1.
    """
    all_names = {
        key
        for rg in registry["groups"]
        for rt in rg["tests"]
        for key in (rt["name"], f"{rg['name']}:{rt['name']}")
    }
    orphans = [k for k in impls if k not in all_names]
    if not orphans:
        return
    for o in orphans:
        sys.stderr.write(
            f"[compat:{suite}] WARNING: impl '{o}' has no matching registry test\n"
        )
    if os.environ.get("OVERCAST_COMPAT_STRICT") == "1":
        raise SystemExit(f"[compat:{suite}] strict mode: orphaned impls found")
