"""Unit tests for check_encoding.py — the mojibake/BOM/U+FFFD gate.

Run directly: python3 scripts/check_encoding_test.py
CI runs every scripts/*_test.py in the script-tests job.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from check_encoding import fix_text, scan_text  # noqa: E402

# Built from escapes so this file never trips its own gate: each constant is
# what a CP1252 misreading leaves behind for one damaged character.
EM_DASH_MOJIBAKE = "\u00e2\u20ac\u201d"  # UTF-8 of an em dash read as CP1252
E_ACUTE_MOJIBAKE = "\u00c3\u00a9"  # UTF-8 of e-acute read as CP1252
SIGMA_MOJIBAKE = "\u00ce\u00a3"  # UTF-8 of capital sigma read as CP1252
BOM = "\ufeff"
REPLACEMENT = "\ufffd"


class ScanTest(unittest.TestCase):
    def test_clean_text_passes(self):
        text = "plain ASCII, a real em dash —, café, →, ✅, box ──\n"
        self.assertEqual(scan_text(text), [])

    def test_flags_each_mojibake_family(self):
        for bad in (EM_DASH_MOJIBAKE, E_ACUTE_MOJIBAKE, SIGMA_MOJIBAKE):
            with self.subTest(bad=bad):
                findings = scan_text(f"line one\nbad {bad} here\n")
                self.assertEqual(len(findings), 1)
                self.assertEqual(findings[0][0], 2)
                self.assertIn("mojibake", findings[0][1])

    def test_flags_bom_and_replacement_char(self):
        findings = scan_text(f"{BOM}first\nlost {REPLACEMENT}\n")
        kinds = sorted(desc.split()[0] for _, desc in findings)
        self.assertEqual(kinds, ["U+FFFD", "UTF-8"])

    def test_allow_pragma_suppresses_the_line(self):
        text = f"docs about {E_ACUTE_MOJIBAKE} damage  # encoding-check:allow\n"
        self.assertEqual(scan_text(text), [])
        # ...but only that line.
        text += f"real damage {E_ACUTE_MOJIBAKE}\n"
        self.assertEqual(len(scan_text(text)), 1)


class FixTest(unittest.TestCase):
    def test_repairs_two_and_three_byte_damage(self):
        text = f"a {EM_DASH_MOJIBAKE} b {E_ACUTE_MOJIBAKE} c {SIGMA_MOJIBAKE}\n"
        self.assertEqual(fix_text(text), "a — b é c Σ\n")

    def test_adjacent_damage_and_legitimate_neighbours(self):
        # A real ellipsis (itself in the continuation set) beside damage must
        # survive; two damaged characters back-to-back must both heal.
        text = f"…{EM_DASH_MOJIBAKE}{EM_DASH_MOJIBAKE}…"
        self.assertEqual(fix_text(text), "…——…")

    def test_strips_bom_and_leaves_pragma_lines_alone(self):
        text = f"{BOM}keep {E_ACUTE_MOJIBAKE} as-is  # encoding-check:allow\n"
        self.assertEqual(
            fix_text(text), f"keep {E_ACUTE_MOJIBAKE} as-is  # encoding-check:allow\n"
        )

    def test_fixed_text_scans_clean(self):
        text = f"{BOM}x {EM_DASH_MOJIBAKE} y {E_ACUTE_MOJIBAKE}\n"
        self.assertEqual(scan_text(fix_text(text)), [])


if __name__ == "__main__":
    unittest.main()
