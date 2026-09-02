+ [config] `LS_LOG`, `ENFORCE_IAM`, `LAMBDA_REMOVE_CONTAINERS` and `DNS_ADDRESS=0` join the LocalStack-compatibility alias table.
  `LAMBDA_REMOVE_CONTAINERS` inverts onto `LAMBDA_KEEP_CONTAINERS`; the two spellings agreeing is agreement, not a conflict
~ [config] twenty more LocalStack variables are recognised as inert instead of silently ignored, each with a startup line saying why.
  `SQS_ENDPOINT_STRATEGY`, `S3_SKIP_SIGNATURE_VALIDATION`, `IAM_SOFT_MODE`, `LAMBDA_DOCKER_FLAGS`, `SNAPSHOT_*`, `PROVIDER_OVERRIDE_*` and the CORS knobs among them
