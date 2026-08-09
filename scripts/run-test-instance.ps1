# run-test-instance.ps1 -- PowerShell twin of run-test-instance.sh: start a
# throwaway Overcast instance on a FREE pair of LOOPBACK ports, never on the
# user-reserved defaults (4566 API / 4567 web UI). See the .sh header for the
# full contract and the reasoning; the two must stay behaviorally identical.
#
# Usage:
#   scripts/run-test-instance.ps1
#   scripts/run-test-instance.ps1 -BasePort 4600
#   scripts/run-test-instance.ps1 -Image overcast:my-branch
#   scripts/run-test-instance.ps1 -EnvVar OVERCAST_STATE=hybrid -DataVolume myvol
#   scripts/run-test-instance.ps1 -MountDockerSocket -NoLogs -Name shot
#
# Options:
#   -Image REF             image to run (default ghcr.io/neaox/overcast:alpha).
#                          Pass the image you built from your own worktree --
#                          `make docker-console` tags it after your branch.
#   -BasePort N            start the free-port scan at N (default 4570).
#   -Name NAME             name the container, so a later step can stop it.
#   -EnvVar KEY=VALUE      one container environment variable. KEY must be
#                          OVERCAST_* or AWS_*. Takes an array for several.
#   -DataVolume NAME       mount the named Docker volume NAME at /data.
#   -MountDockerSocket     bind-mount the host Docker socket (see below).
#   -NoLogs                print the endpoints and exit instead of following
#                          logs. What you want when a later step drives the
#                          instance; the container keeps running either way.
#
# ---------------------------------------------------------------------------
# Why there is no `docker run` passthrough
# ---------------------------------------------------------------------------
# This script is meant to be granted to agents *as a script* -- a permission
# entry naming the script rather than a blanket `docker run`, which would
# permit any image with any flags, `--privileged` included. That grant is only
# narrower than `docker run` if the script cannot be used as a general docker
# proxy. It used to accept a free-form -DockerArgs array and splice it straight
# into the command line, which made the two grants identical and the narrower
# one security theatre.
#
# So the options above are the whole surface: an allow-list, not a deny-list.
# An allow-list cannot be incomplete, whereas a deny-list has to stay ahead of
# every flag Docker adds and every spelling of the ones it already has
# (`--privileged=true`, `--net=host`, `-u 0`, `--mount type=bind,...`). The
# deny-list below survives only to make the *message* better: a caller who
# reaches for `--privileged` is told it is refused by policy rather than merely
# unrecognised. Nothing is ever filtered silently -- an argument this script
# does not understand stops it, so a caller can never believe something applied
# that did not.
#
# ---------------------------------------------------------------------------
# -MountDockerSocket
# ---------------------------------------------------------------------------
# Appends exactly `-v /var/run/docker.sock:/var/run/docker.sock` and nothing
# else. It is what makes the instance genuinely useful, in two ways:
#
#   * Lambda and ECS need it. Both start containers of their own through the
#     daemon; without the socket neither can run at all.
#   * The web console needs it to find its own API. A container cannot see its
#     own port mapping from the inside, so on a remapped port deriveAPIBaseURL
#     (internal/bff/bff.go) cannot tell the SPA where the API is, returns
#     endpointKnown: false, and the console shows its "Connect to Overcast"
#     screen. With the socket, resolvePublishedPort (cmd/overcast/cmd_serve.go)
#     asks Docker for the container's own bindings, recovers the published
#     port, and the console connects unprompted.
#
# Understand what you are granting: a process that can reach the Docker socket
# can start a privileged container and is therefore root on the host. That is
# true of `make docker-run` too. It is called out here because this flag is the
# one an agent will reach for.
#
# ---------------------------------------------------------------------------
# Loopback publishing
# ---------------------------------------------------------------------------
# Ports are published as `-p 127.0.0.1:<port>:4566`, not the bare
# `-p <port>:4566` this script used to use. A bare mapping binds every
# interface, which puts an unauthenticated emulator -- one that will run
# containers for you if the socket is mounted -- on whatever network the
# machine is attached to, and trips the Windows Firewall prompt that first
# exposed it. Nothing that talks to a test instance is off-box: the AWS CLI,
# the SDKs, the compat suites and a headless Chrome all run on the host.
#
# ---------------------------------------------------------------------------
# Why the arguments are parsed by hand
# ---------------------------------------------------------------------------
# An empty param() block, so every argument arrives verbatim in $args and this
# script decides what each one means. That is deliberate, and it is the second
# thing this hardening had to get right after removing the passthrough: a
# declared param() block hands three separate ways to slip a refused flag past
# the check, all of them silent.
#
#   * Positional binding. The first unmatched argument goes to the first
#     positional parameter, so a bare `--privileged` became the *image name*
#     and was reported as a bad image reference rather than as a refused flag.
#   * Prefix matching. PowerShell resolves any unambiguous abbreviation, so
#     `--mount type=bind,src=/,dst=/host` bound -MountDockerSocket and the
#     bind mount's own argument was all that got complained about.
#   * Common parameters. [CmdletBinding()] adds -Verbose and -PipelineVariable,
#     so `-v /:/host` bound -Verbose and `-p 4566:4566` died inside
#     -PipelineVariable's validator with a PowerShell error, exit code 1.
#
# Each was found by a test in scripts/run_test_instance_test.py asserting that
# a refused flag is refused *by name*. Hand-parsing costs tab-completion and
# gains exactly one reading of the command line -- the same one the .sh does,
# which is also what keeps the twins honest.
param()

$ErrorActionPreference = 'Stop'

$Image = 'ghcr.io/neaox/overcast:alpha'
$BasePort = 4570
$Name = ''
$DataVolume = ''
$EnvVar = @()
$MountDockerSocket = $false
$NoLogs = $false
$ShowHelp = $false

$Allowed = '-Image, -BasePort, -Name, -EnvVar KEY=VALUE, -DataVolume, -MountDockerSocket, -NoLogs'

# Flags refused with a reason rather than a shrug. Not a security boundary --
# the allow-list above is -- but the difference between "this script will not
# do that" and "you typed something wrong" is worth spelling out, and these are
# the ones worth spelling it out for. Stored without their dashes and matched
# after both the leading `-`/`--` and any `=value` are stripped, so `-v`,
# `--volume`, `--network=host` and `--privileged=true` all land here.
# -contains is case-insensitive, so `-P` matches the 'p' entry.
$Refused = @(
    'privileged', 'cap-add', 'cap-drop', 'device', 'device-cgroup-rule',
    'security-opt', 'pid', 'ipc', 'uts', 'userns', 'cgroupns', 'cgroup-parent',
    'network', 'net', 'user', 'u', 'volume', 'v', 'mount', 'volumes-from',
    'tmpfs', 'entrypoint', 'sysctl', 'runtime', 'group-add', 'add-host',
    'publish', 'p', 'publish-all', 'restart', 'init', 'detach', 'd', 'rm',
    'DockerArgs'
)

function Show-Usage {
    Write-Output @'
run-test-instance.ps1 -- start a throwaway Overcast instance on a free pair of
loopback ports, never on 4566/4567 (those belong to the user's own instance).

Usage: scripts/run-test-instance.ps1 [options]

  -Image REF             image to run (default ghcr.io/neaox/overcast:alpha)
  -BasePort N            start the free-port scan at N (default 4570)
  -Name NAME             name the container so a later step can stop it
  -EnvVar KEY=VALUE      one container env var; KEY must be OVERCAST_* or
                         AWS_*. Takes an array for several.
  -DataVolume NAME       mount the named Docker volume NAME at /data
  -MountDockerSocket     bind-mount /var/run/docker.sock -- what lets the
                         instance run Lambda and ECS, and lets the console
                         discover its own published port and auto-connect
  -NoLogs                print the endpoints and exit instead of following logs
  -Help                  print this

There is deliberately no `docker run` passthrough. The header of this file says
why, and it is worth reading before granting anyone permission to run it.
'@
}

function Stop-WithUsage {
    param([string]$Message, [string[]]$Detail = @())
    [Console]::Error.WriteLine("run-test-instance.ps1: $Message")
    foreach ($line in $Detail) { [Console]::Error.WriteLine("  $line") }
    exit 2
}

# Deny-listed flags get the policy message; everything else unrecognised gets
# the plain one. Both name the offending argument, and both stop the script --
# an argument is never dropped on the floor.
function Stop-WithRefusal {
    param([string]$Argument)
    $bare = $Argument -replace '^-{1,2}', '' -replace '=.*$', ''
    if ($Refused -contains $bare) {
        Stop-WithUsage "refusing '$Argument'." @(
            'This script takes named options only and does not forward arbitrary',
            'docker run flags: it is granted to agents as a whole script so that the',
            'grant is narrower than a blanket docker run permission, and a passthrough',
            'would erase that difference. See the header.',
            '',
            "Allowed: $Allowed",
            'For the socket use -MountDockerSocket; for a data volume use -DataVolume.',
            'Anything else: run docker yourself, and say in the transcript why.'
        )
    }
    Stop-WithUsage "unknown option '$Argument'." @("Allowed: $Allowed")
}

function Get-FlagValue([object[]]$All, [int]$Index, [string]$Flag) {
    if ($Index + 1 -ge $All.Count) { Stop-WithUsage "$Flag needs a value." }
    return [string]$All[$Index + 1]
}

# Exact matches only, case-insensitively -- no abbreviations, which is the whole
# point of not letting PowerShell bind these. The old -DockerArgs passthrough is
# not here, so it falls through to Stop-WithRefusal and is refused by name rather
# than ignored: somebody's muscle memory, or a stale doc, will reach for it.
$i = 0
while ($i -lt $args.Count) {
    $a = [string]$args[$i]
    switch -Regex ($a) {
        '^-Image$'               { $Image = Get-FlagValue $args $i '-Image'; $i += 2 }
        '^-BasePort$'            { $rawPort = Get-FlagValue $args $i '-BasePort'
                                   if ($rawPort -notmatch '^[0-9]+$') {
                                       Stop-WithUsage "-BasePort takes a number, got '$rawPort'."
                                   }
                                   $BasePort = [int]$rawPort; $i += 2 }
        '^-Name$'                { $Name = Get-FlagValue $args $i '-Name'; $i += 2 }
        '^-DataVolume$'          { $DataVolume = Get-FlagValue $args $i '-DataVolume'; $i += 2 }
        '^-EnvVar$'              { $EnvVar += Get-FlagValue $args $i '-EnvVar'; $i += 2 }
        '^-MountDockerSocket$'   { $MountDockerSocket = $true; $i += 1 }
        '^-NoLogs$'              { $NoLogs = $true; $i += 1 }
        '^(-Help|-h|--help)$'    { $ShowHelp = $true; $i += 1 }
        '^--$'                   { Stop-WithUsage "'--' is no longer a passthrough for docker run arguments." @(
                                       'It was removed on purpose: see the header.',
                                       "Allowed: $Allowed"
                                   ) }
        default                  { Stop-WithRefusal $a }
    }
}

if ($ShowHelp) { Show-Usage; exit 0 }

$dockerArgs = @()

foreach ($pair in $EnvVar) {
    if ($pair -notmatch '=') {
        Stop-WithUsage "-EnvVar takes KEY=VALUE, got '$pair'."
    }
    $key = $pair.Substring(0, $pair.IndexOf('='))
    if ($key -notmatch '^(OVERCAST|AWS)_') {
        Stop-WithUsage "refusing to set '$key'." @(
            '-EnvVar is limited to OVERCAST_* and AWS_* so that it configures the emulator',
            'and nothing else. PATH, LD_PRELOAD and friends are not the emulator''s',
            'configuration surface.'
        )
    }
    if ($key -notmatch '^[A-Z0-9_]+$') {
        Stop-WithUsage "'$key' is not a valid environment variable name (A-Z, 0-9, _)."
    }
    $dockerArgs += @('-e', $pair)
}

# An image reference that starts with '-' would be read by docker as a flag,
# which is the one way a value could still become one.
if ($Image -eq '' -or $Image.StartsWith('-')) {
    Stop-WithUsage "-Image needs an image reference, got '$Image'."
}
if ($Image -notmatch '^[A-Za-z0-9._:/@-]+$') {
    Stop-WithUsage "'$Image' is not a valid image reference."
}
if ($BasePort -lt 1024 -or $BasePort -gt 64000) {
    Stop-WithUsage "-BasePort must be between 1024 and 64000, got '$BasePort'."
}

if ($Name -ne '') {
    if ($Name -notmatch '^[A-Za-z0-9][A-Za-z0-9_.-]*$') {
        Stop-WithUsage "'$Name' is not a valid container name."
    }
    $dockerArgs += @('--name', $Name)
}
if ($DataVolume -ne '') {
    # A named volume, never a host path: a bind mount is the passthrough this
    # script exists to not have.
    if ($DataVolume -notmatch '^[A-Za-z0-9][A-Za-z0-9_.-]*$') {
        Stop-WithUsage "-DataVolume takes a named Docker volume, got '$DataVolume'."
    }
    $dockerArgs += @('-v', "${DataVolume}:/data")
}
if ($MountDockerSocket) {
    $dockerArgs += @('-v', '/var/run/docker.sock:/var/run/docker.sock')
}

# Get-NetTCPConnection comes from the NetTCPIP module, which ships only with
# Windows. Under pwsh on Linux or macOS the cmdlet does not exist, and with
# $ErrorActionPreference = 'Stop' the missing name killed this script at the
# port scan -- before it reached docker at all.
#
# That was not merely inconvenient. CI runs the script tests on Ubuntu, where
# the only PowerShell host is pwsh, so every assertion about the command line
# this script builds got an empty one and the suite could not verify the
# argument handling that the permission grant rests on. A refusal that is only
# tested on one developer's machine is not a tested refusal.
#
# The Windows path is unchanged. The fallback asks the same question a
# different way -- can something be reached on this loopback port -- which is
# also what run-test-instance.sh falls back to when netstat is absent. It is
# the more precise question here anyway, now that both ports are published to
# 127.0.0.1: a listener bound only to some other interface cannot collide with
# us, and Get-NetTCPConnection would count it as a collision.
$script:HaveGetNetTCPConnection = [bool](Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue)

function Test-PortFree([int]$Port) {
    if ($script:HaveGetNetTCPConnection) {
        return -not (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
    }
    $client = [System.Net.Sockets.TcpClient]::new()
    try {
        # Connect succeeded => something is listening => the port is not free.
        # A refused connection faults the task and Wait throws, which is the
        # free case.
        if ($client.ConnectAsync('127.0.0.1', $Port).Wait(250) -and $client.Connected) {
            return $false
        }
        return $true
    } catch {
        return $true
    } finally {
        $client.Dispose()
    }
}

# 4566 and 4567 are the user's, in either role. Checking each port against both
# is not pedantry -- the guard used to compare the API port against 4566 and the
# UI port against 4567 only, so -BasePort 4565 handed the *UI* the user's API
# port and -BasePort 4567 handed the *API* the user's UI port. Both silently
# break whatever the user is doing, which is the one outcome this whole scan
# exists to avoid.
$reservedPorts = @(4566, 4567)

$apiPort = 0
for ($p = $BasePort; $p -lt 65000; $p += 2) {
    $ui = $p + 1
    if ($reservedPorts -notcontains $p -and $reservedPorts -notcontains $ui -and (Test-PortFree $p) -and (Test-PortFree $ui)) {
        $apiPort = $p
        $uiPort = $ui
        break
    }
}
if ($apiPort -eq 0) { Write-Error "no free port pair found from $BasePort"; exit 1 }

$runArgs = @('run', '-d', '--rm',
    '-p', "127.0.0.1:${apiPort}:4566",
    '-p', "127.0.0.1:${uiPort}:4567") + $dockerArgs + @($Image)
$cid = & docker @runArgs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Output "container: $cid"
Write-Output "API endpoint: http://localhost:$apiPort"
Write-Output "Web UI:       http://localhost:$uiPort"
Write-Output "stop with:    docker stop $($cid.Substring(0, [Math]::Min(12, $cid.Length)))"
if ($NoLogs) { exit 0 }
Write-Output "--- logs (Ctrl-C detaches; container keeps running) ---"
& docker logs -f $cid
