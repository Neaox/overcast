#!/usr/bin/env python3
"""Find compat tests that disagree with themselves across repeated runs.

Both flaky tests found so far were found by accident — someone happened to run
the same tree through CI twice and noticed the answer changed. That is not a
detection strategy. This script makes it deliberate: run a suite N times against
the same code, compare the status of every test across those runs, and report
anything that did not answer the same way every time.

A test already listed in compat/flaky.json is expected to vary and is reported
separately — it is not news. Anything else varying is a *new* flake, and the
exit code says so, because the moment to catch one is before it starts failing
unrelated PRs.

Usage:
    python3 scripts/compat-flake-detect.py run1.json run2.json [run3.json ...]

Options come from the environment so the workflow stays readable:
    FLAKY_FILE   path to the quarantine list (default: compat/flaky.json)
    SUITE        suite name, for the report heading (default: inferred)

Exit codes: 0 — no unquarantined variance; 1 — new flakes found; 2 — bad input.
"""

from __future__ import annotations

import json
import os
import sys
from collections import defaultdict
from pathlib import Path

FLAKY_FILE = Path(os.environ.get("FLAKY_FILE", "compat/flaky.json"))
SUMMARY = Path(os.environ.get("GITHUB_STEP_SUMMARY", "")) if os.environ.get("GITHUB_STEP_SUMMARY") else None

# The report uses ✅/❌ and box-drawing. A Windows console defaults to cp1252
# and raises UnicodeEncodeError on those, which would crash the script on the
# machines most likely to run it by hand.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")  # type: ignore[union-attr]


def load_results(path: Path) -> dict[str, str]:
    """Map suite/group/test -> status for one run."""
    data = json.loads(path.read_text(encoding="utf-8"))
    out: dict[str, str] = {}
    for suite in data.get("Suites") or []:
        for group in suite.get("Groups") or []:
            for test in group.get("Tests") or []:
                key = f"{suite.get('Suite', '')}/{test.get('group', '')}/{test.get('test', '')}"
                out[key] = test.get("status", "")
    return out


def load_quarantined() -> set[str]:
    try:
        data = json.loads(FLAKY_FILE.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return set()
    return {f"{e['suite']}/{e['group']}/{e['test']}" for e in data.get("flaky", [])}


def emit(line: str = "") -> None:
    print(line)
    if SUMMARY:
        with SUMMARY.open("a", encoding="utf-8") as f:
            f.write(line + "\n")


def main(argv: list[str]) -> int:
    paths = [Path(p) for p in argv[1:]]
    if len(paths) < 2:
        print("usage: compat-flake-detect.py run1.json run2.json [...]", file=sys.stderr)
        return 2
    missing = [p for p in paths if not p.exists()]
    if missing:
        print(f"missing result file(s): {', '.join(str(p) for p in missing)}", file=sys.stderr)
        return 2

    runs = [load_results(p) for p in paths]
    quarantined = load_quarantined()

    # A test must appear in every run to be comparable. One that appears in some
    # runs and not others is itself a form of instability, so it is reported too.
    seen: dict[str, set[str]] = defaultdict(set)
    appearances: dict[str, int] = defaultdict(int)
    for run in runs:
        for key, status in run.items():
            seen[key].add(status)
            appearances[key] += 1

    varying = {k: v for k, v in seen.items() if len(v) > 1}
    intermittent = {k for k, n in appearances.items() if n != len(runs)}

    new_flakes = sorted(k for k in varying if k not in quarantined)
    known = sorted(k for k in varying if k in quarantined)
    new_missing = sorted(k for k in intermittent if k not in quarantined)

    suite = os.environ.get("SUITE", "")
    emit(f"### Flake detection{f' — {suite}' if suite else ''}")
    emit()
    emit(f"{len(runs)} runs of an unchanged tree, {len(seen)} distinct tests.")
    emit()

    if new_flakes:
        emit(f"#### ❌ {len(new_flakes)} test(s) did not answer consistently")
        emit()
        emit("| Test | Statuses seen |")
        emit("| ---- | ------------- |")
        for key in new_flakes:
            emit(f"| `{key}` | {', '.join(sorted(seen[key]))} |")
        emit()
        for key in new_flakes:
            print(
                f"::error title=Compat flake::{key} answered "
                f"{'/'.join(sorted(seen[key]))} across {len(runs)} runs of the same tree"
            )

    if new_missing:
        emit(f"#### ❌ {len(new_missing)} test(s) were not reported by every run")
        emit()
        for key in new_missing:
            emit(f"- `{key}` — reported in {appearances[key]} of {len(runs)} runs")
        emit()

    if known:
        emit(f"<details><summary>{len(known)} known-flaky test(s) varied, as expected</summary>")
        emit()
        for key in known:
            emit(f"- `{key}` — {', '.join(sorted(seen[key]))}")
        emit()
        emit("</details>")
        emit()

    # Machine-readable findings, grouped by (suite, group) — the natural
    # relatedness boundary, since a group shares setup and resources. The
    # workflow turns each cluster into one tracking issue rather than one per
    # test, so a single root cause does not spawn a pile of duplicates.
    clusters: dict[tuple[str, str], list[dict[str, object]]] = defaultdict(list)
    for key in new_flakes + new_missing:
        suite_name, group, test = key.split("/", 2)
        clusters[(suite_name, group)].append(
            {
                "test": test,
                "statuses": sorted(seen[key]),
                "runs_reported": appearances[key],
                "runs_total": len(runs),
            }
        )
    findings = [
        {"suite": s, "group": g, "tests": sorted(t, key=lambda x: str(x["test"]))}
        for (s, g), t in sorted(clusters.items())
    ]
    Path("flake-findings.json").write_text(
        json.dumps({"runs": len(runs), "clusters": findings}, indent=2) + "\n",
        encoding="utf-8",
    )

    if not new_flakes and not new_missing:
        emit("#### ✅ Every test answered the same way in every run")
        emit()
        return 0

    emit(
        "New variance is a bug to fix, not a quarantine to add: a test that "
        "cannot decide will eventually fail an unrelated PR. Quarantine only "
        "buys time, and needs a reviewer's agreement — see "
        "[compat/AGENTS.md](../compat/AGENTS.md#baseline--uniformity-policy)."
    )
    return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
