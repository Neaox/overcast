~ [docs] split docs/README.md into standalone pages, leaving it a short index
  the new pages are docs/configuration.md (env vars, service names, log levels), docs/debug-endpoints.md, docs/persistence.md, docs/multi-container-networking.md and docs/troubleshooting.md
~ [docs] restructured the authored gap-lists in lambda.md, cloudformation.md and ec2.md to be table-first and Ctrl+F-navigable
  a Known-limitations summary table on lambda.md, ~30 CloudFormation Notes bullets converted to headed subsections, and 10 stale/duplicate hand-authored "Summary" tables removed across service docs where they had drifted from the generated one below them (cognito, autoscaling, backup, cloudfront, cloudtrail, organizations, secretsmanager, ssm, sts, transfer)
* [docs/ec2] corrected the OVERCAST_EC2_VPC_STRATEGY documentation in ec2.md, docs/configuration.md and docs/dev/networking.md
  the strict and remapped values are fully implemented, not "planned, falls back to shared" as previously documented; only netns is unimplemented, and it fails startup outright rather than falling back
* [docs/s3] removed a reference to a nonexistent S3_ADDRESSING_STYLE environment variable from s3.md
  addressing style is detected automatically from the request Host header (see networking.md)
* [docs] corrected the LAMBDA_DOCKER_SOCKET default in docs/configuration.md
  it named only the Unix socket path — Windows uses a named pipe by default
~ [docs] improved docs navigation grouping with topic-specific frontmatter sections
  guide pages that were all flatly grouped under "Getting Started" now sit under Networking, Storage & Performance, Reference and Troubleshooting
