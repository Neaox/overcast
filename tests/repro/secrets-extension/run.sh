#!/usr/bin/env bash
# Reproduce the Lambda hang when a function fetches a secret through the AWS
# Parameters and Secrets Lambda Extension.
#
#   OVERCAST_ENDPOINT=http://localhost:4580 \
#   LAYER_ZIP=F:/dev/aws-layers/AWS-Parameters-and-Secrets-Lambda-Extension_90.zip \
#   ./run.sh
#
# The extension layer is AWS-managed, so it cannot be synthesised: fetch the zip
# once with `aws lambda get-layer-version-by-arn` against real AWS, then this
# script publishes it into the target Overcast and hands the ARN to CDK.

set -euo pipefail

ENDPOINT="${OVERCAST_ENDPOINT:-http://localhost:4566}"
LAYER_ZIP="${LAYER_ZIP:?set LAYER_ZIP to the cached extension layer zip}"
REGION="${AWS_REGION:-ap-southeast-2}"

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_REGION="$REGION"
export AWS_DEFAULT_REGION="$REGION"
export AWS_ENDPOINT_URL="$ENDPOINT"

aws() { command aws --endpoint-url "$ENDPOINT" "$@"; }

echo "==> publishing the extension layer into $ENDPOINT"
EXTENSION_LAYER_ARN=$(aws lambda publish-layer-version \
  --layer-name AWS-Parameters-and-Secrets-Lambda-Extension \
  --zip-file "fileb://$LAYER_ZIP" \
  --query LayerVersionArn --output text)
export EXTENSION_LAYER_ARN
echo "    $EXTENSION_LAYER_ARN"

echo "==> deploying the stack"
npx cdk deploy --require-approval never --outputs-file outputs.json

FUNCTION_NAME=$(node -e "console.log(require('./outputs.json').SecretsExtensionRepro.FunctionName)")

echo "==> invoking $FUNCTION_NAME"
time aws lambda invoke --function-name "$FUNCTION_NAME" \
  --payload '{"company":4}' --cli-binary-format raw-in-base64-out \
  response.json || true
cat response.json
echo
