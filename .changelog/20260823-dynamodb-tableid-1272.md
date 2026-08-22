* [dynamodb] `CreateTable`, `DescribeTable`, `UpdateTable`, and `DeleteTable`
  responses now include `TableDescription`/`Table.TableId` — a UUID minted
  once at `CreateTable` time and persisted with the table, so it stays stable
  across reads and restarts. Tooling that keys off a table's identity rather
  than its name or ARN (e.g. CloudFormation's `Fn::GetAtt TableId`) no longer
  gets an empty string with no indication anything was missing.
