* [networking] a Docker network Overcast could not read is no longer reported as correct.
  an inspect that failed for any reason other than "no such network" fell straight into the create, and Docker returns an existing network *unchanged* — so a drifted network stayed drifted while `/_overcast/health` showed no mismatch, no advisory, and the isolation this run asked for rather than the one the network had
  a failed read is now retried once, a blind create is verified rather than trusted, and a network that still cannot be read is reported as unverified: health degrades, the advisory fires, and the reason is quoted
* [networking] `/_overcast/health` stops reporting a network that is no longer there.
  the report kept the last thing Overcast knew about every network forever: after `overcast network reset` rebuilt a drifted plane the advisory telling you to run it stayed up for the life of the daemon, and a deleted VPC's network was still listed as live
  a destroyed network is dropped when the Docker watcher sees it go, and a deleted VPC takes its network out with it — except where a sharer on the `shared` strategy still has it
* [cli] `overcast network reset` says when it could not judge a network, instead of calling it already correct.
  a per-VPC network from before Overcast recorded the internet-gateway state is declined, rightly — there is no way to tell an isolated bridge from a gateway-attached one, and a rebuild on a guess writes a state nothing chose. It reported that as "Every network is already in the state this configuration asks for", which is the opposite claim
  `/_overcast/health` no longer names that command for those networks either; the repair is a restart, and it now says so
+ [networking] a console advisory when `OVERCAST_VPC_EGRESS=none` cannot withhold egress on this host.
  on Docker Desktop with Overcast on the host, containers reach the Lambda Runtime API at the host's own address and an internal control plane would sever it, so that one network stays routable and every container keeps a route out. `none` is set to *prove* a stack has no external dependency, and until now the shortfall was one WARN at boot
  it distinguishes a shortfall from a choice: where the deprecated `OVERCAST_CONTROL_PLANE_INTERNAL=false` is what left the plane routable, the host was never consulted, and the advisory says the isolation was given up rather than refused
* [lambda] the Runtime API reachability probe can now actually fetch its image.
  the 60s image pull was nested inside the 45s budget for the whole candidate walk, so on a cold machine with a slow link it was truncated, every candidate came back unmeasured, and the address was chosen unverified. The pull happens once, before the walk, against its own clock
