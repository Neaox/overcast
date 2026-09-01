#!/usr/bin/env python3
"""release-channel-tags.py — which floating Docker tags may this release move?

`ghcr.io/overcast-sh/overcast:<version>` is immutable: one version, one tag, written
once. Every *other* tag the release workflow publishes is a moving pointer —
`:latest`, `:alpha`, and the line tags `:1` and `:1.2` — and a moving pointer
can move the wrong way. Release 1.2.4 as a maintenance fix for an old line
after 1.3.0 has shipped and a rule of "publish :latest for any stable release"
walks `:latest` backwards, downgrading every user who pinned it. The same
already applies to `:alpha` today: re-publishing an earlier alpha would take
`:alpha` back with it.

So the rule is one sentence, and it is the whole of this file:

    a floating tag moves only when the version being released is at least the
    highest version already released under that tag.

"At least", not "greater than". Equality means the tag already points at this
version — a republish of the same release, which the workflow supports (see
"If The Release Workflow Fails" in RELEASE.md) — and refusing to write a tag
its own value is how a re-run silently loses `:latest`.

What each tag means, and therefore which releases it is compared against:

    :latest    every stable release
    :<channel> every prerelease with that channel identifier — :alpha is
               compared against alphas only. The channels are independent of
               each other and of :latest, because they answer different
               questions: shipping 1.4.0-alpha.1 must not hold back :latest
               for the 1.3.1 patch, and shipping 1.3.1 must not hold back
               :alpha.
    :1.2, :1   stable releases on that line only.

**Line tags are stable-only.** A user pinning `:1.2` is asking for the 1.2
line as it ships, not for whatever prerelease is being tried out on it; below
1.0 the alternative would be `:0` quietly meaning "the current alpha", which
is what `:alpha` already says out loud.

Nothing here decides the immutable `:<version>` tag — the workflow publishes
that unconditionally, where a reader can see it.
"""

from __future__ import annotations

import argparse
import re
import sys
from dataclasses import dataclass


# Semantic version, with the leading `v` of a git tag allowed. Build metadata
# is parsed and then discarded: SemVer says it takes no part in precedence, and
# two releases differing only in build metadata are the same release here.
SEMVER = re.compile(
    r"^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$"
)


@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    # Dot-separated prerelease identifiers; empty for a stable release.
    prerelease: tuple[str, ...] = ()

    @property
    def channel(self) -> str:
        """The floating channel tag this version publishes under.

        The first prerelease identifier, which is where the word lives:
        `0.0.1-alpha.31` is on `alpha`. A prerelease with no leading word —
        `1.0.0-1` — has no name to publish under, so it gets the generic one
        rather than a tag called `:1` that would collide with the major line.
        """
        if not self.prerelease:
            return "latest"
        first = self.prerelease[0]
        return first if not first.isdigit() else "prerelease"

    @property
    def line(self) -> str:
        return f"{self.major}.{self.minor}"

    def precedence(self) -> tuple:
        """A sort key implementing SemVer §11 precedence.

        The part worth spelling out is the prerelease comparison: identifiers
        are compared numerically when they are all digits and lexically
        otherwise, so `alpha.9` precedes `alpha.10`. String comparison would
        put them the other way round, and the alpha counter is already past 9.
        """
        # A stable release outranks every prerelease of the same core version.
        if not self.prerelease:
            return (self.major, self.minor, self.patch, 1, ())

        parts: list[tuple[int, int, str]] = []
        for identifier in self.prerelease:
            if identifier.isdigit():
                # Numeric identifiers always have lower precedence than
                # alphanumeric ones (SemVer §11.4.3).
                parts.append((0, int(identifier), ""))
            else:
                parts.append((1, 0, identifier))
        return (self.major, self.minor, self.patch, 0, tuple(parts))


def parse(text: str) -> Version | None:
    """A Version, or None when `text` is not a semantic version."""
    match = SEMVER.match(text.strip())
    if match is None:
        return None
    major, minor, patch, prerelease = match.groups()
    identifiers = tuple(prerelease.split(".")) if prerelease else ()
    if any(identifier == "" for identifier in identifiers):
        return None
    return Version(int(major), int(minor), int(patch), identifiers)


def parse_released(lines: list[str]) -> list[Version]:
    """Every parseable version in `lines`, unparseable ones warned about.

    The input is `git tag --list`, which is whatever the repository has
    accumulated. A tag that is not a version — `v0.1`, a moved-aside
    `v1.2.3-old` that does not parse — must not be able to stop a release
    tagging its images, so it is reported and dropped rather than raised.
    """
    released: list[Version] = []
    for line in lines:
        candidate = line.strip()
        if not candidate:
            continue
        version = parse(candidate)
        if version is None:
            print(f"ignoring unparseable tag '{candidate}'", file=sys.stderr)
            continue
        released.append(version)
    return released


def _may_move(version: Version, peers: list[Version]) -> bool:
    """Is `version` at least as new as every release the tag already covers?"""
    key = version.precedence()
    return all(key >= peer.precedence() for peer in peers)


def floating_tags(version: Version, released: list[Version]) -> list[str]:
    """The floating tags this release may move, in publication order.

    `released` is every version already released, including `version` itself
    when this is a republish — equality is deliberately not a regression, so
    it makes no difference either way.
    """
    tags: list[str] = []

    if version.prerelease:
        channel = version.channel
        peers = [
            peer
            for peer in released
            if peer.prerelease and peer.channel == channel
        ]
        if _may_move(version, peers):
            tags.append(channel)
        # No line tags: see the module docstring. A prerelease publishes under
        # its channel and nowhere else.
        return tags

    stable = [peer for peer in released if not peer.prerelease]
    if _may_move(version, stable):
        tags.append("latest")

    minor_line = [peer for peer in stable if peer.line == version.line]
    if _may_move(version, minor_line):
        tags.append(version.line)

    major_line = [peer for peer in stable if peer.major == version.major]
    if _may_move(version, major_line):
        tags.append(str(version.major))

    return tags


def render(tags: list[str], style: str) -> str:
    """The tag list as the consumer wants it.

    `metadata` is docker/metadata-action's `tags:` input, so the workflow can
    paste the answer straight in without re-deriving anything from it.
    """
    if style == "metadata":
        return "".join(f"type=raw,value={tag}\n" for tag in tags)
    return "".join(f"{tag}\n" for tag in tags)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version", help="the version being released")
    parser.add_argument(
        "--released",
        default="-",
        help="file of already-released versions, one per line ('-' for stdin)",
    )
    parser.add_argument(
        "--format",
        choices=["plain", "metadata"],
        default="plain",
        help="'plain' tag names, or docker/metadata-action 'type=raw' lines",
    )
    args = parser.parse_args()

    version = parse(args.version)
    if version is None:
        # Unlike an unparseable *tag*, an unparseable release version is fatal:
        # the alternative is publishing a release whose floating tags were
        # decided by a silently empty answer.
        print(f"::error::'{args.version}' is not a semantic version.", file=sys.stderr)
        return 1

    if args.released == "-":
        lines = sys.stdin.read().splitlines()
    else:
        with open(args.released, encoding="utf-8") as handle:
            lines = handle.read().splitlines()

    sys.stdout.write(render(floating_tags(version, parse_released(lines)), args.format))
    return 0


if __name__ == "__main__":
    sys.exit(main())
