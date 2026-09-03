---
title: "Egress modes"
description: "OVERCAST_VPC_EGRESS decides whether the containers Overcast starts reach anything outside your machine: open, none or routed, and what each one changes."
section: "Networking"
tags:
  - docker
  - docs
  - egress
  - networking
  - vpc
---

# Egress modes

`OVERCAST_VPC_EGRESS` decides whether the containers Overcast starts can reach
anything outside your machine.

| Mode | What a container can reach | Use it when |
| --- | --- | --- |
| `open` (default) | Everything: other resources it is allowed to see, Overcast's own APIs, and the internet — including real AWS endpoints and third-party APIs | Normal development, and any stack whose functions call something outside the emulator. This is what LocalStack, Moto and SAM CLI do |
| `none` | Its own plane, and Overcast's own APIs. Nothing outside the machine — outbound connections fail with `ENETUNREACH` | Deterministic CI, air-gapped hosts, and proving a stack has no hidden external dependency |
| [`routed`](./routed-egress.md) | Exactly what its subnet's route table says: a `0.0.0.0/0` route to an attached internet gateway or an available NAT gateway grants egress, and no default route withholds it | Catching a missing NAT gateway locally instead of in a deploy, and any stack whose public/private subnet split is the thing you are testing |

Any other value fails startup rather than falling back to the default: this
setting decides whether your code reaches real AWS, so a typo must not quietly
restore the default.

It is one setting for the whole topology rather than a flag per network, because
a container sits on two Docker networks at once and takes its default route from
whichever of them is routable. Isolating one and not the other settles nothing.

## What `none` covers

**Every container, not only the ones in a VPC.** It makes the shared data plane
`--internal` too, so a Lambda function with no `VpcConfig` loses its route out
along with everything else. Before egress modes that plane was never isolated,
which made "hermetic" leak on the most common placement there is. If you have no
VPCs at all, this setting still changes your stack.

**Invocations keep working.** The Lambda Runtime API and `AWS_ENDPOINT_URL` calls
back into the emulator reach a server on this machine, so they are not egress:
functions still run, and only what leaves the machine is withheld.

> [!WARNING]
> **On Docker Desktop, `none` cannot isolate the control plane.** Containers there
> reach Overcast at your host's own address, which `--internal` would cut off,
> stranding every invocation at INIT. Overcast leaves that one network routable,
> says so at startup, reports it in `/_overcast/health`, and raises the
> `vpc-egress-not-withheld` advisory on the console — so the stack is not
> hermetic and you are not left to notice on your own. Every data plane *is*
> isolated. Run Overcast in a container, or against a native Linux Docker daemon,
> for the whole of `none`.

## Why an `--internal` network still reaches the internet

A VPC network is `--internal` when its VPC has no internet gateway, under `open`
as before. That costs `open` nothing: the container is also on the control plane,
which `open` leaves routable, so it has egress either way. The flag stays honest
about your template instead of being flattened — what changed is that it no
longer *decides* egress on its own, which is what used to make a private subnet
behind a NAT gateway indistinguishable from an isolated one. Under `routed`,
every VPC plane is `--internal` whatever the gateway says, and the route out is a
second network per VPC.

So `docker network inspect` can report `Internal: true` for a network whose
containers plainly reach the internet. Three places say why, and all three agree:

```sh
overcast network status     # "… — egress via overcast_control"
docker network inspect overcast-vpc-<id> --format '{{index .Labels "overcast.network.egress"}}'
```

and the startup log's `vpc network isolation` line, which names the mode and the
route out for every VPC network as it is created.

## Reaching real AWS from a container

A hybrid stack — most of it emulated, one client talking to a real regional
endpoint, a peered private endpoint, or a third-party API — works under `open`
with no extra configuration: every container Overcast starts has a route out,
`VpcConfig` or not. The only work is telling the SDK which client goes where.
Overcast injects the emulator's endpoint into every container it starts, and a
Lambda container's region and credentials as well:

| Variable | Injected into | Effect |
| --- | --- | --- |
| `AWS_ENDPOINT_URL` | every container | Every SDK client defaults to Overcast |
| `AWS_ENDPOINT_URL_SSM`, `AWS_ENDPOINT_URL_SECRETS_MANAGER` | Lambda | The two per-service overrides the AWS Parameters and Secrets extension reads |
| `AWS_REGION` / `AWS_DEFAULT_REGION` | Lambda | The emulator's region |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN` | Lambda | Dummy credentials the emulator accepts |

An ECS task gets `AWS_ENDPOINT_URL` and nothing else, so put its region and
credentials in the task definition's `environment` as you would for a deploy.

So the default is "everything is local", and you opt one client out of it —
with a real endpoint and real credentials:

```javascript
import { S3Client } from "@aws-sdk/client-s3";
import { SecretsManagerClient } from "@aws-sdk/client-secrets-manager";

// Emulated: picks up AWS_ENDPOINT_URL, no configuration needed.
const localS3 = new S3Client({});

// Real AWS: an explicit endpoint beats the injected variable, and real
// credentials beat the injected dummies.
const realSecrets = new SecretsManagerClient({
  region: "ap-southeast-2",
  endpoint: "https://secretsmanager.ap-southeast-2.amazonaws.com",
  credentials: {
    accessKeyId: process.env.REAL_AWS_ACCESS_KEY_ID,
    secretAccessKey: process.env.REAL_AWS_SECRET_ACCESS_KEY,
  },
});
```

| Rule | Detail |
| --- | --- |
| Pass the real credentials in under names of your own | Never the `AWS_*` ones, which Overcast owns and would overwrite |
| Explicit `endpoint` wins | Per-client configuration beats `AWS_ENDPOINT_URL` in every AWS SDK |
| Real calls need real credentials | The injected dummies are rejected by AWS with `InvalidClientTokenId` |
| Costs are real | This is your account. A loop in a local function bills like a loop in a deployed one |
| Not for CI | Set `none` there, so the same code fails fast with `ENETUNREACH` instead of quietly reaching production |
| Or make it match your template | `routed` gives a function egress only where its subnet's route table does, so a `VpcConfig` in a private subnet with no NAT gateway fails locally as it would deployed |

## Control-plane isolation

**Deprecated.** `OVERCAST_CONTROL_PLANE_INTERNAL=auto|true|false` pins the
`overcast_control` network's isolation on top of the mode above. It still works
and still wins where it is set, and setting it logs a deprecation notice.

Prefer the mode: `OVERCAST_VPC_EGRESS=none` for what `true` meant, `open` for
what `false` meant — applied to every network rather than to one. Egress is a
property of the whole topology, and pinning a single network never settled it.

## Related

- [`routed`: egress from your route tables](./routed-egress.md) — the per-subnet mode
- [The Docker networks Overcast uses](./docker-networks.md) — what the modes are applied to
- [Lambda, ECS and VPCs](./vpcs.md) — what VPC membership restricts on top of egress
- [Networking and host-based addressing](../networking.md) — the rest of the addressing story
