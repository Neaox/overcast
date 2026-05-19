#!/usr/bin/env bash
# cleanup-oc-resources.sh — Delete all emulator resources left over from compat
# test runs. Resources are identified by the "oc-" prefix that the test harness
# uses for every runId.
#
# Usage: bash scripts/cleanup-oc-resources.sh [endpoint]
# Defaults to http://localhost:4566

set -euo pipefail

ENDPOINT="${1:-http://localhost:4566}"
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1
export AWS_PAGER=""

AWS="aws --endpoint-url=$ENDPOINT"

log()  { echo "[cleanup] $*"; }
warn() { echo "[cleanup] WARN: $*" >&2; }

# ── S3 ────────────────────────────────────────────────────────────────────────
log "S3: listing buckets..."
BUCKETS=$($AWS s3api list-buckets \
  --query 'Buckets[?starts_with(Name, `oc-`)].Name' \
  --output text 2>/dev/null || true)

for bucket in $BUCKETS; do
  log "S3: deleting bucket $bucket (purging objects + versions first)"
  # Delete all versioned objects
  versions=$($AWS s3api list-object-versions --bucket "$bucket" \
    --query '{Objects: Versions[].{Key:Key,VersionId:VersionId}, DeleteMarkers: DeleteMarkers[].{Key:Key,VersionId:VersionId}}' \
    --output json 2>/dev/null || echo '{}')
  objects=$(echo "$versions" | python3 -c "
import sys, json
d = json.load(sys.stdin)
items = (d.get('Objects') or []) + (d.get('DeleteMarkers') or [])
if items:
    print(json.dumps({'Objects': [i for i in items if i]}))
" 2>/dev/null || true)
  if [ -n "$objects" ] && [ "$objects" != "null" ]; then
    $AWS s3api delete-objects --bucket "$bucket" --delete "$objects" >/dev/null 2>&1 || warn "delete-objects failed for $bucket"
  fi
  # Abort any in-progress multipart uploads
  uploads=$($AWS s3api list-multipart-uploads --bucket "$bucket" \
    --query 'Uploads[*].{Key:Key,UploadId:UploadId}' \
    --output json 2>/dev/null || echo '[]')
  echo "$uploads" | python3 -c "
import sys, json, subprocess, os
uploads = json.load(sys.stdin) or []
for u in uploads:
    subprocess.run([
        'aws', '--endpoint-url', os.environ['ENDPOINT'],
        's3api', 'abort-multipart-upload',
        '--bucket', sys.argv[1], '--key', u['Key'], '--upload-id', u['UploadId']
    ], env={**os.environ}, capture_output=True)
" "$bucket" 2>/dev/null || true
  # Delete remaining objects (non-versioned)
  keys=$($AWS s3api list-objects-v2 --bucket "$bucket" \
    --query 'Contents[*].{Key:Key}' --output json 2>/dev/null || echo '[]')
  non_empty=$(echo "$keys" | python3 -c "import sys,json; d=json.load(sys.stdin); print(json.dumps({'Objects':d}) if d else '')" 2>/dev/null || true)
  if [ -n "$non_empty" ]; then
    $AWS s3api delete-objects --bucket "$bucket" --delete "$non_empty" >/dev/null 2>&1 || true
  fi
  $AWS s3api delete-bucket --bucket "$bucket" 2>/dev/null \
    && log "S3: deleted $bucket" \
    || warn "S3: failed to delete $bucket"
done

# ── SQS ───────────────────────────────────────────────────────────────────────
log "SQS: listing queues..."
QUEUES=$($AWS sqs list-queues --queue-name-prefix oc- \
  --query 'QueueUrls' --output text 2>/dev/null || true)
for url in $QUEUES; do
  $AWS sqs delete-queue --queue-url "$url" 2>/dev/null \
    && log "SQS: deleted $url" \
    || warn "SQS: failed to delete $url"
done

# ── SNS ───────────────────────────────────────────────────────────────────────
log "SNS: listing topics..."
TOPICS=$($AWS sns list-topics --query 'Topics[*].TopicArn' --output text 2>/dev/null || true)
for arn in $TOPICS; do
  name="${arn##*:}"
  if [[ "$name" == oc-* ]]; then
    # Unsubscribe all subscriptions first
    subs=$($AWS sns list-subscriptions-by-topic --topic-arn "$arn" \
      --query 'Subscriptions[*].SubscriptionArn' --output text 2>/dev/null || true)
    for sub in $subs; do
      [[ "$sub" == "PendingConfirmation" ]] && continue
      $AWS sns unsubscribe --subscription-arn "$sub" 2>/dev/null || true
    done
    $AWS sns delete-topic --topic-arn "$arn" 2>/dev/null \
      && log "SNS: deleted $arn" \
      || warn "SNS: failed to delete $arn"
  fi
done

# ── DynamoDB ──────────────────────────────────────────────────────────────────
log "DynamoDB: listing tables..."
TABLES=$($AWS dynamodb list-tables \
  --query 'TableNames[?starts_with(@, `oc-`)]' \
  --output text 2>/dev/null || true)
for table in $TABLES; do
  $AWS dynamodb delete-table --table-name "$table" >/dev/null 2>/dev/null \
    && log "DynamoDB: deleted $table" \
    || warn "DynamoDB: failed to delete $table"
done

# ── Lambda functions ──────────────────────────────────────────────────────────
log "Lambda: listing functions..."
FUNCTIONS=$($AWS lambda list-functions \
  --query 'Functions[?starts_with(FunctionName, `oc-`)].FunctionName' \
  --output text 2>/dev/null || true)
for fn in $FUNCTIONS; do
  $AWS lambda delete-function --function-name "$fn" 2>/dev/null \
    && log "Lambda: deleted function $fn" \
    || warn "Lambda: failed to delete function $fn"
done

# ── Lambda layers ─────────────────────────────────────────────────────────────
log "Lambda: listing layers..."
LAYERS=$($AWS lambda list-layers \
  --query 'Layers[?starts_with(LayerName, `oc-`)].{Name:LayerName,Version:LatestMatchingVersion.Version}' \
  --output text 2>/dev/null || true)
while IFS=$'\t' read -r name version; do
  [ -z "$name" ] && continue
  $AWS lambda delete-layer-version --layer-name "$name" --version-number "$version" 2>/dev/null \
    && log "Lambda: deleted layer $name v$version" \
    || warn "Lambda: failed to delete layer $name v$version"
done <<< "$LAYERS"

# ── KMS keys (aliases with oc- prefix) ───────────────────────────────────────
log "KMS: listing aliases..."
KMS_ALIASES=$($AWS kms list-aliases \
  --query 'Aliases[?starts_with(AliasName, `alias/compat-oc-`)].{Alias:AliasName,Key:TargetKeyId}' \
  --output text 2>/dev/null || true)
while IFS=$'\t' read -r alias_name key_id; do
  [ -z "$alias_name" ] && continue
  $AWS kms delete-alias --alias-name "$alias_name" 2>/dev/null \
    && log "KMS: deleted alias $alias_name" \
    || warn "KMS: failed to delete alias $alias_name"
  if [ -n "$key_id" ]; then
    $AWS kms schedule-key-deletion --key-id "$key_id" --pending-window-in-days 7 >/dev/null 2>/dev/null \
      && log "KMS: scheduled deletion of key $key_id" \
      || warn "KMS: failed to schedule deletion of key $key_id"
  fi
done <<< "$KMS_ALIASES"

# ── SecretsManager ────────────────────────────────────────────────────────────
log "SecretsManager: listing secrets..."
SECRETS=$($AWS secretsmanager list-secrets \
  --query 'SecretList[?starts_with(Name, `oc-`)].Name' \
  --output text 2>/dev/null || true)
for secret in $SECRETS; do
  $AWS secretsmanager delete-secret --secret-id "$secret" \
    --force-delete-without-recovery >/dev/null 2>/dev/null \
    && log "SecretsManager: deleted $secret" \
    || warn "SecretsManager: failed to delete $secret"
done

# ── SSM parameters ────────────────────────────────────────────────────────────
log "SSM: listing parameters..."
PARAMS=$($AWS ssm describe-parameters \
  --parameter-filters "Key=Name,Option=BeginsWith,Values=/oc-" \
  --query 'Parameters[*].Name' \
  --output text 2>/dev/null || true)
for param in $PARAMS; do
  $AWS ssm delete-parameter --name "$param" 2>/dev/null \
    && log "SSM: deleted $param" \
    || warn "SSM: failed to delete $param"
done

# ── CloudWatch Logs ───────────────────────────────────────────────────────────
log "Logs: listing log groups..."
LOG_GROUPS=$($AWS logs describe-log-groups \
  --query 'logGroups[?starts_with(logGroupName, `/overcast/oc-`) || starts_with(logGroupName, `/aws/lambda/oc-`)].logGroupName' \
  --output text 2>/dev/null || true)
for lg in $LOG_GROUPS; do
  $AWS logs delete-log-group --log-group-name "$lg" 2>/dev/null \
    && log "Logs: deleted $lg" \
    || warn "Logs: failed to delete $lg"
done

# ── EventBridge ───────────────────────────────────────────────────────────────
log "EventBridge: listing rules on default bus..."
EB_RULES=$($AWS events list-rules \
  --query 'Rules[?starts_with(Name, `oc-`)].Name' \
  --output text 2>/dev/null || true)
for rule in $EB_RULES; do
  # Remove targets before deleting rule
  targets=$($AWS events list-targets-by-rule --rule "$rule" \
    --query 'Targets[*].Id' --output text 2>/dev/null || true)
  if [ -n "$targets" ]; then
    $AWS events remove-targets --rule "$rule" --ids $targets >/dev/null 2>/dev/null || true
  fi
  $AWS events delete-rule --name "$rule" 2>/dev/null \
    && log "EventBridge: deleted rule $rule" \
    || warn "EventBridge: failed to delete rule $rule"
done

log "EventBridge: listing custom buses..."
EB_BUSES=$($AWS events list-event-buses \
  --query 'EventBuses[?starts_with(Name, `oc-`)].Name' \
  --output text 2>/dev/null || true)
for bus in $EB_BUSES; do
  $AWS events delete-event-bus --name "$bus" 2>/dev/null \
    && log "EventBridge: deleted bus $bus" \
    || warn "EventBridge: failed to delete bus $bus"
done

# ── Kinesis streams ───────────────────────────────────────────────────────────
log "Kinesis: listing streams..."
STREAMS=$($AWS kinesis list-streams \
  --query 'StreamNames[?starts_with(@, `oc-`)]' \
  --output text 2>/dev/null || true)
for stream in $STREAMS; do
  $AWS kinesis delete-stream --stream-name "$stream" 2>/dev/null \
    && log "Kinesis: deleted $stream" \
    || warn "Kinesis: failed to delete $stream"
done

log "Done. All oc- resources cleaned up (or warnings printed for failures)."
