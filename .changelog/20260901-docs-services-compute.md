~ [docs] the compute and orchestration service pages are rewritten to the service page template.
  Each opens with a one-sentence positioning line, a status token and a copy-pasteable quick start, then what works and how it diverges from AWS, with the long-form material moved onto limitations/examples/troubleshooting sub-pages.
  Lambda drops from 1,169 lines to 130, ECS from 490 to 94 and CloudFormation from 450 to 109; the hand-maintained CloudFormation resource-type table and the duplicated Lambda environment-variable table are gone, because docs/cdk.md and docs/configuration.md already own them.
  Covers lambda, ecs, eks, ecr, stepfunctions, scheduler, pipes, eventbridge, cloudformation, appconfig, appconfigdata and appregistry, which leave docslint RestructurePending so the full structural bar applies to them
* [docs/scheduler] the cron section called L, W, # and the three-letter month and day names unsupported.
  Scheduler has evaluated cron through internal/awscron, shared with EventBridge rules, for several releases, so all of those expressions are accepted. The section also never mentioned that day-of-week is AWS's 1-7 from Sunday
* [docs/cloudformation] the resource-type counts now read 127 provisioned and 9 stubs of 136 registered.
  That matches the resourceHandlers map; the page previously said 126/10 in one place and 132 in another
* [docs/lambda] the CDK examples target port 4566 rather than 2456, which is not a port Overcast ever listens on
* [docs/eventbridge] ECS RunTask is listed as a target for pattern-matched rules, not only scheduled ones.
  Auto Scaling also joins the list of services that publish their own events onto the default bus
* [docs/pipes] LogConfiguration and KmsKeyIdentifier are described as discarded rather than stored-but-inert.
  Neither field exists on the pipe, so DescribePipe never returns them
