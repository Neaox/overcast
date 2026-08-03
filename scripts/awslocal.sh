#!/bin/sh
# awslocal.sh — run the AWS CLI against a local Overcast instance, with the
# ambient AWS environment stripped first.
#
# The problem this solves: a developer machine usually has AWS_PROFILE,
# AWS_REGION, SSO state, or an AWS_ENDPOINT_URL left over from something else.
# Those leak into every `aws` call and change what happens in ways that do not
# announce themselves — a wrong profile silently signs as a different
# principal, a stale AWS_ENDPOINT_URL sends the call somewhere else entirely.
# The failure looks like a bug in Overcast, and it costs an hour to find.
#
# So this does not unset a curated list of the variables that bite most often.
# It unsets *every* AWS_* variable in the environment and then sets back only
# the four a local call needs. A new AWS_* variable invented by a future SDK
# release is covered without anyone updating this script.
#
# Only this process is affected. Your shell keeps its own environment.
#
# Usage:
#   scripts/awslocal.sh s3 mb s3://my-bucket
#   scripts/awslocal.sh sqs create-queue --queue-name my-queue
#
#   OVERCAST_ENDPOINT=http://localhost:4580 scripts/awslocal.sh sqs list-queues
#   OVERCAST_PORT=4580 scripts/awslocal.sh sqs list-queues     # same thing
#
# Configuration (these are read *before* the sweep, so they still work):
#   OVERCAST_ENDPOINT   full base URL. Wins over OVERCAST_PORT.
#   OVERCAST_PORT       port on localhost. Default 4566.
#   OVERCAST_REGION     default us-east-1.
#
# NOT for endpoint testing. This passes --endpoint-url explicitly, which is
# exactly what suppresses the SQS queue-URL origin override in the JS/.NET/Java
# SDKs — the bug class docs/dev/manual-testing.md exists to catch. Verifying
# URL minting, origins or routing needs a *bare* SDK client with no endpoint
# configured at all. Use this for setup and for everything else.
set -eu

endpoint="${OVERCAST_ENDPOINT:-http://localhost:${OVERCAST_PORT:-4566}}"
region="${OVERCAST_REGION:-us-east-1}"

# Unset every AWS_* variable. Names only ever contain [A-Za-z0-9_], so reading
# them off `env` is safe even when a value contains newlines or spaces.
for name in $(env | sed -n 's/^\(AWS_[A-Za-z0-9_]*\)=.*/\1/p'); do
    unset "$name" || true
done

# Overcast accepts any credentials and validates none of them, so these are
# placeholders that exist only because the SDKs refuse to sign without them.
AWS_ACCESS_KEY_ID=test
AWS_SECRET_ACCESS_KEY=test
AWS_DEFAULT_REGION="$region"
AWS_REGION="$region"
# Never page: an agent or CI job piping output would otherwise hang on `less`.
AWS_PAGER=""
# Do not let the CLI hunt for instance-profile credentials it will not find.
AWS_EC2_METADATA_DISABLED=true
export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION AWS_REGION \
    AWS_PAGER AWS_EC2_METADATA_DISABLED

# A profile is resolved from the config files even with no AWS_PROFILE set, so
# point both files at an empty one rather than trusting that ~/.aws is
# uninteresting.
#
# An empty temp file, not /dev/null. Under Git Bash the `aws` on PATH is the
# native Windows build, which cannot open a POSIX path: /dev/null reaches it
# unconverted whenever MSYS path conversion is off (MSYS_NO_PATHCONV=1, which
# other calls on this machine need) and every command then dies with
# "Unable to parse config file: /dev/null". cygpath gives the native form when
# we are on Windows and is absent everywhere else, where the path is already
# fine.
empty_config="$(mktemp)"
if command -v cygpath >/dev/null 2>&1; then
    AWS_CONFIG_FILE="$(cygpath -w "$empty_config")"
else
    AWS_CONFIG_FILE="$empty_config"
fi
AWS_SHARED_CREDENTIALS_FILE="$AWS_CONFIG_FILE"
export AWS_CONFIG_FILE AWS_SHARED_CREDENTIALS_FILE

# Not exec: the temp file has to be cleaned up after the CLI exits, so run it
# as a child and pass its status on. `|| status=$?` rather than a bare call,
# because `set -e` would otherwise exit here the moment the CLI returns
# non-zero — which is every ordinary AWS error — and leak the temp file.
status=0
aws --endpoint-url "$endpoint" "$@" || status=$?
rm -f "$empty_config"
exit "$status"
