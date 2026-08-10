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
3. [`OVERCAST_HOST` is not `OVERCAST_HOSTNAME`](#3-overcast_host-is-not-overcast_hostname)
4. [Which service owns a request](#4-which-service-owns-a-request)
5. [The addresses Overcast hands back](#5-the-addresses-overcast-hands-back)
6. [Names: the split-horizon domains](#6-names-the-split-horizon-domains)
7. [The container networks](#7-the-container-networks)
8. [A container reaching Overcast](#8-a-container-reaching-overcast)
9. [A container reaching another container](#9-a-container-reaching-another-container)
10. [ECR and the fourth caller](#10-ecr-and-the-fourth-caller)
11. [IPv6](#11-ipv6)
12. [The bridge: friendly names on port 80](#12-the-bridge-friendly-names-on-port-80)
13. [Known gaps](#13-known-gaps)
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

| Listener | Default port | Config | Honours `OVERCAST_HOST`? |
|---|---|---|---|
| AWS API | 4566 | `OVERCAST_PORT` | **Yes** — every address in the list |
| Web console / BFF | 4567 | `OVERCAST_UI_PORT`, `--ui-port`; `0` disables | **Partly** — first address only |
| Lambda Runtime API | 9001 | `LAMBDA_RUNTIME_API_PORT` | **No** — resolved independently |
| SMTP capture | 1025 | `OVERCAST_SMTP_PORT` | **No** — hardcoded loopback |
| Container DNS (UDP + TCP) | 53 | `OVERCAST_DNS_PORT`, `OVERCAST_DNS` | **No** — binds the wildcard |
| Bridge proxy (opt-in) | 80 | `--http-port`, `--bridge` | **No** — binds the wildcard |

Four of those diverge, each for a reason.

**`OVERCAST_HOST` takes a list.** Comma-separated; blanks dropped and duplicates collapsed, since binding one address twice would fail the second listen. A value that names *no* address is a hard error rather than a silent fall back to the default — the reasoning being that unset means "use the default", while set-to-nothing means the value is wrong, and defaulting it would bind every interface, the opposite of what anyone setting it wants.

**A wildcard mixed with a specific address is refused.** `127.0.0.1,0.0.0.0` reads as a widening — "loopback, and also everything" — when it is the opposite of what the specific address asked for. On Linux the second bind fails anyway, and it fails as an opaque listen error rather than as the configuration mistake it is. Note `::` counts as a wildcard here too, not only `0.0.0.0`.

The list exists for a specific shape: a throwaway instance that wants loopback and nowhere routable, but whose test suite runs in a sibling container reaching the host over the Docker bridge, where loopback is invisible. `OVERCAST_HOST=127.0.0.1,172.17.0.1` serves both without putting the emulator on the network the machine is attached to. The compat harness does exactly this.

If a later address in the list fails to bind, everything already opened is closed before the error returns, so a partial bind never leaves a port held for a reason that no longer exists.

**The web console is deliberately narrower than the API.** It binds only the first address. `OVERCAST_HOST=127.0.0.1` is how an unauthenticated emulator is kept off the network the machine is attached to, and a console that ignored it would leave the state browser — and its write actions — reachable from that network anyway. The extra addresses exist so container-side clients can reach the API; the console is a browser surface on this machine.

**SMTP is loopback-only regardless of `OVERCAST_HOST`.** That means an SMTP client in a sibling container cannot reach it. This appears to be undeclared rather than deliberate — see [Known gaps](#13-known-gaps).

**The Lambda Runtime API narrows deliberately, and the wildcard is only a fallback.** A wildcard would put an unauthenticated control channel for every Lambda container on whatever network this machine is attached to, and nothing off this machine is a legitimate caller. But loopback alone would strand every invocation, because containers connect back to it over the Lambda network. So it binds loopback plus one reachable address, chosen from where Overcast is running — its own network IP when containerised, the network gateway on native Linux, the host's default-route address under Docker Desktop.

Which case applies is decided by **actually attempting the bind** rather than by inspecting the operating system: getting it wrong by inspection leaves Lambda unable to answer an invocation, and `uname -s` reports Linux under WSL2 either way.

The full matrix, and why the gateway is preferred on native Linux, is in
[container-networking.md](./container-networking.md).

---

## 3. Bind address versus advertised name

Two separate concerns, as in any server reachable by a name it does not bind — nginx has `listen` and `server_name`; Kafka has `listeners` and `advertised.listeners`.

| | Binds | Advertises |
|---|---|---|
| Overcast | `OVERCAST_HOST` | `OVERCAST_HOSTNAME` |
| LocalStack | `GATEWAY_LISTEN` | `LOCALSTACK_HOST` |
| Floci | — (`FLOCI_PORT` only) | `FLOCI_HOSTNAME` |

`OVERCAST_HOSTNAME` is the name that goes inside URLs Overcast hands back, and it is additive to the built-in wildcard bases rather than replacing them. It also becomes a virtual-hosted base for [host classification](#4-which-service-owns-a-request), an `/etc/hosts` entry inside every container Overcast starts, and a TLS SAN.

**`OVERCAST_HOSTNAME=localhost` is the one setting that silently breaks container callers.** Inside a container, `localhost` is the container. The code defends halfway — loopback names are dropped from the hostname set, so it produces neither an `/etc/hosts` entry nor a DNS claim — but URL minting still uses it, because a hostname *was* configured. Nothing warns.

**A trap for anyone arriving from LocalStack:** the two `*_HOST` variables mean opposite things. `LOCALSTACK_HOST` advertises; `OVERCAST_HOST` binds. Overcast's true analogue of `LOCALSTACK_HOST` is `OVERCAST_HOSTNAME`. A code comment currently asserts the wrong equivalence, and a rename to remove the collision is proposed in
[#870](https://github.com/Neaox/overcast/issues/870).

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

| Network | Env var | Default | Who attaches |
|---|---|---|---|
| Lambda | `LAMBDA_NETWORK` | `overcast_lambda` | every Lambda container; Overcast itself; RDS and ElastiCache containers, aliased |
| ECS | `ECS_NETWORK` | `overcast_ecs` | every ECS task container; Overcast itself; RDS containers, aliased |
| RDS | `RDS_NETWORK` | `overcast_rds` | engine containers |
| ElastiCache | `ELASTICACHE_NETWORK` | `overcast_elasticache` | cache containers |
| MSK | `MSK_NETWORK` | `overcast_msk` | Redpanda containers |
| EKS | `EKS_NETWORK` | `overcast_eks` | live-mode control-plane containers |
| EFS | `EFS_NETWORK` | `overcast_efs` | NFS export containers, only when `OVERCAST_EFS_NFS` is on |
| Per-VPC | — | `overcast-vpc-{vpcId}` | Lambda functions with a `VpcConfig`; ECS `awsvpc` tasks; RDS instances in a VPC; Overcast itself when containerised |

**Several containers sit on more than one network.** An RDS engine container joins its own network, plus the Lambda and ECS networks with aliases, plus its VPC network if it has one — because a task placed in a VPC reaches it over the VPC network, and the compute-network aliases cover everything else. A Lambda function with a `VpcConfig` joins the Lambda network at creation and the VPC network after.

**An EC2 VPC becomes one Docker bridge** named after the VPC, with the subnet taken from its CIDR, and marked `--internal` unless the VPC has an attached internet gateway. `OVERCAST_EC2_VPC_STRATEGY` declares four strategies but only `shared` is implemented; the others fall back to it with a startup warning. A VPC whose network could not be created reports status `unbacked` and services refuse to place into it — the usual cause is a CIDR collision with a leftover network from an earlier run.

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

**A stricter resolver would not fix it.** Overcast cannot distinguish "this subdomain should have been a container" from "this subdomain is one of mine" — resource hostnames are minted at runtime inside a namespace it legitimately owns. Refusing to answer for names it owns would break virtual-hosted S3 and every host-routed service, and forging NXDOMAIN would be worse, since clients cache negative answers and the failure would outlive the fix.

**Overcast performs the registration itself.** There is nothing to configure. It becomes your concern only when adding a service that starts containers, and then it has three parts — miss any one and you get the same hang:

1. **Attach the hostname as a network alias.** A container attached without one is reachable by address and nothing else.
2. **Register every hostname base the endpoint could have been minted under**, not just the one in play when the container was created. The name a caller holds depends on the endpoint *that caller* used, and the process resolving it later is often not the one that received it.
3. **Do it on every network a caller might be on.** An alias on the Lambda network does nothing for an ECS task — that was the bug behind a Fargate task being unable to reach its RDS instance.

RDS is the reference implementation.

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

That is not an oversight so much as a choice never to take IPv6 on: the API binds the IPv4 wildcard by default, container address discovery cannot produce an IPv6 address at all, and CloudFormation states outright that there is no IPv6 networking for a template to describe. An explicit IPv6 bind address is accepted and formatted correctly, but nothing downstream is built for one.

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

- **An ECS task cannot resolve an ElastiCache node.** Cache containers are aliased on the Lambda network only, and only under the configured hostname — so a caller that reached Overcast on any other base gets a name nothing registered. It fails as a hang, for the reason in [§9](#9-a-container-reaching-another-container). Tracked as [#872](https://github.com/Neaox/overcast/issues/872).
- **HTTPS fails for API Gateway, Lambda function URLs and AppSync.** A one-level wildcard certificate matches exactly one label, so `{id}.execute-api.{region}.{base}` falls outside the auto-minted SANs. Those need their own certificate.
- **A container cannot reach the SMTP capture server.** It binds loopback whatever `OVERCAST_HOST` says.
- **On a native Windows host, wildcard subdomains do not resolve inside containers.** The resolver needs `/etc/resolv.conf` to find upstreams and silently does not start without one, logging at debug level. The apex names still work, because those come from `/etc/hosts`.
- **Setting `OVERCAST_EC2_VPC_STRATEGY` to anything but `shared` does nothing.** `strict`, `remapped` and `netns` are declared but unimplemented, and fall back with a startup warning. Overlapping VPCs share one network, and isolation leaks between them.
- **You cannot tell which addresses Overcast bound.** Nothing logs it, and the default is the wildcard — so a native run listens on every interface with no signal either way. Tracked as [#761](https://github.com/Neaox/overcast/issues/761), which also asks whether that default should narrow.

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
