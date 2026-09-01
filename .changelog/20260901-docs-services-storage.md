~ [docs] rewrote the storage and data service pages to the service page template
  s3, dynamodb, dynamodbstreams, rds, efs, elasticache, glue, athena, opensearch, backup, transfer, kinesis, firehose and msk
  each opens with one line of positioning, a status token and a runnable quick start, with divergences as a table
  rds.md drops from 464 lines to a landing page plus rds/limitations.md and rds/troubleshooting.md, s3.md to a landing page plus s3/limitations.md, and the EFS NFS walkthrough moves to efs/examples.md
* [docs/backup] backup.md said tagging was not implemented; TagResource, ListTags and UntagResource all exist
  inline BackupVaultTags and BackupPlanTags are stored at creation rather than dropped
* [docs/msk] msk.md described cluster readiness as a TCP health check on port 9092
  a cluster reaches ACTIVE only once the broker answers Kafka ApiVersions, and one that never does ends in FAILED with the reason in stateInfo
  the page also gains VPC placement from clientSubnets, serverless clusters, and the per-caller bootstrap broker string
* [docs/rds] rds.md listed one Docker image per engine
  MySQL also runs mysql:8.4 and mysql:5.7, PostgreSQL postgres:15 and postgres:14, MariaDB mariadb:10.11, and Aurora MySQL 4.0 runs mysql:8.4
* [docs/s3] s3.md said SSE headers are accepted and echoed
  object-level server-side-encryption request headers are ignored and never echoed back; only the bucket-level encryption configuration round-trips
* [docs/s3] s3.md frontmatter still named a nonexistent S3_ADDRESSING_STYLE variable, which the page body had already corrected
* [docs/glue] glue.md claimed the whole TableInput round-trips
  a Glue table stores only Name, DatabaseName, TableType, Description and CatalogId, so StorageDescriptor, PartitionKeys and Parameters are accepted and discarded
* [docs] transfer, opensearch, msk and elasticache pages now name the request fields their records drop rather than implying every input is stored
