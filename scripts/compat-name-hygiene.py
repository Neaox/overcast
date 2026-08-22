#!/usr/bin/env python3
"""Lint compat suite source for the resource-name hygiene convention.

Every compat resource name is supposed to embed a group-specific token next
to the run id (`{runId}-<group-token>-...`) so that two different registry
groups never fight over the same literal AWS resource name in the same test
run. docs/plans/compat-baseline-and-uniformity.md § "Framework audit" records
one real instance of this bug class: `sts-assume`'s `AssumeRole` targeted
`role/{runId}-role`, the *exact* role name `iam-roles` creates and tears down
in a sibling parallel slot (fixed by renaming to `-sts-assume-role`). The
audit's own scanner found that one instance by comparing name literals across
group files and flagged several more that turned out, on human inspection,
to be intentional (rust-sdk's per-group setup/teardown maps; python's
logs/kinesis groups sharing a tail across genuinely different services). The
plan explicitly declined to make that scanner a CI gate because it needs
per-language parsing and produces exactly that kind of false positive.

This script automates the same comparison at a coarser, cheaper grain: it
compares name-construction literals **per suite, per source file** rather
than per registry group (finding the group a specific line belongs to needs
the "banner-section" parsing the plan judged not worth building).

A first version of this script compared *every* name literal across files and
was unusable: nearly every suite defaults to a bare `compat-{runId}` (or
`compat-<word>-{runId}`) scaffold name for whichever resource in a group
doesn't need a descriptive name, and dozens of unrelated services share that
scaffold harmlessly — it produced 60+ "collisions" against the current,
already-audited-clean tree, all false positives of exactly the shape the
plan's own scanner ran into ("python logs/kinesis share tails across
different services (no collision)"). Two different AWS services never
actually collide on a name string: the emulator's state is keyed by
(service, resource-kind, name), not by name alone, so a shared scaffold word
with no resource-kind context is *not* a hygiene bug.

The real bug found by the audit (`sts-assume` reusing `iam-roles`'s exact
`{runId}-role` name) collided because both literals name the *same* AWS
resource kind — an IAM role — which genuinely has one flat namespace per
account. So this script only compares literals that share a **resource-kind
tag**, inferred cheaply from a keyword next to the literal on the same line
(`role`, `bucket`, `queue`, `topic`, `table`, `function`, `secret`, `alias`,
in RESOURCE_KIND_KEYWORDS below) — untagged literals (the `compat` scaffolds)
are never compared, which is what keeps this check's false-positive rate low
enough to gate CI. It is deliberately narrower than "every possible naming
collision"; known-intentional same-kind matches (if any legitimate one turns
up) are recorded in compat/suites/name-hygiene-allowlist.json, mirroring how
compat/parity-debt.json records reviewed, deliberate gaps rather than
grandfathering silently.

Two independent checks:

1. **Cross-file same-resource-kind collision** — the same static text (run id
   placeholder removed) is used to build a same-kind resource name (e.g. two
   different files both naming an IAM role the same thing) in two *different*
   group source files of the same suite, and the pair is not in the
   allowlist.
2. **Bare run id** — a resource name literal reduces to *no* static text at
   all once the run id is removed (e.g. just the run id, no group-specific
   segment whatsoever). Always a violation, regardless of collisions.

Usage:
    python3 scripts/compat-name-hygiene.py
    python3 scripts/compat-name-hygiene.py --root path/to/repo

Exit codes:
    0  no (non-allowlisted) violations found
    1  violations found
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_ALLOWLIST = REPO_ROOT / "compat" / "suites" / "name-hygiene-allowlist.json"

# One entry per suite: where its per-service group files live, and which
# language-specific extraction strategy applies. Helper/registration files
# that aren't "one file roughly = one service" are excluded by name.
SUITES = [
    {
        "name": "node-js-sdk",
        "dir": "compat/suites/node-js-sdk/src/groups",
        "ext": ".ts",
        "lang": "ts",
        "exclude": {"index.ts"},
    },
    {
        "name": "python-sdk",
        "dir": "compat/suites/python-sdk/groups",
        "ext": ".py",
        "lang": "python",
        "exclude": {"__init__.py"},
    },
    {
        "name": "go-sdk",
        "dir": "compat/suites/go-sdk/internal/groups",
        "ext": ".go",
        "lang": "go",
        "exclude": {"groups.go", "groups_test.go"},
    },
    {
        "name": "cli",
        "dir": "compat/suites/cli/internal/groups",
        "ext": ".go",
        "lang": "go",
        "exclude": {"groups.go", "groups_test.go", "util.go"},
    },
    {
        "name": "java-sdk",
        "dir": "compat/suites/java-sdk/src/main/java/io/overcast/compat/groups",
        "ext": ".java",
        "lang": "java",
        "exclude": {"ServiceGroup.java"},
    },
    {
        "name": "dotnet-sdk",
        "dir": "compat/suites/dotnet-sdk/Groups",
        "ext": ".cs",
        "lang": "csharp",
        "exclude": {"IServiceGroup.cs"},
    },
    {
        "name": "rust-sdk",
        "dir": "compat/suites/rust-sdk/src/groups",
        "ext": ".rs",
        "lang": "rust",
        "exclude": {"mod.rs"},
    },
]

# Keywords that identify a genuinely flat, account-wide AWS namespace where a
# name collision between two different registry groups is a real bug, not a
# coincidence. Deliberately short: broadening it re-introduces the noise
# described above (e.g. "cluster" spans ElastiCache and RDS, which do not
# share a namespace, so it stays out).
RESOURCE_KIND_KEYWORDS = [
    "role",
    "bucket",
    "queue",
    "topic",
    "table",
    "function",
    "secret",
    "alias",
]
KIND_RE = {kw: re.compile(rf"\b{kw}\w*\b", re.IGNORECASE) for kw in RESOURCE_KIND_KEYWORDS}

RUNID_RE = re.compile(r"run_?id", re.IGNORECASE)
TS_INTERP_RE = re.compile(r"\$\{[^}]*\}")
PY_INTERP_RE = re.compile(r"\{[^}]*\}")
RUST_INTERP_RE = re.compile(r"\{[^}]*\}")
FORMAT_SPEC_RE = re.compile(r"%[sdvq]")

# Sprintf-style call: literal format string followed by its args on one line.
SPRINTF_RE = re.compile(r'fmt\.Sprintf\(\s*"((?:[^"\\]|\\.)*)"\s*,\s*([^)]*)\)')
RUST_FORMAT_RE = re.compile(r'format!\(\s*"((?:[^"\\]|\\.)*)"\s*,\s*([^)]*)\)')


@dataclass
class Candidate:
    suite: str
    file: str
    line: int
    raw: str
    static_text: str
    kind: str | None = None


def resource_kind(line: str) -> str | None:
    """Best-effort tag for which flat AWS namespace this literal names.

    Returns None ("untagged") when no recognized keyword appears on the
    line — untagged candidates are never compared, so a shared scaffold word
    like "compat" with no resource-kind context nearby is not flagged.
    """
    for kw, pattern in KIND_RE.items():
        if pattern.search(line):
            return kw
    return None


@dataclass
class Violations:
    collisions: list[tuple[Candidate, Candidate]] = field(default_factory=list)
    bare_runid: list[Candidate] = field(default_factory=list)


def strip_line_comments(line: str, lang: str) -> str:
    """Remove a trailing line comment, but never inside a string literal."""
    comment_marker = "#" if lang == "python" else "//"
    in_string: str | None = None  # the quote char currently open, if any
    i = 0
    n = len(line)
    while i < n:
        ch = line[i]
        if in_string:
            if ch == "\\":
                i += 2
                continue
            if ch == in_string:
                in_string = None
            i += 1
            continue
        if ch in ("\"", "'", "`"):
            in_string = ch
            i += 1
            continue
        if line[i : i + len(comment_marker)] == comment_marker:
            return line[:i]
        i += 1
    return line


def strip_placeholders(text: str, lang: str) -> str:
    """Remove interpolation/format-placeholder syntax, leaving only static text."""
    if lang == "ts":
        text = TS_INTERP_RE.sub("", text)
    elif lang == "python":
        text = PY_INTERP_RE.sub("", text)
    elif lang == "rust":
        text = RUST_INTERP_RE.sub("", text)
    text = FORMAT_SPEC_RE.sub("", text)
    return text


ARN_PREFIX_RE = re.compile(r"^arn:[^:]*:[^:]*:[^:]*:[^:]*:")
RESOURCE_TYPE_PREFIX_RE = re.compile(
    r"^(?:" + "|".join(RESOURCE_KIND_KEYWORDS) + r")\w*[/:]", re.IGNORECASE
)


def normalize(text: str) -> str:
    """Reduce a literal to the actual resource-name segment for comparison.

    An ARN embeds the name after `arn:<partition>:<service>:<region>:<account>:`
    and then a resource-type separator (e.g.
    `arn:aws:iam::000000000000:role/{runId}-role`), so two literals that
    differ only in ARN scaffolding but agree on the trailing name segment
    must still compare equal — that is exactly the shape of the sts-assume
    bug this check exists to catch. Deliberately narrow (only known ARN and
    resource-type-keyword prefixes are stripped) so an unrelated path-like
    name such as a CloudWatch Logs group `/overcast/{runId}` is left intact
    rather than having its only static segment stripped as if it were a
    separator.
    """
    text = text.strip(" -_").lower()
    text = ARN_PREFIX_RE.sub("", text)
    text = RESOURCE_TYPE_PREFIX_RE.sub("", text)
    return text.strip(" -_")


def extract_interpolated_literals(line: str, lang: str) -> list[str]:
    """Case A: run id referenced *inside* a template/f-string literal."""
    out = []
    if lang == "ts":
        for m in re.finditer(r"`([^`]*)`", line):
            content = m.group(1)
            interps = TS_INTERP_RE.findall(content)
            if any(RUNID_RE.search(x) for x in interps):
                out.append(content)
    elif lang == "python":
        for m in re.finditer(r'f(["\'])((?:[^"\'{}]|\{[^}]*\})*)\1', line):
            content = m.group(2)
            interps = PY_INTERP_RE.findall(content)
            if any(RUNID_RE.search(x) for x in interps):
                out.append(content)
    return out


def extract_format_call_literals(line: str, lang: str) -> list[str]:
    """Case B: Sprintf(fmt, ...runId...) / format!(fmt, ...run_id...)."""
    out = []
    pattern = SPRINTF_RE if lang == "go" else RUST_FORMAT_RE if lang == "rust" else None
    if pattern is None:
        return out
    for m in pattern.finditer(line):
        literal, args = m.group(1), m.group(2)
        if RUNID_RE.search(args):
            out.append(literal)
    return out


def extract_concat_literals(line: str, lang: str) -> list[str]:
    """Case C: "a" + ctx.runId() + "b" (Java/C#) style concatenation."""
    if lang not in ("java", "csharp"):
        return []
    if not RUNID_RE.search(line):
        return []
    if '"""' in line:
        # A Java text-block delimiter line (opening or closing """) is not a
        # single-line string literal our quote-pairing regex understands;
        # skip rather than mis-parse it into a spurious empty literal.
        return []
    # Pull out every double-quoted literal on the line, in order. If a run id
    # reference also appears on this line, treat the concatenation of those
    # literals as one candidate name construction.
    literals = re.findall(r'"((?:[^"\\]|\\.)*)"', line)
    non_string = re.sub(r'"(?:[^"\\]|\\.)*"', "", line)
    if literals and RUNID_RE.search(non_string):
        return ["".join(literals)]
    return []


def scan_file(suite: str, path: Path, lang: str) -> list[Candidate]:
    candidates: list[Candidate] = []
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        return candidates
    for lineno, raw_line in enumerate(text.splitlines(), start=1):
        line = strip_line_comments(raw_line, lang)
        if not RUNID_RE.search(line):
            continue
        raws: list[str] = []
        raws.extend(extract_interpolated_literals(line, lang))
        raws.extend(extract_format_call_literals(line, lang))
        raws.extend(extract_concat_literals(line, lang))
        for raw in raws:
            static_text = normalize(strip_placeholders(raw, lang))
            candidates.append(
                Candidate(
                    suite=suite,
                    file=str(path),
                    line=lineno,
                    raw=raw,
                    static_text=static_text,
                    kind=resource_kind(line),
                )
            )
    return candidates


def load_allowlist(path: Path) -> set[tuple[str, str]]:
    if not path.exists():
        return set()
    data = json.loads(path.read_text(encoding="utf-8"))
    return {(e["suite"], e["pattern"]) for e in data.get("allow", [])}


def find_violations(
    root: Path, allowlist: set[tuple[str, str]], suites: list[dict] | None = None
) -> Violations:
    v = Violations()
    for suite_cfg in suites if suites is not None else SUITES:
        suite = suite_cfg["name"]
        directory = root / suite_cfg["dir"]
        if not directory.is_dir():
            continue
        by_static: dict[tuple[str, str], list[Candidate]] = {}
        for path in sorted(directory.glob(f"*{suite_cfg['ext']}")):
            if path.name in suite_cfg["exclude"] or "test" in path.name.lower():
                continue
            for cand in scan_file(suite, path, suite_cfg["lang"]):
                if not cand.static_text:
                    v.bare_runid.append(cand)
                    continue
                if cand.kind is None:
                    continue  # untagged: no evidence of a shared AWS namespace
                by_static.setdefault((cand.kind, cand.static_text), []).append(cand)

        for (kind, static_text), cands in by_static.items():
            files = {c.file for c in cands}
            if len(files) < 2:
                continue
            if (suite, static_text) in allowlist:
                continue
            # Report the pair of files involved (first occurrence each).
            seen_files: dict[str, Candidate] = {}
            for c in cands:
                seen_files.setdefault(c.file, c)
            ordered = list(seen_files.values())
            for a, b in zip(ordered, ordered[1:]):
                v.collisions.append((a, b))
    return v


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=REPO_ROOT)
    parser.add_argument("--allowlist", type=Path, default=DEFAULT_ALLOWLIST)
    args = parser.parse_args()

    allowlist = load_allowlist(args.allowlist)
    v = find_violations(args.root, allowlist)

    problems = len(v.collisions) + len(v.bare_runid)
    if problems == 0:
        print("compat name hygiene OK — no cross-file naming collisions, no bare-run-id names")
        return 0

    print(f"ERROR: {problems} compat name-hygiene problem(s):", file=sys.stderr)
    for cand in v.bare_runid:
        rel = Path(cand.file).resolve()
        try:
            rel = rel.relative_to(args.root.resolve())
        except ValueError:
            pass
        print(
            f"  bare run id, no group-specific text: {rel}:{cand.line}: `{cand.raw}`",
            file=sys.stderr,
        )
    for a, b in v.collisions:
        ra, rb = Path(a.file), Path(b.file)
        try:
            ra = ra.resolve().relative_to(args.root.resolve())
            rb = rb.resolve().relative_to(args.root.resolve())
        except ValueError:
            pass
        print(
            f"  cross-file name collision in {a.suite!r}: {ra}:{a.line} and {rb}:{b.line} "
            f"both build a name reducing to {a.static_text!r} — add a group-specific token, or "
            f"add {{\"suite\": {a.suite!r}, \"pattern\": {a.static_text!r}, \"reason\": ...}} to "
            f"compat/suites/name-hygiene-allowlist.json if this is intentional",
            file=sys.stderr,
        )
    return 1


if __name__ == "__main__":
    sys.exit(main())
