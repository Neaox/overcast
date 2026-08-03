"""
Unit tests for the registry loader's impl-key resolution rules.

These are not compat tests — they need no emulator. They pin the two rules that
stop a run from reporting a result for a test that never executed:

- a key that resolves to nothing aborts, instead of warning;
- a bare key for a name several groups declare is refused, instead of binding
  whichever group's implementation happened to be registered last.

Run with:  python -m unittest discover -s tests  (from compat/suites/python-sdk/)
"""

from __future__ import annotations

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from lib.registry import (  # noqa: E402
    ambiguous_test_names,
    build_groups_from_registry,
    load_registry,
    test_name_owners,
    validate_impls,
)


def _noop(ctx):
    return None


# Two unrelated groups declaring a test of the same name, plus a name owned by
# exactly one group — the shape that made a mis-binding possible.
TWO_GROUPS_ONE_NAME = {
    "groups": [
        {
            "service": "iam",
            "name": "iam-users",
            "tests": [{"name": "ListUsers"}, {"name": "CreateUser"}],
        },
        {
            "service": "cognito",
            "name": "cognito-userpools",
            "tests": [{"name": "ListUsers"}],
        },
    ]
}


def _find(groups, group_name, test_name):
    for g in groups:
        if g.name != group_name:
            continue
        for tc in g.tests:
            if tc.name == test_name:
                return tc
    raise AssertionError(f"no test {group_name}/{test_name} in built groups")


class UnresolvableKeysAbort(unittest.TestCase):
    def test_wrong_separator(self):
        with self.assertRaises(SystemExit) as cm:
            validate_impls(TWO_GROUPS_ONE_NAME, {"iam-users/CreateUser": _noop}, "python-sdk")
        message = str(cm.exception)
        self.assertIn("iam-users/CreateUser", message)
        self.assertIn("matches no registry entry", message)
        # The message must point at the colon form, since that is the fix.
        self.assertIn("iam-users:CreateUser", message)

    def test_unknown_group(self):
        with self.assertRaises(SystemExit) as cm:
            validate_impls(TWO_GROUPS_ONE_NAME, {"iam-usres:CreateUser": _noop}, "python-sdk")
        self.assertIn("iam-usres:CreateUser", str(cm.exception))

    def test_unknown_test(self):
        with self.assertRaises(SystemExit) as cm:
            validate_impls(TWO_GROUPS_ONE_NAME, {"CreateUsr": _noop}, "python-sdk")
        self.assertIn("CreateUsr", str(cm.exception))


class AmbiguousBareKeysRefused(unittest.TestCase):
    def test_bare_key_for_shared_name(self):
        with self.assertRaises(SystemExit) as cm:
            validate_impls(TWO_GROUPS_ONE_NAME, {"ListUsers": _noop}, "python-sdk")
        message = str(cm.exception)
        self.assertIn("ambiguous", message)
        self.assertIn("iam-users", message)
        self.assertIn("cognito-userpools", message)

    def test_resolvable_keys_accepted(self):
        validate_impls(
            TWO_GROUPS_ONE_NAME,
            {
                "CreateUser": _noop,  # bare, single owner
                "iam-users:ListUsers": _noop,
                "cognito-userpools:ListUsers": _noop,
            },
            "python-sdk",
        )


class BuildGroupsResolution(unittest.TestCase):
    """Defence in depth: even bypassing validation, no cross-group binding."""

    def test_cross_group_bare_fallback_refused(self):
        groups = build_groups_from_registry(
            TWO_GROUPS_ONE_NAME, {"ListUsers": _noop}, "python-sdk"
        )
        cognito = _find(groups, "cognito-userpools", "ListUsers")
        self.assertEqual(cognito.skip, "not yet implemented in python-sdk test suite")
        self.assertTrue(_find(groups, "iam-users", "ListUsers").skip)

    def test_qualified_key_binds_only_its_group(self):
        groups = build_groups_from_registry(
            TWO_GROUPS_ONE_NAME, {"iam-users:ListUsers": _noop}, "python-sdk"
        )
        self.assertFalse(_find(groups, "iam-users", "ListUsers").skip)
        self.assertTrue(_find(groups, "cognito-userpools", "ListUsers").skip)

    def test_unambiguous_bare_fallback_still_works(self):
        groups = build_groups_from_registry(
            TWO_GROUPS_ONE_NAME, {"CreateUser": _noop}, "python-sdk"
        )
        self.assertFalse(_find(groups, "iam-users", "CreateUser").skip)


class OwnerTracking(unittest.TestCase):
    def test_owners_and_ambiguity(self):
        self.assertIn("ListUsers", ambiguous_test_names(TWO_GROUPS_ONE_NAME))
        self.assertNotIn("CreateUser", ambiguous_test_names(TWO_GROUPS_ONE_NAME))
        self.assertEqual(
            ["cognito-userpools", "iam-users"],
            test_name_owners(TWO_GROUPS_ONE_NAME)["ListUsers"],
        )


class RealRegistrations(unittest.TestCase):
    """
    The suite's own registrations must resolve against the real registry.json.

    This is the check that catches a mis-binding before a run reports one, in
    `python -m unittest` rather than in results that silently describe the wrong
    test.

    The group modules are imported directly rather than via `runner`, which
    starts a suite run at import time. Every module exposing IMPLS is collected,
    so a module runner forgets to list is still checked.
    """

    def test_registered_impls_resolve(self):
        import importlib
        import pkgutil

        try:
            import groups as groups_pkg
        except ImportError as exc:  # pragma: no cover - depends on environment
            self.skipTest(f"suite group modules unavailable: {exc}")

        impls = {}
        for info in pkgutil.iter_modules(groups_pkg.__path__):
            try:
                module = importlib.import_module(f"groups.{info.name}")
            except ImportError as exc:  # pragma: no cover
                self.skipTest(f"cannot import groups.{info.name}: {exc}")
            impls.update(getattr(module, "IMPLS", {}))

        self.assertTrue(impls, "no IMPLS collected from the groups package")
        validate_impls(load_registry(), impls, "python-sdk")


if __name__ == "__main__":
    unittest.main()
