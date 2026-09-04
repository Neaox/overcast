* [networking] A repaired Docker network reports `ok` in health, instead of vanishing from `docker.networks` for the rest of the daemon's life.
  the daemon re-verifies a managed network on its Docker `create` event, against the spec the probe resolved for it — read-only, so it never races `overcast network reset`.
  a `destroy` delivered after the probe recorded a network now heals on the `create` behind it, rather than under-reporting the network until Overcast is restarted.
* [networking] Docker network events reach the daemon for the first time, so health stops reporting a network that has been removed.
  the event stream filtered on the `overcast.managed` label, and the Engine sends no labels at all on a network event — so every network event had always been suppressed.
  container events are still filtered to Overcast's own; a network event is acted on, and published, only for a name this process holds a spec or a record for.
