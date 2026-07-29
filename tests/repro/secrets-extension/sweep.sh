#!/usr/bin/env bash
# Sweep secret sizes through the extension, one cold container per run.
#
# Each size gets its own secret name so the extension's 5-minute cache cannot
# serve a later run, and the function's configuration is bumped between runs so
# the pool cannot hand back a warm container that already holds the answer.

set -uo pipefail

ENDPOINT="${OVERCAST_ENDPOINT:-http://localhost:4580}"
FUNCTION="${FUNCTION_NAME:-repro-queue-listing-updates}"
SIZES="${SIZES:-500 2000 4000 8000 16000 32000 65000}"
REPEATS="${REPEATS:-1}"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export AWS_REGION="${AWS_REGION:-ap-southeast-2}" AWS_DEFAULT_REGION="${AWS_REGION:-ap-southeast-2}"
export MSYS_NO_PATHCONV=1

aws() { command aws --endpoint-url "$ENDPOINT" "$@"; }

for run in $(seq 1 "$REPEATS"); do
  for size in $SIZES; do
    name="sm-sweep-${size}-${run}-$RANDOM"
    python -c "open('sv.txt','w').write('x'*$size)"
    aws secretsmanager create-secret --name "$name" --secret-string file://sv.txt >/dev/null 2>&1

    # Bump the configuration so the next invoke gets a cold container.
    aws lambda update-function-configuration --function-name "$FUNCTION" \
      --environment "Variables={SECRET_NAME=$name,PARAMETERS_SECRETS_EXTENSION_LOG_LEVEL=debug}" \
      >/dev/null 2>&1
    sleep 4

    start=$(date +%s%N)
    aws lambda invoke --function-name "$FUNCTION" --payload '{"company":4}' \
      --cli-binary-format raw-in-base64-out r.json >/dev/null 2>&1
    ms=$(( ($(date +%s%N) - start) / 1000000 ))

    if grep -q "Task timed out" r.json 2>/dev/null; then
      printf 'size=%-6s run=%s  HUNG   %s ms\n' "$size" "$run" "$ms"
    else
      printf 'size=%-6s run=%s  ok     %s ms  %s\n' "$size" "$run" "$ms" "$(head -c 60 r.json)"
    fi
  done
done
