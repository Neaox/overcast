#!/usr/bin/env node
const cdk = require("aws-cdk-lib")
const sqs = require("aws-cdk-lib/aws-sqs")
const lambda = require("aws-cdk-lib/aws-lambda")
const sns = require("aws-cdk-lib/aws-sns")
const subs = require("aws-cdk-lib/aws-sns-subscriptions")
const app = new cdk.App()
const stack = new cdk.Stack(app, "TraceTest")
const q = new sqs.Queue(stack, "Q")
const t = new sns.Topic(stack, "T")
t.addSubscription(new subs.SqsSubscription(q))
new lambda.Function(stack, "F", {
  runtime: lambda.Runtime.NODEJS_22_X,
  handler: "index.handler",
  code: lambda.Code.fromInline("exports.handler = async () => ({ statusCode: 200 })"),
}).addEventSource(new cdk.aws_lambda_event_sources.SqsEventSource(q))
