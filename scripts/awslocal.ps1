<#
.SYNOPSIS
    Run the AWS CLI against a local Overcast instance, with the ambient AWS
    environment stripped first. PowerShell twin of scripts/awslocal.sh.

.DESCRIPTION
    A developer machine usually has AWS_PROFILE, AWS_REGION, SSO state, or an
    AWS_ENDPOINT_URL left over from something else. Those leak into every `aws`
    call and change what happens without announcing themselves -- a wrong
    profile silently signs as a different principal, a stale AWS_ENDPOINT_URL
    sends the call somewhere else entirely. The failure looks like a bug in
    Overcast and costs an hour to find.

    So this does not unset a curated list. It removes *every* AWS_* variable
    and sets back only the four a local call needs, then restores your session
    exactly as it was. A new AWS_* variable invented by a future SDK release is
    covered without anyone updating this script.

    PowerShell scripts share the caller's process environment, so the removal
    is undone in a finally block rather than relying on process exit.

    NOT for endpoint testing. This passes --endpoint-url explicitly, which is
    exactly what suppresses the SQS queue-URL origin override in the
    JS/.NET/Java SDKs -- the bug class docs/dev/manual-testing.md exists to
    catch. Verifying URL minting, origins or routing needs a *bare* SDK client
    with no endpoint configured at all.

.PARAMETER Args
    Arguments passed through to `aws`.

.EXAMPLE
    scripts\awslocal.ps1 s3 mb s3://my-bucket

.EXAMPLE
    $env:OVERCAST_PORT = 4580
    scripts\awslocal.ps1 sqs list-queues
#>
[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$Args
)

# Read configuration before the sweep, since OVERCAST_* survives it anyway but
# the intent is clearer read together.
$endpoint = if ($env:OVERCAST_ENDPOINT) {
    $env:OVERCAST_ENDPOINT
} else {
    $port = if ($env:OVERCAST_PORT) { $env:OVERCAST_PORT } else { '4566' }
    "http://localhost:$port"
}
$region = if ($env:OVERCAST_REGION) { $env:OVERCAST_REGION } else { 'us-east-1' }

$saved = @{}
foreach ($item in Get-ChildItem Env: | Where-Object { $_.Name -like 'AWS_*' }) {
    $saved[$item.Name] = $item.Value
    Remove-Item "Env:$($item.Name)"
}

try {
    # Overcast accepts any credentials and validates none of them, so these are
    # placeholders that exist only because the SDKs refuse to sign without them.
    $env:AWS_ACCESS_KEY_ID = 'test'
    $env:AWS_SECRET_ACCESS_KEY = 'test'
    $env:AWS_DEFAULT_REGION = $region
    $env:AWS_REGION = $region
    # Never page: a CI job or agent piping output would otherwise hang.
    $env:AWS_PAGER = ''
    # Do not let the CLI hunt for instance-profile credentials it will not find.
    $env:AWS_EC2_METADATA_DISABLED = 'true'
    # A profile is resolved from the config files even with no AWS_PROFILE set.
    $env:AWS_CONFIG_FILE = 'NUL'
    $env:AWS_SHARED_CREDENTIALS_FILE = 'NUL'

    & aws --endpoint-url $endpoint @Args
    exit $LASTEXITCODE
} finally {
    foreach ($name in (Get-ChildItem Env: | Where-Object { $_.Name -like 'AWS_*' } | Select-Object -ExpandProperty Name)) {
        Remove-Item "Env:$name"
    }
    foreach ($name in $saved.Keys) {
        Set-Item "Env:$name" $saved[$name]
    }
}
