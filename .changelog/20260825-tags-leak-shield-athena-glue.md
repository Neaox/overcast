*! [shield] `ListProtections`/`DescribeProtection` no longer echo a `Tags` member on `Protection` — the Shield model's `Protection` shape has no `Tags` member; it was leaking straight out of the persisted record
  migration: read tags via `ListTagsForResource` instead of `Protection.Tags`
*! [shield] `DescribeSubscription` drops the hardcoded `SubscriptionState` member — that field belongs only to `GetSubscriptionStateResponse`, a separate operation Overcast does not implement, and was never part of `Subscription`
  migration: none available; `GetSubscriptionState` is not implemented
*! [athena] `GetWorkGroup` no longer echoes a `Tags` member on `WorkGroup` — the Athena model's `WorkGroup` shape has no `Tags` member; it was leaking straight out of the persisted record
  migration: read tags via `ListTagsForResource` instead of `WorkGroup.Tags`
*! [glue] `GetDatabase`/`GetDatabases`/`GetTable`/`GetTables` no longer echo a `Tags` member on `Database`/`Table` — the Glue model's `Database` and `Table` shapes have no `Tags` member; it was leaking straight out of the persisted record
  migration: read tags via `GetTags` instead of `Database.Tags`/`Table.Tags`
