# Install scripts

`install.sh` and `install.ps1` are the one-line installers behind
`https://overcast.sh/install.sh` and `https://overcast.sh/install.ps1`. They
live here, in the Overcast repository, because they encode facts only the
release pipeline knows: the asset names `build-binaries.yml` produces, the two
binary flavours, the `SHA256SUMS` format, and how "latest" is resolved while
every release is a prerelease. A copy kept in the website repository would
drift the first time an asset was renamed. The website serves these files
from the tagged checkout it already builds from; the release workflow
attaches them to every release as well.

The user-facing page is [docs/install.md](../docs/install.md). This file is
for people changing the scripts.

## What each script does

1. Parse flags and `OVERCAST_INSTALL_*` variables. Nothing is ever asked
   interactively: under `curl | sh` the script *is* stdin, and CI and
   `Dockerfile RUN` lines have no terminal.
2. Detect the platform (`linux`/`darwin` × `amd64`/`arm64`; Windows amd64,
   with ARM64 machines taking the amd64 build under emulation and being told
   so). A Rosetta shell on Apple Silicon still gets the arm64 binary.
3. Resolve the version: `--version`, else the baked tag, else the newest
   release from the GitHub API. `releases/latest` is never used — it answers
   404 while every release is a prerelease.
4. Download the asset and `SHA256SUMS` from
   `https://github.com/overcast-sh/overcast/releases/download/<tag>/`, with
   retries, treating an empty body as a failure and a 404 as final.
5. Verify the checksum. There is no way to skip this; a host with none of
   `sha256sum`, `shasum` or `openssl` fails rather than installing blind.
6. Rename the verified file over the target in a per-user directory. No
   `sudo`, ever. An install that is already at the requested version is a
   no-op; an older one is reported as an upgrade.
7. Deal with PATH. Unix prints the line for the user's shell, or appends it
   to the rc file with `--modify-path`. Windows adds the directory to the user
   PATH (and the current session) unless `-NoModifyPath`.

Every function is defined first and `main` runs on the last line, so a
transfer cut off part-way executes nothing.

## Flags and variables

| Unix flag | Windows parameter | Variable | Default |
| --- | --- | --- | --- |
| `--version <tag>` | `-Version <tag>` | `OVERCAST_INSTALL_VERSION` | baked tag, else newest release |
| `--slim` / `--both` / `--full` | `-Slim` / `-Both` / `-Flavor` | `OVERCAST_INSTALL_FLAVOR` (`full`, `slim`, `both`) | `full` |
| `--dir <path>` | `-Dir <path>` | `OVERCAST_INSTALL_DIR` | `~/.local/bin`; `%LOCALAPPDATA%\Programs\overcast\bin` |
| `--modify-path` / `--no-modify-path` | `-NoModifyPath` | `OVERCAST_INSTALL_MODIFY_PATH` (`0`/`1`) | print on Unix, modify on Windows |
| `--dry-run` | `-DryRun` | | |
| `--uninstall` | `-Uninstall` | | |
| | | `OVERCAST_INSTALL_OS`, `OVERCAST_INSTALL_ARCH` | detected |
| | | `OVERCAST_INSTALL_BASE_URL`, `OVERCAST_INSTALL_RELEASES_API` | GitHub |
| | | `OVERCAST_INSTALL_RETRIES` | `4` |

`irm | iex` cannot pass parameters, which is why every parameter has a
variable. The last three rows exist for the tests and for anyone mirroring
releases.

## The baked version

Each script carries one marker line, empty in the repository:

```sh
OVERCAST_INSTALL_BAKED_VERSION=""
```

```powershell
$BakedVersion = ""
```

`bake.py` rewrites that line with a release tag and writes the copy
somewhere else; the repository files are never modified. The release
workflow runs it before attaching the scripts, and the website runs the same
substitution when it serves them, so the default install path makes no API
call. The tests assert each script has exactly one marker, because a script
that lost it would silently fall back to the API.

```sh
python3 install/bake.py --version v0.0.1-alpha.40 --out dist install/install.sh install/install.ps1
```

## Tests

`install_test.py` starts a local HTTP server with a fake release layout and
runs the real scripts against it through `OVERCAST_INSTALL_BASE_URL` and
`OVERCAST_INSTALL_RELEASES_API`. It runs `install.sh` under every shell it
finds (`sh`, `dash`, `bash`, `zsh`, `busybox sh`) and `install.ps1` under
`pwsh` and `powershell` when present, including once through `iex` to prove
the `param` block survives `irm | iex`. Nothing touches the real PATH or rc
files.

```sh
python3 install/install_test.py
```

CI runs the tests on Linux, macOS and Windows, lints `install.sh` with
shellcheck and `install.ps1` with PSScriptAnalyzer, and after a release
installs from the published assets on all three.

## Conventions

- `install.sh` is POSIX sh. No bashisms, no `local`, no arrays; it has to run
  under dash and busybox. `.gitattributes` pins it to LF, because a CRLF
  checkout from Windows is a script `sh` cannot run.
- `install.ps1` targets PowerShell 5.1: no `??`, no ternary, no `&&`.
- Output goes to stdout as plain lines; errors go to stderr with an
  `install.sh:` / `install.ps1:` prefix and a next step.
