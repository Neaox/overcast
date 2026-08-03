/**
 * index.ts — the suite's complete group list, in registration order.
 *
 * Separate from `runner.ts`, which starts a run as a side effect of being
 * imported, so tests can resolve the suite's real impl keys against the real
 * registry.json without running anything.
 */
import type { TestGroup } from "../lib/harness.js";
import type { ImplMap } from "../lib/registry.js";
import { makeS3Groups } from "./s3.js";
import { makeSQSGroups } from "./sqs.js";
import { makeDynamoDBGroups } from "./dynamodb.js";
import { makeSNSGroups } from "./sns.js";
import { makeLambdaGroups } from "./lambda.js";
import { makeCloudWatchLogsGroups } from "./cloudwatch-logs.js";
import { makeSESGroups } from "./ses.js";
import { makeIAMGroups } from "./iam.js";
import { makeSTSGroups } from "./sts.js";
import { makeSecretsManagerGroups } from "./secretsmanager.js";
import { makeKMSGroups } from "./kms.js";
import { makeSSMGroups } from "./ssm.js";
import { makeKinesisGroups } from "./kinesis.js";
import { makeEventBridgeGroups } from "./eventbridge.js";
import { makePipesGroups } from "./pipes.js";
import { makeCloudFormationGroups } from "./cloudformation.js";
import { makeEC2Groups } from "./ec2.js";
import { makeECSGroups } from "./ecs.js";
import { makeCognitoGroups } from "./cognito.js";
import { makeAppSyncGroups } from "./appsync.js";
import { makeAPIGatewayGroups } from "./apigateway.js";
import { makeCloudFrontGroups } from "./cloudfront.js";
import { makeRDSGroups } from "./rds.js";
import { makeElastiCacheGroups } from "./elasticache.js";
import { makeStepFunctionsGroups } from "./stepfunctions.js";
import { makeWAFGroups } from "./waf.js";
import { makeShieldGroups } from "./shield.js";
import { makeEFSGroups } from "./efs.js";

/** Every group the suite implements, in registration order. */
export function makeAllGroups(suite: string): TestGroup[] {
  return [
    ...makeS3Groups(suite),
    ...makeSQSGroups(suite),
    ...makeDynamoDBGroups(suite),
    ...makeSNSGroups(suite),
    ...makeLambdaGroups(suite),
    ...makeCloudWatchLogsGroups(suite),
    ...makeSESGroups(suite),
    ...makeIAMGroups(suite),
    ...makeSTSGroups(suite),
    ...makeSecretsManagerGroups(suite),
    ...makeKMSGroups(suite),
    ...makeSSMGroups(suite),
    ...makeKinesisGroups(suite),
    ...makeEventBridgeGroups(suite),
    ...makePipesGroups(suite),
    ...makeCloudFormationGroups(suite),
    ...makeEC2Groups(suite),
    ...makeECSGroups(suite),
    ...makeCognitoGroups(suite),
    ...makeAppSyncGroups(suite),
    ...makeAPIGatewayGroups(suite),
    ...makeCloudFrontGroups(suite),
    ...makeRDSGroups(suite),
    ...makeElastiCacheGroups(suite),
    ...makeStepFunctionsGroups(suite),
    ...makeWAFGroups(suite),
    ...makeShieldGroups(suite),
    ...makeEFSGroups(suite),
  ];
}

/**
 * The suite's impl map: group-qualified keys only.
 *
 * A bare test name cannot say which group it implements when several groups
 * declare it, so every key carries its group — see `validateImpls`.
 */
export function makeImplMap(groups: TestGroup[]): ImplMap {
  const impls: ImplMap = {};
  for (const g of groups) {
    for (const t of g.tests) {
      if (!t.skip) impls[`${g.name}:${t.name}`] = t.fn;
    }
  }
  return impls;
}
