*! [s3] `ListObjectsV2` and `ListObjects` validate `max-keys` instead of returning a 500 or ignoring it
  `max-keys=0` returns an empty untruncated page rather than crashing the request; a negative value crashed it too
  a non-numeric or out-of-int32 value is rejected with `InvalidArgument` where it used to be replaced by the 1000 default
  a value above 1000 is clamped, and `MaxKeys` reports the clamped page size; `ListObjectVersions` shares the validation
  migration: clients sending a malformed or negative `max-keys` now get a 400 and must send a value between 0 and 2147483647
+ [s3] `encoding-type=url` and `fetch-owner` on the bucket listings
  `Key`, `Prefix`, `Delimiter`, `StartAfter` and `CommonPrefixes` come back percent-encoded, with `EncodingType` echoed; an unsupported value is rejected with `InvalidArgument`
  `fetch-owner=true` adds the `Owner` element to each `ListObjectsV2` entry
