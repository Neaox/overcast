---
title: "Install Overcast"
description: "One line installs the native binary on macOS, Linux or Windows: what the installer does, every flag and variable, the Docker images, and installing by hand."
section: "Getting Started"
tags:
  - install
  - installer
  - binaries
  - docker
  - docs
  - overcast
---

# Install Overcast

One line installs the `overcast` binary for your machine and puts it on your PATH.

macOS and Linux:

```bash
curl -fsSL https://overcast.sh/install.sh | sh
```

Windows, in PowerShell:

```powershell
irm https://overcast.sh/install.ps1 | iex
```

Then start it and point a client at it:

```bash
overcast serve
eval "$(overcast env)"        # PowerShell: overcast env | iex
aws s3 mb s3://hello-overcast
```

The web console is on <http://localhost:4567> and the AWS-compatible API on
port 4566. The [CLI reference](./cli.md) covers the rest of the commands.

## What the installer does

It works out your OS and CPU, downloads the matching binary from the
[GitHub release](https://github.com/overcast-sh/overcast/releases), checks it
against the release's `SHA256SUMS`, and renames the verified file into a
directory that belongs to you. It never asks a question and never uses `sudo`
or elevation, so the same line works in a terminal, in CI and in a
`Dockerfile`. The script is plain text: read it before you run it at
[install.sh](https://github.com/overcast-sh/overcast/blob/main/install/install.sh)
and [install.ps1](https://github.com/overcast-sh/overcast/blob/main/install/install.ps1).

| | macOS and Linux | Windows |
| --- | --- | --- |
| Installs to | `~/.local/bin` | `%LOCALAPPDATA%\Programs\overcast\bin` |
| PATH | prints the line for your shell; `--modify-path` writes it | adds the directory to your user PATH; `-NoModifyPath` skips that |
| Verifies with | `sha256sum`, `shasum` or `openssl` | the .NET SHA-256 implementation |
| Version | the release the script shipped with | the same |

Run the same line again to upgrade. An install that is already at the
requested version is left alone.

## Choosing what to install

Flags go after `sh -s --` on Unix. `irm | iex` cannot take parameters, so on
Windows every choice is also an environment variable, set before the line
runs; the parameters work when the script is saved to disk first.

```bash
curl -fsSL https://overcast.sh/install.sh | sh -s -- --slim --version v0.0.1-alpha.39
```

```powershell
$env:OVERCAST_INSTALL_FLAVOR = "slim"; irm https://overcast.sh/install.ps1 | iex
```

| Choice | Unix flag | Windows parameter | Variable |
| --- | --- | --- | --- |
| A specific release | `--version <tag>` | `-Version <tag>` | `OVERCAST_INSTALL_VERSION` |
| The headless daemon, `overcastd` | `--slim` | `-Slim` | `OVERCAST_INSTALL_FLAVOR=slim` |
| Both binaries | `--both` | `-Both` | `OVERCAST_INSTALL_FLAVOR=both` |
| Another directory | `--dir <path>` | `-Dir <path>` | `OVERCAST_INSTALL_DIR` |
| PATH handling | `--modify-path` | `-NoModifyPath` | `OVERCAST_INSTALL_MODIFY_PATH=1` or `0` |
| See the plan, change nothing | `--dry-run` | `-DryRun` | `OVERCAST_INSTALL_DRY_RUN=1` (Windows) |
| Remove it | `--uninstall` | `-Uninstall` | `OVERCAST_INSTALL_UNINSTALL=1` (Windows) |

`overcastd` is the slim build: the same emulator and the same commands without
the web console. It is the one to put in CI. The version tag works with or
without its leading `v`.

## Docker instead

The images need no installer. The full image serves the console on 4567; the
slim image is the emulator alone.

```bash
docker run --rm -p 4566:4566 -p 4567:4567 ghcr.io/overcast-sh/overcast:latest
docker run --rm -p 4566:4566 ghcr.io/overcast-sh/overcast-slim:latest
```

Compose, Testcontainers and the image tags are covered in
[Testcontainers](./testcontainers.md) and on the
[downloads page](https://overcast.sh/downloads/).

## By hand

Every release lists its binaries with their checksums: `overcast` and
`overcastd` for Linux and macOS on amd64 and arm64, and for Windows on
amd64. Download one, verify it against `SHA256SUMS`, make it executable and
put it on your PATH. A copy of each install script is attached to the release
too, pinned to that release, for an install that must not move with the
website.

Windows on ARM has no arm64 build yet; the installer puts the amd64 binary in
place, which Windows 11 runs under its x64 emulation, and says so.

## Related

- [CLI reference](./cli.md) — every `overcast` subcommand
- [Using AWS SDKs and CLI](./sdk-cli.md) — pointing your tools at the running emulator
- [Configuration](./configuration.md) — the variables the daemon reads
- [Troubleshooting](./troubleshooting.md) — a symptom, and where its answer lives
- [GitHub releases](https://github.com/overcast-sh/overcast/releases) — every binary and checksum
