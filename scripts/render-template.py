#!/usr/bin/env python3
"""Render a release-bot message template with values from the environment.

The bot's messages live as markdown under .github/release-bot/ rather than as
printf blocks inside the workflow: they are prose that humans read, and prose
is far easier to review as a file than as shell quoting.

Placeholders are string.Template's $NAME / ${NAME}. Unknown ones are left
as-is rather than blanked, so a typo shows up in the output instead of
silently deleting a sentence.
"""

from __future__ import annotations

import os
import string
import sys
from pathlib import Path


def render(text: str, values: dict[str, str]) -> str:
    return string.Template(text).safe_substitute(values)


def main() -> int:
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            reconfigure(encoding="utf-8")
    if len(sys.argv) != 2:
        print("usage: render-template.py <template.md>", file=sys.stderr)
        return 2
    sys.stdout.write(render(Path(sys.argv[1]).read_text(encoding="utf-8"), dict(os.environ)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
