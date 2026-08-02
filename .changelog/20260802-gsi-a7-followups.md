* [dynamodb] LSI queries read only the queried partition instead of scanning the whole table, and LSI reads now honour the sparse-index rule — an item without the index sort key is no longer returned
*! [dynamodb] `ConsistentRead=true` on a Query or Scan against a global secondary index is rejected with the `ValidationException` AWS returns, instead of silently serving a read AWS has no way to serve
  migration: drop `ConsistentRead` (or set it to `false`) on GSI queries — the same call already fails against real AWS
