# run-test-instance.ps1 — PowerShell twin of run-test-instance.sh: start a
# throwaway Overcast instance on FREE ports, never on the user-reserved
# defaults (4566 API / 4567 web UI). See the .sh header for the full contract.
#
# Usage:
#   scripts/run-test-instance.ps1
#   scripts/run-test-instance.ps1 -BasePort 4600
#   scripts/run-test-instance.ps1 -Image myimage:dev -DockerArgs @('-e','OVERCAST_STATE=hybrid','-v','myvol:/data')
param(
    [string]$Image = "ghcr.io/neaox/overcast:latest",
    [int]$BasePort = 4570,
    [string[]]$DockerArgs = @()
)

function Test-PortFree([int]$Port) {
    -not (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
}

$apiPort = 0
for ($p = $BasePort; $p -lt 65000; $p += 2) {
    $ui = $p + 1
    if ($p -ne 4566 -and $ui -ne 4567 -and (Test-PortFree $p) -and (Test-PortFree $ui)) {
        $apiPort = $p
        $uiPort = $ui
        break
    }
}
if ($apiPort -eq 0) { Write-Error "no free port pair found from $BasePort"; exit 1 }

$runArgs = @('run', '-d', '--rm', '-p', "${apiPort}:4566", '-p', "${uiPort}:4567") + $DockerArgs + @($Image)
$cid = & docker @runArgs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Output "container: $cid"
Write-Output "API endpoint: http://localhost:$apiPort"
Write-Output "Web UI:       http://localhost:$uiPort"
Write-Output "stop with:    docker stop $($cid.Substring(0, 12))"
Write-Output "--- logs (Ctrl-C detaches; container keeps running) ---"
& docker logs -f $cid
