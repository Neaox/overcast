*! [dynamodb] key attributes are validated against `KeySchema`/`AttributeDefinitions` on every item and key operation (#1637)
  a key whose type disagreed with the declared one used to be stored under an encoding a correctly typed `GetItem` never looked under
  covers `PutItem`, `GetItem`, `UpdateItem`, `DeleteItem`, the batch and transact operations, and `Query` key conditions; non-key attributes stay schemaless
  migration: send key attributes with the type `AttributeDefinitions` declares, or declare the type your code sends — real AWS rejects the mismatch either way
