~ [docs] Every service page now states its divergences as `| Area | On AWS | Overcast |`, and the linter keeps them that way
  Twenty-four landing pages had the AWS half buried inside the Overcast cell ("Real CloudWatch keeps 15 months") or negated into the Area cell ("No query engine"). Same facts, own column.
  `make docs-check` fails a two-column `## Differences from AWS` table unless the page says why in a `docs-differences-two-column:` marker, which itself fails once the table has three columns.
