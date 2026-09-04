//go:build dev

package dynamodb

import "github.com/overcast-sh/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Table management
		capabilities.Capability{Service: "dynamodb", Operation: "CreateTable", Category: "Table management", Status: capabilities.StatusSupported, Notes: "Includes GSI/LSI definitions; an omitted `BillingMode` defaults to `PROVISIONED`, which requires `ProvisionedThroughput` on the table and on every GSI, while `PAY_PER_REQUEST` rejects it"},
		capabilities.Capability{Service: "dynamodb", Operation: "DeleteTable", Category: "Table management", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodb", Operation: "DescribeTable", Category: "Table management", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodb", Operation: "ListTables", Category: "Table management", Status: capabilities.StatusSupported, Notes: "Region-scoped — lists only tables in the request's region; Limit (default/max 100) and ExclusiveStartTableName honored; LastEvaluatedTableName echoed when more tables remain"},
		capabilities.Capability{Service: "dynamodb", Operation: "UpdateTable", Category: "Table management", Status: capabilities.StatusSupported, Notes: "BillingMode, ProvisionedThroughput, GSI create/delete/update-throughput, AttributeDefinitions, StreamSpecification"},
		capabilities.Capability{Service: "dynamodb", Operation: "DescribeTimeToLive", Category: "Table management", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodb", Operation: "UpdateTimeToLive", Category: "Table management", Status: capabilities.StatusSupported, Notes: "TTL-based item expiry; sweeper deletes expired items hourly"},
		// Item operations
		capabilities.Capability{Service: "dynamodb", Operation: "PutItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "Includes `ConditionExpression`, `ReturnValues` (`ALL_OLD`); key attributes must be present and carry their declared `AttributeDefinitions` type, while non-key attributes stay schemaless; records AWS/DynamoDB CloudWatch metrics SuccessfulRequestLatency, ConsumedWriteCapacityUnits, UserErrors/SystemErrors"},
		capabilities.Capability{Service: "dynamodb", Operation: "GetItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "Includes `ProjectionExpression`; a `Key` must name exactly the key schema's attributes with their declared types; records SuccessfulRequestLatency, ConsumedReadCapacityUnits, UserErrors/SystemErrors"},
		capabilities.Capability{Service: "dynamodb", Operation: "UpdateItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "SET/REMOVE/ADD/DELETE clauses; all `ReturnValues` variants; upsert; a `Key` must name exactly the key schema's attributes with their declared types; records SuccessfulRequestLatency, ConsumedWriteCapacityUnits, UserErrors/SystemErrors"},
		capabilities.Capability{Service: "dynamodb", Operation: "DeleteItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "`ConditionExpression`, `ReturnValues` (`ALL_OLD`); a `Key` must name exactly the key schema's attributes with their declared types; records SuccessfulRequestLatency, ConsumedWriteCapacityUnits, UserErrors/SystemErrors"},
		capabilities.Capability{Service: "dynamodb", Operation: "BatchGetItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "Up to 100 items across tables; every key is checked against its table's key schema before any read; records SuccessfulRequestLatency/ConsumedReadCapacityUnits per table touched"},
		capabilities.Capability{Service: "dynamodb", Operation: "BatchWriteItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "Up to 25 put/delete operations; every item and key is checked against its table's key schema before any write is applied; records SuccessfulRequestLatency/ConsumedWriteCapacityUnits per table touched"},
		// Query & scan
		capabilities.Capability{Service: "dynamodb", Operation: "Query", Category: "Query & scan", Status: capabilities.StatusSupported, Notes: "`KeyConditionExpression` (values type-checked against the declared key types), `FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, `ScanIndexForward`, `Select=COUNT`; `ConsistentRead=true` on a GSI is rejected as AWS does; records SuccessfulRequestLatency and ConsumedReadCapacityUnits (with GlobalSecondaryIndexName when IndexName names a real GSI)"},
		capabilities.Capability{Service: "dynamodb", Operation: "Scan", Category: "Query & scan", Status: capabilities.StatusSupported, Notes:
		// Transactions
		"`FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, parallel scan (`Segment`/`TotalSegments`), `Select=COUNT`; `ConsistentRead=true` on a GSI is rejected as AWS does; records SuccessfulRequestLatency and ConsumedReadCapacityUnits (with GlobalSecondaryIndexName when IndexName names a real GSI)"},

		capabilities.Capability{Service: "dynamodb", Operation: "TransactGetItems", Category: "Transactions", Status: capabilities.StatusSupported, Notes: "Up to 100 items across tables; every key is checked against its table's key schema; records SuccessfulRequestLatency/ConsumedReadCapacityUnits per table (2x weighted, matching AWS's transactional-read capacity accounting)"},
		capabilities.Capability{Service: "dynamodb", Operation: "TransactWriteItems", Category: "Transactions", Status: capabilities.StatusSupported, Notes: "Put, Update, Delete, ConditionCheck; all-or-nothing; a key-schema mismatch is a `ValidationException` raised before anything is applied, not a cancellation reason; records SuccessfulRequestLatency/ConsumedWriteCapacityUnits per table (2x weighted, matching AWS's transactional-write capacity accounting)"},
		// Tags
		capabilities.Capability{Service: "dynamodb", Operation: "TagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Merges tags; max 50; validates key/value lengths"},
		capabilities.Capability{Service: "dynamodb", Operation: "ListTagsOfResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Returns tags as Key/Value array"},
		capabilities.Capability{Service: "dynamodb", Operation: "UntagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Removes specified keys; idempotent on missing keys"},
		// Compatibility notes previously documented in manual tables.
		capabilities.Capability{Service: "dynamodb", Operation: "GetShardIterator", Category: "Streams interoperability", Status: capabilities.StatusSupported, Notes: "TRIM_HORIZON, LATEST, AT/AFTER_SEQUENCE_NUMBER", DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_GetShardIterator.html)"},
		capabilities.Capability{Service: "dynamodb", Operation: "RestoreTableFromBackup", Category: "Table management", Status: capabilities.StatusUnsupported, DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_RestoreTableFromBackup.html)"},
		// Global tables are modeled by AWS, so they answer 501 rather than
		// UnknownOperationException — see the Known limitations section of
		// docs/services/dynamodb.md.
		capabilities.Capability{Service: "dynamodb", Operation: "CreateGlobalTable", Category: "Global tables", Status: capabilities.StatusUnsupported, Notes: "Overcast emulates a single region", DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateGlobalTable.html)"},
		capabilities.Capability{Service: "dynamodb", Operation: "DescribeGlobalTable", Category: "Global tables", Status: capabilities.StatusUnsupported, Notes: "Overcast emulates a single region", DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeGlobalTable.html)"},
		capabilities.Capability{Service: "dynamodb", Operation: "DescribeGlobalTableSettings", Category: "Global tables", Status: capabilities.StatusUnsupported, Notes: "Overcast emulates a single region", DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeGlobalTableSettings.html)"},
		capabilities.Capability{Service: "dynamodb", Operation: "ListGlobalTables", Category: "Global tables", Status: capabilities.StatusUnsupported, Notes: "Overcast emulates a single region", DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListGlobalTables.html)"},
		capabilities.Capability{Service: "dynamodb", Operation: "UpdateGlobalTable", Category: "Global tables", Status: capabilities.StatusUnsupported, Notes: "Overcast emulates a single region", DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateGlobalTable.html)"},
		capabilities.Capability{Service: "dynamodb", Operation: "UpdateGlobalTableSettings", Category: "Global tables", Status: capabilities.StatusUnsupported, Notes: "Overcast emulates a single region", DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateGlobalTableSettings.html)"},
	)
}
