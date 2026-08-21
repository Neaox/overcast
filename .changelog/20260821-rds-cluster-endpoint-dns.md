* [rds] Aurora cluster endpoints now resolve from other containers, and carry a port the caller can dial.
  `Endpoint` and `ReaderEndpoint` were registered nowhere in Docker's DNS, so a name ending in a
  split-horizon domain fell through to public DNS and answered loopback — an ECS task or Lambda
  reading `cluster.clusterEndpoint.hostname` from a CDK stack dialled itself and got a refused
  connection, and only the per-instance endpoint worked. The writer member's engine container now
  carries both cluster names alongside its own, and `DescribeDBClusters` renders the address and
  port from that instance on the same per-caller rules `DescribeDBInstances` has always used, so a
  host caller is given the published port instead of the engine port nothing binds on the host.
  The reader endpoint resolves to the writer: Overcast's cluster members do not share storage, so
  spreading reads over the replicas would answer from an empty database — see
  [rds.md](docs/services/rds.md#connecting-to-a-cluster).

~! [rds] The Aurora writer endpoint is now `{cluster}.cluster.{region}.rds.{base}`, not `{cluster}.cluster-rw.…`.
  `cluster-rw` was a label AWS has never used; dropping the account-specific hash from AWS's own
  `{cluster}.cluster-{hash}.…`, as every Overcast endpoint name does, leaves a bare `cluster`. The
  reader endpoint is unchanged.
  migration: nothing to do if you read the endpoint from `DescribeDBClusters` or CloudFormation —
  the new name arrives on the next deploy. If you hard-coded or reconstructed `…cluster-rw.…`
  anywhere (task definition, secret, `.env`), replace it with the value the API returns; the old
  spelling keeps resolving for an existing cluster and stops once that cluster is recreated.
