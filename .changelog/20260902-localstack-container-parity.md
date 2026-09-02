+ [config] `DOCKER_HOST` is honoured when `LAMBDA_DOCKER_SOCKET` is unset, so Colima, Rancher Desktop, Podman and rootless Docker reach their daemon.
  `unix://`, `tcp://`, `npipe://` and `http://` are dialable; `ssh://` and `https://` warn and fall back to the platform socket
+ [config] a volume mounted at LocalStack's `/var/lib/localstack` becomes the state directory when nothing else says where state goes.
  a compose file migrated from LocalStack unchanged used to run silently ephemeral, because the mount was not where Overcast looks
