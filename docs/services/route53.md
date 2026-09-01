---
title: "Route 53 — Amazon Route 53"
description: "Hosted zones, record sets, tags, and health checks are real metadata with AWS-faithful validation, and Overcast's own DNS resolver actually answers zone queries — health check probes are still never sent."
section: "Service Reference"
tags:
  - amazon
  - docs
  - route
  - route53
  - services
---

# Route 53 — Amazon Route 53

Route 53 is served as a REST-XML API under the `/2013-04-01/` path. Hosted
zones, resource record sets, tags, and health checks are real metadata with
AWS-faithful validation rules, error codes, defaults, auto-created child
records, and pagination. Health check probes are still never sent — see
"Known divergences" below — but DNS queries **are** answered: Overcast's own
internal resolver (`internal/dns`, `OVERCAST_DNS`/`OVERCAST_DNS_PORT`) is
authoritative for any hosted zone in the store, in addition to the
split-horizon emulator hostnames it already served.

## DNS serving

- **What's served.** For a query whose name falls under a hosted zone (the
  apex or any subdomain), the resolver answers from that zone's record sets:
  `A`, `AAAA`, `CNAME` (chased across zones, up to 8 hops, with a cycle
  terminating the chain rather than looping), `MX`, `TXT`, `NS`, and `SOA` —
  including the apex `NS`/`SOA` records every zone gets on creation. One-label
  wildcard records (`*.example.com`) are matched exactly as real AWS scopes
  them: a wildcard replaces exactly one label, so `*.example.com` answers
  `foo.example.com` but not `foo.bar.example.com`. TTLs come from the record's
  own stored TTL, not the split-horizon default. `ALIAS` records resolve to
  Overcast's own address — the one the calling container or host can actually
  reach — since that is the only correct answer for an alias pointed at an
  emulated ELB/CloudFront/S3-website/API Gateway hostname in this
  environment; an `AAAA`-typed alias has no address to give (Overcast has no
  IPv6 address) and answers NODATA. A name that exists in the zone but has no
  record of the queried type is NODATA; a name absent from the zone entirely
  is NXDOMAIN; both carry the zone's SOA in the authority section for
  negative caching. `NAPTR`/`PTR`/`SRV`/`SPF`/`CAA`/`DS`/`TLSA`/`SSHFP`/`SVCB`/`HTTPS`
  records can be stored via `ChangeResourceRecordSets` but are not served —
  a query for one of them gets NODATA/NXDOMAIN like any other type absent at
  that name.
- **How a Route 53 zone interacts with the container-name resolver.** These
  are two independent authorities the same resolver consults, checked in
  order: a name some hosted zone in the store claims is answered from that
  zone first and always authoritatively (never forwarded, even if it also
  happens to fall under one of Overcast's own split-horizon domains); only a
  name no hosted zone claims falls through to the split-horizon
  zone/forwarding behaviour `internal/dns`'s package doc describes. A
  container-endpoint name (an RDS endpoint, an ElastiCache node) is still
  resolved by Docker's embedded resolver from the container's network
  aliases before either authority is reached, so it is unaffected by this
  feature either way.
- **Private zones and VPC association.** Real Route 53 only serves a private
  hosted zone's records to a resolver inside one of the zone's associated
  VPCs. Overcast does not model "inside a VPC" as a property of a DNS query —
  `AssociateVPCWithHostedZone`/`DisassociateVPCFromHostedZone` are not
  implemented at all (see below) — so, kept deliberately honest and simple,
  every container Overcast starts is answered from every private zone the
  store holds, with no VPC filtering. When a public and a private zone share
  the same name, the private zone wins. This can only make more names resolve
  in the emulator than would on real AWS, never fewer, for a container
  correctly associated with the zone's VPC.
- **Public zones and host resolution.** The emulator's DNS resolver is
  consulted only by the containers Overcast starts (pointed at it via
  Docker's embedded resolver) and by a host that has explicitly pointed its
  own resolution at Overcast. Overcast never attempts to become an
  internet-reachable authority for a public zone's real domain name — there
  is no NS delegation, no port 53 published to the internet, nothing that
  would let it answer for anyone but its own containers and an opted-in host.

## Behavior Notes

- Route 53 is treated as a global service in this emulator.
- Hosted zone IDs are stored and returned in AWS-style path format, for example
  `/hostedzone/Z123...`; change IDs likewise (`/change/C123...`).
- Errors use Route 53's `ErrorResponse` envelope (`Type`/`Code`/`Message`),
  not S3's bare `<Error>` shape.
- `CreateHostedZone` requires a `CallerReference` and rejects a reused one with
  `HostedZoneAlreadyExists` (HTTP 409). Each new zone gets the default apex
  `NS` and `SOA` record sets and a four-server delegation set, derived
  deterministically from the zone ID. The new zone's URL is returned in the
  `Location` header, as on real AWS.
- Domain and record names are canonicalised to lowercase with a trailing dot.
- `ChangeResourceRecordSets` validates the whole batch atomically before
  applying anything: `InvalidChangeBatch` for creating an existing record,
  deleting a missing record or one whose provided values don't match, records
  outside the zone, a CNAME at the zone apex, and deleting the default apex
  NS/SOA records. Weighted/latency/failover/geolocation routing metadata
  (`SetIdentifier`, `Weight`, `Region`, `Failover`, `GeoLocation`,
  `MultiValueAnswer`, `HealthCheckId`) is stored and returned, but routing is
  not evaluated — including at DNS-answer time: several record sets sharing a
  name and type under different `SetIdentifier`s (weighted, multivalue,
  failover) are all merged into the same answer rather than one being chosen.
- `ListResourceRecordSets` returns records in DNS order (names compared with
  labels reversed) and paginates with `name`/`type`/`identifier`/`maxitems`.
- `DeleteHostedZone` returns `HostedZoneNotEmpty` while non-default records
  exist; a successful delete cascades the default records and the zone's tags.
- Health checks are metadata-only: configuration is stored with AWS defaults
  (`RequestInterval` 30, `FailureThreshold` 3, port 80/443 by type) and
  versioned by `UpdateHealthCheck`, but no endpoint is ever probed.

### Known divergences

- `ChangeResourceRecordSets` applies synchronously, so change status is
  `INSYNC` immediately — there is no `PENDING` phase.
- `GetChange` reports `INSYNC` for unknown change IDs instead of
  `NoSuchChange`, so CDK/CLI waiters always converge (even across a store
  reset).
- Private hosted zones may be created without a VPC (real AWS requires one);
  the VPC passed to `CreateHostedZone` is stored and returned, but
  `AssociateVPCWithHostedZone`/`DisassociateVPCFromHostedZone` are not
  implemented.
- `InvalidChangeBatch`/`InvalidInput` message texts approximate AWS's wording;
  codes and HTTP statuses match.
- DNS serving has no notion of "inside a VPC": a private zone answers for
  every container Overcast starts rather than only ones associated with the
  zone's VPC (see "Private zones and VPC association" above).
- Empty non-terminal names — a label with no records of its own but with
  records below it (e.g. querying `mid.example.com` when only
  `leaf.mid.example.com` has data) — are answered NXDOMAIN rather than the
  strictly correct NODATA, since the resolver does not enumerate implicit
  non-terminals; this only affects a query for the exact intermediate name,
  which is rare in practice.
- No compression is used in DNS answers beyond the question-name pointer the
  split-horizon fast path already used; a Route 53 answer with many large
  records could in principle exceed the 512-byte UDP-without-EDNS0 limit and
  need the TCP retry, which is otherwise unaffected.

<!-- BEGIN overcast:capabilities -->

## Operations

19 of 25 listed operations are implemented.
Per-operation status, notes and AWS API links: [Route 53 operations](route53/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/Route53/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
