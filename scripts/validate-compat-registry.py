#!/usr/bin/env python3
"""Validate compat/suites/registry.json against its JSON Schema.

The registry is the join key for every compat suite: group and test names are
matched verbatim across seven suite implementations, the dashboard, and
compat/baseline.json. Nothing enforced the schema until now, so names could
drift from the documented PascalCase convention without CI noticing.

Beyond the schema, this checks two referential invariants JSON Schema cannot
express: names must be unique, and every `depends` entry must name a test in
the same group.

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
import sys
from pathlib import Path
from typing import NoReturn

REPO_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_REGISTRY = REPO_ROOT / "compat" / "suites" / "registry.json"
DEFAULT_SCHEMA = REPO_ROOT / "compat" / "suites" / "registry.schema.json"


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


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--registry", type=Path, default=DEFAULT_REGISTRY)
    parser.add_argument("--schema", type=Path, default=DEFAULT_SCHEMA)
    args = parser.parse_args()

    registry = load_json(args.registry)
    schema = load_json(args.schema)

    errors = schema_errors(registry, schema) + reference_errors(registry)
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
