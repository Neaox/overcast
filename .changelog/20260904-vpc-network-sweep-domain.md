* [ec2/docker] Wiping the data directory no longer strands the Docker networks and containers the previous run created, so a redeploy gets its CIDR back
  The identity every Docker resource is labelled with was a UUID stored inside the very state store it named, so a wipe minted a new one and the old resources became nobody's.
  Each wipe held another VPC's /16 on the daemon until `CreateVpc` was refused with `Pool overlaps with other one on this address space` and the deploy failed at RDS.
  The identity is now derived from the data directory — its volume or bind in a container, its path and host name natively — so it survives a wipe and a memory-backed restart.
+ [router/ec2] The `vpc-network-unbacked` advisory reports a VPC whose Docker network Docker refused to create, the moment it happens
  `CreateVpc` still answers 200 and the next reconcile retries, but the health page now names the VPC and quotes Docker's reason rather than leaving an RDS or ECS failure to say so later.
