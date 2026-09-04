#!/usr/bin/env python3
"""Tests for install.sh, install.ps1 and bake.py.

A local HTTP server plays GitHub: a release list at /releases and assets under
/download/<tag>/. The real scripts run against it through
OVERCAST_INSTALL_BASE_URL and OVERCAST_INSTALL_RELEASES_API, with the platform
pinned through OVERCAST_INSTALL_OS / OVERCAST_INSTALL_ARCH so the same asset
names are exercised on every host. install.sh runs under every shell found on
PATH; install.ps1 under pwsh and Windows PowerShell when present. Nothing here
touches the real PATH, rc files or home directory.

    python3 install/install_test.py
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import threading
import unittest
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import bake  # noqa: E402

SH = HERE / "install.sh"
PS1 = HERE / "install.ps1"

NEWEST = "v0.0.1-alpha.40"
OLDER = "v0.0.1-alpha.39"
BAD_SUMS = "v0.0.1-alpha.38"
PLATFORMS = ["linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64.exe"]


def fake_binary(name: str, platform: str, tag: str) -> bytes:
    """What a release asset contains in this fake.

    Unix assets are shell scripts that answer `--version` the way the real
    binary does, so the "already installed" path is exercised. Windows assets
    are opaque bytes: they cannot run, and the installer must treat that as an
    unknown version rather than fail.
    """
    if platform.endswith(".exe"):
        return f"MZ fake {name} {platform} {tag}\n".encode()
    return f'#!/bin/sh\necho "{name} version {tag[1:]}"\n'.encode()


class FakeReleases:
    """A directory laid out like GitHub Releases, served over HTTP."""

    def __init__(self) -> None:
        self.root = Path(tempfile.mkdtemp(prefix="overcast-fake-releases-"))
        self.assets: dict[tuple[str, str], bytes] = {}
        for tag in (NEWEST, OLDER, BAD_SUMS):
            self._write_release(tag, corrupt=(tag == BAD_SUMS))
        releases = [{"tag_name": tag, "prerelease": True} for tag in (NEWEST, OLDER, BAD_SUMS)]
        (self.root / "releases").write_text(json.dumps(releases, indent=2), encoding="utf-8")
        handler = partial(SimpleHTTPRequestHandler, directory=str(self.root))
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        self.server.daemon_threads = True
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def _write_release(self, tag: str, corrupt: bool) -> None:
        release_dir = self.root / "download" / tag
        release_dir.mkdir(parents=True)
        lines = []
        for name in ("overcast", "overcastd"):
            for platform in PLATFORMS:
                asset = f"{name}-{platform}"
                content = fake_binary(name, platform, tag)
                (release_dir / asset).write_bytes(content)
                self.assets[(tag, asset)] = content
                digest = hashlib.sha256(content).hexdigest()
                if corrupt:
                    digest = "0" * 64
                lines.append(f"{digest}  {asset}")
        # The real SHA256SUMS lists provenance files too; the installer has to
        # match its asset by exact name and ignore the rest.
        lines.append(f"{'f' * 64}  overcast-sh.overcast.ABCDEF.dockerbuild")
        (release_dir / "SHA256SUMS").write_text("\n".join(lines) + "\n", encoding="utf-8")

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self.server.server_port}/download"

    @property
    def api_url(self) -> str:
        return f"http://127.0.0.1:{self.server.server_port}/releases"

    @property
    def missing_url(self) -> str:
        return f"http://127.0.0.1:{self.server.server_port}/nowhere"

    def close(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        shutil.rmtree(self.root, ignore_errors=True)


RELEASES: FakeReleases | None = None


def setUpModule() -> None:  # noqa: N802 - unittest hook
    global RELEASES
    RELEASES = FakeReleases()


def tearDownModule() -> None:  # noqa: N802 - unittest hook
    if RELEASES:
        RELEASES.close()


def posix(path: Path) -> str:
    """A path a Unix shell on this host accepts (Git Bash takes C:/x/y)."""
    return path.as_posix()


# --------------------------------------------------------------------------
# install.sh
# --------------------------------------------------------------------------


def available_shells() -> list[list[str]]:
    found: list[list[str]] = []
    for candidate in (["sh"], ["dash"], ["bash"], ["zsh"], ["busybox", "sh"]):
        # The resolved path, not the bare name: on Windows CreateProcess looks
        # in System32 before PATH, and System32\bash.exe is the WSL launcher.
        exe = shutil.which(candidate[0])
        if not exe:
            continue
        argv = [exe, *candidate[1:]]
        # A shell that cannot see this checkout (WSL reads F:\ as /mnt/f)
        # cannot run the script either.
        probe = subprocess.run([*argv, "-c", f'test -f "{posix(SH)}"'], capture_output=True)
        if probe.returncode == 0:
            found.append(argv)
    return found


class ShInstallerTests(unittest.TestCase):
    """Runs install.sh under one shell. Subclasses set `shell`."""

    shell: list[str] = []

    @classmethod
    def setUpClass(cls) -> None:
        if not cls.shell:
            raise unittest.SkipTest("abstract")

    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="overcast-install-test-"))
        self.home = self.tmp / "home"
        self.home.mkdir()
        self.bin = self.tmp / "bin"

    def tearDown(self) -> None:
        shutil.rmtree(self.tmp, ignore_errors=True)

    def env(self, **extra: str) -> dict[str, str]:
        assert RELEASES
        env = {
            k: v
            for k, v in os.environ.items()
            if not k.startswith("OVERCAST_INSTALL_")
        }
        env.update(
            {
                "HOME": posix(self.home),
                "SHELL": "/bin/bash",
                "OVERCAST_INSTALL_BASE_URL": RELEASES.base_url,
                "OVERCAST_INSTALL_RELEASES_API": RELEASES.api_url,
                "OVERCAST_INSTALL_OS": "linux",
                "OVERCAST_INSTALL_ARCH": "amd64",
                "OVERCAST_INSTALL_RETRIES": "2",
            }
        )
        env.update(extra)
        return env

    def run_sh(self, *args: str, script: Path = SH, env: dict[str, str] | None = None, piped: bool = False) -> subprocess.CompletedProcess[str]:
        env = env or self.env()
        argv = [*args]
        if "--dir" not in argv and not any(a.startswith("--dir=") for a in argv):
            argv = ["--dir", posix(self.bin), *argv]
        if piped:
            # `curl ... | sh -s -- <args>`: the script arrives on stdin. Bytes,
            # not text: text mode would rewrite the LF file as CRLF on Windows
            # and dash rejects `set -eu\r` — a fair stand-in for what a CRLF
            # checkout would do to a real user, and why .gitattributes pins LF.
            raw = subprocess.run(
                [*self.shell, "-s", "--", *argv],
                input=script.read_bytes(),
                capture_output=True,
                env=env,
                timeout=120,
            )
            return subprocess.CompletedProcess(raw.args, raw.returncode, raw.stdout.decode("utf-8", "replace"), raw.stderr.decode("utf-8", "replace"))
        return subprocess.run(
            [*self.shell, posix(script), *argv],
            capture_output=True,
            text=True,
            env=env,
            timeout=120,
        )

    def assert_installed(self, name: str, tag: str, platform: str = "linux-amd64") -> None:
        assert RELEASES
        target = self.bin / name
        self.assertTrue(target.is_file(), f"{target} missing")
        self.assertEqual(target.read_bytes(), RELEASES.assets[(tag, f"{name}-{platform}")])
        if os.name != "nt":
            self.assertTrue(target.stat().st_mode & stat.S_IXUSR, f"{target} is not executable")

    def test_default_installs_newest_full_binary(self) -> None:
        result = self.run_sh()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_installed("overcast", NEWEST)
        self.assertFalse((self.bin / "overcastd").exists())
        self.assertIn(f"installed overcast {NEWEST}", result.stdout)
        self.assertIn("is not on your PATH", result.stdout)
        self.assertIn(f'export PATH="{posix(self.bin)}:$PATH"', result.stdout)
        self.assertIn("overcast serve", result.stdout)
        # No partial download left behind.
        self.assertEqual(sorted(p.name for p in self.bin.iterdir()), ["overcast"])

    def test_piped_invocation_takes_flags_after_dash_s(self) -> None:
        result = self.run_sh("--slim", piped=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_installed("overcastd", NEWEST)
        self.assertFalse((self.bin / "overcast").exists())
        self.assertIn("overcastd serve", result.stdout)

    def test_version_flag_accepts_tag_with_or_without_v(self) -> None:
        result = self.run_sh("--version", OLDER[1:])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_installed("overcast", OLDER)

        again = self.run_sh("--version", OLDER)
        self.assertEqual(again.returncode, 0, again.stderr)
        self.assertIn(f"overcast {OLDER} is already installed", again.stdout)

    def test_reinstalling_a_newer_release_reports_an_upgrade(self) -> None:
        self.run_sh("--version", OLDER)
        result = self.run_sh()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"upgraded overcast v{OLDER[1:]} -> {NEWEST}", result.stdout)
        self.assert_installed("overcast", NEWEST)

    def test_version_from_environment(self) -> None:
        result = self.run_sh(env=self.env(OVERCAST_INSTALL_VERSION=OLDER))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_installed("overcast", OLDER)

    def test_both_installs_both_binaries(self) -> None:
        result = self.run_sh("--both")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_installed("overcast", NEWEST)
        self.assert_installed("overcastd", NEWEST)

    def test_flavor_from_environment(self) -> None:
        result = self.run_sh(env=self.env(OVERCAST_INSTALL_FLAVOR="slim"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_installed("overcastd", NEWEST)
        bad = self.run_sh(env=self.env(OVERCAST_INSTALL_FLAVOR="tiny"))
        self.assertNotEqual(bad.returncode, 0)
        self.assertIn("must be full, slim or both", bad.stderr)

    def test_platform_override_picks_that_asset(self) -> None:
        result = self.run_sh(env=self.env(OVERCAST_INSTALL_OS="darwin", OVERCAST_INSTALL_ARCH="arm64"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_installed("overcast", NEWEST, platform="darwin-arm64")

    def test_dry_run_writes_nothing(self) -> None:
        result = self.run_sh("--dry-run")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"platform linux-amd64, release {NEWEST}", result.stdout)
        self.assertIn("would download", result.stdout)
        self.assertFalse(self.bin.exists())

    def test_checksum_mismatch_fails_and_leaves_nothing(self) -> None:
        result = self.run_sh("--version", BAD_SUMS)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum mismatch", result.stderr)
        self.assertEqual(list(self.bin.iterdir()), [])

    def test_missing_release_fails_without_retrying(self) -> None:
        result = self.run_sh("--version", "v9.9.9")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("could not download SHA256SUMS", result.stderr)
        self.assertNotIn("retrying", result.stderr)

    def test_unreachable_api_names_the_version_flag(self) -> None:
        assert RELEASES
        result = self.run_sh(env=self.env(OVERCAST_INSTALL_RELEASES_API=RELEASES.missing_url))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--version", result.stderr)
        self.assertFalse(self.bin.exists())

    def test_baked_version_needs_no_api(self) -> None:
        assert RELEASES
        baked = self.tmp / "install.sh"
        baked.write_text(bake.bake(SH.read_text(encoding="utf-8"), ".sh", OLDER), encoding="utf-8", newline="\n")
        result = self.run_sh(script=baked, env=self.env(OVERCAST_INSTALL_RELEASES_API=RELEASES.missing_url))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_installed("overcast", OLDER)

    def test_explicit_version_beats_baked(self) -> None:
        baked = self.tmp / "install.sh"
        baked.write_text(bake.bake(SH.read_text(encoding="utf-8"), ".sh", OLDER), encoding="utf-8", newline="\n")
        result = self.run_sh("--version", NEWEST, script=baked)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_installed("overcast", NEWEST)

    def test_uninstall_removes_both_binaries(self) -> None:
        self.run_sh("--both")
        result = self.run_sh("--uninstall")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.bin / "overcast").exists())
        self.assertFalse((self.bin / "overcastd").exists())
        again = self.run_sh("--uninstall")
        self.assertIn("nothing to remove", again.stdout)

    def test_modify_path_appends_once_to_the_shell_rc(self) -> None:
        # zsh: its rc file is the same on every OS. bash's is not — macOS
        # gets .bash_profile when there is no .bashrc — and that branch is
        # the script's business, not this test's.
        env = self.env(SHELL="/bin/zsh")
        rc = self.home / ".zshrc"
        first = self.run_sh("--modify-path", env=env)
        self.assertEqual(first.returncode, 0, first.stderr)
        line = f'export PATH="{posix(self.bin)}:$PATH"'
        self.assertIn(line, rc.read_text(encoding="utf-8"))
        second = self.run_sh("--modify-path", env=env)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(rc.read_text(encoding="utf-8").count(line), 1)
        self.assertIn("already adds", second.stdout)

    def test_fish_gets_fish_add_path(self) -> None:
        result = self.run_sh(env=self.env(SHELL="/usr/bin/fish"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"fish_add_path {posix(self.bin)}", result.stdout)

    def test_directory_already_on_path_prints_no_hint(self) -> None:
        if os.name == "nt":
            # MSYS rewrites PATH into /c/... form as the shell starts, so a
            # C:/... entry can never match; Linux and macOS CI cover this.
            self.skipTest("PATH is rewritten by the MSYS runtime on Windows")
        env = self.env()
        env["PATH"] = f"{posix(self.bin)}:{env['PATH']}"
        result = self.run_sh(env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("is not on your PATH", result.stdout)

    def test_unknown_option_is_a_usage_error(self) -> None:
        result = self.run_sh("--frobnicate")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unknown option: --frobnicate", result.stderr)
        self.assertIn("Options", result.stderr)

    def test_help(self) -> None:
        result = self.run_sh("--help")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--slim", result.stdout)
        self.assertIn("install.ps1", result.stdout)

    def test_windows_shell_is_sent_to_the_powershell_installer(self) -> None:
        env = self.env()
        del env["OVERCAST_INSTALL_OS"]
        result = self.run_sh(env=env)
        uname = subprocess.run([*self.shell, "-c", "uname -s"], capture_output=True, text=True, env=env).stdout.strip().lower()
        if uname.startswith(("msys", "mingw", "cygwin")):
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("install.ps1", result.stderr)
        else:
            # A real Unix host: detection works without the override.
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue((self.bin / "overcast").is_file())


def _make_shell_case(shell: list[str]) -> type[ShInstallerTests]:
    label = "_".join([Path(shell[0]).stem, *shell[1:]])
    return type(f"ShInstaller_{label}", (ShInstallerTests,), {"shell": shell})


for _shell in available_shells():
    _case = _make_shell_case(_shell)
    globals()[_case.__name__] = _case


# --------------------------------------------------------------------------
# install.ps1
# --------------------------------------------------------------------------


def available_powershells() -> list[str]:
    return [exe for exe in ("pwsh", "powershell") if shutil.which(exe)]


ANSI = re.compile(r"\x1b\[[0-9;]*m")


def plain(stderr: str) -> str:
    """PowerShell 7's concise error view colours the message and wraps it into a
    gutter of `|` lines; Windows PowerShell prints it whole. Reduce both to one
    plain line so an assertion on the message text holds under either."""
    lines = []
    for line in ANSI.sub("", stderr).splitlines():
        line = re.sub(r"^\s*\|\s?", "", line)
        lines.append(line.strip())
    return re.sub(r"\s+", " ", " ".join(lines))


class PsInstallerTests(unittest.TestCase):
    """Runs install.ps1 under one PowerShell. Subclasses set `exe`."""

    exe: str = ""

    @classmethod
    def setUpClass(cls) -> None:
        if not cls.exe:
            raise unittest.SkipTest("abstract")

    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="overcast-install-test-"))
        self.bin = self.tmp / "bin"

    def tearDown(self) -> None:
        shutil.rmtree(self.tmp, ignore_errors=True)

    def env(self, **extra: str) -> dict[str, str]:
        assert RELEASES
        env = {k: v for k, v in os.environ.items() if not k.startswith("OVERCAST_INSTALL_")}
        env.update(
            {
                "OVERCAST_INSTALL_BASE_URL": RELEASES.base_url,
                "OVERCAST_INSTALL_RELEASES_API": RELEASES.api_url,
                "OVERCAST_INSTALL_ARCH": "amd64",
                "OVERCAST_INSTALL_RETRIES": "2",
                # Never touch the real user PATH from a test.
                "OVERCAST_INSTALL_MODIFY_PATH": "0",
            }
        )
        env.update(extra)
        return env

    def run_ps(self, *args: str, script: Path = PS1, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
        argv = [*args]
        if "-Dir" not in argv:
            argv = ["-Dir", str(self.bin), *argv]
        if "-NoModifyPath" not in argv:
            argv.append("-NoModifyPath")
        return subprocess.run(
            [self.exe, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", str(script), *argv],
            capture_output=True,
            text=True,
            env=env or self.env(),
            timeout=180,
        )

    def run_iex(self, script: Path = PS1, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
        # `irm url | iex` hands the script to Invoke-Expression as one string;
        # the param block has to survive that, with every choice coming from
        # the environment.
        command = f"Get-Content -Raw -LiteralPath '{script}' | Invoke-Expression"
        return subprocess.run(
            [self.exe, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command],
            capture_output=True,
            text=True,
            env=env or self.env(),
            timeout=180,
        )

    def assert_installed(self, name: str, tag: str) -> None:
        assert RELEASES
        target = self.bin / f"{name}.exe"
        self.assertTrue(target.is_file(), f"{target} missing")
        self.assertEqual(target.read_bytes(), RELEASES.assets[(tag, f"{name}-windows-amd64.exe")])

    def test_default_installs_newest_full_binary(self) -> None:
        result = self.run_ps()
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assert_installed("overcast", NEWEST)
        self.assertFalse((self.bin / "overcastd.exe").exists())
        self.assertIn(f"installed overcast {NEWEST}", result.stdout)
        self.assertIn("is not on your PATH", result.stdout)
        self.assertEqual(sorted(p.name for p in self.bin.iterdir()), ["overcast.exe"])

    def test_version_parameter(self) -> None:
        result = self.run_ps("-Version", OLDER[1:])
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assert_installed("overcast", OLDER)

    def test_slim_and_both(self) -> None:
        slim = self.run_ps("-Slim")
        self.assertEqual(slim.returncode, 0, slim.stderr + slim.stdout)
        self.assert_installed("overcastd", NEWEST)
        self.assertFalse((self.bin / "overcast.exe").exists())
        # The next-steps footer names the binary that was installed, whole.
        self.assertIn("overcastd serve", slim.stdout)
        both = self.run_ps("-Both")
        self.assertEqual(both.returncode, 0, both.stderr + both.stdout)
        self.assert_installed("overcast", NEWEST)

    def test_bad_flavor_is_rejected(self) -> None:
        result = self.run_ps("-Flavor", "tiny")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must be full, slim or both", plain(result.stderr))

    def test_dry_run_writes_nothing(self) -> None:
        result = self.run_ps("-DryRun")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertIn("would download", result.stdout)
        self.assertFalse(self.bin.exists())

    def test_checksum_mismatch_fails_and_leaves_nothing(self) -> None:
        result = self.run_ps("-Version", BAD_SUMS)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum mismatch", plain(result.stderr))
        self.assertEqual(list(self.bin.iterdir()), [])

    def test_missing_release_fails_without_retrying(self) -> None:
        result = self.run_ps("-Version", "v9.9.9")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("could not download SHA256SUMS", plain(result.stderr))
        self.assertNotIn("retrying", result.stderr)

    def test_baked_version_needs_no_api(self) -> None:
        assert RELEASES
        baked = self.tmp / "install.ps1"
        baked.write_text(bake.bake(PS1.read_text(encoding="utf-8"), ".ps1", OLDER), encoding="utf-8")
        result = self.run_ps(script=baked, env=self.env(OVERCAST_INSTALL_RELEASES_API=RELEASES.missing_url))
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assert_installed("overcast", OLDER)

    def test_uninstall(self) -> None:
        self.run_ps("-Both")
        result = self.run_ps("-Uninstall")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertFalse((self.bin / "overcast.exe").exists())
        self.assertFalse((self.bin / "overcastd.exe").exists())

    def test_iex_reads_every_choice_from_the_environment(self) -> None:
        result = self.run_iex(env=self.env(OVERCAST_INSTALL_DIR=str(self.bin), OVERCAST_INSTALL_FLAVOR="slim", OVERCAST_INSTALL_VERSION=OLDER))
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assert_installed("overcastd", OLDER)
        self.assertFalse((self.bin / "overcast.exe").exists())

    def test_iex_dry_run_and_uninstall_from_the_environment(self) -> None:
        dry = self.run_iex(env=self.env(OVERCAST_INSTALL_DIR=str(self.bin), OVERCAST_INSTALL_DRY_RUN="1"))
        self.assertEqual(dry.returncode, 0, dry.stderr + dry.stdout)
        self.assertIn("would download", dry.stdout)
        self.assertFalse(self.bin.exists())
        self.run_ps()
        gone = self.run_iex(env=self.env(OVERCAST_INSTALL_DIR=str(self.bin), OVERCAST_INSTALL_UNINSTALL="1"))
        self.assertEqual(gone.returncode, 0, gone.stderr + gone.stdout)
        self.assertFalse((self.bin / "overcast.exe").exists())


for _exe in available_powershells():
    _ps_case = type(f"PsInstaller_{_exe}", (PsInstallerTests,), {"exe": _exe})
    globals()[_ps_case.__name__] = _ps_case


# --------------------------------------------------------------------------
# bake.py
# --------------------------------------------------------------------------


class BakeTests(unittest.TestCase):
    def test_each_script_has_exactly_one_marker(self) -> None:
        for script in (SH, PS1):
            text = script.read_text(encoding="utf-8")
            pattern, _ = bake.MARKERS[script.suffix]
            self.assertEqual(len(pattern.findall(text)), 1, script.name)

    def test_bake_sets_the_marker(self) -> None:
        sh = bake.bake(SH.read_text(encoding="utf-8"), ".sh", "v1.2.3")
        self.assertIn('OVERCAST_INSTALL_BAKED_VERSION="v1.2.3"', sh)
        self.assertNotIn('OVERCAST_INSTALL_BAKED_VERSION=""', sh)
        ps = bake.bake(PS1.read_text(encoding="utf-8"), ".ps1", "v1.2.3")
        self.assertIn('$BakedVersion = "v1.2.3"', ps)

    def test_bake_refuses_a_script_without_the_marker(self) -> None:
        with self.assertRaises(ValueError):
            bake.bake("echo hi\n", ".sh", "v1.2.3")
        with self.assertRaises(ValueError):
            bake.bake("x\n", ".txt", "v1.2.3")

    def test_normalise_tag(self) -> None:
        self.assertEqual(bake.normalise_tag("0.0.1-alpha.40"), "v0.0.1-alpha.40")
        self.assertEqual(bake.normalise_tag("v1.0.0"), "v1.0.0")
        with self.assertRaises(ValueError):
            bake.normalise_tag("latest")
        with self.assertRaises(ValueError):
            bake.normalise_tag('v1.0.0"; rm -rf /')

    def test_cli_preserves_line_endings(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "src"
            src.mkdir()
            crlf = src / "install.ps1"
            crlf.write_bytes(PS1.read_text(encoding="utf-8").replace("\r\n", "\n").replace("\n", "\r\n").encode())
            lf = src / "install.sh"
            lf.write_bytes(SH.read_text(encoding="utf-8").replace("\r\n", "\n").encode())
            out = Path(tmp) / "out"
            code = bake.main(["--version", "0.0.1-alpha.40", "--out", str(out), str(lf), str(crlf)])
            self.assertEqual(code, 0)
            baked_sh = (out / "install.sh").read_bytes()
            baked_ps = (out / "install.ps1").read_bytes()
            self.assertNotIn(b"\r\n", baked_sh)
            self.assertIn(b'OVERCAST_INSTALL_BAKED_VERSION="v0.0.1-alpha.40"\n', baked_sh)
            self.assertIn(b'$BakedVersion = "v0.0.1-alpha.40"\r\n', baked_ps)
            self.assertNotIn(b"\n\n\n\n", baked_ps.replace(b"\r\n", b"\n"))

    def test_cli_rejects_a_bad_version(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            code = bake.main(["--version", "latest", "--out", tmp, str(SH)])
            self.assertEqual(code, 2)


if __name__ == "__main__":
    unittest.main(verbosity=2)
