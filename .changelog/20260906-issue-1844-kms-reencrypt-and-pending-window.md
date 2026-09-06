*! [kms] `ReEncrypt` refuses a `SourceKeyId` that did not produce the ciphertext with `IncorrectKeyException`
  migration: drop the wrong `SourceKeyId`, or name the key the ciphertext was encrypted under; omitting it still resolves the key from the symmetric ciphertext blob
* [kms] `ScheduleKeyDeletion` returns `PendingWindowInDays` alongside `KeyId`, `DeletionDate` and `KeyState`
  the response omitted the waiting period that applied, so a caller could not read back what it asked for or discover the 30-day default
