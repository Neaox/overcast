---
title: "Running two instances on one host"
description: "Which listeners find a free port on their own, which port bases you have to move by hand, what a pinned port that is taken does, and what a second instance loses without port 53."
section: "Reference"
tags:
  - configuration
  - docs
  - host
  - instances
  - ports
  - running
---

# Running two instances on one host

Move the AWS API port and, if state is persistent, the data directory. The
listeners Overcast binds for itself get out of the way on their own; the ports
it publishes for database-style containers do not, so move those bases too if
both instances will run them:

```bash
OVERCAST_PORT=4576 OVERCAST_DATA_DIR=~/.overcast/second RDS_PORT_BASE=34060 overcast serve
```

| Listener           | Default | When the default is taken                                        |
| ------------------ | ------- | ---------------------------------------------------------------- |
| AWS API            | `4566`  | Startup fails — set `OVERCAST_PORT`                              |
| Web console        | `4567`  | Falls back to an ephemeral port, logged at startup               |
| Lambda Runtime API | `9001`  | Falls back to an ephemeral port, logged at startup               |
| SMTP capture       | `1025`  | Falls back to an ephemeral port, logged at startup               |
| ECR registry       | `4510`  | Falls back to an ephemeral port                                  |
| Container DNS      | `53`    | The second instance runs without its resolver — see below        |

The fallbacks are safe because nothing is told the default port: each Lambda
execution environment is handed its own per-container Runtime API address, and
the mailer that feeds the Inbox learns the address the SMTP server actually
bound. The console prints its port, and Lambda and the Inbox keep working.

## The port bases are different

`RDS_PORT_BASE`, `ELASTICACHE_PORT_BASE`, `MSK_PORT_BASE` and
`EFS_NFS_PORT_BASE` hand out ports above their base from each instance's own
records, without asking the host. Two instances sharing a base both offer their
first database the same port, and the second one fails to start the container.
Give the second instance bases of its own.

## A port you set yourself is pinned

A pinned port that is taken is not replaced. For the web console that stops
startup. The Lambda Runtime API and SMTP capture start degraded instead: a
warning at startup names the variable to change, Lambda invocations fail until
it is fixed — as do SES, SNS and Cognito mail — and `GET /_overcast/health`
reports `status: degraded` with the failed listener, its bind error and the fix
under `listeners`. A listener that fell back appears there too, with
`fellBack: true` and its actual address.

## The data directory is the instance's identity

Every Docker network, container and volume an instance creates is labelled with
an identity derived from its data directory, and an instance only ever removes
what carries its own label. Two instances on separate data directories therefore
leave each other's resources alone, whatever they share otherwise. Two on one
data directory share the label and the sweep: each treats the other's networks
as its own, and one's startup or shutdown can remove what the other is using.
That holds for memory-backed instances too — `OVERCAST_STATE=memory` does not
write to the directory, but the directory still names the instance — so give
the second instance its own `OVERCAST_DATA_DIR` even when neither persists
anything.

## DNS and the Docker network

Port `53` cannot be shared, so the second instance runs without the built-in
resolver: the containers it starts still reach it by the exact split-horizon
hostnames, but not by their subdomains (virtual-hosted S3, API Gateway and
Lambda function URLs). Both instances also share the `overcast` Docker network
by default; set a different `OVERCAST_NETWORK` on the second if their containers
must not see each other.

## Related

- [Bind address and port](./ports.md)
- [Environment variable reference](./reference.md)
- [Configuration](../configuration.md)
