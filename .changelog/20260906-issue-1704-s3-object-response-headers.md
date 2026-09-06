* [s3] `HeadObject` now returns the `x-amz-meta-*` user metadata `GetObject` returns for the same object, instead of an empty metadata map
* [s3] an unsatisfiable `Range` now answers `416` with an `InvalidRange` XML body and a `Content-Length` matching it
  it previously announced the whole object length and sent no body, so SDK clients saw a broken connection rather than an error code
  `HeadObject` honours `Range` too, and a `Range` S3 cannot parse is ignored — the whole object, as on AWS — rather than a 416
