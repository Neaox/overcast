~! [networking] `OVERCAST_VPC_EGRESS=open|routed|none` decides container egress; `open` is the default, so every container reaches the internet.
  migration: nothing to do on Docker Desktop, which already behaved this way. A containerised or native-Linux host that relied on the control plane being `--internal` gets egress back — set `OVERCAST_VPC_EGRESS=none` to keep the isolation
  `none` makes every network Overcast creates `--internal` — the two planes and every per-VPC network — for deterministic CI and air-gapped hosts. Overcast's own APIs and the Lambda Runtime API keep working, because reaching a server on this machine is not egress
  `routed` decides egress per subnet, from that subnet's route table — see the entry below
  a VPC network still follows its internet gateway under `open`, and that now costs nothing: the container is also on the routable control plane and takes its default route from there. What changed is that the gateway no longer decides egress on its own, which is what made a private-with-NAT subnet indistinguishable from an isolated one
deprecated [networking] `OVERCAST_CONTROL_PLANE_INTERNAL` — set `OVERCAST_VPC_EGRESS` instead. Still honoured, and setting it logs a notice.
  it pins one network, and a container takes its default route from whichever of its networks is routable, so isolating one of them settled nothing. `true` becomes `OVERCAST_VPC_EGRESS=none`, `false` becomes `open`
+ [networking] every Docker network Overcast reuses is verified field by field against the state this configuration would create, on every start.
  driver, isolation, IPv6, IPAM and driver options are all compared, not just the isolation flag — Docker's create-network call returns an existing network unchanged, so one made by an older version keeps every setting it was born with while looking correct
  a network with no `overcast.network.spec-hash` label is treated as mismatched: those are the networks that have actually been wrong
  a mismatched plane with nothing attached is recreated; one with containers attached is left alone, warned about by name and field, reported as degraded in `/_overcast/health`, and raised as a console advisory. A network another tool created is never rebuilt, whatever its name
  on the first start after upgrading, every VPC network mismatches — none carries a spec-hash label yet — and each is rebuilt once under its containers, which drops open connections across that VPC bridge. Containers are reconnected at the address and aliases they had, and their control-plane connection is untouched, so an in-flight invocation keeps its Runtime API
+ [cli] `overcast network status` and `overcast network reset` report and rebuild the Docker networks Overcast manages.
  reset stops the containers Overcast started, disconnects containers it did not and leaves them running, then rebuilds the network to spec. `--dry-run` prints the plan and changes nothing
* [ec2] one Overcast instance no longer deletes another's VPC networks.
  the reconcile sweep removed every network labelled `overcast.service=ec2` that its own store did not claim, which on a shared daemon is a neighbour's live VPC network. Every network now carries the identity of the instance that created it, and an instance removes only its own
  VPC networks are named `{OVERCAST_NETWORK}-vpc-{vpcID}`, unchanged at the default
