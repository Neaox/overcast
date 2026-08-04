#!/usr/bin/env python3
"""Every committed .ps1 file must be pure ASCII, or carry a UTF-8 BOM.

Windows PowerShell 5.1 (`powershell.exe`, the one still shipped in the box and
the one AGENTS.md's Windows instructions reach) decodes a BOM-less script file
as the *system ANSI codepage* — Windows-1252 on a UK/US machine — not as UTF-8.
PowerShell 7 (`pwsh`) assumes UTF-8, which is why nobody notices.

So a UTF-8 em dash in a BOM-less script is read as three CP1252 characters, and
the third one is the problem:

    U+2014 —  ->  E2 80 94  ->  'â' '€' '"'    (0x94 is CP1252 U+201D)
    U+2192 ->  ->  E2 86 92  ->  'â' '†' '''    (0x92 is CP1252 U+2019)

PowerShell's tokenizer accepts curly quotes as string delimiters, so both of
those *open or close a string literal*. Inside a comment that is harmless.
Inside a string literal it silently swallows the rest of the file: scripts/
pr-wait.ps1 reported "The Try statement is missing its Catch or Finally block"
75 lines above the em dash that actually broke it, and was unusable on the
supported Windows path until this was found.

The invariant is therefore checked on the bytes, not on where the character
happens to sit today — a comment's em dash is one copy-paste away from being a
message's em dash, and the failure is remote from the cause either way.

Run: python scripts/powershell_ascii_test.py   (CI runs every scripts/*_test.py)
"""

import subprocess
import sys
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent

BOM = b"\xef\xbb\xbf"

# CP1252 renderings of the characters this repo's prose actually reaches for,
# so the failure message can name the fix rather than just the offence.
SUGGESTIONS = {
    "—": "--",   # em dash
    "–": "-",    # en dash
    "→": "->",   # rightwards arrow
    "…": "...",  # ellipsis
    "§": "section",
    "‘": "'",
    "’": "'",
    "“": '"',
    "”": '"',
    " ": " ",    # no-break space
}


def powershell_files():
    out = subprocess.run(
        ["git", "ls-files", "-z", "*.ps1", "*.psm1", "*.psd1"],
        cwd=REPO, capture_output=True, text=True, check=True,
    ).stdout
    return sorted(p for p in out.split("\0") if p)


class PowerShellAsciiTest(unittest.TestCase):
    def test_repo_has_powershell_files(self):
        # Guards the guard: a glob that matches nothing passes every test below.
        self.assertTrue(powershell_files(), "git ls-files found no .ps1 files")

    def test_bomless_scripts_are_ascii(self):
        offences = []
        for rel in powershell_files():
            raw = (REPO / rel).read_bytes()
            if raw.startswith(BOM):
                continue  # decoded as UTF-8 by every PowerShell; nothing to check
            text = raw.decode("utf-8")
            for lineno, line in enumerate(text.splitlines(), 1):
                for ch in line:
                    if ord(ch) < 128:
                        continue
                    fix = SUGGESTIONS.get(ch)
                    hint = f" -> write {fix!r}" if fix else ""
                    offences.append(
                        f"{rel}:{lineno}: U+{ord(ch):04X} {ch!r}{hint}"
                    )
        self.assertEqual(
            offences, [],
            "non-ASCII in a BOM-less PowerShell script; Windows PowerShell 5.1 "
            "reads these as CP1252 mojibake and some of it opens a string "
            "literal:\n  " + "\n  ".join(offences),
        )


if __name__ == "__main__":
    unittest.main(verbosity=2 if "-v" in sys.argv else 1)
