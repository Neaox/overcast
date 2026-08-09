#!/usr/bin/env python3

"""Tests for run-test-instance.{sh,ps1} — the throwaway-instance launcher.

The script is meant to be *granted* to agents as a whole, with a permission
entry naming the script instead of a blanket `docker run`. That only narrows
anything if the script cannot be talked into being a general docker proxy, so
what is asserted here is mostly what it *refuses*, and the exact command line it
builds when it agrees.

Both halves of the pair are driven, and driven the same way: a `docker` stub
first on PATH that appends its argv to a log and echoes a container id. That is
the only honest way to assert the final command line — a real `docker run`
would need the daemon, and the socket mount is the very thing under test.

The two scripts must stay behaviorally identical, so the shared cases run
against both and one test asserts the command lines match byte for byte.

Run: python scripts/run_test_instance_test.py   (CI runs every scripts/*_test.py)
"""

from __future__ import annotations

import importlib.util
import os
import shutil
import socket
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent
SH = SCRIPTS / "run-test-instance.sh"
PS1 = SCRIPTS / "run-test-instance.ps1"


def _load(path: Path, name: str):
	spec = importlib.util.spec_from_file_location(name, path)
	assert spec is not None and spec.loader is not None
	module = importlib.util.module_from_spec(spec)
	spec.loader.exec_module(module)
	return module


# find_bash() knows that a Windows `bash` is usually the WSL launcher, which
# cannot open the C:\ or F:\ path it is about to be handed. Reusing it rather
# than restating it: the failure it prevents is a silent exit 127, and one copy
# of that reasoning is enough.
BASH = _load(SCRIPTS / "pr-wait_test.py", "pr_wait_test").BASH


def find_dash() -> str | None:
	"""A genuine POSIX sh, or None.

	run-test-instance.sh is `#!/bin/sh`, so on Debian, Ubuntu and most Linux CI
	images it is dash that runs it — but every test here drives it through
	BASH, which accepts bashisms silently. That gap hid a real one: the free
	port check fell back to `exec 3<>/dev/tcp/...`, a bash builtin that under
	dash fails to open, and the `!` in front of it read the failure as "nothing
	is listening". Every port then looked free.

	The same gap on the other side of the pair is what _powershell_hosts()
	closes: one host tested where two exist, and a Windows-only cmdlet under
	the untested one.

	Git for Windows ships dash next to its bash, so this runs on a dev box too
	rather than only on Linux CI, where it would be the one place the branch is
	exercised.
	"""
	found = shutil.which("dash")
	if found:
		return found
	git = shutil.which("git")
	if git:
		root = Path(git).resolve().parent.parent
		for candidate in (root / "usr" / "bin" / "dash.exe", root / "bin" / "dash.exe"):
			if candidate.is_file():
				return str(candidate)
	return None


DASH = find_dash()


def _powershell_hosts():
	"""Every PowerShell host on this machine, as (name, path).

	Both are driven where both exist, rather than picking one, because they are
	genuinely different programs and this suite is the only thing standing
	between the argument handling and a permission grant.

	`powershell` is Windows PowerShell 5.1 — what the Taskfile invokes and what
	ships to Windows users. `pwsh` is PowerShell Core, and on Linux it is the
	only one there, so it is what CI exercises. The two disagree about more than
	version: a Windows-only cmdlet resolves under one and not the other, which
	is exactly how this class came to be running on CI without ever reaching a
	`docker` command line.
	"""
	hosts = []
	for name in ("powershell", "pwsh"):
		path = shutil.which(name)
		if path:
			hosts.append((name, path))
	return hosts


POWERSHELL_HOSTS = _powershell_hosts()

SH_STUB = """#!/bin/sh
d=$(dirname "$0")
printf '%s\\n' "$*" >>"$d/invocations.log"
if [ "${1-}" = "run" ]; then echo deadbeefcafe0123456789; fi
exit 0
"""

# `%*` rather than a shift loop over `%~1`: cmd.exe treats '=' as an argument
# delimiter for %1-style parsing, so a shift loop reports `-e OVERCAST_STATE=x`
# as three arguments and the assertion fails on the stub's behaviour rather than
# the script's. `%*` is the raw command-line tail and preserves it.
BAT_STUB = """@echo off
>>"%~dp0invocations.log" echo(%*
if "%~1"=="run" echo deadbeefcafe0123456789
exit /b 0
"""


class Run:
	"""One invocation of one of the two scripts, against a stubbed docker."""

	def __init__(self, out: str, err: str, code: int, invocations: list[str]):
		self.out = out
		self.err = err
		self.code = code
		self.invocations = invocations

	@property
	def argv(self) -> list[str]:
		"""The `docker run` command line, split into arguments.

		Nothing this script passes to docker may contain a space, so splitting
		the logged line recovers the exact argv. A case that needed a space
		would be a case the script should have refused.
		"""
		self_run = [i for i in self.invocations if i.startswith("run ")]
		assert self_run, f"docker run was never invoked: {self.invocations}"
		return self_run[0].split()


def _posix_toolchain(shell: str) -> list[str]:
	"""Directories that must be on PATH for `shell` to have a POSIX toolset.

	Only on Windows, and only because of how the shells are packaged there:
	Git's bash.exe is a launcher that puts Git's usr/bin (grep, cut, sed) on
	PATH for whatever it runs, and dash.exe — sitting in that same usr/bin — is
	not, so a dash started straight from Windows inherits a Windows PATH and
	cannot find grep. Handing it its own directory is what makes the two shells
	comparable. Git bundles neither ss.exe nor netstat.exe there, so this does
	not smuggle a port probe into the bare-PATH case.

	On Linux and macOS this returns nothing: the shell's directory is /bin or
	/usr/bin, which is where ss and netstat live, and adding it would defeat
	that same case.
	"""
	if os.name != "nt":
		return []
	return [str(Path(shell).resolve().parent)]


def _invoke(
	argv: list[str],
	stub: str,
	stub_name: str,
	*,
	bare_path: bool = False,
	toolchain: list[str] | None = None,
) -> Run:
	with tempfile.TemporaryDirectory() as tmp:
		stubdir = Path(tmp)
		stubfile = stubdir / stub_name
		newline = "\r\n" if stub_name.endswith(".bat") else "\n"
		with stubfile.open("w", encoding="ascii", newline=newline) as fh:
			fh.write(stub)
		stubfile.chmod(0o755)

		env = dict(os.environ)
		# bare_path: the stub directory and the shell's own toolset, and
		# nothing else — how a machine with no ss and no netstat is reproduced
		# anywhere. The shell is launched by absolute path, so what is left is
		# a working shell that cannot find a port probe.
		parts = [str(stubdir), *(toolchain or [])]
		if not bare_path:
			parts.append(env.get("PATH", ""))
		env["PATH"] = os.pathsep.join(p for p in parts if p)
		proc = subprocess.run(argv, capture_output=True, text=True, env=env, cwd=str(SCRIPTS.parent))

		log = stubdir / "invocations.log"
		lines = log.read_text(encoding="utf-8").splitlines() if log.exists() else []
		return Run(proc.stdout, proc.stderr, proc.returncode, [ln.strip() for ln in lines if ln.strip()])


def run_sh(*args: str, shell: str | None = None, bare_path: bool = False) -> Run:
	# as_posix(): Git Bash reads the backslashes in a native Windows path as
	# escapes. Forward slashes work on every platform.
	shell = shell or BASH
	return _invoke(
		[shell, SH.as_posix(), *args],
		SH_STUB,
		"docker",
		bare_path=bare_path,
		toolchain=_posix_toolchain(shell),
	)


# The stub has to be the kind of executable the *host OS* can find on PATH: a
# .bat on Windows, a shebang script everywhere else. Getting this wrong fails
# silently in the worst way — PowerShell simply never finds `docker`, the log
# stays empty, and every assertion about the command line reports "docker run
# was never invoked" without saying why.
PS_STUB, PS_STUB_NAME = (BAT_STUB, "docker.bat") if os.name == "nt" else (SH_STUB, "docker")


def run_ps1(host: str, *args: str) -> Run:
	return _invoke(
		[host, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(PS1), *args],
		PS_STUB,
		PS_STUB_NAME,
	)


needs_powershell = unittest.skipUnless(
	POWERSHELL_HOSTS, "neither powershell nor pwsh is on PATH"
)
needs_dash = unittest.skipUnless(DASH, "no dash found (Linux CI and Git for Windows both have one)")

# The same intent spelled in each script's own dialect. Keyed by a short name so
# a failure says which case, not which index.
BOTH = {
	"socket": (["--mount-docker-socket", "--no-logs"], ["-MountDockerSocket", "-NoLogs"]),
	"plain": (["--no-logs"], ["-NoLogs"]),
	"env": (["--no-logs", "--env", "OVERCAST_STATE=hybrid"], ["-NoLogs", "-EnvVar", "OVERCAST_STATE=hybrid"]),
	"volume": (["--no-logs", "--data-volume", "myvol"], ["-NoLogs", "-DataVolume", "myvol"]),
	"named": (["--no-logs", "--name", "shot"], ["-NoLogs", "-Name", "shot"]),
	"image": (["--no-logs", "--image", "overcast:my-branch"], ["-NoLogs", "-Image", "overcast:my-branch"]),
}

# Every flag the hardening exists to refuse, with a value where docker would
# take one. `--network=host` and `--privileged=true` are here to pin the
# `=value` spelling, which a naive equality check would miss.
REFUSED = [
	["--privileged"],
	["--privileged=true"],
	["--cap-add", "SYS_ADMIN"],
	["--pid", "host"],
	["--ipc", "host"],
	["--uts", "host"],
	["--network=host"],
	["--net", "host"],
	["--user", "0:0"],
	["-u", "0"],
	["--device", "/dev/kmsg"],
	["--security-opt", "seccomp=unconfined"],
	["-v", "/:/host"],
	["--volume", "/:/host"],
	["--mount", "type=bind,src=/,dst=/host"],
	["--volumes-from", "other"],
	["--entrypoint", "/bin/sh"],
	["--userns=host"],
	["--cgroup-parent", "/"],
	["--sysctl", "kernel.shmmax=1"],
	["--tmpfs", "/tmp"],
	["-p", "4566:4566"],
	["--add-host", "evil:1.2.3.4"],
	["--runtime", "runsc"],
	["--group-add", "docker"],
]


class ShellArguments(unittest.TestCase):
	"""run-test-instance.sh: what it builds, and what it will not."""

	def test_socket_flag_adds_exactly_the_socket_mount(self):
		argv = run_sh(*BOTH["socket"][0]).argv
		self.assertEqual(
			[a for a in argv if a == "-v" or "docker.sock" in a],
			["-v", "/var/run/docker.sock:/var/run/docker.sock"],
		)

	def test_no_socket_flag_means_no_mount_at_all(self):
		self.assertNotIn("-v", run_sh(*BOTH["plain"][0]).argv)

	def test_ports_publish_to_loopback_only(self):
		argv = run_sh("--no-logs").argv
		published = [argv[i + 1] for i, a in enumerate(argv) if a == "-p"]
		self.assertEqual(len(published), 2, argv)
		for mapping in published:
			# A bare "<port>:4566" binds every interface. That is what put an
			# unauthenticated emulator on the LAN and tripped the firewall.
			self.assertTrue(mapping.startswith("127.0.0.1:"), mapping)
		self.assertTrue(published[0].endswith(":4566"), published)
		self.assertTrue(published[1].endswith(":4567"), published)

	def test_refuses_every_dangerous_flag_by_name(self):
		for case in REFUSED:
			with self.subTest(flag=case[0]):
				result = run_sh(*case)
				self.assertEqual(result.code, 2, result.err)
				self.assertIn("refusing", result.err)
				# Named, so the caller knows which argument stopped it — and
				# never silently dropped, which would leave them believing it
				# applied.
				self.assertIn(case[0].split("=")[0], result.err)
				self.assertEqual(result.invocations, [])

	def test_double_dash_passthrough_is_gone_and_says_so(self):
		result = run_sh("--", "-e", "X=1")
		self.assertEqual(result.code, 2, result.err)
		self.assertIn("passthrough", result.err)
		self.assertEqual(result.invocations, [])

	def test_unknown_flag_is_distinguished_from_a_refused_one(self):
		result = run_sh("--wat")
		self.assertEqual(result.code, 2)
		self.assertIn("unknown option '--wat'", result.err)

	def test_env_is_limited_to_the_emulators_own_variables(self):
		result = run_sh("--env", "PATH=/evil")
		self.assertEqual(result.code, 2, result.err)
		self.assertIn("PATH", result.err)
		self.assertEqual(result.invocations, [])

	def test_env_passes_an_overcast_variable_through(self):
		self.assertIn("OVERCAST_STATE=hybrid", run_sh(*BOTH["env"][0]).argv)

	def test_env_needs_a_key_value_pair(self):
		self.assertEqual(run_sh("--env", "OVERCAST_STATE").code, 2)

	def test_data_volume_takes_a_named_volume_not_a_path(self):
		self.assertIn("myvol:/data", run_sh(*BOTH["volume"][0]).argv)
		self.assertEqual(run_sh("--data-volume", "/etc").code, 2)
		self.assertEqual(run_sh("--data-volume", "../etc").code, 2)

	def test_image_cannot_be_smuggled_in_as_a_flag(self):
		self.assertEqual(run_sh("--image", "--privileged").code, 2)
		self.assertEqual(run_sh("--image", "").code, 2)

	def test_a_flag_missing_its_value_is_an_error_not_a_silent_default(self):
		result = run_sh("--image")
		self.assertEqual(result.code, 2)
		self.assertIn("needs a value", result.err)

	def test_no_logs_stops_after_the_run(self):
		self.assertEqual([i.split()[0] for i in run_sh("--no-logs").invocations], ["run"])

	def test_logs_are_followed_by_default(self):
		self.assertEqual([i.split()[0] for i in run_sh().invocations], ["run", "logs"])

	def test_help_explains_itself_without_running_anything(self):
		result = run_sh("--help")
		self.assertEqual(result.code, 0)
		self.assertIn("--mount-docker-socket", result.out)
		self.assertEqual(result.invocations, [])


class PortSelection(unittest.TestCase):
	"""The free-port search, and the two ports that are never the answer."""

	def assert_skips_a_listening_port(self, **kwargs):
		# The scan steps in twos from the base, so the occupied port has to be
		# the base itself for the skip to be the thing under test. An ephemeral
		# port would be odd half the time and test nothing on those runs.
		with socket.socket() as taken:
			for candidate in range(20000, 20100, 2):
				try:
					taken.bind(("127.0.0.1", candidate))
				except OSError:
					continue
				break
			else:
				self.skipTest("no free even port in 20000-20100")
			taken.listen(1)
			port = taken.getsockname()[1]
			argv = run_sh("--no-logs", "--base-port", str(port), **kwargs).argv
			published = [argv[i + 1] for i, a in enumerate(argv) if a == "-p"]
			self.assertNotIn(f"127.0.0.1:{port}:4566", published)

	def test_skips_a_port_that_is_already_listening(self):
		self.assert_skips_a_listening_port()

	@needs_dash
	def test_skips_a_port_that_is_already_listening_under_posix_sh(self):
		# Same assertion, under the shell that actually runs this script on
		# Debian and Ubuntu. Bash is forgiving of things `#!/bin/sh` promises
		# not to use, so a scan that works under BASH says nothing about the
		# one CI runs — and the probe this replaced was wrong in precisely that
		# gap. See find_dash().
		self.assert_skips_a_listening_port(shell=DASH)

	def test_stops_when_it_cannot_tell_which_ports_are_in_use(self):
		# Neither ss nor netstat on PATH. The old fallback was a bash `/dev/tcp`
		# redirect that dash cannot perform, and the failed redirect was read as
		# "the port is free" — so this exact case used to publish the base port
		# with no idea whether anything was on it, and would happily have done
		# that to the user's own 4566/4567. Guessing free is the one answer this
		# check may never give.
		for name, shell in (("bash", BASH), ("dash", DASH)):
			if shell is None:
				continue
			with self.subTest(shell=name):
				result = run_sh("--no-logs", shell=shell, bare_path=True)
				self.assertEqual(result.code, 1, result.err or result.out)
				# Named, so the reader knows what to install rather than only
				# that something is missing.
				self.assertIn("ss(8)", result.err)
				self.assertIn("netstat(8)", result.err)
				self.assertEqual(result.invocations, [])

	def test_never_lands_on_the_users_reserved_pair(self):
		# 4565 and 4567 are the bases that used to slip through: the guard only
		# refused 4566 for the API role and 4567 for the UI role, so a scan from
		# 4565 published the UI on the user's API port, and a scan from 4567
		# published the API on the user's UI port. Either one silently breaks
		# whatever the user is doing.
		for base in ("4565", "4566", "4567"):
			with self.subTest(base=base):
				argv = run_sh("--no-logs", "--base-port", base).argv
				published = [argv[i + 1] for i, a in enumerate(argv) if a == "-p"]
				host_ports = {m.split(":")[1] for m in published}
				self.assertFalse(host_ports & {"4566", "4567"}, published)

	def test_base_port_must_be_a_number_in_range(self):
		self.assertEqual(run_sh("--base-port", "abc").code, 2)
		self.assertEqual(run_sh("--base-port", "80").code, 2)


@needs_powershell
class PowerShellTwin(unittest.TestCase):
	"""run-test-instance.ps1 must refuse and build exactly what the .sh does.

	Every case runs against every PowerShell host present — see
	_powershell_hosts. On CI that is pwsh alone; on a Windows box it is usually
	Windows PowerShell 5.1, and both when both are installed.
	"""

	def test_the_suite_actually_found_a_host(self):
		# Paranoia with a reason: this class once ran green-looking on CI while
		# reaching nothing, and a silently empty host list would do it again.
		self.assertTrue(POWERSHELL_HOSTS, "no PowerShell host was discovered")

	def test_builds_the_same_command_line_as_the_shell_script(self):
		for host, ps in POWERSHELL_HOSTS:
			for name, (sh_args, ps_args) in BOTH.items():
				with self.subTest(host=host, case=name):
					# Pin the port so the two runs cannot differ merely because
					# something grabbed a port between them.
					sh_argv = run_sh(*sh_args, "--base-port", "4590").argv
					ps_argv = run_ps1(ps, *ps_args, "-BasePort", "4590").argv
					self.assertEqual(sh_argv, ps_argv)

	def test_socket_flag_adds_exactly_the_socket_mount(self):
		for host, ps in POWERSHELL_HOSTS:
			with self.subTest(host=host):
				argv = run_ps1(ps, *BOTH["socket"][1]).argv
				self.assertEqual(
					[a for a in argv if a == "-v" or "docker.sock" in a],
					["-v", "/var/run/docker.sock:/var/run/docker.sock"],
				)

	def test_ports_publish_to_loopback_only(self):
		for host, ps in POWERSHELL_HOSTS:
			with self.subTest(host=host):
				argv = run_ps1(ps, "-NoLogs").argv
				published = [argv[i + 1] for i, a in enumerate(argv) if a == "-p"]
				self.assertEqual(len(published), 2, argv)
				for mapping in published:
					self.assertTrue(mapping.startswith("127.0.0.1:"), mapping)

	def test_refuses_every_dangerous_flag_by_name(self):
		for host, ps in POWERSHELL_HOSTS:
			for case in REFUSED:
				with self.subTest(host=host, flag=case[0]):
					result = run_ps1(ps, *case)
					self.assertEqual(result.code, 2, result.err or result.out)
					self.assertIn("refusing", result.err)
					self.assertIn(case[0].split("=")[0].lstrip("-"), result.err)
					self.assertEqual(result.invocations, [])

	def test_the_old_dockerargs_passthrough_is_refused_by_name(self):
		# The parameter this hardening removed. Somebody's muscle memory, or a
		# stale doc, will reach for it; it must stop rather than be ignored.
		for host, ps in POWERSHELL_HOSTS:
			with self.subTest(host=host):
				result = run_ps1(ps, "-DockerArgs", "--privileged")
				self.assertEqual(result.code, 2, result.err or result.out)
				self.assertIn("DockerArgs", result.err)
				self.assertEqual(result.invocations, [])

	def test_env_is_limited_to_the_emulators_own_variables(self):
		for host, ps in POWERSHELL_HOSTS:
			with self.subTest(host=host):
				result = run_ps1(ps, "-EnvVar", "PATH=/evil")
				self.assertEqual(result.code, 2, result.err or result.out)
				self.assertIn("PATH", result.err)

	def test_never_lands_on_the_users_reserved_pair(self):
		for host, ps in POWERSHELL_HOSTS:
			for base in ("4565", "4566", "4567"):
				with self.subTest(host=host, base=base):
					argv = run_ps1(ps, "-NoLogs", "-BasePort", base).argv
					published = [argv[i + 1] for i, a in enumerate(argv) if a == "-p"]
					host_ports = {m.split(":")[1] for m in published}
					self.assertFalse(host_ports & {"4566", "4567"}, published)

	def test_skips_a_port_that_is_already_listening(self):
		# The Windows-only Get-NetTCPConnection has a loopback-connect fallback
		# for pwsh elsewhere; this is the case that tells the two apart.
		for host, ps in POWERSHELL_HOSTS:
			with self.subTest(host=host):
				with socket.socket() as taken:
					for candidate in range(20200, 20300, 2):
						try:
							taken.bind(("127.0.0.1", candidate))
						except OSError:
							continue
						break
					else:
						self.skipTest("no free even port in 20200-20300")
					taken.listen(1)
					port = taken.getsockname()[1]
					argv = run_ps1(ps, "-NoLogs", "-BasePort", str(port)).argv
					published = [argv[i + 1] for i, a in enumerate(argv) if a == "-p"]
					self.assertNotIn(f"127.0.0.1:{port}:4566", published)


if __name__ == "__main__":
	unittest.main(verbosity=2)
