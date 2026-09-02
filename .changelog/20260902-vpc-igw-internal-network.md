* [ec2] Attaching an internet gateway now takes a VPC network out of `--internal` even with containers on it, and fails the call when it cannot.
  containers are moved to the recreated network with their addresses and DNS aliases; a refusal answers InternalError and records no attachment
  under the `shared` strategy a network shared by several VPCs is external while any of them has a gateway; a flag found stale at startup is repaired or reported as the `vpc-network-isolation-stale` health advisory
