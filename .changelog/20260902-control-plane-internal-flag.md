~ [networking/config] `OVERCAST_CONTROL_PLANE_INTERNAL=auto|true|false` pins whether `overcast_control` is `--internal`, rather than only inferring it.
  `auto` is the default and keeps alpha.37's probe exactly, so nothing changes for anyone who does not set it; `true` and `false` make one pinned Overcast behave the same on every machine
  the decision and its reason are logged at info on every startup (`control plane network isolation network=overcast_control internal=true reason="auto: Overcast is containerised"`) and reported by `/_overcast/health` under `docker.networks`
  this is what decides whether compute in a VPC with no internet gateway reaches the internet, and it was previously invisible: two engineers on one pinned version got different answers with nothing anywhere saying why
+ [networking] isolating the control plane now warns loudly at startup, because the cost lands a long way from the cause.
  a VPC-attached container joins only its VPC network and the control plane; when both are internal it gets no default route at all and fails with `ENETUNREACH` inside application code, minutes later
  Overcast does not route NAT gateways or internet gateways — route tables are metadata only — so a private-with-egress subnet is not distinguished from an isolated one; the warning says that and names `OVERCAST_CONTROL_PLANE_INTERNAL=false` as the way back
  `docs/troubleshooting.md` gains a symptom-to-fix entry for `ENETUNREACH`, covering the hybrid case of reaching real AWS from a locally emulated function
* [networking] a Docker network whose isolation disagrees with the configuration is now recreated at startup, when nothing is attached to it.
  Docker never applies `--internal` retroactively, so a plane created before alpha.37 silently kept egress forever — the machine that "still worked" in the report that prompted this
  a network that still has containers attached is left alone and warned about at WARN, naming the `docker network rm` that fixes it; both planes are checked, not just the control plane
+ [compat] `MAIN_DOCKER_NETWORK` joins the recognised-but-inert LocalStack variables, with the same reason `LAMBDA_DOCKER_NETWORK` has.
  `docs/localstack-compatibility.md` now states the one behavioural divergence plainly: LocalStack isolates no network, so a gateway-less VPC there still has egress; set `OVERCAST_CONTROL_PLANE_INTERNAL=false` for that behaviour while migrating
