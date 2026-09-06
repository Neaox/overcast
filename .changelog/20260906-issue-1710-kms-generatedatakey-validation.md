* [kms] `GenerateDataKey` and `GenerateDataKeyWithoutPlaintext` honour `NumberOfBytes` instead of always returning 32 bytes
  a request for 64 bytes returned 32, so envelope-encryption code splitting a data key derived both halves from overlapping bytes
*! [kms] `GenerateDataKey` and `GenerateDataKeyWithoutPlaintext` require exactly one of `KeySpec` or `NumberOfBytes`, as AWS does
  both, neither, a `NumberOfBytes` outside 1-1024, or a `KeySpec` other than `AES_256`/`AES_128` is a `ValidationException`
  migration: pass exactly one length parameter; a call that relied on the silent 32-byte default needs `KeySpec=AES_256`
+ [kms] `GenerateRandom` returns `NumberOfBytes` (1-1024) cryptographically secure random bytes
  it previously returned 501; `CustomKeyStoreId` and `Recipient` are ignored
