#!/usr/bin/env python3
"""Tests for scripts/pr-wait.sh's log summarising.

This parsing has been wrong three separate ways in as many days:

  1. Counting every line in a `--watch` log, which redraws the whole table on
     each refresh — a 20-minute run reported "2356 checks passed" for 42.
  2. Deduping failing checks on the whole line, which does not collapse
     redraws because the elapsed-time column changes between them.
  3. Not deduping at all, so a check listed under two triggering workflow
     events had its annotations and logs fetched and printed twice.

Each was found only by running the script against a live PR. The summarising is
exposed as `pr-wait.sh --summarize-log <file>` so it can be exercised here
instead, against synthetic logs, with no network and no pull request.

Run: python scripts/pr-wait_test.py   (CI runs every scripts/*_test.py)
"""

import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "pr-wait.sh"


def _under_system_root(path):
    """True for the WSL launcher shipped in C:\\Windows\\System32."""
    if os.name != "nt":
        return False
    try:
        path.resolve().relative_to(Path(os.environ.get("SystemRoot", r"C:\Windows")).resolve())
    except (ValueError, OSError):
        return False
    return True


def find_bash():
    """A bash that can open a Windows path, because that is what we hand it.

    On Windows, `bash` often resolves to C:\\Windows\\System32\\bash.exe -- the
    WSL launcher, whose filesystem has no F:\\ or C:\\ -- so it reports "No such
    file or directory" and exits 127 for a script that is plainly there.

    Which one wins is decided by PATH order, and System32 sits ahead of Git on
    a stock install, so shutil.which alone is not enough: this file used to say
    which() "honours PATH order and finds Git Bash", and all seven tests failed
    with exit 127 on a dev box while CI stayed green, because CI is Ubuntu and
    never exercises the branch. Git for Windows' bash is now preferred
    explicitly, found next to git itself rather than searched for.

    On Linux and macOS the first candidates do not exist and PATH's bash is
    returned unchanged.
    """
    candidates = []
    git = shutil.which("git")
    if git:
        root = Path(git).resolve().parent.parent
        candidates += [root / "bin" / "bash.exe", root / "usr" / "bin" / "bash.exe"]
    found = shutil.which("bash")
    if found:
        candidates.append(Path(found))

    for candidate in candidates:
        if candidate.is_file() and not _under_system_root(candidate):
            return str(candidate)
    return "bash"


BASH = find_bash()


def row(name, bucket, elapsed, url):
    return f"{name}\t{bucket}\t{elapsed}\t{url}"


def summarize(lines):
    """Run pr-wait.sh --summarize-log over `lines`, return (passed, failing)."""
    with tempfile.NamedTemporaryFile("w", suffix=".log", delete=False, newline="\n") as f:
        f.write("\n".join(lines) + "\n")
        path = f.name
    try:
        # as_posix(): Git Bash reads the backslashes in a native Windows path
        # as escapes. Forward slashes work on every platform.
        out = subprocess.run(
            [BASH, SCRIPT.as_posix(), "--summarize-log", Path(path).as_posix()],
            capture_output=True, text=True, check=True,
        ).stdout.splitlines()
    finally:
        Path(path).unlink(missing_ok=True)
    passed = int(out[0].removeprefix("passed="))
    return passed, out[1:]


class BashResolutionTest(unittest.TestCase):
    def test_resolved_bash_can_read_the_script(self):
        # Without this the WSL mix-up arrives as seven CalledProcessErrors
        # reporting exit 127, which reads like the summarising being broken
        # rather than bash never having opened the file.
        result = subprocess.run(
            [BASH, "-c", 'test -f "$1"', "sh", SCRIPT.as_posix()],
            capture_output=True, text=True,
        )
        self.assertEqual(
            result.returncode, 0,
            f"{BASH} cannot see {SCRIPT}. On Windows this is usually "
            f"C:\\Windows\\System32\\bash.exe, the WSL launcher, whose "
            f"filesystem has no {SCRIPT.drive or 'such'} drive.",
        )


class SummarizeLogTest(unittest.TestCase):
    def test_counts_distinct_checks_not_lines(self):
        # A --watch log holds the whole table once per refresh.
        lines = []
        for elapsed in ("5s", "12s", "19s", "26s"):
            lines.append(row("Lint", "pass", elapsed, "u1"))
            lines.append(row("Vet", "pass", elapsed, "u2"))
        passed, failing = summarize(lines)
        self.assertEqual(passed, 2, "four redraws of two checks is two checks")
        self.assertEqual(failing, [])

    def test_dedupes_failing_check_across_redraws(self):
        # Same failure, three refreshes, elapsed time differing each time —
        # which is why deduping whole lines was not enough.
        lines = [
            row("Changelog entry", "fail", e, "https://x/actions/runs/1/job/99")
            for e in ("5s", "12s", "19s")
        ]
        passed, failing = summarize(lines)
        self.assertEqual(passed, 0)
        self.assertEqual(len(failing), 1)
        self.assertTrue(failing[0].startswith("Changelog entry\tfail\t"))

    def test_dedupes_check_listed_under_two_workflow_events(self):
        # gh lists a check once per triggering event; identical lines.
        line = row("Changelog entry", "fail", "10s", "https://x/actions/runs/1/job/99")
        passed, failing = summarize([line, line])
        self.assertEqual(failing, [line])
        self.assertEqual(passed, 0)

    def test_keeps_distinct_failures_apart(self):
        lines = [
            row("Lint", "fail", "9s", "https://x/actions/runs/1/job/1"),
            row("Vet", "fail", "9s", "https://x/actions/runs/1/job/2"),
            row("Test", "pass", "9s", "https://x/actions/runs/1/job/3"),
        ]
        passed, failing = summarize(lines)
        self.assertEqual(passed, 1)
        self.assertEqual([f.split("\t")[0] for f in failing], ["Lint", "Vet"])

    def test_cancelled_counts_as_failing(self):
        lines = [row("Docker build", "cancel", "3s", "https://x/actions/runs/1/job/4")]
        _, failing = summarize(lines)
        self.assertEqual(len(failing), 1)

    def test_skipping_and_pending_are_neither(self):
        lines = [
            row("Publish", "skipping", "0", "u1"),
            row("Compat", "pending", "0", "u2"),
        ]
        passed, failing = summarize(lines)
        self.assertEqual(passed, 0)
        self.assertEqual(failing, [], "pending is not a failure — it is why we wait")

    def test_empty_log(self):
        passed, failing = summarize([])
        self.assertEqual(passed, 0)
        self.assertEqual(failing, [])


if __name__ == "__main__":
    unittest.main(verbosity=2 if "-v" in sys.argv else 1)
