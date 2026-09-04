* [networking] A repaired Docker network reports `ok` in health again, instead of vanishing from `docker.networks` until the next probe.
  the daemon re-verifies a managed network on its Docker `create` event, against the spec the probe resolved for it — read-only, so it never races `overcast network reset`.
  a `destroy` delivered after the probe recorded a network now heals on the `create` behind it, rather than under-reporting the network until the next probe.
* [networking] Docker network events reach the daemon again, so health stops reporting a network that has been removed.
  the event stream filtered on the `overcast.managed` label, and the Engine sends no labels at all on a network event — so every network event was suppressed.
  container events are still filtered to Overcast's own; networks are matched by name against the specs and records this process holds.
