#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("changelog-required.py")
SPEC = importlib.util.spec_from_file_location("changelog_required", SCRIPT)
assert SPEC is not None
gate = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
# Registered before exec: @dataclass resolves its own module through
# sys.modules, and a hyphenated filename cannot be imported any other way.
sys.modules["changelog_required"] = gate
SPEC.loader.exec_module(gate)


def comment(body: str, *, association: str = "OWNER", login: str = "someone", at: str = "2026-08-03T10:00:00Z", id: int = 1) -> dict:
	return {
		"id": id,
		"body": body,
		"author_association": association,
		"login": login,
		"created_at": at,
		"html_url": f"https://github.com/o/r/pull/1#issuecomment-{id}",
	}


class ParseCommandTest(unittest.TestCase):
	def test_waiver_with_a_reason(self) -> None:
		command = gate.parse_command("/no-changelog CI-only: pins an action digest")
		self.assertEqual(gate.COMMAND_WAIVE, command.kind)
		self.assertEqual("CI-only: pins an action digest", command.reason)
		self.assertEqual("", command.problem)

	def test_waiver_without_a_reason_is_refused(self) -> None:
		command = gate.parse_command("/no-changelog")
		self.assertEqual(gate.COMMAND_INVALID, command.kind)
		self.assertIn("needs a reason", command.problem)

	def test_a_token_reason_does_not_count(self) -> None:
		self.assertEqual(gate.COMMAND_INVALID, gate.parse_command("/no-changelog n/a").kind)

	def test_reason_continues_onto_later_lines(self) -> None:
		command = gate.parse_command("/no-changelog test-only:\nnew fixtures for the SQS suite")
		self.assertEqual(gate.COMMAND_WAIVE, command.kind)
		self.assertEqual("test-only: new fixtures for the SQS suite", command.reason)

	def test_revoke(self) -> None:
		self.assertEqual(gate.COMMAND_REVOKE, gate.parse_command("/needs-changelog").kind)
		self.assertEqual(gate.COMMAND_REVOKE, gate.parse_command("/needs-changelog it does after all").kind)

	def test_leading_blank_lines_are_skipped(self) -> None:
		self.assertEqual(gate.COMMAND_WAIVE, gate.parse_command("\r\n\n/no-changelog internal refactor only").kind)

	# The bot's own message names both commands mid-sentence. Reading anything
	# but the first line would make it a command, and the bot would answer
	# itself.
	def test_a_command_further_down_is_not_a_command(self) -> None:
		body = "### This PR adds no changelog fragment\n\nComment `/no-changelog <reason>` and I will clear it."
		self.assertEqual(gate.COMMAND_NONE, gate.parse_command(body).kind)

	def test_ordinary_comment(self) -> None:
		self.assertEqual(gate.COMMAND_NONE, gate.parse_command("looks good to me").kind)
		self.assertEqual(gate.COMMAND_NONE, gate.parse_command("").kind)

	# The reason is quoted back inside a comment the bot finds again by an HTML
	# marker. It must not be able to carry one.
	def test_html_comments_are_stripped_from_the_reason(self) -> None:
		command = gate.parse_command("/no-changelog tooling <!-- overcast-changelog-gate --> only")
		self.assertEqual(gate.COMMAND_WAIVE, command.kind)
		self.assertNotIn("<!--", command.reason)
		self.assertNotIn("-->", command.reason)

	def test_a_long_reason_is_truncated(self) -> None:
		command = gate.parse_command("/no-changelog " + "x" * 900)
		self.assertLessEqual(len(command.reason), gate.MAX_REASON)


class ExemptTest(unittest.TestCase):
	def test_areas_that_never_produce_a_release_note(self) -> None:
		for path in (
			"compat/suites/registry.json",
			"compat/AGENTS.md",
			"tests/integration/sqs/receive_test.go",
			"tests/helpers/server.go",
			"internal/services/sqs/handlers_test.go",
			"scripts/changelog_required_test.py",
			"web/src/components/layout/sidebar/sidebar.test.tsx",
			"web/src/lib/format.spec.ts",
			"cmd/compat/main.go",
			"docs/plans/compat-coverage-modelgen.md",
			"docs/dev/manual-testing.md",
			".agents/skills/pull-request/SKILL.md",
			".claude/settings.json",
			".vscode/tasks.json",
			".devcontainer/devcontainer.json",
			"AGENTS.md",
			"CLAUDE.md",
			"CONTRIBUTING.md",
			".github/copilot-instructions.md",
			".changelog/README.md",
			".golangci.yml",
			".gitignore",
			".gitattributes",
			"opencode.json",
			".mcp.json",
		):
			with self.subTest(path=path):
				self.assertTrue(gate.exempt(path))

	# Everything arguable stays out. A wrong entry here ships a change with no
	# release note and nobody asked, which is the failure the check exists for.
	def test_areas_that_can_reach_a_user(self) -> None:
		for path in (
			"internal/services/sqs/handlers.go",
			"cmd/overcast/main.go",
			"cmd/overcast/cmd_mcp.go",
			"web/src/routes/buckets.tsx",
			"docs/services/sqs.md",
			"docs/README.md",
			"README.md",
			"RELEASE.md",
			"STATUS.md",
			"CHANGELOG.md",
			"Dockerfile",
			"docker-compose.yml",
			# Decides what goes into the image, unlike its .gitignore sibling.
			".dockerignore",
			"Makefile",
			"Taskfile.yml",
			"VERSION",
			".github/workflows/release.yml",
			".github/actions/setup/action.yml",
			"scripts/release-hold.py",
			"go.mod",
			"models/aws/VERSION",
		):
			with self.subTest(path=path):
				self.assertFalse(gate.exempt(path))

	def test_a_single_file_in_scope_puts_the_whole_pr_in_scope(self) -> None:
		self.assertEqual(
			["internal/services/sqs/handlers.go"],
			gate.in_scope([
				"compat/suites/registry.json",
				"tests/integration/sqs/receive_test.go",
				"internal/services/sqs/handlers.go",
			]),
		)

	def test_nothing_changed_is_not_exempt(self) -> None:
		self.assertFalse(gate.exempt(""))
		self.assertEqual([], gate.in_scope([]))


CHANGELOG = """\
# Changelog

## [Unreleased]

## [0.0.1-alpha.29] - 2026-08-03

### Fixed

- **SQS** — `ReceiveMessage` returns an empty result after a long-poll timeout

## [0.0.1-alpha.28] - 2026-07-30

### Added

- **EFS** — NFS mount targets
"""

RELEASE_PR_PATHS = [
	"VERSION",
	"CHANGELOG.md",
	".changelog/20260803-sqs-long-poll.md",
	".changelog/20260803-efs-mount-targets.md",
]


class ReleasePrTest(unittest.TestCase):
	def is_release_pr(self, paths: list[str], *, candidate: bool = True, version: str = "0.0.1-alpha.29", changelog: str = CHANGELOG) -> bool:
		return gate.release_pr(paths, candidate=candidate, version=version, changelog=changelog)

	def test_the_release_pr(self) -> None:
		self.assertTrue(self.is_release_pr(RELEASE_PR_PATHS))

	def test_a_first_release_with_no_fragments_to_consume(self) -> None:
		self.assertTrue(self.is_release_pr(["VERSION", "CHANGELOG.md"]))

	def test_leading_dot_slash_is_the_same_path(self) -> None:
		self.assertTrue(self.is_release_pr([f"./{path}" for path in RELEASE_PR_PATHS]))

	# The one that matters: a release branch is an ordinary branch, and a late
	# fix pushed onto it ships. One file outside the release's own three and
	# the PR owes a fragment like any other.
	def test_one_code_file_on_the_branch_puts_it_back_in_scope(self) -> None:
		self.assertFalse(
			self.is_release_pr([*RELEASE_PR_PATHS, "internal/services/sqs/handlers.go"])
		)

	def test_even_an_otherwise_exempt_file_puts_it_back_in_scope(self) -> None:
		self.assertFalse(self.is_release_pr([*RELEASE_PR_PATHS, "compat/suites/registry.json"]))

	def test_a_version_bump_with_no_section_is_not_a_release(self) -> None:
		self.assertFalse(self.is_release_pr(RELEASE_PR_PATHS, version="0.0.1-alpha.30"))

	def test_an_empty_section_is_not_a_release(self) -> None:
		self.assertFalse(
			self.is_release_pr(
				RELEASE_PR_PATHS,
				changelog="# Changelog\n\n## [0.0.1-alpha.29] - 2026-08-03\n\n### Fixed\n\n## [0.0.1-alpha.28]\n\n- something\n",
			)
		)

	# A revert of VERSION to a version already tagged is not a release, however
	# much of the changelog it touches.
	def test_an_already_published_version_is_not_a_release(self) -> None:
		self.assertFalse(self.is_release_pr(RELEASE_PR_PATHS, candidate=False))

	def test_changelog_alone_is_not_a_release(self) -> None:
		self.assertFalse(self.is_release_pr(["CHANGELOG.md"]))

	def test_version_alone_is_not_a_release(self) -> None:
		self.assertFalse(self.is_release_pr(["VERSION"]))

	def test_an_unreadable_changelog_is_not_a_release(self) -> None:
		self.assertFalse(self.is_release_pr(RELEASE_PR_PATHS, changelog=""))

	def test_no_version_is_not_a_release(self) -> None:
		self.assertFalse(self.is_release_pr(RELEASE_PR_PATHS, version=""))


class HasReleaseSectionTest(unittest.TestCase):
	def test_finds_the_section(self) -> None:
		self.assertTrue(gate.has_release_section(CHANGELOG, "0.0.1-alpha.29"))
		self.assertTrue(gate.has_release_section(CHANGELOG, "0.0.1-alpha.28"))

	def test_unreleased_is_empty(self) -> None:
		self.assertFalse(gate.has_release_section(CHANGELOG, "Unreleased"))

	def test_a_missing_section(self) -> None:
		self.assertFalse(gate.has_release_section(CHANGELOG, "9.9.9"))

	# The version is interpolated into a regex; a dot must not match anything.
	def test_the_version_is_not_a_pattern(self) -> None:
		self.assertFalse(gate.has_release_section(CHANGELOG, "0.0.1-alpha.2x"))
		self.assertFalse(gate.has_release_section(CHANGELOG, "0,0,1-alpha,29"))

	def test_a_commented_out_section_is_empty(self) -> None:
		self.assertFalse(
			gate.has_release_section(
				"## [1.0.0]\n\n<!--\n- a bullet nobody kept\n-->\n\n## [0.9.0]\n\n- kept\n",
				"1.0.0",
			)
		)


class LatestWaiverTest(unittest.TestCase):
	def test_no_comments(self) -> None:
		self.assertIsNone(gate.latest_waiver([]))

	def test_a_waiver_stands(self) -> None:
		waiver = gate.latest_waiver([comment("/no-changelog docs typo, nothing shipped", login="chris")])
		assert waiver is not None
		self.assertEqual("chris", waiver.by)
		self.assertEqual("docs typo, nothing shipped", waiver.reason)

	def test_the_last_command_wins(self) -> None:
		self.assertIsNone(gate.latest_waiver([
			comment("/no-changelog internal refactor only", at="2026-08-03T10:00:00Z", id=1),
			comment("/needs-changelog", at="2026-08-03T11:00:00Z", id=2),
		]))

	def test_a_waiver_after_a_revoke_stands_again(self) -> None:
		waiver = gate.latest_waiver([
			comment("/no-changelog internal refactor only", at="2026-08-03T10:00:00Z", id=1),
			comment("/needs-changelog", at="2026-08-03T11:00:00Z", id=2),
			comment("/no-changelog split the user-visible part into #500", at="2026-08-03T12:00:00Z", id=3),
		])
		assert waiver is not None
		self.assertEqual("split the user-visible part into #500", waiver.reason)

	# Comments arrive from the API in creation order, but nothing in the
	# workflow guarantees it survives the round trip.
	def test_order_comes_from_created_at_not_position(self) -> None:
		self.assertIsNone(gate.latest_waiver([
			comment("/needs-changelog", at="2026-08-03T11:00:00Z", id=2),
			comment("/no-changelog internal refactor only", at="2026-08-03T10:00:00Z", id=1),
		]))

	def test_only_write_access_can_waive(self) -> None:
		for association in ("NONE", "CONTRIBUTOR", "FIRST_TIME_CONTRIBUTOR"):
			with self.subTest(association=association):
				self.assertIsNone(gate.latest_waiver([
					comment("/no-changelog internal refactor only", association=association)
				]))

	def test_only_write_access_can_revoke(self) -> None:
		waiver = gate.latest_waiver([
			comment("/no-changelog internal refactor only", at="2026-08-03T10:00:00Z", id=1),
			comment("/needs-changelog", association="NONE", at="2026-08-03T11:00:00Z", id=2),
		])
		self.assertIsNotNone(waiver)

	# A malformed command is answered on the PR where it was posted. Treating
	# it as a revoke would let a typo cancel a waiver silently.
	def test_a_malformed_command_changes_nothing(self) -> None:
		waiver = gate.latest_waiver([
			comment("/no-changelog internal refactor only", at="2026-08-03T10:00:00Z", id=1),
			comment("/no-changelog", at="2026-08-03T11:00:00Z", id=2),
		])
		assert waiver is not None
		self.assertEqual("internal refactor only", waiver.reason)

	def test_the_api_user_shape_is_understood_too(self) -> None:
		waiver = gate.latest_waiver([{
			"id": 1,
			"body": "/no-changelog build tooling only",
			"author_association": "OWNER",
			"user": {"login": "chris"},
			"created_at": "2026-08-03T10:00:00Z",
		}])
		assert waiver is not None
		self.assertEqual("chris", waiver.by)


class CheckCommandTest(unittest.TestCase):
	def run_check(self, *args: str) -> str:
		result = subprocess.run(
			[sys.executable, str(SCRIPT), "check", *args],
			capture_output=True, text=True, check=True,
		)
		return result.stdout.strip()

	def write_comments(self, comments: list[dict]) -> str:
		path = Path(tempfile.mkdtemp()) / "comments.json"
		path.write_text(json.dumps(comments), encoding="utf-8")
		return str(path)

	def write_changed(self, paths: list[str]) -> str:
		path = Path(tempfile.mkdtemp()) / "changed.txt"
		path.write_text("\n".join(paths) + "\n", encoding="utf-8")
		return str(path)

	def test_a_fragment_settles_it(self) -> None:
		self.assertEqual(gate.VERDICT_FRAGMENT, self.run_check(".changelog/20260803-a-fix.md"))

	# The workflow expands an empty bash array to one empty string.
	def test_an_empty_argument_is_not_a_fragment(self) -> None:
		self.assertEqual(gate.VERDICT_MISSING, self.run_check(""))

	def test_no_fragment_and_no_waiver(self) -> None:
		self.assertEqual(gate.VERDICT_MISSING, self.run_check())

	# CHANGELOG.md belongs to the release PR. While one is open, release-prep
	# merges main into its branch on every push, and a second hand in the same
	# section aborts that merge and stops the PR refreshing itself. So editing
	# it is never the answer here — not in a release window, not outside one.
	def test_a_changelog_edit_is_never_the_answer(self) -> None:
		changed = self.write_changed(["CHANGELOG.md", "internal/services/sqs/handlers.go"])
		self.assertEqual(gate.VERDICT_MISSING, self.run_check("--changed-files-file", changed))

	def test_a_changelog_edit_alone_is_not_an_exempt_area(self) -> None:
		self.assertFalse(gate.exempt("CHANGELOG.md"))

	def test_a_pr_confined_to_exempt_areas_is_not_asked(self) -> None:
		changed = self.write_changed([
			"compat/suites/registry.json",
			"tests/integration/sqs/receive_test.go",
			"internal/services/sqs/handlers_test.go",
		])
		self.assertEqual(gate.VERDICT_NOT_APPLICABLE, self.run_check("--changed-files-file", changed))

	def test_one_file_outside_them_is_asked(self) -> None:
		changed = self.write_changed([
			"compat/suites/registry.json",
			"internal/services/sqs/handlers.go",
		])
		self.assertEqual(gate.VERDICT_MISSING, self.run_check("--changed-files-file", changed))

	def test_an_unreadable_changed_files_list_asks_again(self) -> None:
		self.assertEqual(gate.VERDICT_MISSING, self.run_check("--changed-files-file", "nope.txt"))

	def test_a_waiver_clears_it(self) -> None:
		comments = self.write_comments([comment("/no-changelog compat suites only, no runtime change")])
		waiver_file = str(Path(tempfile.mkdtemp()) / "waiver.json")
		self.assertEqual(
			gate.VERDICT_WAIVED,
			self.run_check("--comments-file", comments, "--waiver-file", waiver_file),
		)
		written = json.loads(Path(waiver_file).read_text(encoding="utf-8"))
		self.assertEqual("compat suites only, no runtime change", written["reason"])
		self.assertEqual("someone", written["by"])

	def test_an_unreadable_comments_file_asks_again(self) -> None:
		self.assertEqual(gate.VERDICT_MISSING, self.run_check("--comments-file", "does-not-exist.json"))

	def test_a_renovate_pr_touching_only_manifests_is_not_asked(self) -> None:
		changed = self.write_changed(["go.mod", "go.sum", "Dockerfile"])
		self.assertEqual(
			gate.VERDICT_DEPENDENCIES,
			self.run_check("--changed-files-file", changed, "--author", "renovate[bot]"),
		)

	# Anyone with write access can push onto a bot's branch, so the author
	# alone must never clear the question.
	def test_a_renovate_pr_carrying_a_code_file_is_asked(self) -> None:
		changed = self.write_changed(["go.mod", "internal/services/sqs/handlers.go"])
		self.assertEqual(
			gate.VERDICT_MISSING,
			self.run_check("--changed-files-file", changed, "--author", "renovate[bot]"),
		)

	def test_a_human_touching_only_manifests_is_asked(self) -> None:
		changed = self.write_changed(["go.mod", "go.sum"])
		self.assertEqual(
			gate.VERDICT_MISSING,
			self.run_check("--changed-files-file", changed, "--author", "someone"),
		)

	# The reviewer of a major bump can still answer with a fragment; it wins
	# before the author is ever consulted.
	def test_a_fragment_on_a_renovate_pr_still_wins(self) -> None:
		changed = self.write_changed(["Dockerfile"])
		self.assertEqual(
			gate.VERDICT_FRAGMENT,
			self.run_check(
				".changelog/20260822-node-24.md",
				"--changed-files-file", changed,
				"--author", "renovate[bot]",
			),
		)

	def test_an_unreadable_changed_list_is_not_a_dependency_pr(self) -> None:
		self.assertEqual(
			gate.VERDICT_MISSING,
			self.run_check("--changed-files-file", "nope.txt", "--author", "renovate[bot]"),
		)

	def write_changelog(self, text: str = CHANGELOG) -> str:
		path = Path(tempfile.mkdtemp()) / "CHANGELOG.md"
		path.write_text(text, encoding="utf-8")
		return str(path)

	def release_pr_args(self, paths: list[str] = RELEASE_PR_PATHS, **overrides: str) -> list[str]:
		args = {
			"--changed-files-file": self.write_changed(paths),
			"--release-candidate": "true",
			"--version": "0.0.1-alpha.29",
			"--changelog": self.write_changelog(),
		}
		args.update(overrides)
		return [item for pair in args.items() for item in pair]

	# The release PR is a release candidate by definition — its own VERSION is
	# the untagged one — so before this exemption existed it fell through to
	# `missing` and was told the release had already merged. It had not; it was
	# the release.
	def test_the_release_pr_is_not_asked(self) -> None:
		self.assertEqual(gate.VERDICT_RELEASE_PR, self.run_check(*self.release_pr_args()))

	def test_a_late_fix_pushed_onto_the_release_branch_is_asked(self) -> None:
		self.assertEqual(
			gate.VERDICT_MISSING,
			self.run_check(*self.release_pr_args([*RELEASE_PR_PATHS, "internal/services/sqs/handlers.go"])),
		)

	# Row 2: main carries the untagged version and no release PR is left. An
	# ordinary PR there is a candidate too, and it *is* asked — the exemption
	# is the release PR's shape, not the window.
	def test_an_ordinary_pr_in_the_release_window_is_still_asked(self) -> None:
		self.assertEqual(
			gate.VERDICT_MISSING,
			self.run_check(*self.release_pr_args(["internal/services/sqs/handlers.go"])),
		)

	def test_an_unreadable_changelog_asks_again(self) -> None:
		self.assertEqual(
			gate.VERDICT_MISSING,
			self.run_check(*self.release_pr_args(**{"--changelog": "does-not-exist.md"})),
		)

	# The release PR still adds nothing under .changelog/, so the exemption has
	# to come before the scope test rather than after it: VERSION and
	# CHANGELOG.md are both deliberately in scope.
	def test_the_exemption_is_reached_before_the_scope_test(self) -> None:
		self.assertNotEqual([], gate.in_scope(RELEASE_PR_PATHS))


class DependencyBotTest(unittest.TestCase):
	def test_manifests(self) -> None:
		for path in (
			"go.mod",
			"go.sum",
			"Dockerfile",
			".node-version",
			"Makefile",
			"web/package.json",
			"web/pnpm-lock.yaml",
			"web/pnpm-workspace.yaml",
			".github/renovate.json5",
			".github/workflows/test.yml",
			".github/actions/docker-hub-mirror/action.yml",
		):
			self.assertTrue(gate.dependency_manifest(path), path)

	def test_everything_else_is_not_a_manifest(self) -> None:
		for path in (
			"internal/services/sqs/handlers.go",
			"scripts/changelog-required.py",
			"compat/suites/cdk/package.json",
			"web/src/main.tsx",
			".github/dependabot.yml",
		):
			self.assertFalse(gate.dependency_manifest(path), path)

	def test_the_author_must_be_the_bot(self) -> None:
		self.assertTrue(gate.dependency_bot_pr("renovate[bot]", ["go.mod"]))
		self.assertFalse(gate.dependency_bot_pr("renovate", ["go.mod"]))
		self.assertFalse(gate.dependency_bot_pr("", ["go.mod"]))

	def test_an_empty_change_list_degrades_towards_asking(self) -> None:
		self.assertFalse(gate.dependency_bot_pr("renovate[bot]", []))
		self.assertFalse(gate.dependency_bot_pr("renovate[bot]", ["", "  "]))


class ParseSubcommandTest(unittest.TestCase):
	def run_parse(self, body: str, *args: str) -> str:
		result = subprocess.run(
			[sys.executable, str(SCRIPT), "parse", *args],
			input=body, capture_output=True, text=True, check=True,
		)
		return result.stdout.strip()

	def test_classifies_from_stdin(self) -> None:
		self.assertEqual("waive", self.run_parse("/no-changelog release tooling only"))
		self.assertEqual("revoke", self.run_parse("/needs-changelog"))
		self.assertEqual("none", self.run_parse("ship it"))

	def test_writes_the_refusal_sentence(self) -> None:
		problem_file = Path(tempfile.mkdtemp()) / "problem.txt"
		self.assertEqual("invalid", self.run_parse("/no-changelog", "--problem-file", str(problem_file)))
		self.assertIn("needs a reason", problem_file.read_text(encoding="utf-8"))

	def test_leaves_the_refusal_file_empty_when_there_is_nothing_wrong(self) -> None:
		problem_file = Path(tempfile.mkdtemp()) / "problem.txt"
		self.run_parse("/no-changelog docs-only, nothing shipped", "--problem-file", str(problem_file))
		self.assertEqual("", problem_file.read_text(encoding="utf-8"))


if __name__ == "__main__":
	unittest.main()
