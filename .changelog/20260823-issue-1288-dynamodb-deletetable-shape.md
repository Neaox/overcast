* [dynamodb] `DeleteTable`'s response now returns the deleted table's
  description under `TableDescription` (with `TableStatus` reported as
  `DELETING`), matching `CreateTable`/`DescribeTable`/`UpdateTable` and AWS's
  documented `DeleteTable` response shape. It previously reused the
  `DescribeTable`-shaped `Table` wrapper, which strict SDK decoders would
  silently drop since the member name didn't match.
