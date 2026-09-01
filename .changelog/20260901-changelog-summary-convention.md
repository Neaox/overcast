+ [release] changelog entries must lead with a standalone summary sentence, capped at 160 chars.
  `scripts/changelog.py check` enforces the cap on new and edited fragments; detail beyond the summary goes on indented continuation lines, rendered as their own line under the bullet
  breaking entries also now sort first within their category when assembled, so a scanner hits **BREAKING** on the first bullet
