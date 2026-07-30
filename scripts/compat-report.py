#!/usr/bin/env python3
"""Render a compat run into the formats GitHub understands.

One pass over the merged results produces three artefacts:

* the **job summary** (`$GITHUB_STEP_SUMMARY`) — regressions first, then the
  suite matrix, failure detail, and parity debt;
* **compat-comment.md** — a digest for the sticky PR comment;
* **compat-junit.xml** — a JUnit report so `action-junit-report` can publish a
  check run with per-test history.

The JUnit mapping is deliberate: only *baseline regressions* are `<failure>`.
A failure the baseline already records is a known gap being burned down, and
rendering 800 expected gaps as failures would make the check run useless. Those
become `<skipped>` with the reason spelled out.

Usage: python3 scripts/compat-report.py   (run from the repo root)

Inputs (all optional except results): compat-results.json, compat/baseline.json,
baseline-regressions.txt, parity-issues.txt, compat/parity-debt.json.
"""

from __future__ import annotations

import json
import os
import re
import sys
import xml.etree.ElementTree as ET
from collections import Counter, defaultdict
from datetime import date
from pathlib import Path

RESULTS = Path("compat-results.json")
BASELINE = Path("compat/baseline.json")
PARITY_DEBT = Path("compat/parity-debt.json")
FLAKY = Path("compat/flaky.json")

# Mirrors the deadlines in cmd/compat/baseline.go — keep them in step.
FLAKY_SOFT_DEADLINE_DAYS = 14
FLAKY_HARD_DEADLINE_DAYS = 30
REGRESSIONS_LOG = Path("baseline-regressions.txt")
PARITY_LOG = Path("parity-issues.txt")

SUMMARY = Path(os.environ.get("GITHUB_STEP_SUMMARY", "compat-summary.md"))
COMMENT = Path("compat-comment.md")
JUNIT = Path("compat-junit.xml")

RUN_URL = "{}/{}/actions/runs/{}".format(
    os.environ.get("GITHUB_SERVER_URL", "https://github.com"),
    os.environ.get("GITHUB_REPOSITORY", ""),
    os.environ.get("GITHUB_RUN_ID", ""),
)

# Matches the messages cmd/compat/baseline.go emits, e.g.
#   compat baseline regression: go-sdk/sqs-fifo/CreateFifoQueue pass -> fail
#   compat baseline new failure, not in baseline: rust-sdk/s3-copy/Create
KEY_RE = re.compile(r"\b([a-z0-9-]+/[a-z0-9-]+/[A-Za-z0-9_]+)\b")


def read_json(path: Path):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None


def read_lines(path: Path) -> list[str]:
    try:
        return [ln.strip() for ln in path.read_text(encoding="utf-8").splitlines() if ln.strip()]
    except OSError:
        return []


def flatten(results) -> list[dict]:
    """Yield one dict per test result, with its suite attached."""
    out = []
    for suite in results.get("Suites") or []:
        for group in suite.get("Groups") or []:
            for test in group.get("Tests") or []:
                out.append(
                    {
                        "suite": suite.get("Suite", ""),
                        "service": test.get("service") or group.get("Service", ""),
                        "group": test.get("group") or group.get("Name", ""),
                        "test": test.get("test", ""),
                        "status": test.get("status", ""),
                        "error": test.get("error", "") or "",
                        "duration_ms": test.get("duration_ms", 0) or 0,
                    }
                )
    return out


def regression_keys(lines: list[str]) -> set[str]:
    """Extract suite/group/test keys from the compare step's stderr."""
    keys = set()
    for line in lines:
        if "compat baseline" not in line:
            continue
        m = KEY_RE.search(line)
        if m:
            keys.add(m.group(1))
    return keys


def md_table(rows: list[list[str]], header: list[str], aligns: list[str]) -> str:
    out = ["| " + " | ".join(header) + " |", "| " + " | ".join(aligns) + " |"]
    for row in rows:
        out.append("| " + " | ".join(row) + " |")
    return "\n".join(out) + "\n"


def build(results, baseline, debt, regressions, parity_issues) -> tuple[str, str, bytes]:
    tests = flatten(results)
    reg_keys = regression_keys(regressions)
    baseline_status = {
        f"{e['suite']}/{e['group']}/{e['test']}": e.get("status", "")
        for e in (baseline or {}).get("entries", [])
    }

    per_suite: dict[str, Counter] = defaultdict(Counter)
    for t in tests:
        per_suite[t["suite"]][t["status"]] += 1
    totals = Counter()
    for counts in per_suite.values():
        totals.update(counts)

    def cell(counts: Counter) -> list[str]:
        total = sum(counts.values())
        return [
            str(counts.get("pass", 0)),
            str(counts.get("fail", 0)),
            str(counts.get("unimplemented", 0)),
            str(counts.get("skip", 0)),
            str(counts.get("na", 0)),
            str(total),
        ]

    header = ["Suite", "Pass", "Fail", "Unimpl.", "Skip", "N/A", "Total"]
    aligns = ["-----", "---:", "---:", "------:", "---:", "--:", "----:"]
    rows = [[s] + cell(per_suite[s]) for s in sorted(per_suite)]
    rows.append(["**Total**"] + [f"**{v}**" for v in cell(totals)])
    matrix = md_table(rows, header, aligns)

    # ---- job summary -----------------------------------------------------
    S: list[str] = ["## Compatibility Test Results\n"]

    if reg_keys or parity_issues:
        S.append("### ❌ Gate failures\n")
        if reg_keys:
            S.append(f"**{len(reg_keys)} baseline regression(s)** — a result got worse than `compat/baseline.json` records, or a new test is failing.\n")
            S.append("```")
            S.extend(regressions[:50])
            if len(regressions) > 50:
                S.append(f"… {len(regressions) - 50} more")
            S.append("```\n")
        if parity_issues:
            S.append(f"**{len(parity_issues)} cross-suite parity issue(s)** — the registry and the suites disagree.\n")
            S.append("```")
            S.extend(parity_issues[:50])
            if len(parity_issues) > 50:
                S.append(f"… {len(parity_issues) - 50} more")
            S.append("```\n")
    else:
        S.append("### ✅ No regressions against the baseline\n")

    S.append(matrix)
    endpoint = results.get("Endpoint", "")
    if endpoint:
        S.append(f"\nEndpoint: `{endpoint}`\n")

    # Failure detail, grouped by whether the baseline already knew about it.
    fails = [t for t in tests if t["status"] == "fail"]
    if fails:
        new_fails = [t for t in fails if f"{t['suite']}/{t['group']}/{t['test']}" in reg_keys]
        known = [t for t in fails if f"{t['suite']}/{t['group']}/{t['test']}" not in reg_keys]

        def detail(items: list[dict]) -> str:
            lines = []
            for t in items:
                err = " ".join(t["error"].split())[:300]
                lines.append(f"- `{t['suite']}` **{t['group']}/{t['test']}** — {err}")
            return "\n".join(lines) + "\n"

        if new_fails:
            S.append(f"\n### New failures ({len(new_fails)})\n")
            S.append(detail(new_fails))
        if known:
            S.append(f"\n<details><summary>Known failures being burned down ({len(known)})</summary>\n\n")
            S.append(detail(known))
            S.append("\n</details>\n")

    if debt:
        debt_entries = debt.get("debt", [])
        by_suite = Counter()
        for e in debt_entries:
            by_suite[e["suite"]] += e.get("tests", 0)
        if by_suite:
            S.append(f"\n### Cross-suite parity debt — {sum(by_suite.values())} test(s) in {len(debt_entries)} group(s)\n")
            S.append(
                md_table(
                    [[s, str(n)] for s, n in sorted(by_suite.items(), key=lambda kv: -kv[1])],
                    ["Suite", "Unimplemented registry tests"],
                    ["-----", "---:"],
                )
            )
            S.append("\nRegistry tests a suite has not implemented yet. This only shrinks — see `compat/parity-debt.json`.\n")

    # Quarantine is a stop-gap, so it is reported on every run with its age.
    # An entry nobody can see is an entry nobody fixes.
    flaky = read_json(FLAKY)
    entries = (flaky or {}).get("flaky") or []
    if entries:
        today = date.today()
        rows = []
        overdue = 0
        for e in entries:
            try:
                age = (today - date.fromisoformat(e.get("since", ""))).days
                age_text = f"{age} day{'s' if age != 1 else ''}"
                if age > FLAKY_HARD_DEADLINE_DAYS:
                    age_text = f"⛔ {age_text} — blocking PRs"
                    overdue += 1
                elif age > FLAKY_SOFT_DEADLINE_DAYS:
                    age_text = f"⚠️ {age_text} — overdue"
                    overdue += 1
            except (TypeError, ValueError):
                age_text = "⚠️ no date"
                overdue += 1
            issue = e.get("issue", "")
            issue_text = f"[track]({issue})" if issue else "⚠️ untracked"
            rows.append([f"`{e['suite']}/{e['group']}/{e['test']}`", age_text, issue_text])
        heading = f"\n### Quarantined as flaky — {len(entries)}"
        if overdue:
            heading += f" ({overdue} needing attention)"
        S.append(heading + "\n")
        S.append(md_table(rows, ["Test", "Quarantined", "Issue"], ["----", "----------", "-----"]))
        S.append(
            f"\nExempt from the gate in both directions while quarantined. Overdue after "
            f"{FLAKY_SOFT_DEADLINE_DAYS} days, blocking after {FLAKY_HARD_DEADLINE_DAYS} — "
            "fix the test and delete the entry.\n"
        )

    # ---- PR comment digest ----------------------------------------------
    C: list[str] = ["### Compatibility Tests\n"]
    if reg_keys or parity_issues:
        bits = []
        if reg_keys:
            bits.append(f"{len(reg_keys)} baseline regression(s)")
        if parity_issues:
            bits.append(f"{len(parity_issues)} parity issue(s)")
        C.append("❌ **" + " · ".join(bits) + "**\n")
        for line in (regressions + parity_issues)[:10]:
            C.append(f"- {line}")
        remaining = len(regressions) + len(parity_issues) - 10
        if remaining > 0:
            C.append(f"- … {remaining} more")
        C.append("")
    else:
        C.append("✅ **No regressions against the baseline**\n")
    C.append(matrix)
    pass_rate = ""
    counted = totals.get("pass", 0) + totals.get("fail", 0) + totals.get("unimplemented", 0)
    if counted:
        pass_rate = f"Pass rate (excluding skips and N/A): **{100.0 * totals.get('pass', 0) / counted:.1f}%**\n"
    if pass_rate:
        C.append("\n" + pass_rate)
    if RUN_URL.strip("/"):
        C.append(f"\n[Full report]({RUN_URL})\n")

    # ---- JUnit ----------------------------------------------------------
    suites_el = ET.Element("testsuites")
    for suite_name in sorted(per_suite):
        items = [t for t in tests if t["suite"] == suite_name]
        suite_el = ET.SubElement(suites_el, "testsuite", name=f"compat.{suite_name}")
        failures = skipped = 0
        for t in items:
            key = f"{t['suite']}/{t['group']}/{t['test']}"
            case = ET.SubElement(
                suite_el,
                "testcase",
                name=f"{t['group']}.{t['test']}",
                classname=f"compat.{suite_name}.{t['service'] or t['group']}",
                time=f"{t['duration_ms'] / 1000.0:.3f}",
            )
            status, err = t["status"], t["error"]
            # A flagged key whose test currently passes is not a failure to
            # report — the compare step can name a key for reasons other than
            # this run's status (a missing-result expectation, for instance), and
            # fabricating a <failure> on a green test would be a lie.
            if key in reg_keys and status != "pass":
                failures += 1
                was = baseline_status.get(key, "not in baseline")
                node = ET.SubElement(case, "failure", message=f"regression: {was} -> {status or 'missing'}")
                node.text = err or "no error message"
            elif status == "fail":
                skipped += 1
                ET.SubElement(case, "skipped", message=f"known failure (recorded in baseline): {' '.join(err.split())[:200]}")
            elif status in ("skip", "na", "unimplemented"):
                skipped += 1
                reason = {"na": "n/a for this tool", "unimplemented": "not implemented by Overcast"}.get(status, "skipped")
                ET.SubElement(case, "skipped", message=f"{reason}: {' '.join(err.split())[:200]}" if err else reason)
        suite_el.set("tests", str(len(items)))
        suite_el.set("failures", str(failures))
        suite_el.set("skipped", str(skipped))
        suite_el.set("errors", "0")

    xml_bytes = b'<?xml version="1.0" encoding="UTF-8"?>\n' + ET.tostring(suites_el, encoding="utf-8")
    return "\n".join(S), "\n".join(C), xml_bytes


def main() -> int:
    results = read_json(RESULTS)
    if not results:
        msg = "_`compat-results.json` not found or unreadable — a suite may have failed before writing results._\n"
        with SUMMARY.open("a", encoding="utf-8") as f:
            f.write("## Compatibility Test Results\n\n" + msg)
        COMMENT.write_text("### Compatibility Tests\n\n" + msg, encoding="utf-8")
        print("compat-report: no results to render", file=sys.stderr)
        return 1

    summary, comment, junit = build(
        results,
        read_json(BASELINE),
        read_json(PARITY_DEBT),
        read_lines(REGRESSIONS_LOG),
        read_lines(PARITY_LOG),
    )
    with SUMMARY.open("a", encoding="utf-8") as f:
        f.write(summary)
    COMMENT.write_text(comment, encoding="utf-8")
    JUNIT.write_bytes(junit)
    print(f"compat-report: wrote {SUMMARY}, {COMMENT}, {JUNIT}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
