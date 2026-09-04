~ [docs] Gave the S3 and RDS limitations pages room to grow again, and turned ECR's into a reference
  `s3/limitations.md` and `rds/limitations.md` sat 42 and 69 characters under the prose budget, so the next factual sentence added to either failed the build.
  S3's EventBridge material is now `s3/notifications.md` and RDS's master account and password rules `rds/master-account.md`; both pages drop to about 4,000 characters.
  `ecr/limitations.md` answered every question in a paragraph; the repository URI, what persists and the `DescribeImages` answers are now tables, 4,720 to 2,583.
