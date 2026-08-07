#!/usr/bin/env pwsh
$ErrorActionPreference = "Continue"
$api = "http://localhost:4570"
$aws = "C:\Program Files\Amazon\AWSCLIV2\aws.exe"
$env:AWS_ACCESS_KEY_ID = "test"
$env:AWS_SECRET_ACCESS_KEY = "test"
$env:AWS_DEFAULT_REGION = "us-east-1"
$env:AWS_PAGER = ""
$env:AWS_ENDPOINT_URL = $api
$env:CDK_DEFAULT_ACCOUNT = "000000000000"
$env:CDK_DEFAULT_REGION = "us-east-1"

Write-Host "=== Rebuilding overcast ===" -ForegroundColor Cyan
docker build -t overcast:dev-fix -f Dockerfile . -q
docker ps -q | ForEach-Object { docker stop $_ }
docker run -d --rm -p 4570:4566 -p 4571:4567 -e OVERCAST_DEBUG=true -v /var/run/docker.sock:/var/run/docker.sock overcast:dev-fix
Start-Sleep -Seconds 6

function aws { & $aws --endpoint-url $api @Args | Out-Null }

Write-Host "--- SQS ---" -ForegroundColor Yellow
aws sqs create-queue --queue-name orders
aws sqs create-queue --queue-name notifications
aws sqs list-queues

Write-Host "--- SNS ---" -ForegroundColor Yellow
aws sns create-topic --name alerts
aws sns list-topics
aws sns subscribe --topic-arn "arn:aws:sns:us-east-1:000000000000:alerts" --protocol sqs --notification-endpoint "arn:aws:sqs:us-east-1:000000000000:notifications"

Write-Host "--- DynamoDB ---" -ForegroundColor Yellow
aws dynamodb create-table --table-name users --attribute-definitions "AttributeName=id,AttributeType=S" --key-schema "AttributeName=id,KeyType=HASH" --billing-mode PAY_PER_REQUEST
aws dynamodb list-tables
'{"TableName":"users","Item":{"id":{"S":"1"},"name":{"S":"Alice"}}}' | Set-Content "$env:TEMP\dyn-item.json" -Encoding utf8
'{"TableName":"users","Key":{"id":{"S":"1"}}}' | Set-Content "$env:TEMP\dyn-key.json" -Encoding utf8
aws dynamodb put-item --cli-input-json "file://$env:TEMP\dyn-item.json" 2>$null
aws dynamodb get-item --cli-input-json "file://$env:TEMP\dyn-key.json" 2>$null

Write-Host "--- Lambda ---" -ForegroundColor Yellow
aws lambda list-functions
aws lambda create-function --function-name echo --runtime nodejs22.x --role "arn:aws:iam::000000000000:role/lambda" --handler index.handler --zip-file fileb://scripts/echo-lambda.zip 2>$null
aws lambda invoke --function-name echo --payload '{"hello":"world"}' /dev/stdout 2>$null | Out-Null

Write-Host "--- KMS ---" -ForegroundColor Yellow
aws kms create-key --key-usage ENCRYPT_DECRYPT
aws kms list-keys

Write-Host "--- SSM ---" -ForegroundColor Yellow
aws ssm put-parameter --name /app/config --value prod --type String --overwrite
aws ssm get-parameter --name /app/config

Write-Host "--- IAM ---" -ForegroundColor Yellow
aws iam list-roles

Write-Host "--- S3 ---" -ForegroundColor Yellow
aws s3 mb s3://test-bucket
aws s3 ls

Write-Host "--- CloudFormation ---" -ForegroundColor Yellow
aws cloudformation describe-stacks

Write-Host "--- Secrets Manager ---" -ForegroundColor Yellow
aws secretsmanager create-secret --name db-password --secret-string s3cret
aws secretsmanager list-secrets

Write-Host "--- CloudWatch Logs ---" -ForegroundColor Yellow
aws logs describe-log-groups

Write-Host "--- Step Functions ---" -ForegroundColor Yellow
aws stepfunctions list-state-machines

Write-Host "--- STS ---" -ForegroundColor Yellow
aws sts get-caller-identity

Write-Host "--- EventBridge ---" -ForegroundColor Yellow
aws events list-event-buses

Write-Host "--- ECS ---" -ForegroundColor Yellow
aws ecs list-clusters

Write-Host "--- CloudFormation (creates hops) ---" -ForegroundColor Yellow
$body = '{\"Resources\":{\"Q\":{\"Type\":\"AWS::SQS::Queue\"},\"T\":{\"Type\":\"AWS::SNS::Topic\"}}}'
aws cloudformation create-stack --stack-name trace-test --template-body $body 2>$null

if (Get-Command cdk -ErrorAction SilentlyContinue) {
  Write-Host "--- CDK (creates hops) ---" -ForegroundColor Yellow
  Push-Location scripts
  try { cdk deploy --app cdk-test.js --require-approval never 2>$null } finally { Pop-Location }
}

Write-Host "Done - http://localhost:4571/debug/traces" -ForegroundColor Green
