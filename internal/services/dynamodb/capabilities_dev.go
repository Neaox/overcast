//go:build dev

package dynamodb

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Table management
		capabilities.Capability{Service: "dynamodb", Operation: "CreateTable", Category: "Table management", Status: capabilities.StatusSupported, Notes: "Includes GSI/LSI definitions"},
		capabilities.Capability{Service: "dynamodb", Operation: "DeleteTable", Category: "Table management", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodb", Operation: "DescribeTable", Category: "Table management", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodb", Operation: "ListTables", Category: "Table management", Status: capabilities.StatusSupported, Notes: "Limit (default/max 100) and ExclusiveStartTableName honored; LastEvaluatedTableName echoed when more tables remain"},
		capabilities.Capability{Service: "dynamodb", Operation: "UpdateTable", Category: "Table management", Status: capabilities.StatusSupported, Notes: "BillingMode, ProvisionedThroughput, GSI create/delete/update-throughput, AttributeDefinitions, StreamSpecification"},
		capabilities.Capability{Service: "dynamodb", Operation: "DescribeTimeToLive", Category: "Table management", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodb", Operation: "UpdateTimeToLive", Category: "Table management", Status: capabilities.StatusSupported, Notes: "TTL-based item expiry; sweeper deletes expired items hourly"},
		// Item operations
		capabilities.Capability{Service: "dynamodb", Operation: "PutItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "Includes `ConditionExpression`, `ReturnValues` (`ALL_OLD`)"},
		capabilities.Capability{Service: "dynamodb", Operation: "GetItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "Includes `ProjectionExpression`"},
		capabilities.Capability{Service: "dynamodb", Operation: "UpdateItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "SET/REMOVE/ADD/DELETE clauses; all `ReturnValues` variants; upsert"},
		capabilities.Capability{Service: "dynamodb", Operation: "DeleteItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "`ConditionExpression`, `ReturnValues` (`ALL_OLD`)"},
		capabilities.Capability{Service: "dynamodb", Operation: "BatchGetItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "Up to 100 items across tables"},
		capabilities.Capability{Service: "dynamodb", Operation: "BatchWriteItem", Category: "Item operations", Status: capabilities.StatusSupported, Notes: "Up to 25 put/delete operations"},
		// Query & scan
		capabilities.Capability{Service: "dynamodb", Operation: "Query", Category: "Query & scan", Status: capabilities.StatusSupported, Notes: "`KeyConditionExpression`, `FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, `ScanIndexForward`, `Select=COUNT`"},
		capabilities.Capability{Service: "dynamodb", Operation: "Scan", Category: "Query & scan", Status: capabilities.StatusSupported, Notes:
		// Transactions
		"`FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, parallel scan (`Segment`/`TotalSegments`), `Select=COUNT`"},

		capabilities.Capability{Service: "dynamodb", Operation: "TransactGetItems", Category: "Transactions", Status: capabilities.StatusSupported, Notes: "Up to 100 items across tables"},
		capabilities.Capability{Service: "dynamodb", Operation: "TransactWriteItems", Category: "Transactions", Status: capabilities.StatusSupported, Notes: "Put, Update, Delete, ConditionCheck; all-or-nothing"},
		// Compatibility notes previously documented in manual tables.
		capabilities.Capability{Service: "dynamodb", Operation: "GetShardIterator", Category: "Streams interoperability", Status: capabilities.StatusSupported, Notes: "TRIM_HORIZON, LATEST, AT/AFTER_SEQUENCE_NUMBER", DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_GetShardIterator.html)"},
		capabilities.Capability{Service: "dynamodb", Operation: "RestoreTableFromBackup", Category: "Table management", Status: capabilities.StatusUnsupported, DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_RestoreTableFromBackup.html)"},
	)
}
