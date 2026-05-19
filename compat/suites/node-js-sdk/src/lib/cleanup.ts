/**
 * cleanup.ts — Post-suite resource sweep for the Overcast compat Node.js suite.
 *
 * All compat resources are named/prefixed with the run ID ("oc-<hex>") OR the
 * shared prefix "oc-" for past orphans. This sweeper lists resources by that
 * prefix and deletes them, so orphans from crashed or interrupted runs are
 * also cleaned up — not just the current run.
 *
 * Call `sweepAll(clients, ctx)` after every suite run (unless suppressed by
 * OVERCAST_COMPAT_NO_CLEANUP=1).
 *
 * Each sweep function is individually fault-tolerant: it logs errors and
 * continues rather than aborting the sweep.
 */

import {
  ListBucketsCommand,
  DeleteBucketCommand,
  ListObjectVersionsCommand,
  DeleteObjectsCommand,
  type S3Client,
} from "@aws-sdk/client-s3";
import {
  ListQueuesCommand,
  DeleteQueueCommand,
  type SQSClient,
} from "@aws-sdk/client-sqs";
import {
  ListTopicsCommand,
  DeleteTopicCommand,
  type SNSClient,
} from "@aws-sdk/client-sns";
import {
  ListTablesCommand,
  DeleteTableCommand,
  type DynamoDBClient,
} from "@aws-sdk/client-dynamodb";
import {
  ListFunctionsCommand,
  DeleteFunctionCommand,
  type LambdaClient,
} from "@aws-sdk/client-lambda";
import {
  DescribeLogGroupsCommand,
  DeleteLogGroupCommand,
  type CloudWatchLogsClient,
} from "@aws-sdk/client-cloudwatch-logs";
import {
  ListUsersCommand,
  DeleteUserCommand,
  ListAccessKeysCommand,
  DeleteAccessKeyCommand,
  ListAttachedUserPoliciesCommand,
  DetachUserPolicyCommand,
  ListRolesCommand,
  DeleteRoleCommand,
  ListAttachedRolePoliciesCommand,
  DetachRolePolicyCommand,
  RemoveRoleFromInstanceProfileCommand,
  ListInstanceProfilesCommand,
  DeleteInstanceProfileCommand,
  ListPoliciesCommand,
  DeletePolicyCommand,
  ListGroupsCommand,
  DeleteGroupCommand,
  type IAMClient,
} from "@aws-sdk/client-iam";
import {
  DescribeStreamCommand,
  ListStreamsCommand,
  DeleteStreamCommand,
  type KinesisClient,
} from "@aws-sdk/client-kinesis";
import {
  ListKeysCommand,
  DescribeKeyCommand,
  ScheduleKeyDeletionCommand,
  ListAliasesCommand,
  DeleteAliasCommand,
  type KMSClient,
} from "@aws-sdk/client-kms";
import {
  ListSecretsCommand,
  DeleteSecretCommand,
  type SecretsManagerClient,
} from "@aws-sdk/client-secrets-manager";
import {
  DescribeParametersCommand,
  DeleteParametersCommand,
  type SSMClient,
} from "@aws-sdk/client-ssm";
import {
  ListEventBusesCommand,
  ListRulesCommand,
  DeleteRuleCommand,
  RemoveTargetsCommand,
  ListTargetsByRuleCommand,
  DeleteEventBusCommand,
  type EventBridgeClient,
} from "@aws-sdk/client-eventbridge";
import {
  ListClustersCommand,
  DeleteClusterCommand,
  ListServicesCommand,
  DeleteServiceCommand,
  UpdateServiceCommand,
  ListTaskDefinitionsCommand,
  DeregisterTaskDefinitionCommand,
  type ECSClient,
} from "@aws-sdk/client-ecs";
import {
  DescribeDBInstancesCommand,
  DeleteDBInstanceCommand,
  DescribeDBSubnetGroupsCommand,
  DeleteDBSubnetGroupCommand,
  DescribeDBParameterGroupsCommand,
  DeleteDBParameterGroupCommand,
  type RDSClient,
} from "@aws-sdk/client-rds";
import {
  DescribeCacheClustersCommand,
  DeleteCacheClusterCommand,
  DescribeReplicationGroupsCommand,
  DeleteReplicationGroupCommand,
  DescribeCacheSubnetGroupsCommand,
  DeleteCacheSubnetGroupCommand,
  DescribeCacheParameterGroupsCommand,
  DeleteCacheParameterGroupCommand,
  type ElastiCacheClient,
} from "@aws-sdk/client-elasticache";
import {
  DescribeVpcsCommand,
  DeleteVpcCommand,
  type EC2Client,
} from "@aws-sdk/client-ec2";
import {
  DescribeRepositoriesCommand,
  DeleteRepositoryCommand,
  type ECRClient,
} from "@aws-sdk/client-ecr";
import type { Clients } from "./clients.js";

type Log = (msg: string) => void;

// ─── Per-service sweepers ─────────────────────────────────────────────────────

async function sweepS3(s3: S3Client, log: Log, prefix: string): Promise<void> {
  try {
    const { Buckets = [] } = await s3.send(new ListBucketsCommand({}));
    const ours = Buckets.filter((b) => b.Name?.startsWith(prefix));
    for (const { Name: bucket } of ours) {
      if (!bucket) continue;
      try {
        // Must delete all object versions before deleting a versioned bucket.
        let keyMarker: string | undefined;
        let versionMarker: string | undefined;
        do {
          const page = await s3.send(
            new ListObjectVersionsCommand({
              Bucket: bucket,
              KeyMarker: keyMarker,
              VersionIdMarker: versionMarker,
            }),
          );
          const objects = [
            ...(page.Versions ?? []).map((v) => ({
              Key: v.Key!,
              VersionId: v.VersionId,
            })),
            ...(page.DeleteMarkers ?? []).map((d) => ({
              Key: d.Key!,
              VersionId: d.VersionId,
            })),
          ];
          if (objects.length > 0) {
            await s3.send(
              new DeleteObjectsCommand({
                Bucket: bucket,
                Delete: { Objects: objects },
              }),
            );
          }
          keyMarker = page.NextKeyMarker;
          versionMarker = page.NextVersionIdMarker;
        } while (keyMarker || versionMarker);

        await s3.send(new DeleteBucketCommand({ Bucket: bucket }));
        log(`cleanup: s3: deleted bucket ${bucket}`);
      } catch (err) {
        log(`cleanup: s3: failed to delete bucket ${bucket}: ${err}`);
      }
    }
  } catch (err) {
    log(`cleanup: s3: list error: ${err}`);
  }
}

async function sweepSQS(
  sqs: SQSClient,
  log: Log,
  prefix: string,
): Promise<void> {
  try {
    let nextToken: string | undefined;
    do {
      const page = await sqs.send(
        new ListQueuesCommand({
          QueueNamePrefix: prefix,
          NextToken: nextToken,
        }),
      );
      for (const url of page.QueueUrls ?? []) {
        try {
          await sqs.send(new DeleteQueueCommand({ QueueUrl: url }));
          log(`cleanup: sqs: deleted ${url}`);
        } catch (err) {
          log(`cleanup: sqs: failed to delete ${url}: ${err}`);
        }
      }
      nextToken = page.NextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: sqs: list error: ${err}`);
  }
}

async function sweepSNS(
  sns: SNSClient,
  log: Log,
  prefix: string,
): Promise<void> {
  try {
    let nextToken: string | undefined;
    do {
      const page = await sns.send(
        new ListTopicsCommand({ NextToken: nextToken }),
      );
      for (const { TopicArn } of page.Topics ?? []) {
        if (!TopicArn) continue;
        try {
          // Topic name is the last segment of the ARN.
          const name = TopicArn.split(":").pop() ?? "";
          if (!name.startsWith(prefix)) continue;
          await sns.send(new DeleteTopicCommand({ TopicArn }));
          log(`cleanup: sns: deleted ${TopicArn}`);
        } catch (err) {
          log(`cleanup: sns: failed to delete ${TopicArn}: ${err}`);
        }
      }
      nextToken = page.NextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: sns: list error: ${err}`);
  }
}

async function sweepDynamoDB(
  ddb: DynamoDBClient,
  log: Log,
  prefix: string,
): Promise<void> {
  try {
    let lastEval: string | undefined;
    do {
      const page = await ddb.send(
        new ListTablesCommand({ ExclusiveStartTableName: lastEval }),
      );
      for (const name of page.TableNames ?? []) {
        if (!name.startsWith(prefix)) continue;
        try {
          await ddb.send(new DeleteTableCommand({ TableName: name }));
          log(`cleanup: dynamodb: deleted table ${name}`);
        } catch (err) {
          log(`cleanup: dynamodb: failed to delete table ${name}: ${err}`);
        }
      }
      lastEval = page.LastEvaluatedTableName;
    } while (lastEval);
  } catch (err) {
    log(`cleanup: dynamodb: list error: ${err}`);
  }
}

async function sweepLambda(
  lambda: LambdaClient,
  log: Log,
  prefix: string,
): Promise<void> {
  try {
    let marker: string | undefined;
    do {
      const page = await lambda.send(
        new ListFunctionsCommand({ Marker: marker }),
      );
      for (const fn of page.Functions ?? []) {
        if (!fn.FunctionName?.startsWith(prefix)) continue;
        try {
          await lambda.send(
            new DeleteFunctionCommand({ FunctionName: fn.FunctionName }),
          );
          log(`cleanup: lambda: deleted ${fn.FunctionName}`);
        } catch (err) {
          log(`cleanup: lambda: failed to delete ${fn.FunctionName}: ${err}`);
        }
      }
      marker = page.NextMarker;
    } while (marker);
  } catch (err) {
    log(`cleanup: lambda: list error: ${err}`);
  }
}

async function sweepCloudWatchLogs(
  logs: CloudWatchLogsClient,
  log: Log,
  prefix: string,
): Promise<void> {
  // Two prefixes: /overcast/<runId>* (explicit test groups) and /aws/lambda/<runId>* (Lambda auto-created)
  const prefixes = [`/overcast/${prefix}`, `/aws/lambda/${prefix}`];
  for (const prefix of prefixes) {
    try {
      let nextToken: string | undefined;
      do {
        const page = await logs.send(
          new DescribeLogGroupsCommand({
            logGroupNamePrefix: prefix,
            nextToken,
          }),
        );
        for (const group of page.logGroups ?? []) {
          if (!group.logGroupName) continue;
          try {
            await logs.send(
              new DeleteLogGroupCommand({ logGroupName: group.logGroupName }),
            );
            log(`cleanup: logs: deleted ${group.logGroupName}`);
          } catch (err) {
            log(
              `cleanup: logs: failed to delete ${group.logGroupName}: ${err}`,
            );
          }
        }
        nextToken = page.nextToken;
      } while (nextToken);
    } catch (err) {
      log(`cleanup: logs: list error (prefix=${prefix}): ${err}`);
    }
  }
}

async function sweepIAM(
  iam: IAMClient,
  log: Log,
  prefix: string,
): Promise<void> {
  // Policies
  try {
    let marker: string | undefined;
    do {
      const page = await iam.send(
        new ListPoliciesCommand({ Scope: "Local", Marker: marker }),
      );
      for (const p of page.Policies ?? []) {
        if (!p.PolicyName?.startsWith(prefix) || !p.Arn) continue;
        try {
          await iam.send(new DeletePolicyCommand({ PolicyArn: p.Arn }));
          log(`cleanup: iam: deleted policy ${p.PolicyName}`);
        } catch (err) {
          log(`cleanup: iam: failed to delete policy ${p.PolicyName}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: iam: list policies error: ${err}`);
  }

  // Instance profiles (must remove roles before deleting)
  try {
    let marker: string | undefined;
    do {
      const page = await iam.send(
        new ListInstanceProfilesCommand({ Marker: marker }),
      );
      for (const ip of page.InstanceProfiles ?? []) {
        if (!ip.InstanceProfileName?.startsWith(prefix)) continue;
        try {
          for (const role of ip.Roles ?? []) {
            await iam.send(
              new RemoveRoleFromInstanceProfileCommand({
                InstanceProfileName: ip.InstanceProfileName,
                RoleName: role.RoleName,
              }),
            );
          }
          await iam.send(
            new DeleteInstanceProfileCommand({
              InstanceProfileName: ip.InstanceProfileName,
            }),
          );
          log(
            `cleanup: iam: deleted instance profile ${ip.InstanceProfileName}`,
          );
        } catch (err) {
          log(
            `cleanup: iam: failed to delete instance profile ${ip.InstanceProfileName}: ${err}`,
          );
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: iam: list instance profiles error: ${err}`);
  }

  // Roles (detach policies first)
  try {
    let marker: string | undefined;
    do {
      const page = await iam.send(new ListRolesCommand({ Marker: marker }));
      for (const role of page.Roles ?? []) {
        if (!role.RoleName?.startsWith(prefix)) continue;
        try {
          const attached = await iam.send(
            new ListAttachedRolePoliciesCommand({ RoleName: role.RoleName }),
          );
          for (const p of attached.AttachedPolicies ?? []) {
            await iam.send(
              new DetachRolePolicyCommand({
                RoleName: role.RoleName,
                PolicyArn: p.PolicyArn!,
              }),
            );
          }
          await iam.send(new DeleteRoleCommand({ RoleName: role.RoleName }));
          log(`cleanup: iam: deleted role ${role.RoleName}`);
        } catch (err) {
          log(`cleanup: iam: failed to delete role ${role.RoleName}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: iam: list roles error: ${err}`);
  }

  // Users (delete access keys + detach policies first)
  try {
    let marker: string | undefined;
    do {
      const page = await iam.send(new ListUsersCommand({ Marker: marker }));
      for (const user of page.Users ?? []) {
        if (!user.UserName?.startsWith(prefix)) continue;
        try {
          const keys = await iam.send(
            new ListAccessKeysCommand({ UserName: user.UserName }),
          );
          for (const k of keys.AccessKeyMetadata ?? []) {
            await iam.send(
              new DeleteAccessKeyCommand({
                UserName: user.UserName,
                AccessKeyId: k.AccessKeyId!,
              }),
            );
          }
          const attached = await iam.send(
            new ListAttachedUserPoliciesCommand({ UserName: user.UserName }),
          );
          for (const p of attached.AttachedPolicies ?? []) {
            await iam.send(
              new DetachUserPolicyCommand({
                UserName: user.UserName,
                PolicyArn: p.PolicyArn!,
              }),
            );
          }
          await iam.send(new DeleteUserCommand({ UserName: user.UserName }));
          log(`cleanup: iam: deleted user ${user.UserName}`);
        } catch (err) {
          log(`cleanup: iam: failed to delete user ${user.UserName}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: iam: list users error: ${err}`);
  }

  // Groups
  try {
    let marker: string | undefined;
    do {
      const page = await iam.send(new ListGroupsCommand({ Marker: marker }));
      for (const group of page.Groups ?? []) {
        if (!group.GroupName?.startsWith(prefix)) continue;
        try {
          await iam.send(
            new DeleteGroupCommand({ GroupName: group.GroupName }),
          );
          log(`cleanup: iam: deleted group ${group.GroupName}`);
        } catch (err) {
          log(
            `cleanup: iam: failed to delete group ${group.GroupName}: ${err}`,
          );
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: iam: list groups error: ${err}`);
  }
}

async function sweepKinesis(
  kinesis: KinesisClient,
  log: Log,
  prefix: string,
): Promise<void> {
  try {
    let nextToken: string | undefined;
    do {
      const page = await kinesis.send(
        new ListStreamsCommand({ NextToken: nextToken }),
      );
      for (const name of page.StreamNames ?? []) {
        if (!name.startsWith(prefix)) continue;
        try {
          // Kinesis requires the current shard count to delete — fetch it.
          const desc = await kinesis.send(
            new DescribeStreamCommand({ StreamName: name }),
          );
          void desc; // only needed to confirm it exists
          await kinesis.send(new DeleteStreamCommand({ StreamName: name }));
          log(`cleanup: kinesis: deleted stream ${name}`);
        } catch (err) {
          log(`cleanup: kinesis: failed to delete stream ${name}: ${err}`);
        }
      }
      nextToken = page.NextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: kinesis: list error: ${err}`);
  }
}

async function sweepKMS(
  kms: KMSClient,
  log: Log,
  prefix: string,
): Promise<void> {
  // Aliases named alias/<prefix>*
  try {
    let marker: string | undefined;
    do {
      const page = await kms.send(new ListAliasesCommand({ Marker: marker }));
      for (const alias of page.Aliases ?? []) {
        if (!alias.AliasName?.startsWith(`alias/${prefix}`)) continue;
        try {
          await kms.send(
            new DeleteAliasCommand({ AliasName: alias.AliasName }),
          );
          log(`cleanup: kms: deleted alias ${alias.AliasName}`);
        } catch (err) {
          log(
            `cleanup: kms: failed to delete alias ${alias.AliasName}: ${err}`,
          );
        }
      }
      marker = page.NextMarker;
    } while (marker);
  } catch (err) {
    log(`cleanup: kms: list aliases error: ${err}`);
  }

  // Keys with Description starting with "compat-oc-"
  try {
    let marker: string | undefined;
    do {
      const page = await kms.send(new ListKeysCommand({ Marker: marker }));
      for (const key of page.Keys ?? []) {
        if (!key.KeyId) continue;
        try {
          const desc = await kms.send(
            new DescribeKeyCommand({ KeyId: key.KeyId }),
          );
          const description = desc.KeyMetadata?.Description ?? "";
          const state = desc.KeyMetadata?.KeyState;
          if (
            !description.startsWith(`compat-${prefix}`) ||
            state === "PendingDeletion" ||
            state === "Disabled"
          )
            continue;
          await kms.send(
            new ScheduleKeyDeletionCommand({
              KeyId: key.KeyId,
              PendingWindowInDays: 7,
            }),
          );
          log(`cleanup: kms: scheduled deletion of key ${key.KeyId}`);
        } catch (err) {
          log(
            `cleanup: kms: failed to schedule deletion of ${key.KeyId}: ${err}`,
          );
        }
      }
      marker = page.NextMarker;
    } while (marker);
  } catch (err) {
    log(`cleanup: kms: list keys error: ${err}`);
  }
}

async function sweepSecretsManager(
  sm: SecretsManagerClient,
  log: Log,
  prefix: string,
): Promise<void> {
  try {
    let nextToken: string | undefined;
    do {
      const page = await sm.send(
        new ListSecretsCommand({ NextToken: nextToken }),
      );
      for (const secret of page.SecretList ?? []) {
        if (!secret.Name?.startsWith(prefix)) continue;
        try {
          await sm.send(
            new DeleteSecretCommand({
              SecretId: secret.ARN ?? secret.Name,
              ForceDeleteWithoutRecovery: true,
            }),
          );
          log(`cleanup: secretsmanager: deleted ${secret.Name}`);
        } catch (err) {
          log(
            `cleanup: secretsmanager: failed to delete ${secret.Name}: ${err}`,
          );
        }
      }
      nextToken = page.NextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: secretsmanager: list error: ${err}`);
  }
}

async function sweepSSM(
  ssm: SSMClient,
  log: Log,
  prefix: string,
): Promise<void> {
  try {
    let nextToken: string | undefined;
    do {
      const page = await ssm.send(
        new DescribeParametersCommand({
          ParameterFilters: [
            { Key: "Name", Option: "BeginsWith", Values: [`/${prefix}`] },
          ],
          NextToken: nextToken,
        }),
      );
      const names = (page.Parameters ?? []).map((p) => p.Name!).filter(Boolean);
      if (names.length > 0) {
        // DeleteParameters accepts up to 10 at a time.
        for (let i = 0; i < names.length; i += 10) {
          const batch = names.slice(i, i + 10);
          try {
            await ssm.send(new DeleteParametersCommand({ Names: batch }));
            log(`cleanup: ssm: deleted parameters ${batch.join(", ")}`);
          } catch (err) {
            log(`cleanup: ssm: failed to delete batch: ${err}`);
          }
        }
      }
      nextToken = page.NextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: ssm: list error: ${err}`);
  }
}

async function sweepEventBridge(
  eb: EventBridgeClient,
  log: Log,
  prefix: string,
): Promise<void> {
  // Rules on the default bus whose name starts with <prefix>
  try {
    let nextToken: string | undefined;
    do {
      const page = await eb.send(
        new ListRulesCommand({ NamePrefix: prefix, NextToken: nextToken }),
      );
      for (const rule of page.Rules ?? []) {
        if (!rule.Name) continue;
        try {
          // Must remove all targets before deleting a rule.
          const targets = await eb.send(
            new ListTargetsByRuleCommand({ Rule: rule.Name }),
          );
          const ids = (targets.Targets ?? []).map((t) => t.Id!).filter(Boolean);
          if (ids.length > 0) {
            await eb.send(
              new RemoveTargetsCommand({ Rule: rule.Name, Ids: ids }),
            );
          }
          await eb.send(new DeleteRuleCommand({ Name: rule.Name }));
          log(`cleanup: eventbridge: deleted rule ${rule.Name}`);
        } catch (err) {
          log(
            `cleanup: eventbridge: failed to delete rule ${rule.Name}: ${err}`,
          );
        }
      }
      nextToken = page.NextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: eventbridge: list rules error: ${err}`);
  }

  // Custom event buses named oc-*
  try {
    let nextToken: string | undefined;
    do {
      const page = await eb.send(
        new ListEventBusesCommand({ NamePrefix: prefix, NextToken: nextToken }),
      );
      for (const bus of page.EventBuses ?? []) {
        if (!bus.Name?.startsWith(prefix)) continue;
        try {
          await eb.send(new DeleteEventBusCommand({ Name: bus.Name }));
          log(`cleanup: eventbridge: deleted bus ${bus.Name}`);
        } catch (err) {
          log(`cleanup: eventbridge: failed to delete bus ${bus.Name}: ${err}`);
        }
      }
      nextToken = page.NextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: eventbridge: list buses error: ${err}`);
  }
}

async function sweepECS(
  ecs: ECSClient,
  log: Log,
  runId: string,
): Promise<void> {
  const compatPrefix = `compat-${runId}`;
  const compatTaskPrefix = `compat-task-${runId}`;
  const compatSvcPrefix = `compat-svc-${runId}`;
  const prefixes = [runId, compatPrefix, compatTaskPrefix, compatSvcPrefix];

  for (const prefix of prefixes) {
    try {
      let nextToken: string | undefined;
      do {
        const page = await ecs.send(
          new ListClustersCommand({ nextToken }),
        );
        for (const arn of page.clusterArns ?? []) {
          const name = arn.split("/").pop() ?? "";
          if (!name.startsWith(prefix)) continue;
          // List and delete services first
          try {
            let svcToken: string | undefined;
            do {
              const svcPage = await ecs.send(
                new ListServicesCommand({ cluster: name, nextToken: svcToken }),
              );
              for (const svcArn of svcPage.serviceArns ?? []) {
                const svcName = svcArn.split("/").pop() ?? "";
                try {
                  try { await ecs.send(new UpdateServiceCommand({ cluster: name, service: svcName, desiredCount: 0 })); } catch {}
                  await ecs.send(new DeleteServiceCommand({ cluster: name, service: svcName }));
                  log(`cleanup: ecs: deleted service ${svcName} in ${name}`);
                } catch (err) {
                  log(`cleanup: ecs: failed to delete service ${svcName}: ${err}`);
                }
              }
              svcToken = svcPage.nextToken;
            } while (svcToken);
          } catch (err) {
            log(`cleanup: ecs: list services error for ${name}: ${err}`);
          }
          try {
            await ecs.send(new DeleteClusterCommand({ cluster: name }));
            log(`cleanup: ecs: deleted cluster ${name}`);
          } catch (err) {
            log(`cleanup: ecs: failed to delete cluster ${name}: ${err}`);
          }
        }
        nextToken = page.nextToken;
      } while (nextToken);
    } catch (err) {
      log(`cleanup: ecs: list clusters error (prefix=${prefix}): ${err}`);
    }
  }

  // Deregister task definitions
  try {
    let nextToken: string | undefined;
    do {
      const page = await ecs.send(
        new ListTaskDefinitionsCommand({ nextToken }),
      );
      for (const arn of page.taskDefinitionArns ?? []) {
        const family = arn.split("/").pop() ?? "";
        if (family.startsWith(compatPrefix) || family.startsWith(compatTaskPrefix) || family.startsWith(compatSvcPrefix)) {
          try {
            await ecs.send(new DeregisterTaskDefinitionCommand({ taskDefinition: arn }));
            log(`cleanup: ecs: deregistered task definition ${family}`);
          } catch (err) {
            log(`cleanup: ecs: failed to deregister ${family}: ${err}`);
          }
        }
      }
      nextToken = page.nextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: ecs: list task definitions error: ${err}`);
  }
}

async function sweepRDS(
  rds: RDSClient,
  log: Log,
  runId: string,
): Promise<void> {
  const compatPrefix = `compat-${runId}`;

  // DB instances
  try {
    let marker: string | undefined;
    do {
      const page = await rds.send(
        new DescribeDBInstancesCommand({ Marker: marker }),
      );
      for (const db of page.DBInstances ?? []) {
        const id = db.DBInstanceIdentifier ?? "";
        if (!id.startsWith(compatPrefix)) continue;
        try {
          await rds.send(
            new DeleteDBInstanceCommand({
              DBInstanceIdentifier: id,
              SkipFinalSnapshot: true,
            }),
          );
          log(`cleanup: rds: deleted db instance ${id}`);
        } catch (err) {
          log(`cleanup: rds: failed to delete db instance ${id}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: rds: list db instances error: ${err}`);
  }

  // DB subnet groups
  try {
    let marker: string | undefined;
    do {
      const page = await rds.send(
        new DescribeDBSubnetGroupsCommand({ Marker: marker }),
      );
      for (const g of page.DBSubnetGroups ?? []) {
        const name = g.DBSubnetGroupName ?? "";
        if (!name.startsWith(compatPrefix)) continue;
        try {
          await rds.send(
            new DeleteDBSubnetGroupCommand({ DBSubnetGroupName: name }),
          );
          log(`cleanup: rds: deleted subnet group ${name}`);
        } catch (err) {
          log(`cleanup: rds: failed to delete subnet group ${name}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: rds: list subnet groups error: ${err}`);
  }

  // DB parameter groups
  try {
    let marker: string | undefined;
    do {
      const page = await rds.send(
        new DescribeDBParameterGroupsCommand({ Marker: marker }),
      );
      for (const g of page.DBParameterGroups ?? []) {
        const name = g.DBParameterGroupName ?? "";
        if (!name.startsWith(compatPrefix)) continue;
        try {
          await rds.send(
            new DeleteDBParameterGroupCommand({ DBParameterGroupName: name }),
          );
          log(`cleanup: rds: deleted parameter group ${name}`);
        } catch (err) {
          log(`cleanup: rds: failed to delete parameter group ${name}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: rds: list parameter groups error: ${err}`);
  }
}

async function sweepElastiCache(
  ec: ElastiCacheClient,
  log: Log,
  runId: string,
): Promise<void> {
  const compatPrefix = `compat-${runId}`;

  // Cache clusters
  try {
    let marker: string | undefined;
    do {
      const page = await ec.send(
        new DescribeCacheClustersCommand({ Marker: marker }),
      );
      for (const c of page.CacheClusters ?? []) {
        const id = c.CacheClusterId ?? "";
        if (!id.startsWith(compatPrefix)) continue;
        try {
          await ec.send(new DeleteCacheClusterCommand({ CacheClusterId: id }));
          log(`cleanup: elasticache: deleted cluster ${id}`);
        } catch (err) {
          log(`cleanup: elasticache: failed to delete cluster ${id}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: elasticache: list clusters error: ${err}`);
  }

  // Replication groups
  try {
    let marker: string | undefined;
    do {
      const page = await ec.send(
        new DescribeReplicationGroupsCommand({ Marker: marker }),
      );
      for (const rg of page.ReplicationGroups ?? []) {
        const id = rg.ReplicationGroupId ?? "";
        if (!id.startsWith(compatPrefix)) continue;
        try {
          await ec.send(new DeleteReplicationGroupCommand({ ReplicationGroupId: id }));
          log(`cleanup: elasticache: deleted replication group ${id}`);
        } catch (err) {
          log(`cleanup: elasticache: failed to delete replication group ${id}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: elasticache: list replication groups error: ${err}`);
  }

  // Subnet groups
  try {
    let marker: string | undefined;
    do {
      const page = await ec.send(
        new DescribeCacheSubnetGroupsCommand({ Marker: marker }),
      );
      for (const g of page.CacheSubnetGroups ?? []) {
        const name = g.CacheSubnetGroupName ?? "";
        if (!name.startsWith(compatPrefix)) continue;
        try {
          await ec.send(new DeleteCacheSubnetGroupCommand({ CacheSubnetGroupName: name }));
          log(`cleanup: elasticache: deleted subnet group ${name}`);
        } catch (err) {
          log(`cleanup: elasticache: failed to delete subnet group ${name}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: elasticache: list subnet groups error: ${err}`);
  }

  // Parameter groups
  try {
    let marker: string | undefined;
    do {
      const page = await ec.send(
        new DescribeCacheParameterGroupsCommand({ Marker: marker }),
      );
      for (const g of page.CacheParameterGroups ?? []) {
        const name = g.CacheParameterGroupName ?? "";
        if (!name.startsWith(compatPrefix)) continue;
        try {
          await ec.send(new DeleteCacheParameterGroupCommand({ CacheParameterGroupName: name }));
          log(`cleanup: elasticache: deleted parameter group ${name}`);
        } catch (err) {
          log(`cleanup: elasticache: failed to delete parameter group ${name}: ${err}`);
        }
      }
      marker = page.Marker;
    } while (marker);
  } catch (err) {
    log(`cleanup: elasticache: list parameter groups error: ${err}`);
  }
}

async function sweepEC2(
  ec2: EC2Client,
  log: Log,
  runId: string,
): Promise<void> {
  // VPCs created by compat tests (tagged with runId or having compat-* name)
  try {
    let nextToken: string | undefined;
    do {
      const page = await ec2.send(
        new DescribeVpcsCommand({ NextToken: nextToken }),
      );
      for (const vpc of page.Vpcs ?? []) {
        const vpcId = vpc.VpcId ?? "";
        const nameTag = vpc.Tags?.find((t) => t.Key === "Name")?.Value ?? "";
        // Delete VPCs that have a compat tag or name
        if (nameTag.startsWith("compat-") || nameTag.startsWith(runId)) {
          try {
            await ec2.send(new DeleteVpcCommand({ VpcId: vpcId }));
            log(`cleanup: ec2: deleted vpc ${vpcId} (${nameTag})`);
          } catch (err) {
            log(`cleanup: ec2: failed to delete vpc ${vpcId}: ${err}`);
          }
        }
      }
      nextToken = page.NextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: ec2: list vpcs error: ${err}`);
  }
}

async function sweepECR(
  ecr: ECRClient,
  log: Log,
  runId: string,
): Promise<void> {
  try {
    let nextToken: string | undefined;
    do {
      const page = await ecr.send(
        new DescribeRepositoriesCommand({ nextToken }),
      );
      for (const repo of page.repositories ?? []) {
        const name = repo.repositoryName ?? "";
        if (!name.startsWith(runId) && !name.startsWith(`compat-${runId}`)) continue;
        try {
          await ecr.send(
            new DeleteRepositoryCommand({
              repositoryName: name,
              force: true,
            }),
          );
          log(`cleanup: ecr: deleted repository ${name}`);
        } catch (err) {
          log(`cleanup: ecr: failed to delete repository ${name}: ${err}`);
        }
      }
      nextToken = page.nextToken;
    } while (nextToken);
  } catch (err) {
    log(`cleanup: ecr: list repositories error: ${err}`);
  }
}

// ─── Public API ───────────────────────────────────────────────────────────────

/**
 * Sweep all resources belonging to `runId` across every service.
 * Scoped to the exact run ID so concurrent runs do not interfere with each
 * other. Runs every sweeper concurrently; each is individually fault-tolerant.
 * Set OVERCAST_COMPAT_NO_CLEANUP=1 to suppress (useful when debugging).
 */
export async function sweepAll(
  clients: Clients,
  log: Log,
  runId: string,
): Promise<void> {
  if (process.env.OVERCAST_COMPAT_NO_CLEANUP === "1") {
    log("cleanup: suppressed by OVERCAST_COMPAT_NO_CLEANUP=1");
    return;
  }

  log(`cleanup: sweeping resources for ${runId}…`);

  await Promise.allSettled([
    sweepS3(clients.s3, log, runId),
    sweepSQS(clients.sqs, log, runId),
    sweepSNS(clients.sns, log, runId),
    sweepDynamoDB(clients.dynamodb, log, runId),
    sweepLambda(clients.lambda, log, runId),
    sweepCloudWatchLogs(clients.logs, log, runId),
    sweepIAM(clients.iam, log, runId),
    sweepKinesis(clients.kinesis, log, runId),
    sweepKMS(clients.kms, log, runId),
    sweepSecretsManager(clients.secretsmanager, log, runId),
    sweepSSM(clients.ssm, log, runId),
    sweepEventBridge(clients.eventbridge, log, runId),
    sweepECS(clients.ecs, log, runId),
    sweepRDS(clients.rds, log, runId),
    sweepElastiCache(clients.elasticache, log, runId),
    sweepEC2(clients.ec2, log, runId),
    sweepECR(clients.ecr, log, runId),
  ]);

  log("cleanup: sweep complete");
}
