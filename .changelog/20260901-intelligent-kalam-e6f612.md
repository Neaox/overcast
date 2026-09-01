~ [web/s3] the console previews text-like objects larger than 1 MiB instead of declining the object outright.
  shows the first megabyte, via the ranged read it already made
* [web/s3] the "Preview (first 1 MiB)" notice appeared on every ranged preview, a 36-byte object included, because S3 answers 206 for any satisfiable range.
  it is now keyed on Content-Range and shows exactly when the object holds more bytes than the preview does
