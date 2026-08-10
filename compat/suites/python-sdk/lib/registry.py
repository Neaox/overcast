"""
lib/registry.py — Shared registry loader and group builder for the Python suite.

Mirrors node-js-sdk/src/lib/registry.ts. Loads compat/suites/registry.json
and builds TestGroup lists from it, auto-skipping unimplemented tests.
"""

from __future__ import annotations

import json
import os
from typing import Callable, Iterable, Optional

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


def _topo_sort(tests: list[dict]) -> list[dict]:
    """
    Topologically sort tests within a group using their ``depends`` edges.

    Tests with no dependencies come first; tests whose deps are all resolved come
    next.  Falls back to the registry declaration order for tests at the same
    dependency depth, so the sort reorders only what the edges require.

    Registry file order is not authoritative on its own: ``cloudformation-stacks``
    listed DeleteStack before the UpdateStack it depends on, and this suite ran it
    that way — deleting the shared stack and then updating it — while the cli and
    node-js suites, which already sorted, did not. Same registry, a different
    order per language. Kept identical to the cli, node-js, Go, Java, .NET and
    Rust sorts.
    """
    by_name = {t["name"]: t for t in tests}
    sorted_tests: list[dict] = []
    visited: set[str] = set()
    visiting: set[str] = set()  # cycle detection

    def visit(t: dict) -> None:
        name = t["name"]
        if name in visited or name in visiting:
            return  # already placed, or a cycle — break it
        visiting.add(name)
        for dep in t.get("depends") or []:
            # Only same-group edges order a run; an unknown name is ignored
            # rather than treated as a missing test.
            if dep in by_name:
                visit(by_name[dep])
        visiting.discard(name)
        visited.add(name)
        sorted_tests.append(t)

    for t in tests:
        visit(t)
    return sorted_tests


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
    ambiguous = ambiguous_test_names(registry)
    groups: list[TestGroup] = []

    for rg in registry["groups"]:
        # CDK lifecycle tests belong to the cdk suite, not SDK suites.
        if rg.get("service") == "cdk":
            continue
        tests: list[TestCase] = []

        # Topologically sort tests by their declared dependencies so that
        # prerequisites always execute before the tests that need them.
        for rt in _topo_sort(rg["tests"]):
            name: str = rt["name"]
            registry_op = rt.get("op")  # str, None (absent), or null (JSON null)
            # Carried onto every TestCase below, skips included: the harness
            # gate reads it to cascade a failed prerequisite into a skip, and a
            # test skipped for its own reason still blocks its dependents.
            depends: list[str] = rt.get("depends") or []

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
                tests.append(TestCase(name=name, fn=_noop, op=op, skip=rt["skip"], depends=depends))
                continue

            # Capability gate
            required_caps: list[str] = rt.get("requires") or []
            missing = [c for c in required_caps if c not in caps]
            if missing:
                reason = f"requires {', '.join(missing)} (not available in this environment)"
                tests.append(TestCase(name=name, fn=_noop, op=op, skip=reason, depends=depends))
                continue

            # Impl lookup — look up by group-qualified key ("groupName:testName")
            # first, then fall back to the bare test name.  The bare fallback is
            # refused for a name claimed by more than one group: it would bind
            # this group to another group's implementation and report its result
            # as ours.  validate_impls rejects such a registration outright; this
            # is the second line of defence, so a mis-bind cannot occur even if
            # validation is bypassed.
            qualified_key = f"{rg['name']}:{name}"
            bare_usable = name not in ambiguous
            has_impl = qualified_key in impls or (bare_usable and name in impls)
            if not has_impl:
                tests.append(TestCase(
                    name=name, fn=_noop, op=op, depends=depends,
                    skip=f"not yet implemented in {suite} test suite",
                ))
                continue

            fn = impls.get(qualified_key) if qualified_key in impls else impls[name]
            if fn is None:
                # Explicitly registered as None → SDK does not expose this.
                tests.append(TestCase(
                    name=name, fn=_noop, op=op, depends=depends,
                    na="not yet supported by the AWS Python SDK (boto3)",
                ))
                continue

            tests.append(TestCase(name=name, fn=fn, op=op, depends=depends))

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

def test_name_owners(registry: dict) -> dict[str, list[str]]:
    """Map each registry test name to the sorted groups that declare it."""
    owners: dict[str, set[str]] = {}
    for rg in registry["groups"]:
        for rt in rg["tests"]:
            owners.setdefault(rt["name"], set()).add(rg["name"])
    return {name: sorted(groups) for name, groups in owners.items()}


def ambiguous_test_names(registry: dict) -> set[str]:
    """
    Test names that more than one registry group declares.

    A bare-name implementation cannot serve these. ``ListUsers`` belongs to both
    ``iam-users`` and ``cognito-userpools``, so a bare ``ListUsers`` impl binds
    whichever group happens to resolve it — and the loser silently runs the
    other service's test and reports the result as its own. Suites must register
    the group-qualified key for an ambiguous name.
    """
    return {name for name, groups in test_name_owners(registry).items() if len(groups) > 1}


def merge_impls(sources: Iterable[tuple[str, ImplMap]], suite: str) -> ImplMap:
    """
    Flatten the per-module impl maps into the single map the loader resolves
    against, refusing any key that two sources both register.

    The merge used to be ``all_impls.update(mod.IMPLS)`` — last writer wins, and
    silently. Two group modules both registering ``lambda-crud:CreateFunction``
    left one implementation unreachable with nothing said about it, and the run
    reported a result for whichever one survived. :func:`validate_impls` cannot
    catch this: by the time it sees the flattened map the discarded
    implementation is already gone, and the surviving key resolves perfectly
    well.

    Args:
        sources: (label, impls) pairs in registration order. The label is the
                 module the impls came from, so a collision can name both sides.
        suite:   Suite name for the error heading.

    Raises SystemExit naming every duplicated key.
    """
    merged: ImplMap = {}
    owner: dict[str, str] = {}  # key → the source that registered it first

    problems: list[str] = []
    for label, impls in sources:
        for key, fn in impls.items():
            first = owner.get(key)
            if first is not None:
                problems.append(_duplicate_problem(key, first, label))
                continue
            owner[key] = label
            merged[key] = fn

    if not problems:
        return merged
    detail = "\n  - ".join(sorted(problems))
    raise SystemExit(
        f"[compat:{suite}] {len(problems)} duplicate impl registration(s):\n  - {detail}"
    )


def _duplicate_problem(key: str, first: str, second: str) -> str:
    """One collision. The two sources are the same when a module registers the
    key twice."""
    where = (
        f"is registered twice by '{first}'"
        if first == second
        else f"is registered by both '{first}' and '{second}'"
    )
    return (
        f"impl '{key}' {where}"
        " — one of the two would be silently discarded; remove or re-key one"
    )


def validate_impls(registry: dict, impls: ImplMap, suite: str) -> None:
    """
    Reject impl keys that cannot be bound to exactly one registry test.

    This used to warn on stderr (fatal only under OVERCAST_COMPAT_STRICT=1),
    which meant an unresolvable key was a line nobody read while the test it was
    meant to implement quietly fell back to another group's implementation and
    reported a pass. Two registrations are refused:

    - a key matching no registry entry — a typo, a stale name, or the wrong
      separator (every suite uses "group:test"; "group/test" is not accepted);
    - a bare key for a test name that several groups declare, which cannot say
      which group it implements.

    Raises SystemExit naming every unusable key.
    """
    owners = test_name_owners(registry)
    all_names = {
        key
        for rg in registry["groups"]
        for rt in rg["tests"]
        for key in (rt["name"], f"{rg['name']}:{rt['name']}")
    }

    problems: list[str] = []
    for name in sorted(impls):
        if name not in all_names:
            msg = f"impl '{name}' matches no registry entry"
            if "/" in name:
                # The Java suite used "group/test" until the separator was
                # unified; a key copied from it resolves to nothing here.
                msg += (
                    " (group-qualified keys use ':', not '/' — did you mean"
                    f" '{name.replace('/', ':', 1)}'?)"
                )
            problems.append(msg)
        elif len(owners.get(name, ())) > 1:
            # Naming every candidate rather than guessing one: only the author
            # knows which group this implementation is for, and binding it to
            # the wrong one is the failure this check exists to prevent.
            candidates = ", ".join(f"'{group}:{name}'" for group in owners[name])
            problems.append(
                f"impl '{name}' is ambiguous: groups {owners[name]} all declare a test"
                f" named '{name}' — qualify it with the group it implements,"
                f" one of: {candidates}"
            )

    if not problems:
        return
    detail = "\n  - ".join(problems)
    raise SystemExit(
        f"[compat:{suite}] {len(problems)} unusable impl registration(s):\n  - {detail}"
    )
