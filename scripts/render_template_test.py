#!/usr/bin/env python3

from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

import importlib.util

spec = importlib.util.spec_from_file_location(
	"render_template", Path(__file__).with_name("render-template.py")
)
assert spec and spec.loader
render_template = importlib.util.module_from_spec(spec)
spec.loader.exec_module(render_template)


class RenderTest(unittest.TestCase):
	def test_substitutes_both_placeholder_forms(self) -> None:
		out = render_template.render("$VERSION and ${SHORT_SHA}", {"VERSION": "1.0.0", "SHORT_SHA": "abc1234"})

		self.assertEqual("1.0.0 and abc1234", out)

	def test_leaves_unknown_placeholders_visible(self) -> None:
		# Blanking them would silently delete a sentence; leaving them makes a
		# typo obvious in the posted comment.
		out = render_template.render("hello $NOPE", {})

		self.assertEqual("hello $NOPE", out)

	def test_every_shipped_template_renders_with_the_documented_values(self) -> None:
		# Guards against a template gaining a placeholder the workflow does not
		# export, which would post a comment with a raw $NAME in it. Every name
		# here is set by the workflow named against the template in
		# .github/release-bot/README.md.
		known = {
			# release-prep.yml
			"VERSION": "0.0.1-alpha.28",
			"SHORT_SHA": "9ba7564c",
			# release-hold.yml
			"RELEASE_VERSION": "1.2.0",
			"RELEASE_LINK": "#463",
			"RELEASE_KIND": "minor",
			"NEXT_MAJOR": "2.0.0",
			"BREAKING_ENTRIES": "- **BREAKING** [state] the v1 on-disk layout",
			# retarget.yml. CREATED_NOTE is empty whenever the branch already
			# existed, which is the case this substitution has to survive.
			"TARGET_BRANCH": "v2.0.0",
			"PREVIOUS_BASE": "main",
			"CURRENT_BASE": "main",
			"CREATED_NOTE": "",
			"REQUESTED_BY": "Neaox",
			"REASON": "`v2..0` is not a branch name I will create or target.",
			# changelog-required.yml
			"WAIVED_BY": "Neaox",
			"WAIVER_URL": "https://github.com/Neaox/overcast/pull/463#issuecomment-1",
			"WAIVER_REASON": "CI-only: pins the release action to a digest",
			# changelog-waiver.yml
			"PROBLEM": "`/no-changelog` needs a reason after it.",
			# release-branch-base.yml. RELEASE_VERSION above is the same name,
			# derived there from BASE_BRANCH rather than read from VERSION.
			"BASE_BRANCH": "release/1.2.0",
			"DEFAULT_BRANCH": "main",
		}
		folder = Path(__file__).resolve().parent.parent / ".github" / "release-bot"
		templates = [p for p in folder.glob("*.md") if p.name != "README.md"]
		self.assertTrue(templates)
		for template in templates:
			with self.subTest(template=template.name):
				out = render_template.render(template.read_text(encoding="utf-8"), known)
				self.assertNotIn("$", out, f"{template.name} has an unfilled placeholder")


class CliTest(unittest.TestCase):
	def test_renders_a_file_from_the_environment(self) -> None:
		root = Path(tempfile.mkdtemp())
		(root / "t.md").write_text("v$VERSION\n", encoding="utf-8")

		result = subprocess.run(
			[sys.executable, str(Path(__file__).with_name("render-template.py")), str(root / "t.md")],
			capture_output=True,
			env={"VERSION": "9.9.9", "PATH": "", "SYSTEMROOT": ""},
		)

		self.assertEqual(0, result.returncode, result.stderr.decode("utf-8", "replace"))
		self.assertEqual("v9.9.9", result.stdout.decode("utf-8").strip())


if __name__ == "__main__":
	unittest.main()
