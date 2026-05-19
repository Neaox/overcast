import * as cdk from "aws-cdk-lib";
import { Construct } from "constructs";
import * as apigateway from "aws-cdk-lib/aws-apigateway";
import * as dynamodb from "aws-cdk-lib/aws-dynamodb";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as events from "aws-cdk-lib/aws-events";
import * as eventTargets from "aws-cdk-lib/aws-events-targets";
import * as iam from "aws-cdk-lib/aws-iam";
import * as kms from "aws-cdk-lib/aws-kms";
import * as lambda from "aws-cdk-lib/aws-lambda";
import * as logs from "aws-cdk-lib/aws-logs";
import * as s3 from "aws-cdk-lib/aws-s3";
import * as secretsmanager from "aws-cdk-lib/aws-secretsmanager";
import * as sns from "aws-cdk-lib/aws-sns";
import * as snsSubs from "aws-cdk-lib/aws-sns-subscriptions";
import * as sqs from "aws-cdk-lib/aws-sqs";
import * as ssm from "aws-cdk-lib/aws-ssm";
import * as sfn from "aws-cdk-lib/aws-stepfunctions";

export class CdkCompatStack extends cdk.Stack {
  constructor(
    scope: Construct,
    id: string,
    props: cdk.StackProps & { runId: string },
  ) {
    super(scope, id, {
      ...props,
      synthesizer: new cdk.LegacyStackSynthesizer(),
    });

    const runId = props.runId;
    const lambdaTimeout = parseInt(
      process.env["CDK_COMPAT_LAMBDA_TIMEOUT"] ?? "10",
      10,
    );

    const bucket = new s3.Bucket(this, "CompatBucket", {
      bucketName: `${runId}-cdk-bucket`,
    });

    const dlq = new sqs.Queue(this, "CompatDlq", {
      queueName: `${runId}-cdk-dlq`,
    });

    const queue = new sqs.Queue(this, "CompatQueue", {
      queueName: `${runId}-cdk-main`,
      deadLetterQueue: {
        queue: dlq,
        maxReceiveCount: 3,
      },
    });

    const topic = new sns.Topic(this, "CompatTopic", {
      topicName: `${runId}-cdk-topic`,
    });
    topic.addSubscription(new snsSubs.SqsSubscription(queue));

    const table = new dynamodb.Table(this, "CompatTable", {
      tableName: `${runId}-cdk-table`,
      partitionKey: { name: "pk", type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      stream: dynamodb.StreamViewType.NEW_AND_OLD_IMAGES,
    });
    table.addGlobalSecondaryIndex({
      indexName: "gsi1",
      partitionKey: { name: "gsiPk", type: dynamodb.AttributeType.STRING },
      projectionType: dynamodb.ProjectionType.ALL,
    });

    const role = new iam.Role(this, "CompatLambdaRole", {
      roleName: `${runId}-cdk-role`,
      assumedBy: new iam.ServicePrincipal("lambda.amazonaws.com"),
      managedPolicies: [
        iam.ManagedPolicy.fromAwsManagedPolicyName(
          "service-role/AWSLambdaBasicExecutionRole",
        ),
      ],
    });

    const fn = new lambda.Function(this, "CompatFunction", {
      functionName: `${runId}-cdk-fn`,
      runtime: lambda.Runtime.NODEJS_20_X,
      handler: "index.handler",
      role,
      timeout: cdk.Duration.seconds(lambdaTimeout),
      memorySize: 256,
      environment: {
        COMPAT_RUN_ID: runId,
      },
      code: lambda.Code.fromInline(
        "exports.handler = async () => ({ statusCode: 200, body: 'ok' });",
      ),
    });

    const esm = new lambda.CfnEventSourceMapping(this, "CompatEsm", {
      batchSize: 1,
      enabled: true,
      eventSourceArn: queue.queueArn,
      functionName: fn.functionName,
    });

    const ddbStreamEsm = new lambda.CfnEventSourceMapping(
      this,
      "CompatDdbStreamEsm",
      {
        batchSize: 1,
        enabled: true,
        startingPosition: "LATEST",
        eventSourceArn: table.tableStreamArn!,
        functionName: fn.functionName,
      },
    );

    // A second ESM on the same stream that filters to INSERT-only events.
    // Used by the compat tests to verify FilterCriteria is provisioned and
    // honoured during delivery.
    const ddbFilteredEsm = new lambda.CfnEventSourceMapping(
      this,
      "CompatDdbFilteredEsm",
      {
        batchSize: 1,
        enabled: true,
        startingPosition: "LATEST",
        eventSourceArn: table.tableStreamArn!,
        functionName: fn.functionName,
        filterCriteria: {
          filters: [{ pattern: JSON.stringify({ eventName: ["INSERT"] }) }],
        },
      },
    );

    const logGroup = new logs.LogGroup(this, "CompatLogGroup", {
      logGroupName: `${runId}-cdk-log`,
    });

    const kmsKey = new kms.Key(this, "CompatKey", {
      alias: `${runId}-cdk-key`,
    });

    const smSecret = new secretsmanager.Secret(this, "CompatSecret", {
      secretName: `${runId}-cdk-secret`,
      generateSecretString: {
        secretStringTemplate: JSON.stringify({ key: "value" }),
        generateStringKey: "generated",
      },
    });

    const ssmParam = new ssm.StringParameter(this, "CompatParameter", {
      parameterName: `/${runId}/cdk/param`,
      stringValue: "compat-test-value",
    });

    const iamPolicy = new iam.ManagedPolicy(this, "CompatPolicy", {
      managedPolicyName: `${runId}-cdk-policy`,
      statements: [
        new iam.PolicyStatement({
          effect: iam.Effect.ALLOW,
          actions: ["s3:ListBucket"],
          resources: [bucket.bucketArn],
        }),
      ],
    });

    const vpc = new ec2.Vpc(this, "CompatVpc", {
      vpcName: `${runId}-cdk-vpc`,
      maxAzs: 1,
      natGateways: 0,
      subnetConfiguration: [
        {
          name: "public",
          subnetType: ec2.SubnetType.PUBLIC,
          cidrMask: 24,
        },
      ],
    });

    const securityGroup = new ec2.SecurityGroup(this, "CompatSG", {
      securityGroupName: `${runId}-cdk-sg`,
      vpc,
      description: "compat test security group",
      allowAllOutbound: true,
    });

    const restApi = new apigateway.RestApi(this, "CompatApi", {
      restApiName: `${runId}-cdk-api`,
      deployOptions: {
        stageName: "test",
      },
    });
    restApi.root.addMethod(
      "GET",
      new apigateway.MockIntegration({
        integrationResponses: [
          {
            statusCode: "200",
            responseTemplates: { "application/json": "{}" },
          },
        ],
        passthroughBehavior: apigateway.PassthroughBehavior.NEVER,
        requestTemplates: { "application/json": '{"statusCode":200}' },
      }),
      { methodResponses: [{ statusCode: "200" }] },
    );

    const eventBus = new events.EventBus(this, "CompatBus", {
      eventBusName: `${runId}-cdk-bus`,
    });

    const sfStateMachine = new sfn.StateMachine(this, "CompatStateMachine", {
      stateMachineName: `${runId}-cdk-sm`,
      definitionBody: sfn.DefinitionBody.fromChainable(
        new sfn.Pass(this, "StartPass"),
      ),
    });

    const sfRole = new iam.Role(this, "CompatSFRole", {
      roleName: `${runId}-cdk-sf-role`,
      assumedBy: new iam.ServicePrincipal("states.amazonaws.com"),
    });
    sfStateMachine.grantStartExecution(sfRole);

    const eventRule = new events.Rule(this, "CompatRule", {
      ruleName: `${runId}-cdk-rule`,
      eventBus,
      eventPattern: {
        source: ["compat.test"],
      },
      targets: [new eventTargets.SqsQueue(queue)],
    });

    const nested = new cdk.NestedStack(this, "CompatNested", {});
    const nestedQueue = new sqs.Queue(nested, "NestedQueue", {
      queueName: `${runId}-cdk-nested-queue`,
    });

    new cdk.CfnOutput(this, "BucketName", { value: bucket.bucketName });
    new cdk.CfnOutput(this, "QueueName", { value: queue.queueName });
    new cdk.CfnOutput(this, "QueueArn", { value: queue.queueArn });
    new cdk.CfnOutput(this, "DlqArn", { value: dlq.queueArn });
    new cdk.CfnOutput(this, "TopicArn", { value: topic.topicArn });
    new cdk.CfnOutput(this, "TableName", { value: table.tableName });
    new cdk.CfnOutput(this, "RoleArn", { value: role.roleArn });
    new cdk.CfnOutput(this, "FunctionName", { value: fn.functionName });
    new cdk.CfnOutput(this, "FunctionArn", { value: fn.functionArn });
    new cdk.CfnOutput(this, "EventSourceMappingId", { value: esm.ref });
    new cdk.CfnOutput(this, "DdbStreamEsmId", { value: ddbStreamEsm.ref });
    new cdk.CfnOutput(this, "DdbFilteredEsmId", { value: ddbFilteredEsm.ref });
    new cdk.CfnOutput(this, "TableStreamArn", {
      value: table.tableStreamArn!,
    });
    new cdk.CfnOutput(this, "LogGroupName", {
      value: logGroup.logGroupName,
    });
    new cdk.CfnOutput(this, "KmsKeyId", { value: kmsKey.keyId });
    new cdk.CfnOutput(this, "KmsKeyArn", { value: kmsKey.keyArn });
    new cdk.CfnOutput(this, "SecretArn", { value: smSecret.secretArn });
    new cdk.CfnOutput(this, "ParameterName", {
      value: ssmParam.parameterName,
    });
    new cdk.CfnOutput(this, "PolicyArn", { value: iamPolicy.managedPolicyArn });
    new cdk.CfnOutput(this, "VpcId", { value: vpc.vpcId });
    new cdk.CfnOutput(this, "SecurityGroupId", {
      value: securityGroup.securityGroupId,
    });
    new cdk.CfnOutput(this, "RestApiId", { value: restApi.restApiId });
    new cdk.CfnOutput(this, "EventBusName", {
      value: eventBus.eventBusName,
    });
    new cdk.CfnOutput(this, "EventBusArn", { value: eventBus.eventBusArn });
    new cdk.CfnOutput(this, "StateMachineArn", {
      value: sfStateMachine.stateMachineArn,
    });
    new cdk.CfnOutput(this, "RuleName", {
      value: eventRule.ruleName,
    });
    new cdk.CfnOutput(this, "NestedStackName", {
      value: nested.stackName,
    });
    new cdk.CfnOutput(this, "NestedQueueName", {
      value: nestedQueue.queueName,
    });
  }
}
