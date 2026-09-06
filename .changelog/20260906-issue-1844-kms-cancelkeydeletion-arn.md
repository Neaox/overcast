* [kms] `CancelKeyDeletion` returns the key ARN in `KeyId`, as `ScheduleKeyDeletion` already does
  it returned the bare key id, which does not parse where a caller feeds the response value into something ARN-shaped; the emulator-only `KeyArn` member is kept
