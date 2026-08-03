+ [cloudformation] `{{resolve:...}}` dynamic references, resolved against Secrets Manager and SSM Parameter Store — a reference that cannot be resolved fails its resource rather than being written into it as literal text
* [rds] stopping a DB instance keeps its container, so `StartDBInstance` can bring the same instance back
* [rds/docker] a Docker error on the container logs endpoint is reported as an error, instead of being de-framed into corrupted-looking log output
