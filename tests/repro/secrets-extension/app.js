#!/usr/bin/env node
// CDK app reproducing the Lambda hang when a function fetches a secret through
// the AWS Parameters and Secrets Lambda Extension.
//
// The extension layer is not something CDK can synthesise — it is an
// AWS-managed layer. Publish the cached zip into the target Overcast first and
// pass the resulting ARN in EXTENSION_LAYER_ARN; run.sh does both.

const { App, Stack, CfnOutput, Duration } = require("aws-cdk-lib")
const lambda = require("aws-cdk-lib/aws-lambda")
const secretsmanager = require("aws-cdk-lib/aws-secretsmanager")
const path = require("path")

const EXTENSION_LAYER_ARN = process.env.EXTENSION_LAYER_ARN
if (!EXTENSION_LAYER_ARN) {
  throw new Error("EXTENSION_LAYER_ARN is required — publish the layer zip first")
}

// Roughly the shape of a Google service-account credential: the payload the
// reporting function actually fetches, and large enough that a response-framing
// bug would not be masked by a tiny body.
const SECRET_VALUE = JSON.stringify({
  type: "service_account",
  project_id: "repro-project",
  private_key_id: "a".repeat(40),
  private_key: `-----BEGIN PRIVATE KEY-----\n${"b".repeat(1600)}\n-----END PRIVATE KEY-----\n`,
  client_email: "repro@repro-project.iam.gserviceaccount.com",
})

class ReproStack extends Stack {
  constructor(scope, id, props) {
    super(scope, id, props)

    const secret = new secretsmanager.Secret(this, "GoogleHotelApis", {
      secretName: "sm-repro-google-hotel-apis",
      secretStringValue: require("aws-cdk-lib").SecretValue.unsafePlainText(SECRET_VALUE),
    })

    const fn = new lambda.Function(this, "QueueListingUpdates", {
      functionName: "repro-queue-listing-updates",
      runtime: lambda.Runtime.NODEJS_22_X,
      handler: "index.handler",
      code: lambda.Code.fromAsset(path.join(__dirname, "lambda")),
      memorySize: 2048,
      timeout: Duration.seconds(30),
      layers: [
        lambda.LayerVersion.fromLayerVersionArn(this, "ParamsAndSecrets", EXTENSION_LAYER_ARN),
      ],
      environment: {
        SECRET_NAME: secret.secretName,
        // Surfaces the extension's own view of the request/response cycle,
        // which is the half of the exchange Overcast's logs cannot see.
        PARAMETERS_SECRETS_EXTENSION_LOG_LEVEL: "debug",
      },
    })
    secret.grantRead(fn)

    new CfnOutput(this, "FunctionName", { value: fn.functionName })
    new CfnOutput(this, "SecretName", { value: secret.secretName })
  }
}

const app = new App()
new ReproStack(app, "SecretsExtensionRepro", {
  env: { account: "000000000000", region: process.env.AWS_REGION ?? "ap-southeast-2" },
})
