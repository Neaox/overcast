+ [networking] isolating the control plane now warns loudly at startup, because the cost lands a long way from the cause.
  a VPC-attached container joins its VPC network and the control plane; when both are internal it gets no default route at all and fails with `ENETUNREACH` inside application code, minutes later
  Overcast does not route NAT gateways or internet gateways — route tables are metadata only — so a private-with-egress subnet is not distinguished from an isolated one; the warning says that, and names `OVERCAST_VPC_EGRESS` as what decides it
  `docs/troubleshooting.md` gains a symptom-to-fix entry for `ENETUNREACH`, covering the hybrid case of reaching real AWS from a locally emulated function
* [networking] a Docker network whose isolation disagrees with the configuration is now recreated at startup, when nothing is attached to it.
  Docker never applies `--internal` retroactively, so a plane created before alpha.37 silently kept egress forever — the machine that "still worked" in the report that prompted this
  a network that still has containers attached is left alone and warned about at WARN, naming the `overcast network reset` that fixes it; both planes are checked, not just the control plane
+ [compat] `MAIN_DOCKER_NETWORK` joins the recognised-but-inert LocalStack variables, with the same reason `LAMBDA_DOCKER_NETWORK` has.
  `docs/localstack-compatibility.md` now states the one behavioural divergence plainly: LocalStack isolates no network, so a gateway-less VPC there still has egress — which is what `OVERCAST_VPC_EGRESS=open`, the default, already gives you
