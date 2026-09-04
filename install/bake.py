#!/usr/bin/env python3
"""bake.py — write install.sh / install.ps1 copies with a release tag baked in.

In the repository both installers carry an empty baked version and resolve
"latest" through the GitHub API. A copy attached to a release, or served by
the website for a release, carries that release's tag instead, so the default
install path makes no API call at all. That matters: the unauthenticated API
allows sixty requests an hour per address, which one office or CI fleet
behind a single NAT exhausts.

The marker is one line per script, and this is the only place that knows its
shape — the tests assert each script has exactly one:

    install.sh   OVERCAST_INSTALL_BAKED_VERSION=""
    install.ps1  $BakedVersion = ""

Usage:
    python3 install/bake.py --version v0.0.1-alpha.40 --out dist install/install.sh install/install.ps1
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

MARKERS = {
    ".sh": (re.compile(r'^OVERCAST_INSTALL_BAKED_VERSION=""$', re.M), 'OVERCAST_INSTALL_BAKED_VERSION="{tag}"'),
    ".ps1": (re.compile(r'^\$BakedVersion = ""$', re.M), '$BakedVersion = "{tag}"'),
}

TAG_RE = re.compile(r"^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$")


def normalise_tag(version: str) -> str:
    version = version.strip()
    if not TAG_RE.match(version):
        raise ValueError(f"not a release version: {version!r}")
    return version if version.startswith("v") else f"v{version}"


def bake(text: str, suffix: str, tag: str) -> str:
    """Return `text` with its baked-version marker set to `tag`.

    Raises ValueError when the marker is missing or appears more than once —
    either means the script and this tool have drifted apart, and a silent
    no-op here would ship an installer that quietly falls back to the API.
    """
    try:
        pattern, replacement = MARKERS[suffix]
    except KeyError:
        raise ValueError(f"no baked-version marker is defined for {suffix!r} files") from None
    if '"' in tag:
        raise ValueError(f"tag may not contain a double quote: {tag!r}")
    count = len(pattern.findall(text))
    if count != 1:
        raise ValueError(f"expected exactly one baked-version marker, found {count}")
    return pattern.sub(replacement.format(tag=tag), text, count=1)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--version", required=True, help="release tag, with or without the leading v")
    parser.add_argument("--out", required=True, type=Path, help="directory to write the baked copies into")
    parser.add_argument("scripts", nargs="+", type=Path, help="install.sh and/or install.ps1")
    args = parser.parse_args(argv)

    try:
        tag = normalise_tag(args.version)
    except ValueError as err:
        print(f"bake.py: {err}", file=sys.stderr)
        return 2

    args.out.mkdir(parents=True, exist_ok=True)
    for script in args.scripts:
        # Bytes in, bytes out: install.sh is LF and install.ps1 is CRLF by
        # .gitattributes, and neither ending may change on the way through.
        raw = script.read_bytes()
        newline = "\r\n" if b"\r\n" in raw else "\n"
        text = raw.decode("utf-8").replace("\r\n", "\n")
        try:
            baked = bake(text, script.suffix, tag)
        except ValueError as err:
            print(f"bake.py: {script}: {err}", file=sys.stderr)
            return 1
        target = args.out / script.name
        target.write_bytes(baked.replace("\n", newline).encode("utf-8"))
        print(f"baked {tag} into {target}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
