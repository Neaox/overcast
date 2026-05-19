import {
  DescribeStacksCommand,
  ListStacksCommand,
  type Output,
} from "@aws-sdk/client-cloudformation";
import { DescribeLogGroupsCommand } from "@aws-sdk/client-cloudwatch-logs";
import { DescribeTableCommand, PutItemCommand } from "@aws-sdk/client-dynamodb";
import {
  DescribeSecurityGroupsCommand,
  DescribeVpcsCommand,
} from "@aws-sdk/client-ec2";
import { DescribeEventBusCommand } from "@aws-sdk/client-eventbridge";
import { GetPolicyCommand, GetRoleCommand } from "@aws-sdk/client-iam";
import { DescribeKeyCommand } from "@aws-sdk/client-kms";
import {
  GetEventSourceMappingCommand,
  GetFunctionConfigurationCommand,
  ListEventSourceMappingsCommand,
} from "@aws-sdk/client-lambda";
import { ListBucketsCommand } from "@aws-sdk/client-s3";
import { DescribeSecretCommand } from "@aws-sdk/client-secrets-manager";
import { DescribeStateMachineCommand } from "@aws-sdk/client-sfn";
import {
  ListSubscriptionsByTopicCommand,
  PublishCommand,
} from "@aws-sdk/client-sns";
import {
  GetQueueAttributesCommand,
  GetQueueUrlCommand,
  ReceiveMessageCommand,
} from "@aws-sdk/client-sqs";
import { GetParameterCommand } from "@aws-sdk/client-ssm";
import { GetRestApisCommand } from "@aws-sdk/client-api-gateway";
import assert from "node:assert/strict";
import { execCmd } from "../lib/exec.js";
import { makeClients } from "../lib/clients.js";
import type { TestContext, TestGroup } from "../lib/harness.js";

function cdkEnv(
  ctx: TestContext,
  extra?: Record<string, string>,
): NodeJS.ProcessEnv {
  return {
    ...process.env,
    OVERCAST_COMPAT_RUN_ID: ctx.runId,
    OVERCAST_ENDPOINT: ctx.endpoint,
    AWS_ENDPOINT_URL: ctx.endpoint,
    AWS_ACCESS_KEY_ID: process.env["AWS_ACCESS_KEY_ID"] ?? "test",
    AWS_SECRET_ACCESS_KEY: process.env["AWS_SECRET_ACCESS_KEY"] ?? "test",
    AWS_DEFAULT_REGION: ctx.region,
    AWS_REGION: ctx.region,
    ...extra,
  };
}

async function runCdk(
  ctx: TestContext,
  args: string[],
  extraEnv?: Record<string, string>,
): Promise<void> {
  await execCmd("npx", ["cdk", ...args], {
    cwd: process.cwd(),
    env: cdkEnv(ctx, extraEnv),
  });
}

function outputMap(outputs: Output[] | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const o of outputs ?? []) {
    if (o.OutputKey && o.OutputValue) out[o.OutputKey] = o.OutputValue;
  }
  return out;
}

async function fetchOutputs(ctx: TestContext): Promise<Record<string, string>> {
  const { cloudformation } = makeClients(ctx.endpoint, ctx.region);
  const resp = await cloudformation.send(
    new DescribeStacksCommand({ StackName: ctx.stackName }),
  );
  const stack = resp.Stacks?.[0];
  assert.ok(stack, `stack ${ctx.stackName} not found in DescribeStacks`);
  return outputMap(stack.Outputs);
}

async function verifyStackStatus(ctx: TestContext): Promise<void> {
  const { cloudformation } = makeClients(ctx.endpoint, ctx.region);
  const resp = await cloudformation.send(
    new DescribeStacksCommand({ StackName: ctx.stackName }),
  );
  const stack = resp.Stacks?.[0];
  assert.ok(stack, "expected stack details from DescribeStacks");
  assert.ok(
    stack.StackStatus === "CREATE_COMPLETE" ||
      stack.StackStatus === "UPDATE_COMPLETE",
    `expected CREATE_COMPLETE/UPDATE_COMPLETE, got ${String(stack.StackStatus)}`,
  );
  const outputs = outputMap(stack.Outputs);
  (ctx as Record<string, unknown>)["_outputs"] = outputs;
}

async function verifyBucket(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const bucketName = outputs["BucketName"];
  assert.ok(bucketName, "missing BucketName output");

  const { s3 } = makeClients(ctx.endpoint, ctx.region);
  const resp = await s3.send(new ListBucketsCommand({}));
  assert.ok(
    resp.Buckets?.some((b) => b.Name === bucketName),
    `bucket ${bucketName} missing from ListBuckets`,
  );
}

async function verifyQueues(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const queueName = outputs["QueueName"];
  const dlqArn = outputs["DlqArn"];
  assert.ok(queueName, "missing QueueName output");
  assert.ok(dlqArn, "missing DlqArn output");

  const { sqs } = makeClients(ctx.endpoint, ctx.region);
  const q = await sqs.send(new GetQueueUrlCommand({ QueueName: queueName }));
  assert.ok(q.QueueUrl, `QueueUrl missing for ${queueName}`);

  const attrs = await sqs.send(
    new GetQueueAttributesCommand({
      QueueUrl: q.QueueUrl,
      AttributeNames: ["All"],
    }),
  );
  const redrivePolicy = attrs.Attributes?.["RedrivePolicy"];
  assert.ok(redrivePolicy, "RedrivePolicy missing on main queue");
  assert.ok(
    redrivePolicy.includes(dlqArn),
    `RedrivePolicy does not reference dlq arn ${dlqArn}`,
  );

  (ctx as Record<string, unknown>)["_queueUrl"] = q.QueueUrl;
}

async function verifyTopicSubscription(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const topicArn = outputs["TopicArn"];
  const queueArn = outputs["QueueArn"];
  assert.ok(topicArn, "missing TopicArn output");
  assert.ok(queueArn, "missing QueueArn output");

  const { sns, sqs } = makeClients(ctx.endpoint, ctx.region);

  const subs = await sns.send(
    new ListSubscriptionsByTopicCommand({ TopicArn: topicArn }),
  );
  const hasSub = subs.Subscriptions?.some(
    (s) => s.Endpoint === queueArn && s.Protocol === "sqs",
  );
  assert.ok(hasSub, `topic ${topicArn} has no SQS subscription to ${queueArn}`);

  await sns.send(
    new PublishCommand({
      TopicArn: topicArn,
      Message: JSON.stringify({ runId: ctx.runId, t: Date.now() }),
    }),
  );

  const queueUrl = ((ctx as Record<string, unknown>)["_queueUrl"] ??
    "") as string;
  assert.ok(queueUrl, "queue url missing from prior queue verification");

  let delivered = false;
  for (let i = 0; i < 10; i++) {
    const recv = await sqs.send(
      new ReceiveMessageCommand({
        QueueUrl: queueUrl,
        WaitTimeSeconds: 0,
        MaxNumberOfMessages: 1,
      }),
    );
    if ((recv.Messages?.length ?? 0) > 0) {
      delivered = true;
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  assert.ok(delivered, "published SNS message was not delivered to SQS queue");
}

async function verifyTable(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const tableName = outputs["TableName"];
  assert.ok(tableName, "missing TableName output");

  const { dynamodb } = makeClients(ctx.endpoint, ctx.region);
  const desc = await dynamodb.send(
    new DescribeTableCommand({ TableName: tableName }),
  );
  const table = desc.Table;
  assert.ok(table, `table ${tableName} missing`);
  assert.ok(
    table.GlobalSecondaryIndexes?.some((i) => i.IndexName === "gsi1"),
    "expected gsi1 in DescribeTable GlobalSecondaryIndexes",
  );
}

async function verifyRole(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const roleArn = outputs["RoleArn"];
  assert.ok(roleArn, "missing RoleArn output");
  const roleName = roleArn.split("/").pop() ?? "";
  assert.ok(roleName, "failed to derive role name from RoleArn");

  const { iam } = makeClients(ctx.endpoint, ctx.region);
  const resp = await iam.send(new GetRoleCommand({ RoleName: roleName }));
  const doc = decodeURIComponent(resp.Role?.AssumeRolePolicyDocument ?? "");
  assert.ok(
    doc.includes("lambda.amazonaws.com"),
    "expected lambda.amazonaws.com in role trust policy",
  );
}

async function verifyFunctionConfig(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const functionName = outputs["FunctionName"];
  const roleArn = outputs["RoleArn"];
  assert.ok(functionName, "missing FunctionName output");
  assert.ok(roleArn, "missing RoleArn output");

  const { lambda } = makeClients(ctx.endpoint, ctx.region);
  const resp = await lambda.send(
    new GetFunctionConfigurationCommand({ FunctionName: functionName }),
  );
  assert.strictEqual(
    resp.Handler,
    "index.handler",
    `expected handler index.handler, got ${String(resp.Handler)}`,
  );
  assert.strictEqual(
    resp.Runtime,
    "nodejs20.x",
    `expected runtime nodejs20.x, got ${String(resp.Runtime)}`,
  );
  assert.strictEqual(resp.Role, roleArn, "function role arn mismatch");
  assert.strictEqual(
    resp.Environment?.Variables?.["COMPAT_RUN_ID"],
    ctx.runId,
    "function environment variable COMPAT_RUN_ID mismatch",
  );
}

async function verifyEventSourceMapping(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const functionName = outputs["FunctionName"];
  const queueArn = outputs["QueueArn"];
  assert.ok(functionName, "missing FunctionName output");
  assert.ok(queueArn, "missing QueueArn output");

  const { lambda } = makeClients(ctx.endpoint, ctx.region);
  const resp = await lambda.send(
    new ListEventSourceMappingsCommand({
      FunctionName: functionName,
      EventSourceArn: queueArn,
    }),
  );
  assert.ok(
    (resp.EventSourceMappings?.length ?? 0) > 0,
    "expected at least one event source mapping for function and queue",
  );
}

async function destroyStack(ctx: TestContext): Promise<void> {
  await runCdk(ctx, ["destroy", ctx.stackName, "--force"]);
}

async function verifyDestroyed(ctx: TestContext): Promise<void> {
  const { cloudformation } = makeClients(ctx.endpoint, ctx.region);
  let found = false;
  let nextToken: string | undefined;
  do {
    const page = await cloudformation.send(
      new ListStacksCommand({ NextToken: nextToken }),
    );
    if (
      page.StackSummaries?.some((s) => {
        return (
          s.StackName === ctx.stackName && s.StackStatus !== "DELETE_COMPLETE"
        );
      })
    ) {
      found = true;
      break;
    }
    nextToken = page.NextToken;
  } while (nextToken);

  assert.ok(!found, `stack ${ctx.stackName} still present after destroy`);
}

async function verifyDynamoDBStream(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const tableName = outputs["TableName"];
  assert.ok(tableName, "missing TableName output");

  const { dynamodb } = makeClients(ctx.endpoint, ctx.region);
  const desc = await dynamodb.send(
    new DescribeTableCommand({ TableName: tableName }),
  );
  const table = desc.Table;
  assert.ok(table, `table ${tableName} missing`);
  const streamSpec = table!.StreamSpecification;
  assert.ok(streamSpec?.StreamEnabled, "expected StreamEnabled on table");
  assert.ok(
    streamSpec?.StreamViewType,
    `expected StreamViewType, got ${String(streamSpec?.StreamViewType)}`,
  );
  const streamArn = table!.LatestStreamArn;
  assert.ok(streamArn, "expected LatestStreamArn on table");
  assert.ok(
    streamArn.includes("/stream/"),
    `stream ARN should contain /stream/: ${streamArn}`,
  );
  (ctx as Record<string, unknown>)["_streamArn"] = streamArn;
}

async function verifyDynamoDBEsm(ctx: TestContext): Promise<void> {
  const streamArn = (ctx as Record<string, unknown>)["_streamArn"] as string;
  assert.ok(streamArn, "streamArn missing from prior test");

  const { lambda } = makeClients(ctx.endpoint, ctx.region);
  const resp = await lambda.send(
    new ListEventSourceMappingsCommand({
      EventSourceArn: streamArn,
    }),
  );
  assert.ok(
    (resp.EventSourceMappings?.length ?? 0) > 0,
    `expected at least one ESM for stream ${streamArn}`,
  );
  const esm = resp.EventSourceMappings![0];
  assert.ok(esm.UUID, "ESM UUID missing");
  (ctx as Record<string, unknown>)["_ddbEsmId"] = esm.UUID;
}

async function putStreamTriggerItem(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const tableName = outputs["TableName"];
  assert.ok(tableName, "missing TableName output");

  const { dynamodb } = makeClients(ctx.endpoint, ctx.region);
  await dynamodb.send(
    new PutItemCommand({
      TableName: tableName,
      Item: { pk: { S: `stream-test-${Date.now()}` } },
    }),
  );
}

async function verifyLambdaInvokedByStream(ctx: TestContext): Promise<void> {
  const esmId = (ctx as Record<string, unknown>)["_ddbEsmId"] as string;
  assert.ok(esmId, "ddbEsmId missing from prior test");

  const { lambda } = makeClients(ctx.endpoint, ctx.region);
  let lastResult = "";
  for (let i = 0; i < 30; i++) {
    const resp = await lambda.send(
      new GetEventSourceMappingCommand({ UUID: esmId }),
    );
    lastResult = resp.LastProcessingResult ?? "";
    if (lastResult && lastResult !== "No records processed") {
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  assert.ok(
    lastResult && lastResult !== "No records processed",
    `expected LastProcessingResult to change after item put, got "${lastResult}"`,
  );
  ctx.log(`DynamoDB Stream ESM LastProcessingResult: ${lastResult}`);
}

async function verifyFilteredDdbEsm(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const filteredEsmId = outputs["DdbFilteredEsmId"];
  assert.ok(filteredEsmId, "missing DdbFilteredEsmId output");

  const { lambda } = makeClients(ctx.endpoint, ctx.region);
  const resp = await lambda.send(
    new GetEventSourceMappingCommand({ UUID: filteredEsmId }),
  );
  assert.ok(resp.UUID, "filtered ESM UUID missing");
  assert.strictEqual(
    resp.State,
    "Enabled",
    `expected filtered ESM state Enabled, got ${resp.State}`,
  );
  const filters = resp.FilterCriteria?.Filters;
  assert.ok(
    Array.isArray(filters) && filters.length > 0,
    "expected FilterCriteria.Filters to be set on filtered ESM",
  );
  ctx.log(
    `Filtered DDB ESM UUID: ${resp.UUID}, filters: ${JSON.stringify(filters)}`,
  );
  (ctx as Record<string, unknown>)["_filteredDdbEsmId"] = filteredEsmId;
}

async function verifyFilteredEsmDelivery(ctx: TestContext): Promise<void> {
  // PutStreamTriggerItem already ran and put an INSERT item. The filtered ESM
  // (INSERT-only) should have processed it and show a non-empty result.
  const esmId = (ctx as Record<string, unknown>)["_filteredDdbEsmId"] as string;
  assert.ok(esmId, "filteredDdbEsmId missing from prior test");

  const { lambda } = makeClients(ctx.endpoint, ctx.region);
  let lastResult = "";
  for (let i = 0; i < 30; i++) {
    const resp = await lambda.send(
      new GetEventSourceMappingCommand({ UUID: esmId }),
    );
    lastResult = resp.LastProcessingResult ?? "";
    if (lastResult && lastResult !== "No records processed") {
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  assert.ok(
    lastResult && lastResult !== "No records processed",
    `filtered ESM: expected LastProcessingResult to be set after INSERT, got "${lastResult}"`,
  );
  ctx.log(`Filtered DDB ESM LastProcessingResult: ${lastResult}`);
}

async function verifyLogGroup(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const logGroupName = outputs["LogGroupName"];
  assert.ok(logGroupName, "missing LogGroupName output");

  const { cloudwatchLogs } = makeClients(ctx.endpoint, ctx.region);
  const resp = await cloudwatchLogs.send(
    new DescribeLogGroupsCommand({ logGroupNamePrefix: logGroupName }),
  );
  assert.ok(
    resp.logGroups?.some((g) => g.logGroupName === logGroupName),
    `log group ${logGroupName} missing from DescribeLogGroups`,
  );
}

async function verifyKmsKey(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const keyId = outputs["KmsKeyId"];
  assert.ok(keyId, "missing KmsKeyId output");

  const { kms } = makeClients(ctx.endpoint, ctx.region);
  const resp = await kms.send(new DescribeKeyCommand({ KeyId: keyId }));
  const meta = resp.KeyMetadata;
  assert.ok(meta, "expected KeyMetadata from DescribeKey");
  assert.ok(meta.Enabled !== false, "expected KMS key to be enabled");
  assert.ok(meta.KeyId === keyId, `key ID mismatch: ${meta.KeyId} vs ${keyId}`);
}

async function verifySecret(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const secretArn = outputs["SecretArn"];
  assert.ok(secretArn, "missing SecretArn output");

  const { secretsmanager } = makeClients(ctx.endpoint, ctx.region);
  const resp = await secretsmanager.send(
    new DescribeSecretCommand({ SecretId: secretArn }),
  );
  assert.ok(resp.ARN, "expected ARN from DescribeSecret");
  assert.strictEqual(resp.ARN, secretArn, "secret ARN mismatch");
}

async function verifyParameter(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const parameterName = outputs["ParameterName"];
  assert.ok(parameterName, "missing ParameterName output");

  const { ssm } = makeClients(ctx.endpoint, ctx.region);
  const resp = await ssm.send(new GetParameterCommand({ Name: parameterName }));
  const param = resp.Parameter;
  assert.ok(param, `parameter ${parameterName} missing`);
  assert.strictEqual(
    param.Value,
    "compat-test-value",
    `expected value compat-test-value, got ${String(param.Value)}`,
  );
}

async function verifyPolicy(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const policyArn = outputs["PolicyArn"];
  assert.ok(policyArn, "missing PolicyArn output");

  const { iam } = makeClients(ctx.endpoint, ctx.region);
  const resp = await iam.send(new GetPolicyCommand({ PolicyArn: policyArn }));
  assert.ok(resp.Policy, "expected Policy from GetPolicy");
  assert.ok(resp.Policy!.Arn?.includes(policyArn), "policy ARN mismatch");
}

async function verifyVpc(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const vpcId = outputs["VpcId"];
  assert.ok(vpcId, "missing VpcId output");

  const { ec2 } = makeClients(ctx.endpoint, ctx.region);
  const resp = await ec2.send(new DescribeVpcsCommand({ VpcIds: [vpcId] }));
  assert.ok((resp.Vpcs?.length ?? 0) > 0, `VPC ${vpcId} not found`);
}

async function verifySecurityGroup(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const sgId = outputs["SecurityGroupId"];
  assert.ok(sgId, "missing SecurityGroupId output");

  const { ec2 } = makeClients(ctx.endpoint, ctx.region);
  const resp = await ec2.send(
    new DescribeSecurityGroupsCommand({ GroupIds: [sgId] }),
  );
  assert.ok(
    (resp.SecurityGroups?.length ?? 0) > 0,
    `SecurityGroup ${sgId} not found`,
  );
}

async function verifyRestApi(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const restApiId = outputs["RestApiId"];
  assert.ok(restApiId, "missing RestApiId output");

  const { apigateway } = makeClients(ctx.endpoint, ctx.region);
  const resp = await apigateway.send(new GetRestApisCommand({}));
  assert.ok(
    resp.items?.some((api) => api.id === restApiId),
    `REST API ${restApiId} not found in GetRestApis`,
  );
}

async function verifyEventBus(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const eventBusName = outputs["EventBusName"];
  const eventBusArn = outputs["EventBusArn"];
  assert.ok(eventBusName, "missing EventBusName output");
  assert.ok(eventBusArn, "missing EventBusArn output");

  const { eventbridge } = makeClients(ctx.endpoint, ctx.region);
  const resp = await eventbridge.send(
    new DescribeEventBusCommand({ Name: eventBusName }),
  );
  assert.ok(
    resp.Name === eventBusName,
    `event bus name mismatch: ${resp.Name} vs ${eventBusName}`,
  );
  assert.ok(
    resp.Arn === eventBusArn,
    `event bus ARN mismatch: ${resp.Arn} vs ${eventBusArn}`,
  );
}

async function verifyStateMachine(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const stateMachineArn = outputs["StateMachineArn"];
  assert.ok(stateMachineArn, "missing StateMachineArn output");

  const { sfn } = makeClients(ctx.endpoint, ctx.region);
  const resp = await sfn.send(
    new DescribeStateMachineCommand({ stateMachineArn }),
  );
  assert.ok(
    resp.stateMachineArn === stateMachineArn,
    `state machine ARN mismatch: ${resp.stateMachineArn} vs ${stateMachineArn}`,
  );
}

async function verifyStateMachineStatus(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const stateMachineArn = outputs["StateMachineArn"];
  assert.ok(stateMachineArn, "missing StateMachineArn output");

  const { sfn } = makeClients(ctx.endpoint, ctx.region);
  const resp = await sfn.send(
    new DescribeStateMachineCommand({ stateMachineArn }),
  );
  assert.ok(
    resp.status === "ACTIVE",
    `expected ACTIVE status, got ${String(resp.status)}`,
  );
}

async function verifyNestedStack(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const nestedStackName = outputs["NestedStackName"];
  assert.ok(nestedStackName, "missing NestedStackName output");

  const { cloudformation } = makeClients(ctx.endpoint, ctx.region);
  const resp = await cloudformation.send(
    new DescribeStacksCommand({ StackName: nestedStackName }),
  );
  const stack = resp.Stacks?.[0];
  assert.ok(stack, `nested stack ${nestedStackName} not found`);
  assert.ok(
    stack.StackStatus === "CREATE_COMPLETE" ||
      stack.StackStatus === "UPDATE_COMPLETE",
    `expected nested stack CREATE_COMPLETE/UPDATE_COMPLETE, got ${String(stack.StackStatus)}`,
  );
}

async function verifyNestedQueue(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const nestedQueueName = outputs["NestedQueueName"];
  assert.ok(nestedQueueName, "missing NestedQueueName output");

  const { sqs } = makeClients(ctx.endpoint, ctx.region);
  const q = await sqs.send(
    new GetQueueUrlCommand({ QueueName: nestedQueueName }),
  );
  assert.ok(q.QueueUrl, `QueueUrl missing for nested queue ${nestedQueueName}`);
}

async function updateLambdaTimeout(ctx: TestContext): Promise<void> {
  await runCdk(ctx, ["synth", ctx.stackName], {
    CDK_COMPAT_LAMBDA_TIMEOUT: "15",
  });
  await runCdk(ctx, ["deploy", ctx.stackName, "--require-approval", "never"], {
    CDK_COMPAT_LAMBDA_TIMEOUT: "15",
  });
}

async function verifyUpdatedFunctionConfig(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const functionName = outputs["FunctionName"];
  assert.ok(functionName, "missing FunctionName output");

  const { lambda } = makeClients(ctx.endpoint, ctx.region);
  const resp = await lambda.send(
    new GetFunctionConfigurationCommand({ FunctionName: functionName }),
  );
  assert.strictEqual(
    resp.Timeout,
    15,
    `expected updated timeout 15, got ${String(resp.Timeout)}`,
  );
}

export function makeLifecycleGroups(suite: string): TestGroup[] {
  return [
    {
      suite,
      service: "cdk",
      name: "cdk-lifecycle",
      tests: [
        {
          name: "Bootstrap",
          fn: async (ctx) =>
            runCdk(ctx, ["bootstrap", `aws://000000000000/${ctx.region}`]),
        },
        {
          name: "Synth",
          depends: ["Bootstrap"],
          fn: async (ctx) => runCdk(ctx, ["synth", ctx.stackName]),
        },
        {
          name: "Deploy",
          depends: ["Synth"],
          fn: async (ctx) =>
            runCdk(ctx, [
              "deploy",
              ctx.stackName,
              "--require-approval",
              "never",
            ]),
        },
        {
          name: "VerifyStackStatus",
          depends: ["Deploy"],
          fn: verifyStackStatus,
        },
        {
          name: "VerifyBucket",
          depends: ["VerifyStackStatus"],
          fn: verifyBucket,
        },
        {
          name: "VerifyQueues",
          depends: ["VerifyStackStatus"],
          fn: verifyQueues,
        },
        {
          name: "VerifyTopicSubscription",
          depends: ["VerifyQueues"],
          fn: verifyTopicSubscription,
        },
        {
          name: "VerifyTable",
          depends: ["VerifyStackStatus"],
          fn: verifyTable,
        },
        {
          name: "VerifyRole",
          depends: ["VerifyStackStatus"],
          fn: verifyRole,
        },
        {
          name: "VerifyFunctionConfig",
          depends: ["VerifyStackStatus", "VerifyRole"],
          fn: verifyFunctionConfig,
        },
        {
          name: "VerifyEventSourceMapping",
          depends: ["VerifyQueues", "VerifyFunctionConfig"],
          fn: verifyEventSourceMapping,
        },
        {
          name: "VerifyDynamoDBStream",
          depends: ["VerifyStackStatus"],
          fn: verifyDynamoDBStream,
        },
        {
          name: "VerifyDynamoDBEsm",
          depends: ["VerifyDynamoDBStream", "VerifyFunctionConfig"],
          fn: verifyDynamoDBEsm,
        },
        {
          name: "PutStreamTriggerItem",
          depends: ["VerifyDynamoDBEsm"],
          fn: putStreamTriggerItem,
        },
        {
          name: "VerifyLambdaInvokedByStream",
          depends: ["PutStreamTriggerItem"],
          fn: verifyLambdaInvokedByStream,
        },
        {
          name: "VerifyFilteredDdbEsm",
          depends: ["VerifyDynamoDBStream", "VerifyFunctionConfig"],
          fn: verifyFilteredDdbEsm,
        },
        {
          name: "VerifyFilteredEsmDelivery",
          depends: ["VerifyFilteredDdbEsm", "PutStreamTriggerItem"],
          fn: verifyFilteredEsmDelivery,
        },
        {
          name: "VerifyLogGroup",
          depends: ["VerifyStackStatus"],
          fn: verifyLogGroup,
        },
        {
          name: "VerifyKmsKey",
          depends: ["VerifyStackStatus"],
          fn: verifyKmsKey,
        },
        {
          name: "VerifySecret",
          depends: ["VerifyStackStatus"],
          fn: verifySecret,
        },
        {
          name: "VerifyParameter",
          depends: ["VerifyStackStatus"],
          fn: verifyParameter,
        },
        {
          name: "VerifyPolicy",
          depends: ["VerifyStackStatus"],
          fn: verifyPolicy,
        },
        {
          name: "VerifyVpc",
          depends: ["VerifyStackStatus"],
          fn: verifyVpc,
        },
        {
          name: "VerifySecurityGroup",
          depends: ["VerifyVpc"],
          fn: verifySecurityGroup,
        },
        {
          name: "VerifyRestApi",
          depends: ["VerifyStackStatus"],
          fn: verifyRestApi,
        },
        {
          name: "VerifyEventBus",
          depends: ["VerifyStackStatus"],
          fn: verifyEventBus,
        },
        {
          name: "VerifyStateMachine",
          depends: ["VerifyStackStatus"],
          fn: verifyStateMachine,
        },
        {
          name: "VerifyStateMachineStatus",
          depends: ["VerifyStateMachine"],
          fn: verifyStateMachineStatus,
        },
        {
          name: "VerifyNestedStack",
          depends: ["VerifyStackStatus"],
          fn: verifyNestedStack,
        },
        {
          name: "VerifyNestedQueue",
          depends: ["VerifyNestedStack"],
          fn: verifyNestedQueue,
        },
        {
          name: "UpdateLambdaTimeout",
          depends: ["VerifyFunctionConfig"],
          fn: updateLambdaTimeout,
        },
        {
          name: "VerifyUpdateStatus",
          depends: ["UpdateLambdaTimeout"],
          fn: verifyStackStatus,
        },
        {
          name: "VerifyUpdatedFunctionConfig",
          depends: ["VerifyUpdateStatus"],
          fn: verifyUpdatedFunctionConfig,
        },
        { name: "Destroy", fn: destroyStack },
        { name: "VerifyDestroyed", depends: ["Destroy"], fn: verifyDestroyed },
      ],
      teardown: async (ctx) => {
        try {
          await destroyStack(ctx);
        } catch {
          // best-effort cleanup only
        }
      },
    },
  ];
}
