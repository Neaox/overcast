* [ec2] the first placement into a region the startup reconcile did not cover no longer rebuilds a drifted VPC network under its containers.
  the lazy pass adopts what exists and creates what is missing; repairing a network's isolation stays with the startup pass, where a failed repair already reaches the health advisories
  a network it adopts is reported to `/_overcast/health` as it stands, a drift it leaves included, with the command that repairs it
* [ec2] a placement that cannot read the daemon or the store waits 30 seconds before asking again, instead of listing networks on every placement.
  every RunTask or invoke into the region paid a Docker round-trip serialised on the reconcile lock while the daemon was down
~ [docs/ec2] the EC2 limitations page says CIDR overlap is judged per region, and that the same CIDR in a second region lands as `unbacked`.
* [ec2] a VPC created while the startup reconcile was reading the store keeps its network, instead of being recorded unbacked.
  the daemon is listed again after the store scan, so every record the pass sees has a network older than the list; the orphan sweep takes only what the snapshot before the scan held and no record names afterwards
