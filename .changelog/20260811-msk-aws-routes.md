* [msk] the v2 cluster API is served at `/api/v2/clusters`, the path AWS binds it to, so `CreateClusterV2` and `DescribeClusterV2` are reachable from an unmodified SDK instead of answering 501; they were registered at the invented `/v2/clusters` in every release since `v0.0.1-alpha.0`
- [msk] the emulator-only `/v2/clusters` path prefix
  migration: use AWS's paths — `POST /api/v2/clusters`, `GET /api/v2/clusters/{ClusterArn}` with the ARN percent-encoded into a single path segment, as every AWS SDK sends it
+ [msk] `ListClustersV2`, with `clusterNameFilter`, `clusterTypeFilter`, `maxResults` and `nextToken` read from the query string where AWS binds them; a `nextToken` that cannot be decoded gets a `BadRequestException` in place of a silent restart at the first page
* [msk] `CreateClusterV2` rejects a request naming both `provisioned` and `serverless`, or neither, in place of silently creating a serverless cluster the caller never asked for
* [msk] `ListClusters`' `clusterNameFilter` matches on the cluster-name prefix, as AWS documents; it used to require the whole name
~ [msk] a malformed request is answered with `BadRequestException`, the only client-error shape the kafka model declares, in place of `ValidationException`
- [msk/cloudformation] MSK's `X-Amz-Target` dispatch and its typed JSON/CBOR surface, a second copy of the REST handlers for a protocol AWS does not model for kafka; `AWS::MSK::Cluster` and `AWS::MSK::Configuration` provisioning now dispatches over MSK's REST routes
  migration: call MSK over HTTP the way the AWS SDKs do — no MSK operation carries an `X-Amz-Target` header or a Smithy RPC v2 binding in the pinned model, so nothing an SDK sends is affected
