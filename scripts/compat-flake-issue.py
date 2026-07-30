#!/usr/bin/env python3
"""Raise a tracking issue for each cluster of newly detected flaky tests.

Detecting a flake and leaving it in a job log achieves nothing — nobody reads a
nightly that is green by default. Quarantine is only allowed with a tracking
issue (`compat/flaky.json` requires the field, and the PR lint enforces it), so
the detector opens that issue itself.

One issue per (suite, group) cluster, not per test: tests in a group share
setup and resources, so a cluster is almost always one root cause, and filing
three issues for it would just create three places to say the same thing. A
recurrence re-opens and comments on the existing issue rather than duplicating.

Usage: python3 scripts/compat-flake-issue.py flake-findings.json

Environment: GH_TOKEN (required by gh), SUITE, RUN_URL.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

RUN_URL = os.environ.get("RUN_URL", "")
LABELS = "bug,compat,status/needs-triage"

# Lets a recurrence find the issue it belongs to, whatever the title says.
MARKER = "<!-- compat-flake:{suite}/{group} -->"


def gh(*args: str, check: bool = True) -> str:
    result = subprocess.run(["gh", *args], capture_output=True, text=True)
    if check and result.returncode != 0:
        print(f"gh {' '.join(args)} failed: {result.stderr.strip()}", file=sys.stderr)
        raise SystemExit(1)
    return result.stdout.strip()


def find_existing(marker: str) -> tuple[str, str] | None:
    """Return (number, state) of an issue carrying the marker, if any."""
    raw = gh(
        "issue", "list", "--state", "all", "--limit", "100",
        "--search", f'"{marker}" in:body', "--json", "number,state",
        check=False,
    )
    try:
        rows = json.loads(raw) if raw else []
    except json.JSONDecodeError:
        return None
    if not rows:
        return None
    return str(rows[0]["number"]), rows[0].get("state", "OPEN")


def body_for(cluster: dict, runs: int) -> str:
    suite, group = cluster["suite"], cluster["group"]
    lines = [
        MARKER.format(suite=suite, group=group),
        "",
        f"Nightly flake detection ran `{suite}` **{runs} times against an unchanged tree** "
        f"and these tests in `{group}` did not answer the same way each time.",
        "",
        "| Test | Statuses seen |",
        "| ---- | ------------- |",
    ]
    for t in cluster["tests"]:
        statuses = ", ".join(f"`{s}`" for s in t["statuses"]) or "—"
        if t["runs_reported"] != t["runs_total"]:
            statuses += f" (reported in only {t['runs_reported']} of {t['runs_total']} runs)"
        lines += [f"| `{suite}/{group}/{t['test']}` | {statuses} |"]
    lines += [
        "",
        "## Why this matters",
        "",
        "A test that cannot decide will eventually fail an unrelated pull request, and the",
        "usual response — re-run it — is how a gate stops being read. Worse, most compat",
        "flakes so far have been emulator state races rather than test bugs: a resource is",
        "created successfully and the next call cannot find it. That is a real defect users",
        "would hit and be unable to reproduce.",
        "",
        "## Reproducing",
        "",
        "```sh",
        f"for i in 1 2 3; do go run ./cmd/compat --suite {suite} --results-file \"run-$i.json\"; done",
        "```",
        "",
        "```sh",
        "python3 scripts/compat-flake-detect.py run-1.json run-2.json run-3.json",
        "```",
        "",
        "## Definition of done",
        "",
        "- Root cause found and fixed, with a reproducing test.",
        f"- Any `compat/flaky.json` entry for `{group}` deleted (the lint allows removals freely).",
        "- Nightly detection green for three consecutive nights.",
        "",
        "Quarantining these tests is a stop-gap and needs a reviewer's agreement — it does not",
        "close this issue. See [compat/AGENTS.md](../blob/main/compat/AGENTS.md#stabilising-a-flaky-test).",
    ]
    if RUN_URL:
        lines += ["", f"Detected by [this run]({RUN_URL})."]
    return "\n".join(lines)


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print("usage: compat-flake-issue.py flake-findings.json", file=sys.stderr)
        return 2
    path = Path(argv[1])
    if not path.exists():
        print(f"no findings file at {path} — nothing to raise", file=sys.stderr)
        return 0

    data = json.loads(path.read_text(encoding="utf-8"))
    clusters = data.get("clusters") or []
    runs = data.get("runs", 0)
    if not clusters:
        return 0

    for cluster in clusters:
        suite, group = cluster["suite"], cluster["group"]
        marker = MARKER.format(suite=suite, group=group)
        title = f"compat flake: {suite}/{group} ({len(cluster['tests'])} test(s))"
        body = body_for(cluster, runs)

        existing = find_existing(marker)
        if existing is None:
            url = gh("issue", "create", "--title", title, "--label", LABELS, "--body", body)
            print(f"raised {url}")
            continue

        number, state = existing
        if state.upper() == "CLOSED":
            gh("issue", "reopen", number, check=False)
            print(f"reopened #{number} — the flake came back")
        gh("issue", "comment", number, "--body",
           f"Detected again by nightly flake detection.\n\n{body}", check=False)
        print(f"commented on #{number}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
