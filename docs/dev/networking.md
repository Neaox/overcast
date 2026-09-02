# Networking in Overcast

> How traffic reaches Overcast, how the containers it starts reach it and each
> other, and how the addresses in its responses are chosen. Companion to
> [architecture.md](./architecture.md), which covers the same ground at summary
> level in §7.
>
> **Who this is for:** anyone debugging a connection that should work and does
> not, or adding a service that starts containers. It assumes you have used AWS
> and can program. It does not assume familiarity with Docker's networking
> model — the parts that matter are explained where they come up.

---

## Contents

1. [The problem AWS does not have](#1-the-problem-aws-does-not-have)
2. [What binds where](#2-what-binds-where)
3. [Bind address versus advertised name](#3-bind-address-versus-advertised-name)
4. [Which service owns a request](#4-which-service-owns-a-request)
5. [The addresses Overcast hands back](#5-the-addresses-overcast-hands-back)
6. [Names: the split-horizon domains](#6-names-the-split-horizon-domains)
7. [The container networks](#7-the-container-networks)
8. [A container reaching Overcast](#8-a-container-reaching-overcast)
9. [A container reaching another container](#9-a-container-reaching-another-container)
10. [ECR and the fourth caller](#10-ecr-and-the-fourth-caller)
11. [IPv6](#11-ipv6)
12. [The bridge: friendly names on port 80](#12-the-bridge-friendly-names-on-port-80)
13. [Things that will bite you](#13-things-that-will-bite-you)
14. [Where to go next](#14-where-to-go-next)

---

## 1. The problem AWS does not have

On real AWS every hostname in every response is publicly routable. `sqs.us-east-1.amazonaws.com` resolves the same way from your laptop, from a Lambda function and from an ECS task, and reaches the same place. Whether the caller can reach an address is never a question, so nothing in the AWS API has to think about it.

Locally it is the whole problem, and it has two halves.

**The same name must work from several places.** A queue URL handed to your test suite has to be dialable from your machine. The same queue URL handed to a Lambda function has to be dialable from inside a container, where `localhost` means that container and nothing else.

**The same resource is not at the same address for everyone.** A database Overcast started is reachable from your machine on a published port, and from a sibling container on the engine's own port over a different network path. Both are correct; neither works from the other place.

Everything below follows from those two facts.

### Four callers, not two

They do not all reach Overcast the same way, and it is worth keeping the whole cast in mind:

| Caller | Reaches Overcast how | Where `localhost` points |
|---|---|---|
| **Your machine** — SDK, CLI, IaC tool | the published port | your machine |
| **A container Overcast started** — Lambda, ECS | an injected endpoint name, over a Docker network | that container |
| **Another container** — a database, a cache | not Overcast at all; a Docker network alias | itself |
| **The Docker daemon** — pulling an ECR image | a published port it can dial itself | the daemon's host, which under Docker Desktop is a VM |

The fourth catches people out, and it is why ECR gets [its own section](#10-ecr-and-the-fourth-caller).

---

## 2. What binds where

| Listener | Default port | Config | Honours `OVERCAST_LISTEN`? |
|---|---|---|---|
| AWS API | 4566 | `OVERCAST_PORT` | **Yes** — every address in the list |
| Web console / BFF | 4567 | `OVERCAST_UI_PORT`, `--ui-port`; `0` disables | **Partly** — first address only |
| Lambda Runtime API | 9001 | `LAMBDA_RUNTIME_API_PORT`; `0` = ephemeral; a taken default falls back to ephemeral | **No** — resolved independently |
| SMTP capture | 1025 | `OVERCAST_SMTP_PORT`; `0` = ephemeral; a taken default falls back to ephemeral | **No** — hardcoded loopback |
| Container DNS (UDP + TCP) | 53 | `OVERCAST_DNS_PORT`, `OVERCAST_DNS` | **No** — binds the wildcard |
| Bridge proxy (opt-in) | 80 | `--http-port`, `--bridge` | **No** — binds the wildcard |

Four of those diverge, each for a reason.

**`OVERCAST_LISTEN` takes a list.** Comma-separated; blanks dropped and duplicates collapsed, since binding one address twice would fail the second listen. A value that names *no* address is a hard error rather than a silent fall back to the default — the reasoning being that unset means "use the default", while set-to-nothing means the value is wrong, and defaulting it would bind every interface, the opposite of what anyone setting it wants. The pre-rename name, `OVERCAST_HOST` (see [§3](#3-bind-address-versus-advertised-name)), has been removed rather than kept as an alias — Overcast is alpha software, so a leftover `OVERCAST_HOST` is a startup error naming the replacement rather than a silent no-op.

**The default depends on where Overcast is running (#761).** Containerised — detected the same way storage-mode auto-detection tells the Docker image apart from a native run, via the `OVERCAST_DATA_DIR_SOURCE=image` marker the Dockerfile bakes in — it defaults to `0.0.0.0`, which Docker's `-p` publishing requires. Native, it defaults to `127.0.0.1`. An explicit `OVERCAST_LISTEN` always wins over either default, in both directions: set it to `0.0.0.0` natively to restore the old reach from a VM, a second machine, or a phone on the same network. The startup log line names which default fired and why.

**A wildcard mixed with a specific address is refused.** `127.0.0.1,0.0.0.0` reads as a widening — "loopback, and also everything" — when it is the opposite of what the specific address asked for. On Linux the second bind fails anyway, and it fails as an opaque listen error rather than as the configuration mistake it is. Note `::` counts as a wildcard here too, not only `0.0.0.0`.

The list exists for a specific shape: a throwaway instance that wants loopback and nowhere routable, but whose test suite runs in a sibling container reaching the host over the Docker bridge, where loopback is invisible. `OVERCAST_LISTEN=127.0.0.1,172.17.0.1` serves both without putting the emulator on the network the machine is attached to. The compat harness does exactly this.

If a later address in the list fails to bind, everything already opened is closed before the error returns, so a partial bind never leaves a port held for a reason that no longer exists.

**The web console is deliberately narrower than the API.** It binds only the first address. `OVERCAST_LISTEN=127.0.0.1` is how an unauthenticated emulator is kept off the network the machine is attached to, and a console that ignored it would leave the state browser — and its write actions — reachable from that network anyway. The extra addresses exist so container-side clients can reach the API; the console is a browser surface on this machine.

**SMTP is loopback-only regardless of `OVERCAST_LISTEN`.** That means an SMTP client in a sibling container cannot reach it. This appears to be undeclared rather than deliberate — see [§13](#13-things-that-will-bite-you).

**The Lambda Runtime API narrows deliberately, and the wildcard is only a fallback.** A wildcard would put an unauthenticated control channel for every Lambda container on whatever network this machine is attached to, and nothing off this machine is a legitimate caller. But loopback alone would strand every invocation, because containers connect back to it over the control plane. So it binds loopback plus one address a container can actually reach — and *reach* is the operative word: since #1579 Overcast binds each candidate in turn and has a throwaway container connect back, keeping the first that answers. The order is its own address on the control plane, the network gateway, `host.docker.internal`, the host's own address, then the wildcard. The verdict is remembered per daemon and control plane in `<data dir>/runtime-api-host-<network>.json`; `LAMBDA_RUNTIME_API_HOST` pins it and skips the walk. Before that the address was chosen by asking whether *this process could bind it*, which is a different question, and on a Windows host whose firewall blocks a fresh binary the two disagreed — every invocation stranded at INIT with nothing saying why.

Inside the container the value of `AWS_LAMBDA_RUNTIME_API` is now the AWS one, `127.0.0.1:9001` — the loopback proxy Overcast's init serves — and this listener's per-environment port travels in `OVERCAST_RUNTIME_API`, which only the init reads. The port, the config variable and the bind policy are unchanged; the same listener also serves the init's log stream, `POST /overcast/v1/logs`.

Which case applies is decided by **actually attempting the bind** rather than by inspecting the operating system: getting it wrong by inspection leaves Lambda unable to answer an invocation, and `uname -s` reports Linux under WSL2 either way.

The full matrix, and why the gateway is preferred on native Linux, is in
[container-networking.md](./container-networking.md).

---

## 3. Bind address versus advertised name

Two separate concerns, as in any server reachable by a name it does not bind — nginx has `listen` and `server_name`; Kafka has `listeners` and `advertised.listeners`.

| | Binds | Advertises |
|---|---|---|
| Overcast | `OVERCAST_LISTEN` | `OVERCAST_HOSTNAME` |
| LocalStack | `GATEWAY_LISTEN` | `LOCALSTACK_HOST` |
| Floci | — (`FLOCI_PORT` only) | `FLOCI_HOSTNAME` |

`OVERCAST_LISTEN` follows LocalStack's `GATEWAY_LISTEN` idiom directly — same shape of variable, same job — which is what closed the naming collision that used to live in this section (see [#870](https://github.com/overcast-sh/overcast/issues/870)). The pre-rename name, `OVERCAST_HOST`, has been removed: Overcast is alpha software, and the project's policy for a removed setting is a startup error naming the replacement rather than a silently-honoured leftover.

`OVERCAST_HOSTNAME` is the name that goes inside URLs Overcast hands back, and it is additive to the built-in wildcard bases rather than replacing them. It also becomes a virtual-hosted base for [host classification](#4-which-service-owns-a-request), an `/etc/hosts` entry inside every container Overcast starts, and a TLS SAN. It is the true analogue of LocalStack's `LOCALSTACK_HOST`, not `OVERCAST_LISTEN`.

**`OVERCAST_HOSTNAME=localhost` is the one setting that silently breaks container callers.** Inside a container, `localhost` is the container. The code defends halfway — loopback names are dropped from the hostname set, so it produces neither an `/etc/hosts` entry nor a DNS claim — but URL minting still uses it, because a hostname *was* configured. Nothing warns.

---

## 4. Which service owns a request

Overcast serves every AWS service on one port, so something must decide which service a request belongs to. Most of that is the wire protocol's business and is covered in [architecture.md §4](./architecture.md#4-the-request-path). The part belonging here is the decision made from the `Host` header alone.

Precedence follows the host grammar rather than middleware registration order. Three tiers:

- **Tier A** — `{bucket}.s3.{base}` or the legacy `{bucket}.s3-{base}` → **S3**. The bucket is everything before the first separator, so dotted bucket names remain addressable.
- **Tier B** — `{bucket}.{base}` for a recognised base → **S3**, unless the candidate carries a reserved label, which vetoes to tier C.
- **Tier C** — a registered host-route label (`execute-api`, `lambda-url`, `appsync-api`, `appsync-realtime-api`, `cloudfront`) at segment index 1 or later → **that service**.
- Anything else is unclaimed, keeps its path, and falls through to S3 path-style.

A must beat C so a dotted bucket addressed explicitly wins: `my.execute-api.s3.localhost` is a bucket, not an API Gateway invoke. B must beat C so a configured `OVERCAST_HOSTNAME` wins over a generic label match inside it.

Tier B is a local-only convenience — real AWS has no bare `{bucket}.{base}` form — but it is what AWS SDKs emit against an endpoint override with virtual-hosted addressing, and what CDK's asset publisher uses unconditionally.

**Reserved labels do not restrict bucket creation.** Real AWS accepts such names, so Overcast does too, and both path-style and tier A keep working for them. The set is deliberately not "every AWS endpoint prefix" — that would make names like `my.logs` collide for no benefit.

Two details that follow from the grammar rather than from any table: bases are sorted longest-first, so a configured parent domain never shadows a longer default — with `OVERCAST_HOSTNAME=overcast.sh`, `x.localhost.overcast.sh` is bucket `x`, not `x.localhost`. And hostnames are case-folded before classification through the same helper used when minting, because casing must never decide which service answers, and one implementation is the only way the two directions cannot drift.

The user-facing account, with the recognised subdomain list, is
[docs/networking.md](../networking.md#how-overcast-decides-who-owns-a-host).

---

## 5. The addresses Overcast hands back

A queue URL, a function URL, an API Gateway endpoint and a database endpoint are all addresses Overcast invents and a client then dials. Getting them wrong is the commonest source of "it works with the CLI but not from my app".

**The rule, one part at a time:**

- **Host** — the configured `OVERCAST_HOSTNAME` when set, because a configured hostname is the operator's assertion that one name resolves for every party. With nothing configured, the **caller's own origin** is echoed back, because their own host is the only name known to resolve for them.
- **Port** — **always the caller's**. Their request is the only proof of a dialable port, since Overcast cannot see its own port mapping from the inside. The configured port fills in only when a request carries no port at all, as with internal dispatch.
- **Scheme** — https if Overcast serves TLS *or* the caller arrived over https. Never a downgrade.

The reason any of this matters is in [architecture.md §2](./architecture.md#2-what-an-aws-sdk-call-actually-is): several AWS SDKs send a request to the **queue URL's own origin** rather than to the endpoint you configured. Hand out a URL naming a host the caller cannot reach and those SDKs fail while the AWS CLI succeeds, because the CLI ignores the queue URL's origin entirely.

This all funnels through one helper, and it is worth knowing why: before it existed the repository had four base-URL precedences and two of them disagreed about the port, which was exactly the bug.

### Three deliberate divergences

- **SQS queue URLs echo your exact origin**, with no hostname substitution, because SDKs dial the `QueueUrl` itself.
- **ECR's `repositoryUri`** always uses the configured host and port, because the Docker daemon dials it rather than your API client. See [§10](#10-ecr-and-the-fourth-caller).
- **The Cognito issuer carries your port**, because OIDC requires `issuer` to match the URL discovery was fetched from.

### Why a port can differ by caller

With `-p 4652:4566`, a host caller reaches Overcast on 4652 and a container reaches it on 4566. The same stack output legitimately reads `:4652` from the host and `:4566` inside a Lambda. Both dial correctly — this is deliberate, not drift.

The sharp edge: a Cognito token minted from the host carries `:4652` in `iss`, and a validator inside a container comparing literally against `:4566` reports a mismatch. **No single port can be dialable from both sides of a remap.** Publish 1:1 if you need one.

### Not every name can be host-routed

An IP literal cannot carry a subdomain, and a bare single-label name like `localhost` or a compose alias only resolves under wildcard DNS that Windows and macOS do not provide for `*.localhost`. Fields with a path-style alternative degrade to it. Fields with none — a Lambda function URL, an API Gateway v2 `apiEndpoint` — are host-routed unconditionally, because there is nothing to fall back to.

---

## 6. Names: the split-horizon domains

Three domains are built in — `localhost.overcast.sh`, `localhost.localstack.cloud`, `localhost.floci.io` — and `OVERCAST_SPLIT_HORIZON_HOSTS` appends more.

They resolve to `127.0.0.1` in **public** DNS, so a URL built on one works unmodified from the host. Inside containers the same names are remapped to Overcast's address through `/etc/hosts` entries, which take precedence over DNS in both glibc and musl. One URL, dialable from both sides. `localhost.localstack.cloud` is included so URLs carried over from a LocalStack setup keep working.

### Why a DNS server rather than `/etc/hosts`

`/etc/hosts` is an **exact-match table**, and DNS names are hierarchical. The apex can be shadowed that way; a subdomain cannot.

The URLs AWS SDKs build are overwhelmingly subdomains — virtual-hosted S3, API Gateway, AppSync, Lambda function URLs. Those names miss the table, fall through to public DNS, and resolve to `127.0.0.1` **by design**, which inside a container is the container itself. The lookup *succeeds*, so the failure surfaces as a refused connection rather than an unknown host.

Enumeration cannot close the gap either: bucket names and API IDs are minted *after* a container starts, and a container's `/etc/hosts` is fixed at creation.

Both mechanisms are kept. `/etc/hosts` covers the apex names with no dependency on the resolver being reachable; the resolver covers everything below them.

### How the resolver answers

It claims each split-horizon domain **and every subdomain**, with the boundary falling on a label separator — so `notlocalhost.overcast.sh` is not claimed.

For an address it asks the **kernel's routing table** which source address would be used to reach the querying peer, by opening a UDP socket that transmits nothing. That is exactly the address the peer can reach back on, and it stays correct as Docker networks come and go, with no interface enumeration to refresh. Answers are cached per peer. Loopback and unspecified results are discarded, since handing `127.0.0.1` to a container would name the container itself.

This exists because Overcast is attached to several networks at once and therefore has more than one address; there is no single correct answer to give everybody.

Three behaviours are worth knowing because they are load-bearing elsewhere:

- **Only A records are served.** AAAA for an owned name is deliberate NODATA — see [§11](#11-ipv6).
- **Out-of-zone queries are forwarded only for loopback, private and link-local peers**; anyone else gets REFUSED. Publishing the resolver by accident would otherwise expose an open forwarding resolver — the reflector half of a DNS amplification attack.
- **Failing to bind port 53 is not fatal.** It is logged, containers are not pointed at a resolver that is not there, and the `/etc/hosts` entries still cover the apex names. This is why the resolver is best understood as covering *subdomains* — the apex works without it.

Overcast also never forges a negative answer, and distinguishes "I own this name but cannot resolve it" from "there is nothing here", because clients cache the second.

---

## 7. The container networks

There are two, plus one per VPC. `internal/dataplane` is the only thing that
picks between them; no service chooses a network for itself.

| Plane | Env var | Default | Who attaches | Carries endpoint aliases |
|---|---|---|---|---|
| **Control** | derived, `{network}_control` | `overcast_control` | every container Overcast starts, and Overcast itself | no |
| **Default data** | `OVERCAST_NETWORK` | `overcast` | every container belonging to no VPC | yes |
| **Per-VPC** | derived, `{OVERCAST_NETWORK}-vpc-{vpcId}` | `overcast-vpc-{vpcId}` | resources placed in that VPC; Overcast itself when containerised | yes |

The split is the point. The **control plane** carries Overcast's own channel to the containers it starts — the Lambda Runtime API, and the `AWS_ENDPOINT_URL` calls function and task code makes back into the emulator. On AWS the Runtime API lives inside the execution sandbox and the emulator's API stands in for a VPC endpoint per service; both are reachable whatever VPC a function joins. A **data plane** carries traffic *between* resources. Only the second is something a VPC should be able to restrict, and while both rode one network, "keep the Runtime API" was inseparable from "keep reaching every database".

**Every container is created on the control plane and attached to a data plane before it starts.** Docker accepts one network at create, and the control plane is the one that must be up from the first packet: a Lambda container that cannot reach the Runtime API never finishes INIT. The ordering matters in the other direction too — application code routinely opens a database connection during INIT, so a container that started before its data-plane attachment landed would dial a name that does not resolve yet.

The one exception is ECS's `awsvpc` attachment, which reads back the address Docker assigned and therefore cannot run until the container does. The same task joins the default plane first, when it is entitled to one.

**A VPC-placed resource gets its VPC network and nothing else.** Naming a VPC subtracts, as it does on AWS, and `dataplane.DataNetworks` is the one place that decides it. Two things widen it back. `Placement.Public` — set from `PubliclyAccessible` on an RDS instance, `assignPublicIp: ENABLED` on an ECS task — re-adds the default plane, deliberately spelled with AWS's fields so the local fix is the deployed fix. And the restriction is withheld entirely where Overcast's resolver is not running, because it is only safe where a forbidden connection fails by name; see [§13](#13-things-that-will-bite-you) for which hosts those are.

The consequence for anyone adding a container-backed service: **resolving the resource's VPC is no longer optional**. A service that never calls `PlaceInVPC` used to lose a little fidelity; now it strands its resource on the default plane where the consumers in its own VPC cannot see it.

**An EC2 VPC becomes one Docker bridge** named `{OVERCAST_NETWORK}-vpc-{vpcID}`, with the subnet taken from its CIDR. Whether its containers reach the internet follows `OVERCAST_VPC_EGRESS` and not the VPC's own topology. Under `open`, the default, both planes are routable and the bridge itself is still `--internal` when the VPC has no internet gateway — honest about the template, and free, because the container is also on the routable control plane and takes its default route from there. `none` makes every network `--internal`. `routed` makes every *plane* `--internal` and carries the route out on a second network per VPC, `{OVERCAST_NETWORK}-vpc-{vpcID}-egress`, pinned to a `/24` from `OVERCAST_VPC_EGRESS_POOL` and joined only by the containers whose subnet's route table has a `0.0.0.0/0` route to an attached internet gateway or an available NAT gateway (`ec2.Handler.egressNetworkForSubnets`, `dataplane.PlaceInSubnets`); a route-table mutation revisits every recorded placement in the VPC and connects or disconnects in place (`ec2.Handler.reconcileVPCEgress`). See [docs/networking.md § Egress modes](../networking.md#egress-modes), `dataplane.VPCNetworkSpec` and `docker.EnsureNetwork`, which verifies each network against its full spec on every start. A VPC whose network could not be created reports status `unbacked` and services refuse to place into it; the usual cause is a CIDR collision with a leftover network from an earlier run.

`OVERCAST_EC2_VPC_STRATEGY` selects how VPCs map onto bridges. `shared` (the default), `strict` and `remapped` are all implemented; `netns` is declared but not implemented, and fails startup outright rather than falling back (`config.Load` rejects it before a strategy is ever resolved — see `resolveVPCNetworkStrategy`'s dead `netns` case in `internal/services/ec2/vpc_strategy.go`, which the config-level rejection makes unreachable). The choice matters because enforcement is per Docker network, so isolation is exactly as strong as the strategy in use — `shared` puts two same-CIDR VPCs on one bridge, and they are not isolated from each other.

### The default VPC

Every region seeds one on first use, as every real AWS account has: `IsDefault`, `172.31.0.0/16`, a default subnet per availability zone, an attached internet gateway, a main route table carrying the default route, and a `default` security group.

Its backing network **is** the default data plane. So "this resource named no VPC" and "this resource is in the default VPC" are the same place by construction rather than by coincidence — which is what lets the reachability question stay a single comparison instead of a special case for the empty VPC ID.

Two consequences follow from that network being the emulator's own. `DeleteVpc` on the default VPC removes the record and leaves the network, because every container Overcast started is attached to it; and a deleted default VPC stays deleted rather than being reseeded on the next describe, as on AWS, where `CreateDefaultVpc` is the way back.

**ECS `awsvpc` tasks have two things allocating addresses from the same subnet**, and they do not know about each other. EC2's own counter decides what the task's ENI record says; Docker's IPAM decides what the container actually gets. They agree until one of them allocates once more than the other, and then they diverge silently — leaving a load balancer to read the ENI address and dial somewhere nothing is listening.

So Overcast asks rather than assumes. It requests the EC2-allocated address when attaching the container, and if Docker refuses it — outside the subnet, or already taken — it accepts what Docker gives and rewrites the ENI record to match. The recorded address is always the real one.

Only the first container in a task carries that address. AWS gives an `awsvpc` task a single shared network namespace, so one address covers every container in it; Docker gives each container its own.

---

## 8. A container reaching Overcast

A Lambda function calling `s3.putObject` has to reach Overcast, and `localhost` will not do it.

So Overcast injects an endpoint into every container it starts, choosing the address in this order:

1. **Overcast is itself containerised** → join the network and use its own IP on it, directly routable from siblings.
2. **Overcast is on the host** → a routable host interface, preferring the **default-route address**. That is found by opening a UDP socket toward a TEST-NET address — a routing-table lookup that sends nothing. A developer machine routinely has many interfaces and `net.InterfaceAddrs` makes no ordering promise: measured on one Windows Docker Desktop host, three of six non-loopback candidates were unreachable from a task container.
3. **Neither resolved** → `host.docker.internal`, paired with an explicit `host-gateway` entry so it works on native Linux too, where Docker does not synthesise that name.

Step 2 is deliberately skipped when Overcast is containerised: its own interface addresses belong to whatever networks it happens to be attached to, and a sibling on the task network cannot necessarily route to them. A confidently wrong address is worse than falling through.

**A name is injected rather than an address**, for two reasons. It survives Overcast's own container being recreated on a different address, which an address baked into a warm execution environment does not. And it can carry a subdomain, so an SDK deriving a virtual-hosted URL from the endpoint — `{bucket}.s3.{host}` — produces a name that resolves. From an IP endpoint the SDK is forced to path-style, and any URL it builds by prefixing labels is unusable.

Two related mechanisms handle URLs that arrive **already baked in** rather than minted at request time — a queue URL written into a function's environment by a host-side `cdk deploy`, for instance. Loopback origins on Overcast's own ports are rewritten to the container-reachable endpoint. Where the *name* is already right and only the port is wrong, the hostname is preserved rather than replaced, which keeps virtual-hosted forms working. Both operate on raw bytes rather than parsed URLs, because deploy tools pass URLs inside JSON blobs and comma-separated lists as often as they pass them bare.

### Published ports

A container cannot see its own port mapping from the inside. Started with `-p 4580:4566`, the process listens on 4566 and nothing in its environment records that host callers arrive on 4580.

Overcast finds out by identifying its own container and asking Docker, bounded to three seconds because it sits on the startup path.

Sometimes it cannot: Overcast is not containerised, there is no Docker socket, or the port is not published at all. The information genuinely does not exist then, so Overcast falls back to the port it listens on — correct for every caller except one arriving through a remap. Two things lose accuracy as a result:

- **The web console** tells the browser to use the internal port, which is closed on the host, so requests from it fail. This is the reason the detection exists at all: without it the console works only when the published and internal ports happen to coincide.
- **Published-port URL rewriting is off**, so a queue URL baked in on the published port is not rewritten, and a container dials its own loopback on a dead port.

Both are avoided by mounting the Docker socket when running Overcast in a container, or by publishing 1:1 so the two ports are the same.

---

## 9. A container reaching another container

Two mechanisms answer names inside a container Overcast started, and they answer different questions:

| Question | Answered by |
|---|---|
| "Where is Overcast?" | Overcast's own DNS server, authoritative for the split-horizon domains |
| "Where is that other container?" | Docker's embedded resolver, from **network aliases** |

Both are correct, and the split is necessary. Overcast's resolver must be authoritative for the whole domain so wildcard subdomains resolve; Docker's resolver must own container-to-container names because only Docker knows the topology.

**What goes wrong when they compose.** Docker aliases are exact-match. An unregistered name misses, so Docker forwards the query upstream — to Overcast's resolver, which *is* authoritative for that domain and duly answers with **Overcast's own address**. The client opens a connection to Overcast on port 3306 and **hangs**, because nothing there speaks that protocol. The symptom is a hang rather than a name-resolution failure, so the investigation starts at the database.

**A blanket stricter resolver would not fix it.** Overcast cannot distinguish "this subdomain should have been a container" from "this subdomain is one of mine" by pattern — resource hostnames are minted at runtime inside a namespace it legitimately owns. Refusing every name it owns would break virtual-hosted S3 and every host-routed service, and forging NXDOMAIN would be worse, since clients cache negative answers and the failure would outlive the fix.

Matching the *shape* of a data-plane name does not work either, and the reason is worth knowing before anyone tries it: `rds`, `cache` and `kafka` are ordinary words in a bucket name. A bucket legitimately called `my.rds` is addressed virtual-hosted as `my.rds.localhost`, which matches any grammar you would write for an RDS endpoint. This is the same reasoning that keeps those labels out of the host-routing table (`docs/networking.md` § Known AWS resource subdomains).

**What does work is refusing on positive identification.** When a name the zone owns reaches Overcast's resolver, it asks Docker a factual question: is some container advertising this exact alias, on a network this caller is not attached to? If so, the only answer Overcast could give is its own address, which produces the hang — so it refuses instead, and logs both sides:

```
refusing a data-plane name the caller cannot reach
  name:            cache-1.us-east-1.cfg.localhost.overcast.sh
  target:          elasticache cache-1
  caller:          ecs app/9f2
  target_networks: [overcast-vpc-vpc-0abc]
  caller_networks: [overcast_control overcast]
```

That cannot misidentify a bucket, because nothing advertises `my.rds.localhost` as an alias. Two limits fall out of the same rule. A caller Overcast did not start has unknown attachments rather than known-disjoint ones, so it is answered rather than refused; and a name with no container behind it anywhere — an RDS instance whose engine never started — is logged but still answered, since it cannot be told apart from an ordinary bucket with enough confidence to refuse.

The check is affordable because of where it sits. A container's resolver is Docker's, which answers from aliases and forwards only what it cannot answer — so every data-plane name that reaches Overcast is *already* a miss, and the Docker lookup is paid by connections that are already broken.

**Overcast performs the registration itself.** There is nothing to configure. It becomes your concern only when adding a service that starts containers, and `internal/dataplane` is where all of it lives:

1. **Attach the hostname as a network alias** — `dataplane.Attach`, after create and before start. A container attached without one is reachable by address and nothing else.
2. **Register every hostname base the endpoint could have been minted under** — `dataplane.Hostnames`. Not just the one in play when the container was created: the name a caller holds depends on the endpoint *that caller* used, and the process resolving it later is often not the one that received it.
3. **Let the helper pick the plane.** Reaching for a network name directly is the mistake this package exists to prevent — when each service chose for itself, RDS attached to two compute networks, ElastiCache to one and MSK to none, and whether any two things could talk came down to which service remembered.

---

## 10. ECR and the fourth caller

Every other address in this document is dialled by your code, or by a container running your code. An ECR registry address is different: **the Docker daemon dials it**, because the daemon is what pulls and pushes images.

That matters because the daemon is not necessarily on your machine. Under Docker Desktop it runs inside a VM, so an address that works from your shell may be meaningless to it — and an address that works from a Lambda container is on a network the daemon is not using either.

This is why the registry URI is one of the [deliberate divergences](#5-the-addresses-overcast-hands-back) from the per-caller rule: it always uses the configured host and port rather than echoing the caller's origin, because the caller asking for the URI is not the party that will dial it.

### Overcast cannot verify the port itself

The registry runs as a container with a published host port, and picking which port to advertise turns out to be the hard part.

Publishing must be dual-stack: an explicit `0.0.0.0` restricts the binding to IPv4, and Docker Desktop does not wire an ephemeral v4-only binding into the VM the daemon runs in. But a dual-stack ephemeral publish can land the IPv4 and IPv6 bindings **on different ports** — and which family `localhost` resolves to is the daemon's business, not Overcast's. A port confirmed reachable from Overcast's own vantage still produced `dial tcp [::1]:<v4port>: connection refused` on CI.

So Overcast does not check the port itself. **It asks the daemon to dial**, using the same operation `docker login` performs, and takes the first port that answers.

The probe carries this instance's credentials, which makes it an *identity* check rather than a liveness one — and that detail is load-bearing. Two Overcast instances sharing a daemon can have their dual-stack publishes interleave, so that A's IPv4 port is B's IPv6 port. An anonymous probe would reach B, be refused exactly as A's own registry would refuse it, and A would then advertise a port serving B. Every token A issued afterwards would fail against B's credentials — as an authentication error carrying a valid-looking token, which is a miserable thing to debug. Offering the password separates them: our registry accepts it and reports the probe repository missing; a sibling's rejects it.

### When it cannot be reached

Failure is not fatal. Overcast advertises its first candidate port anyway — its own registry access may still work, and a stated-but-wrong port beats no registry — and warns, naming the remedy: set `OVERCAST_ECR_REGISTRY_PORT` and add the registry to the daemon's `insecure-registries`.

The symptom to recognise: **`docker push` to the emulated registry fails, and so do ECS and Lambda pulls of its images** — while everything else about the instance works. Lambda functions with `PackageType=Image` break while zip-packaged functions on the same instance are fine. That asymmetry is the clue.

---

## 11. IPv6

**Overcast is IPv4 in practice.** Do not plan on reaching it over IPv6.

That is not an oversight so much as a choice never to take IPv6 on: the API's IPv4 default (`0.0.0.0` containerised, `127.0.0.1` natively — see [§2](#2-what-binds-where)) is IPv4 either way, container address discovery cannot produce an IPv6 address at all, and CloudFormation states outright that there is no IPv6 networking for a template to describe. An explicit IPv6 bind address is accepted and formatted correctly, but nothing downstream is built for one.

Where IPv6 has forced itself on Overcast, it has been dealt with deliberately and with evident effort — the DNS server's A-only answer below, and the daemon-side port probing in [§10](#10-ecr-and-the-fourth-caller), both exist because the dual-stack case produced real failures. What is absent is IPv6 as a supported way to reach the emulator, not attention to IPv6 where it intrudes.

The DNS server serves **A records only**, and answers AAAA for its own names with a deliberate empty response rather than forwarding. That is not an oversight: the public record for the split-horizon domains is `::1`, so forwarding an AAAA query would hand a dual-stack client its own loopback — exactly the failure the resolver exists to prevent.

The one place the distinction has a visible, reproducible effect is [ECR](#10-ecr-and-the-fourth-caller), where the publish shape decides whether the Docker daemon can reach the registry at all.

---

## 12. The bridge: friendly names on port 80

`overcast bridge`, or `overcast serve --bridge`, is opt-in and does two things.

**Publishes mDNS records** — `overcast.local` for the API and `overcast-app.local` for the console — then tails Overcast's internal domain feed and advertises every registered API Gateway custom domain as it appears. It needs a platform backend (`dns-sd` on macOS and Windows, `avahi-utils` on Linux) and reports a clear error when there is none.

**Proxies port 80** to whichever backend the `Host` header names, matched case-insensitively. Failing to bind port 80 is not fatal: it logs a platform-specific privilege hint, and mDNS still works.

**It is refused while TLS is enabled.** The proxy speaks plain HTTP to its backends, so pointing it at TLS listeners would fail every request.

---

## 13. Things that will bite you

Current behaviour, symptom first.

- **"My Lambda can no longer reach my database."** If the database is in a VPC and the function is not — or they are in different VPCs — that is now refused rather than silently allowed, because it would not work deployed either. The refusal names both sides in the log. Fixes, all AWS fields: put the function in the VPC, or set `PubliclyAccessible: true` on the instance / `assignPublicIp: ENABLED` on the task. Full comparison in [docs/networking.md § Lambda, ECS and VPCs](../networking.md#lambda-ecs-and-vpcs).
- **A resource whose service does not resolve its VPC lands on the default plane, where its own VPC cannot see it.** This is the failure mode enforcement introduces for any service that never calls `PlaceInVPC` — the resource looks placed to the API and is not. Check the service wires a VPC resolver before assuming the caller is at fault.
- **Security groups, NACLs and subnets are not enforced.** Docker network membership expresses "in this VPC or not" and nothing finer, so there is no port- or source-level filtering and no public/private subnet distinction within a VPC.
- **A VPC with no internet gateway still reaches the internet under `open`.** Its own network is `--internal`, but every container also sits on the control plane, which `open` leaves routable — so the bridge flag is honest about the template and decides nothing about egress. `OVERCAST_VPC_EGRESS` decides that, for the whole topology. The decision and its reason are logged at startup and reported in `/_overcast/health` under `docker.networks`.
- **`OVERCAST_VPC_EGRESS=none` cannot be delivered in full on Docker Desktop.** Containers there reach the Runtime API at the host's own address, which `--internal` would sever, so the control plane is left routable and every container keeps a route out. Every data plane *is* isolated, a WARN says the stack is not hermetic, and the `vpc-egress-not-withheld` advisory says so on the console. Run Overcast containerised, or against a native Linux daemon, for the whole of it.
- **`OVERCAST_CONTROL_PLANE_INTERNAL` is deprecated.** Still honoured for that one network, and setting it logs a notice naming the mode that means the same thing. It pins one network, and a container takes its default route from whichever of its networks is routable, so it only ever settled a third of the question. A pin of `false` always wins; a pin of `true` is still overridden back to `false` where an internal control plane would sever the Runtime API — the host veto applies to a pinned isolation exactly as it applies to one the mode asked for.
- **A `{network}_control` created by an older Overcast keeps the isolation it was born with.** Docker never applies `--internal` retroactively. Overcast recreates a drifted plane at startup when nothing is attached, and warns and leaves it when something is — so a long-lived machine can lag a version pin until the network is rebuilt. `overcast network reset` is the rebuild: it stops the containers Overcast started, disconnects the ones it did not and leaves them running, then recreates the network to spec.
- **HTTPS fails for API Gateway, Lambda function URLs and AppSync.** A one-level wildcard certificate matches exactly one label, so `{id}.execute-api.{region}.{base}` falls outside the auto-minted SANs. Those need their own certificate.
- **A container cannot reach the SMTP capture server.** It binds loopback whatever `OVERCAST_LISTEN` says.
- **On a native Windows or macOS host, wildcard subdomains do not resolve inside containers, the data-plane guard does not run, and VPC placement is therefore not enforced.** The resolver needs `/etc/resolv.conf` to find upstreams and silently does not start without one, logging at debug level. The apex names still work, because those come from `/etc/hosts`. Since a forbidden connection could only fail by hanging there, the restriction is withheld rather than delivered blind — so the same stack behaves differently on a host run and a containerised one. Run Overcast in a container to get either.
- **Same-CIDR VPCs are not isolated under the default strategy.** `shared` puts them on one Docker bridge, so anything scoped to a network is scoped to both. `strict` and `remapped` give real separation; `netns` is declared but not implemented, and rejects startup rather than falling back.
- ~~A native run still defaults to the wildcard.~~ Resolved by [#761](https://github.com/overcast-sh/overcast/issues/761): the default now depends on where Overcast is running — `0.0.0.0` when containerised (Docker's `-p` publishing requires it), `127.0.0.1` natively. A startup log line names every bound address and, when defaulted, why. An explicit `OVERCAST_LISTEN` always wins over either default.

---

## 14. Where to go next

| Question | Document |
|---|---|
| How does a request get routed to a service at all? | [architecture.md §4](./architecture.md#4-the-request-path) |
| Which host and port will a returned URL carry? | [docs/networking.md](../networking.md) |
| Why can my Lambda not reach my database? | [container-networking.md](./container-networking.md) |
| How do I test an endpoint change properly? | [manual-testing.md](./manual-testing.md) |
| How do I set up HTTPS? | [docs/https.md](../https.md) |
| Which host and port will a URL carry, in user terms? | [docs/networking.md](../networking.md#which-host-and-port-a-url-carries-and-why) |
| What subdomains does Overcast recognise? | [docs/networking.md](../networking.md#known-aws-resource-subdomains) |
