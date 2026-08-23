~ [web] the DynamoDB, RDS, ElastiCache, EFS, S3 and ECR list pages render
  through the shared resource table, so each one sorts by clicking a column
  header — the identity column plus the counts and timestamps (items, nodes,
  mount targets, created) — and the sort is deep-linkable as `?sort=name` /
  `?sort=-name`, surviving a reload. RDS, ElastiCache and EFS also gain the
  **Columns** menu for their wider tables. The ECR images table and DynamoDB's
  secondary-index tables sort the same way.
