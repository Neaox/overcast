* [s3] An unsatisfiable `Range` now answers with the `InvalidRange` document real S3 sends, and without the `Content-Range` header real S3 omits.
    The 416 body carries `RangeRequested` and `ActualObjectSize`, the two fields a ranged-download client compares to find its own off-by-one; both were missing.
    The `Content-Range: bytes */<size>` sent alongside it is gone: RFC 9110 says a 416 SHOULD carry one, real S3 sends none, and AWS is the contract Overcast emulates.
    Confirmed against real S3 on 2026-09-06; the transcript is recorded so the next reader does not restore the plausible answer.
