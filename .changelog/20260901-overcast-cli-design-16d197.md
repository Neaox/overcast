+ [cli] `overcast env` prints AWS environment exports for sh, PowerShell, and fish.
  includes unset lines for any other `AWS_*` variables the shell exports (`AWS_PROFILE`, `AWS_SESSION_TOKEN`, per-service endpoint overrides, …) so nothing left over can redirect a call to real AWS
+ [cli] `overcast aws` runs the host AWS CLI against the emulator with ambient `AWS_*` variables scrubbed
+ [cli] `overcast aws` tab-completes its passthrough arguments via the AWS CLI's own `aws_completer` when installed.
  falls back to file completion otherwise
+ [cli] `overcast wait` blocks until the daemon reports healthy
+ [cli] `overcast services` lists enabled services and their emulation tiers
