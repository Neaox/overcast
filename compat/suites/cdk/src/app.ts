import * as cdk from "aws-cdk-lib";
import { CdkCompatStack } from "./stack.js";

const runId = process.env["OVERCAST_COMPAT_RUN_ID"] ?? "oc-local";
const region = process.env["OVERCAST_DEFAULT_REGION"] ?? "us-east-1";
const account = process.env["OVERCAST_ACCOUNT_ID"] ?? "000000000000";

const stackName = `OcCompat-${runId}`;

class CompatStage extends cdk.Stage {
  constructor(
    scope: cdk.App,
    id: string,
    props: cdk.StageProps & { runId: string },
  ) {
    super(scope, id, props);
    new CdkCompatStack(this, "Stack", {
      stackName,
      runId: props.runId,
      env: props.env,
    });
  }
}

const app = new cdk.App();

new CompatStage(app, `CompatStage-${runId}`, {
  runId,
  env: {
    account,
    region,
  },
});
