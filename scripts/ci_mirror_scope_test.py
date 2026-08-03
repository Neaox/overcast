#!/usr/bin/env python3
"""Keep the Docker Hub mirror out of any job that produces a shipped artifact.

A registry mirror is a fine thing to put in front of a *test* job and a bad
thing to put in front of a *publishing* one. `mirror.gcr.io` is a cache: it
serves images that were deleted upstream for up to several days, and it
resolves a tag to whatever it cached rather than to what Docker Hub would
answer now. For a job that runs tests, that is a fidelity question. For a job
whose output is pushed to a registry and pulled by users, the base layers of a
published image would be coming from a cache instead of the canonical source,
which is a supply-chain question and a different kind of answer.

The split therefore exists on purpose, and this test is what makes it
deliberate rather than a coincidence of which jobs happened to need it first.
Without it the obvious tidy-up — "the docker build job pulls from Hub too, add
the mirror there for consistency" — silently changes what feeds a release.

Dependency-free by necessity: the `script-tests` CI job installs nothing, so
this cannot rely on PyYAML. The workflow shape it needs is small and regular
(a job header at two spaces, its body indented further), so it is parsed
directly and the parser is tested below rather than trusted.
"""

from __future__ import annotations

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
WORKFLOWS = REPO / ".github" / "workflows"

MIRROR_ACTION = "./.github/actions/docker-hub-mirror"

# A job that can publish. `packages: write` is the permission that lets a job
# push to GHCR; the action and command forms catch a push that borrows a token
# from elsewhere.
PUBLISH_MARKERS = (
    "packages: write",
    "docker/build-push-action",
    "docker push",
    "push: true",
)

JOB_HEADER = re.compile(r"^  ([A-Za-z0-9_-]+):\s*$")


def job_blocks(text: str) -> dict[str, str]:
    """Split a workflow into {job name: job body}.

    Only the `jobs:` mapping has entries at exactly two spaces followed by a
    bare name, which is what makes this reliable without a YAML parser.
    """
    blocks: dict[str, str] = {}
    current: str | None = None
    lines: list[str] = []
    in_jobs = False

    for line in text.splitlines():
        if line.startswith("jobs:"):
            in_jobs = True
            continue
        if not in_jobs:
            continue
        # A non-indented, non-blank line ends the jobs mapping.
        if line and not line.startswith(" "):
            break
        header = JOB_HEADER.match(line)
        if header:
            if current is not None:
                blocks[current] = "\n".join(lines)
            current = header.group(1)
            lines = []
            continue
        if current is not None:
            lines.append(line)

    if current is not None:
        blocks[current] = "\n".join(lines)
    return blocks


def mirrored_jobs() -> list[tuple[str, str, str]]:
    """Every (workflow, job, body) that uses the mirror action."""
    found = []
    for path in sorted(WORKFLOWS.glob("*.yml")):
        text = path.read_text(encoding="utf-8")
        if MIRROR_ACTION not in text:
            continue
        for job, body in job_blocks(text).items():
            if MIRROR_ACTION in body:
                found.append((path.name, job, body))
    return found


class TestJobBlocks(unittest.TestCase):
    """The parser earns its keep before the policy leans on it."""

    def test_splits_jobs_and_keeps_bodies(self):
        blocks = job_blocks(
            "name: CI\n"
            "on:\n"
            "  push:\n"
            "jobs:\n"
            "  build:\n"
            "    runs-on: ubuntu-latest\n"
            "    steps:\n"
            "      - run: make\n"
            "  publish:\n"
            "    permissions:\n"
            "      packages: write\n"
        )
        self.assertEqual(sorted(blocks), ["build", "publish"])
        self.assertIn("- run: make", blocks["build"])
        self.assertIn("packages: write", blocks["publish"])
        self.assertNotIn("packages: write", blocks["build"])

    def test_top_level_keys_before_jobs_are_not_jobs(self):
        # `push:` under `on:` sits at two spaces too, and must not be mistaken
        # for a job — the reason the parser ignores everything before `jobs:`.
        blocks = job_blocks("on:\n  push:\n  pull_request:\njobs:\n  test:\n    runs-on: x\n")
        self.assertEqual(list(blocks), ["test"])

    def test_stops_at_the_end_of_the_jobs_mapping(self):
        blocks = job_blocks("jobs:\n  a:\n    runs-on: x\nfooter: value\n")
        self.assertEqual(list(blocks), ["a"])
        self.assertNotIn("footer", blocks["a"])


class TestMirrorScope(unittest.TestCase):
    def test_mirror_is_actually_used(self):
        # A policy over an empty set passes vacuously and proves nothing; if
        # the action is gone, this test should be deleted with it.
        self.assertTrue(
            mirrored_jobs(),
            f"no workflow job uses {MIRROR_ACTION}; delete this test or restore the action",
        )

    def test_no_publishing_job_pulls_through_the_mirror(self):
        for workflow, job, body in mirrored_jobs():
            hits = [m for m in PUBLISH_MARKERS if m in body]
            # assertTrue rather than assertNotIn: the latter renders the whole
            # job body into the failure, burying the one line that matters.
            self.assertFalse(
                hits,
                f"{workflow}: job '{job}' uses the Docker Hub mirror and also looks like it "
                f"publishes ({', '.join(repr(h) for h in hits)}). What ships must come from the "
                f"canonical registry, not from a cache that can serve images deleted upstream. "
                f"Split the job, or drop the mirror from it.",
            )

    def test_release_workflow_never_mirrors(self):
        release = WORKFLOWS / "release.yml"
        if release.exists():
            self.assertNotIn(
                MIRROR_ACTION,
                release.read_text(encoding="utf-8"),
                "release.yml must pull from the canonical registry, never through a mirror",
            )


if __name__ == "__main__":
    unittest.main()
