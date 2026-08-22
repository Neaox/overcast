# EC2 typed-dispatch migration — audit + design

> **Status:** closed, 2026-08-23 (#754). Both waves landed: wave 1 (#1217) routed the twenty-one
> `Describe*` operations; wave 2 routes the remaining forty-eight, so `ec2TypedDispatchRemainder`
> is empty and every one of EC2's 69 registered typed operations now answers from the typed
> registry. See §0 for what shipped and what the strategy became across both waves; §§1–6 below
> are the original 2026-07-26 proposal, kept for the reasoning, with its §2 inventory stale in its
> particulars (see §0.3). Written per [level2-codegen.md](./level2-codegen.md) Track 2.3's explicit
> work queue: EC2 is the largest "kept-on-legacy" item from the P1 Query-dispatch landing (~69
> operations, entire typed branch disabled at the dispatch level — the largest single item in that
> register). Modeled on [dynamodb-gsi-design.md](./dynamodb-gsi-design.md)'s shape: audit → root
> causes → design → gated phases → deferred items.

---

## 0. What landed, and what the strategy became

### 0.1 Shipped (#754, 2026-08-22)

- **Phase 0 — the error codec.** `ec2ErrorCodec` is restored in
  [service.go](../../internal/services/ec2/service.go) from the commit that deleted it (74479583),
  so a typed operation's errors render EC2's `<Errors><Error>` envelope rather than the generic
  Query one. `TestTypedDispatch_errorsUseEC2sEnvelope` pins it.
- **Phase 5, scoped — dispatch is on, per operation.** `DispatchQuery` consults
  `ec2TypedOps` ([typed_dispatch.go](../../internal/services/ec2/typed_dispatch.go)) and answers
  those operations from `h.typedOp`; everything else falls through to the legacy map unchanged.
  The list is an **allow-list**, not the empty denylist §5 Phase 5 proposed — see §0.2.
- **Phases 1–3, for the twenty-one `Describe*` operations,** by a route the original plan did not
  consider: rather than bringing each typed body up to its legacy twin, each operation now has
  **one body** that both paths call. See §0.2.
- **A differential parity test**
  ([typed_parity_dev_test.go](../../internal/services/ec2/typed_parity_dev_test.go), `dev`-tagged):
  every routed operation, over ~80 request cases, put to both dispatch paths against one seeded
  region and required to answer the same status and the same bytes. An operation cannot be routed
  without a row (`TestTypedParity_coversEveryRoutedOp`), and every operation declaring a filterSpec
  must be checked with a filter name it does not implement, so #1032's rule cannot be lost on one
  path only.

### 0.2 What changed about the strategy, and why

§4 decided to "fix the typed twins in place", on the grounds that the typed bodies were already
independent, better-factored implementations worth keeping. Re-reading them against legacy after
#1032/#1037/#1144 does not support that any more: the typed twins are **strictly poorer** in every
`Describe*` — no filters, no ID selection, no tag view, and in `describeVpcAttributeTyped` a
hardcoded `true` where legacy reads the store. Fixing twenty-one of them in place would have meant
writing the same filter-and-select loop twice more per operation and then holding the two copies
together by test.

So the shape became **one body, two front doors**
([describe.go](../../internal/services/ec2/describe.go)). A `describeQuery` carries what a describe
selects on — the `<Resource>Id.N` selection and the filters — decoded either off the form
(`requestQuery`) or out of the typed request struct (`typedQuery`). `filterSpec.parse` and
`parseTagFilters` take a `filterSeq` rather than an `*http.Request`, which is the whole of the
seam. Each `Describe*` then has a single implementation that both the legacy `http.HandlerFunc`
and the typed operation call, and the typed operation returns the legacy `xmlDescribe…Response`
type rather than a second declaration of the same XML.

The consequence worth stating plainly: **parity is now structural, not tested-for.** The
differential test guards the seam and the not-yet-migrated remainder; it is not what makes the
twenty-one agree.

Two smaller reversals:

- **RC2 needs no fix.** §3 called for `decodeItem` in
  [query.go](../../internal/protocol/codec/query.go) to recurse via `decodeStruct` for
  struct-kind slice elements. It already does — the branch was added for IAM's
  `ContextEntries.member.N.ContextKeyValues.member.M` after this document was written, and
  `Filter.N.Name`/`Filter.N.Value.M` decodes correctly through it today. No codec change shipped.
- **Allow-list, not denylist.** §5 Phase 5 proposed the `cfnLegacyOnlyOps` shape with an empty
  denylist. A denylist re-enables everything not yet found broken, and the first attempt is the
  evidence that "not yet found broken" is not "checked". `ec2TypedOps` names what has been checked;
  `ec2TypedDispatchRemainder` names the rest with the defect each still carries, and
  `TestTypedDispatch_everyOpIsClassified` fails on an operation in neither — which is how sixty-nine
  of them came to be unreachable without anything failing.

### 0.3 The §2 inventory, re-baselined

§2's "46 correct twins, 23 diverged" predates #1032. Once the legacy path learned to refuse an
unrecognised filter name and to read `*`/`?` as AWS does, **every** `Describe*` diverged, not the
fifteen §2 lists: `DescribeRegions`, `DescribeAvailabilityZones`, `DescribeVpcs`,
`DescribeAddresses`, `DescribeVpnGateways`, `DescribeNetworkInterfaces` and `DescribeDhcpOptions`
joined the list without their entries changing. The correct-twin count as of 2026-08-21 was closer
to **thirty than forty-six**. The list of *diverged mutations* in §2 held up exactly, and is now
recorded per operation in `ec2TypedDispatchRemainder` rather than here, where it can go stale
again.

### 0.4 What wave 2 closed — 48 operations (2026-08-23)

All 48 are now routed; `ec2TypedDispatchRemainder` is empty. In the same grouping wave 1 left them:

1. **Stubbed mutations (12)** — `TerminateInstances`, `StartInstances`, `StopInstances`,
   `DeleteTags`, `DeleteVpcEndpoints`, `ModifyInstanceAttribute`, the four
   `Authorize`/`RevokeSecurityGroup*`, `ModifySubnetAttribute`, `ModifyVpcAttribute`. Every one of
   these now performs the write its response claimed: state transitions (with the same scheduler
   callback and `h.publish` calls legacy makes), security-group rule append/remove (using the same
   `IpPermission`/`IpRange` store types the legacy path fills, decoded straight off the
   `IpPermissions.N.*` wire shape — RC2's decodeStruct recursion, already proven for `CreateTags`,
   turns out to need no further codec work for a doubly-nested list), tag deletion, VPC-endpoint
   deletion, and — the two `Modify*Attribute` operations `#1144` had routed around — persisting
   `MapPublicIpOnLaunch.Value` / `EnableDnsSupport.Value` / `EnableDnsHostnames.Value` /
   `InstanceType.Value` via a small `ec2AttributeBoolValue`/`ec2AttributeStringValue` wrapper type
   that decodes AWS's `Attribute.Value=x` shape (a nested single-field struct, not a list — the
   same top-level `decodeStruct` recursion RC2 already does, just not through the list branch).
2. **Creates that dropped part of what they were given (5)** — `RunInstances` now carries
   `SecurityGroupId.N` (resolved to `groupSet` the same way legacy does) and `TagSpecification.N`
   (validated and persisted through `putResourceTags`, the same store `CreateTags` uses) and
   publishes `EC2InstanceLaunched`; `CreateVpc` now defaults `EnableDnsSupport=true` (matching the
   stored field #1144 made real) and publishes `EC2VpcCreated`; `CreateSubnet`/`CreateSecurityGroup`
   publish their lifecycle events (`TagSpecification.N` turned out not to be something *legacy*
   supported for either op either — the original classification was wrong on that count, corrected
   here rather than carried forward); `CreateTags` turned out to already be a correct twin — there
   is no EC2 tag lifecycle event for legacy to publish and typed to have skipped, so nothing to fix
   beyond routing it.
3. **The remaining 31**, read against legacy line by line: three (`DeleteVpc`/`DeleteSubnet`/
   `DeleteSecurityGroup`) needed the `#1233` dependency checks
   ([delete_dependencies.go](../../internal/services/ec2/delete_dependencies.go)) wired into the
   typed body, plus the same `h.publish` calls legacy makes; `CreateNatGateway`/`CreateVpnGateway`
   were missing `TagSpecification.N` support legacy already had; `CreateVpcEndpoint` was missing the
   `VpcEndpointType` default legacy already honoured; `AllocateAddress` was ignoring `Domain`
   entirely (found only once the mutation parity harness existed to catch it — a divergence the
   line-by-line read had missed); `CreateInternetGateway`/`CreateRouteTable`/`CreateNetworkInterface`
   were missing a `tagSet` element their legacy twin renders (empty, since none of the three support
   create-time tags either — a pure wire-shape gap, not a behavioral one). The other twenty-four
   were, on inspection, already correct twins — same store calls, same validation, same (absence
   of) event-bus publish — and needed only a parity row to prove it.

**Mutation parity** ([typed_parity_mutations_dev_test.go](../../internal/services/ec2/typed_parity_mutations_dev_test.go))
is a second differential table alongside wave 1's describe-only one
([typed_parity_dev_test.go](../../internal/services/ec2/typed_parity_dev_test.go)): a mutation
cannot be checked by replaying the same request against both paths on one handler the way a
describe can (a second `DeleteVpc` against the same store answers "there is nothing left to
delete", not "the two paths disagree"), so each case runs against two independently seeded
handlers — one legacy, one typed — and compares the masked result, following up with a legacy-driven
`Describe*` where the mutation's own response (often a bare `Return: true`) cannot show what was
written. 80 cases across the 48 operations.

**#754 is closed.** `ec2TypedDispatchRemainder` is kept, empty, so
`TestTypedDispatch_everyOpIsClassified` still refuses a newly registered typed operation that
arrives unclassified.

### 0.5 Known, deliberate imprecision in the seam

The Query codec renders a gap in an indexed parameter (`Filter.3` present, `Filter.2` absent) as a
zero-valued element, where the form readers truncate at the gap. `eachDecodedFilter` and
`selectedIDs` both stop at the first unnamed/empty entry to match, which makes the two paths agree
for every shape an AWS SDK emits and for the truncating case. A *sparse* index — a caller sending
`Value.1` and `Value.3` but not `Value.2` — is read by the typed path as an empty middle value
where legacy truncated. No SDK produces it; recorded here rather than fixed.

---

---

## 1. Context — why EC2 is parked on legacy

[level2-codegen.md §2](./level2-codegen.md) Track 1.1 taught the Query codec how to resolve
`Action` for dispatch, which made every Query service's typed operation registry reachable for
the first time (previously ~250 typed registrations existed but were dead code — no request ever
reached them). Re-enabling that path per-service surfaced real divergences for three services;
two were small (CloudFormation's unmarshalable `struct{}` typed `Out`, IAM's missing
`PolicySourceArn` validation) and were fixed in place. EC2 was not: per the P1 landing note in
[level2-codegen.md §4](./level2-codegen.md#p1-landing-notes-2026-07-24):

> **ec2 — entire typed branch disabled**: filters ignored across `Describe*` ops, mutations
> (Terminate/Stop/ModifyAttribute/DeleteTags/DeleteVpcEndpoints) not taking effect. Needs its own
> audited migration.

The mechanism is explicit in code today — [internal/services/ec2/service.go:132-160](../../internal/services/ec2/service.go):
`DispatchQuery` never consults `s.handler.typedOp` at all. Unlike CloudFormation/SNS, which keep
a legacy-only denylist (`cfnLegacyOnlyOps`, `snsLegacyOnlyOps`) and dispatch everything else
typed-first, EC2 has no such conditional — every operation, without exception, resolves
`Action` and calls straight into `s.handler.ops[action]` (the legacy `http.HandlerFunc` map).
The comment at service.go:134-148 also records that the `ec2ErrorCodec` wrapper (which rendered
typed-op errors in EC2's XML error shape) was deleted along with the branch "to keep lint clean"
and must be recovered from git history when re-enabling — this document treats that as a
prerequisite, not a footnote (see §5, Phase 0).

This audit inventories all 69 registered typed operations, classifies each, and finds the
divergences are not 69 (or even 23) independent bugs — they collapse into **three systemic
patterns**, each fixable once, described in §3.

---

## 2. Inventory and classification

**Every one of the 69 operations in [typed_ops.go](../../internal/services/ec2/typed_ops.go) has
a typed implementation registered** (verified: `s.handler.typedOp` and `s.handler.ops` — the
legacy map, [handler.go:59-131](../../internal/services/ec2/handler.go) — carry the identical
69-key set; `typed_ops_test.go`'s `TestTypedOps_matchLegacyRegistry` already pins the count and
names). There is no "missing typed impl" category for EC2 — the gap is entirely **quality**, not
**coverage**. Classification is therefore two-way: **correct twin** (byte-for-byte equivalent
client-observable behavior) vs. **diverged** (a real behavioral gap from legacy, confirmed by
reading both implementations side by side and, where a test already exists, tracing which path
it currently exercises).

**Counts: 46 correct twins, 23 diverged, 0 missing** (of 69 registered operations).

### Diverged operations (23), grouped by root cause (see §3 for the causes)

| Operation | Divergence | Evidence (typed / legacy) |
|---|---|---|
| `DescribeInstances` | `InstanceId.N` and `Filter` (`instance-state-name`) silently ignored — always returns every instance | [typed_logic.go:1094](../../internal/services/ec2/typed_logic.go) (`_ *describeInstancesReq`, empty struct) vs [handler_instances.go:239-261](../../internal/services/ec2/handler_instances.go) |
| `DescribeInstanceTypes` | Always returns an empty set regardless of `InstanceType.N` | [typed_logic.go:1138-1143](../../internal/services/ec2/typed_logic.go) vs [handler.go:228-277](../../internal/services/ec2/handler.go) (hardcoded catalog lookup) |
| `AuthorizeSecurityGroupIngress` | `IpPermissions.N.*` rules validated for presence but never appended to the group | [typed_logic.go:1314-1323](../../internal/services/ec2/typed_logic.go) vs [handler_security.go:113-150](../../internal/services/ec2/handler_security.go) (`parseIpPermissions` + `store.putSecurityGroup`) |
| `AuthorizeSecurityGroupEgress` | Same pattern, egress rules | [typed_logic.go:1325-1334](../../internal/services/ec2/typed_logic.go) vs [handler_security.go:155-191](../../internal/services/ec2/handler_security.go) |
| `RevokeSecurityGroupIngress` | Same pattern; legacy also validates rule existence (`InvalidPermission.NotFound`) | [typed_logic.go:1336-1345](../../internal/services/ec2/typed_logic.go) vs [handler_security.go:196-243](../../internal/services/ec2/handler_security.go) |
| `RevokeSecurityGroupEgress` | Same pattern | [typed_logic.go:1347-1356](../../internal/services/ec2/typed_logic.go) vs [handler_security.go:248-295](../../internal/services/ec2/handler_security.go) |
| `DescribeSecurityGroups` | `GroupId.N` + `Filter` (`group-id`/`group-name`/`vpc-id`) ignored | [typed_logic.go:1358-1406](../../internal/services/ec2/typed_logic.go) vs [handler_security.go:330-...](../../internal/services/ec2/handler_security.go) |
| `DescribeSubnets` | `SubnetId.N` + `Filter` (`subnet-id`/`vpc-id`/`availability-zone`) ignored | [typed_logic.go:1408-1433](../../internal/services/ec2/typed_logic.go) vs [handler_security.go:413-...](../../internal/services/ec2/handler_security.go) |
| `RunInstances` | `SecurityGroupId.N` and `TagSpecification.N` ignored (instance persisted with no SGs/tags, response missing `groupSet`/`tagSet`); lifecycle event not published | [typed_logic.go:1435-1523](../../internal/services/ec2/typed_logic.go) (`runInstancesReq` has no SG/tag fields) vs [handler_instances.go:103-233](../../internal/services/ec2/handler_instances.go) |
| `TerminateInstances` | Total stub — ignores `InstanceId.N`, no state change, empty response | [typed_logic.go:1525-1530](../../internal/services/ec2/typed_logic.go) vs [handler_instances.go:307-361](../../internal/services/ec2/handler_instances.go) |
| `StartInstances` | Total stub | [typed_logic.go:1539-1544](../../internal/services/ec2/typed_logic.go) vs [handler_instances.go](../../internal/services/ec2/handler_instances.go) (`StartInstances`) |
| `StopInstances` | Total stub | [typed_logic.go:1532-1537](../../internal/services/ec2/typed_logic.go) vs [handler_instances.go:366-...](../../internal/services/ec2/handler_instances.go) |
| `DescribeImages` | `ImageId.N` filter ignored | [typed_logic.go:1546-1556](../../internal/services/ec2/typed_logic.go) vs [handler_images.go:85-106](../../internal/services/ec2/handler_images.go) |
| `DescribeKeyPairs` | `KeyName.N` filter ignored | [typed_logic.go:1587-1605](../../internal/services/ec2/typed_logic.go) vs [handler_keypairs.go:112-143](../../internal/services/ec2/handler_keypairs.go) |
| `DescribeRouteTables` | `RouteTableId.N` + `Filter` (`route-table-id`/`vpc-id`) ignored | [typed_logic.go:1647-1661](../../internal/services/ec2/typed_logic.go) vs [handler_routetables.go:129-136](../../internal/services/ec2/handler_routetables.go) |
| `DescribeInternetGateways` | `InternetGatewayId.N` + `Filter` (`internet-gateway-id`/`attachment.vpc-id`) ignored | [typed_logic.go:1810-1824](../../internal/services/ec2/typed_logic.go) vs [handler_igw.go:82-89](../../internal/services/ec2/handler_igw.go) |
| `DescribeVpcPeeringConnections` | `VpcPeeringConnectionId.N` + `Filter` (`status-code`/`requester-vpc-info.vpc-id`/`accepter-vpc-info.vpc-id`) ignored | [typed_logic.go:1966-1980](../../internal/services/ec2/typed_logic.go) vs [handler_vpc_peering.go:186-...](../../internal/services/ec2/handler_vpc_peering.go) |
| `DeleteTags` | Total stub — `ResourceId.N`/`Tag.N.*` never applied, nothing deleted | [typed_logic.go:2035-2041](../../internal/services/ec2/typed_logic.go) vs [handler_tags.go:66-88](../../internal/services/ec2/handler_tags.go) |
| `DescribeTags` | `Filter` (`resource-id`) ignored | [typed_logic.go:2043-2062](../../internal/services/ec2/typed_logic.go) vs [handler_tags.go:107-146](../../internal/services/ec2/handler_tags.go) |
| `DescribeNatGateways` | `NatGatewayId.N` + `Filter` (`nat-gateway-id`/`vpc-id`/`subnet-id`/`state`) ignored | [typed_logic.go:2217-2239](../../internal/services/ec2/typed_logic.go) vs [handler_natgw.go:140-195](../../internal/services/ec2/handler_natgw.go) |
| `ModifyInstanceAttribute` | Fetches the instance, discards it (`_ = inst`), never applies `InstanceType.Value` or persists | [typed_logic.go:2554-2568](../../internal/services/ec2/typed_logic.go) vs [handler_instances.go:586-616](../../internal/services/ec2/handler_instances.go) |
| `DescribeVpcEndpoints` | `VpcEndpointId.N` + `Filter` (`vpc-id`/`service-name`) ignored | [typed_logic.go:2601-2621](../../internal/services/ec2/typed_logic.go) vs [handler_vpcendpoints.go:102-148](../../internal/services/ec2/handler_vpcendpoints.go) |
| `DeleteVpcEndpoints` | Total stub — `VpcEndpointId.N` never applied, nothing deleted | [typed_logic.go:2623-2629](../../internal/services/ec2/typed_logic.go) vs [handler_vpcendpoints.go:154-168](../../internal/services/ec2/handler_vpcendpoints.go) |

### Correct twins (46)

`DescribeRegions`, `DescribeAvailabilityZones`, `CreateVpc`, `DescribeVpcs`, `DeleteVpc`,
`CreateSubnet`, `DeleteSubnet`, `CreateSecurityGroup`, `DeleteSecurityGroup`, `CreateKeyPair`,
`DeleteKeyPair`, `CreateRouteTable`, `DeleteRouteTable`, `CreateRoute`,
`DeleteRoute`, `AssociateRouteTable`, `DisassociateRouteTable`, `CreateInternetGateway`,
`DeleteInternetGateway`, `AttachInternetGateway`, `DetachInternetGateway`,
`CreateVpcPeeringConnection`, `AcceptVpcPeeringConnection`, `DeleteVpcPeeringConnection`,
`CreateTags`, `AllocateAddress`, `ReleaseAddress`, `DescribeAddresses`, `AssociateAddress`,
`DisassociateAddress`, `CreateNatGateway`, `DeleteNatGateway`, `ModifySubnetAttribute`,
`ModifyVpcAttribute`, `DescribeVpcAttribute`, `DescribeDhcpOptions`, `DescribeAccountAttributes`,
`CreateVpnGateway`, `AttachVpnGateway`, `DescribeVpnGateways`, `DetachVpnGateway`,
`DeleteVpnGateway`, `CreateNetworkInterface`, `DescribeNetworkInterfaces`,
`DeleteNetworkInterface`, `CreateVpcEndpoint` (46 named, matching the §2 count: 46 correct + 23
diverged = 69 registered operations, 0 missing).

Correctness here mostly means "the operation has no ID-list/filter surface to lose in the first
place" (creates, deletes-by-single-ID, attach/detach, accept semantics) or "legacy itself is
metadata-only and the typed twin matches that" (`ModifySubnetAttribute`/`ModifyVpcAttribute`
acknowledge without persisting attribute state on **both** paths — verified against
[handler_misc.go:25-122](../../internal/services/ec2/handler_misc.go) — this is not a divergence,
it is a pre-existing, matched-on-both-sides simplification and is out of scope here).

A secondary, non-blocking observation: several correct-twin mutations (e.g. `AttachInternetGateway`,
`DeleteVpc`) call `h.store`/`h.vpcStrategy` correctly but skip the `h.publish` event-bus call the
legacy handler makes. Events are internal (UI/reconciliation signal, not part of the AWS wire
response) and out of scope for "AWS fidelity," but should be picked up as a lint-level parity
item during the per-op mutation pass in Phase 3 rather than tracked as its own root cause.

---

## 3. Root causes — three systemic patterns, not 23 bugs

### RC1 — Typed request structs omit list/filter fields the operation actually needs

Every `Describe*` operation above has its typed request declared as a **literally empty
struct**, e.g. [typed_logic.go:18](../../internal/services/ec2/typed_logic.go) `type
describeInstancesReq struct{}`. Compare `createTagsReq` at
[typed_logic.go:162-165](../../internal/services/ec2/typed_logic.go), which *does* declare
`ResourceIDs []string \`json:"ResourceId"\`` and `Tags []ec2TagRequest \`json:"Tag"\`` and decodes
correctly (confirmed: `createTagsTyped` is a correct twin) — so this is not "the codec can't do
lists," it's that most request structs were never given the fields legacy parses ad hoc via
`r.FormValue`/`parseIndexedParam`/`parseFilterValues` (three separate, redundant idioms in legacy
code itself — see the note at the end of this section). `RunInstances` is the mutation-side
instance of the same pattern: `runInstancesReq` ([typed_logic.go:72-78](../../internal/services/ec2/typed_logic.go))
has `ImageID`/`InstanceType`/`MinCount`/`MaxCount`/`SubnetID` but no `SecurityGroupIds`/`Tags`.

**Fix shape:** add the missing fields to each request struct (`InstanceIDs []string
\`json:"InstanceId"\``, `Filters []ec2Filter \`json:"Filter"\`` with `ec2Filter{ Name string;
Values []string \`json:"Value"\` }`, etc.) — mechanical, per-op, no codec change required by
itself. This alone is not sufficient for the `Filter.N.Value.M` shape — see RC2.

### RC2 — The Query-form decoder cannot decode a nested indexed list inside a list element

Even after RC1 is fixed, a `Filters []ec2Filter` field with `Values []string \`json:"Value"\``
would **still** decode incorrectly for the AWS wire shape `Filter.1.Name=X&Filter.1.Value.1=A&
Filter.1.Value.2=B`, and likewise `IpPermissions.1.IpRanges.1.CidrIp=...` would still fail for
`AuthorizeSecurityGroupIngress`/`Egress`. The bug is in the shared codec, not in EC2:

[internal/protocol/codec/query.go:286-318](../../internal/protocol/codec/query.go) `decodeItem`
— the function that decodes one element of a slice-of-struct field — only does a **flat,
exact-tag-name lookup**:

```go
for i := range rt.NumField() {
    tag := rt.Field(i).Tag.Get("json")
    ...
    if values.Has(name) {                       // exact match only — "Value" != "Value.1"
        setFieldValue(rv.Field(i), values.Get(name))
    }
}
```

For a `Filter.N` item, the values passed in are keyed by what remains after stripping
`Filter.N.` — for `Filter.1.Value.1` that is `Value.1`. `values.Has("Value")` is `false`, so the
nested list is silently dropped. Contrast this with the **top-level** `decodeStruct`
([query.go:115-284](../../internal/protocol/codec/query.go)), which already has full,
general-purpose handling for exactly this shape (its "flattened EC2 Query list" branch at
[query.go:176-186](../../internal/protocol/codec/query.go): `len(parts) >= 2 &&
strconv.Atoi(parts[1])` succeeds → treat as an indexed list, recurse). **`decodeItem` reimplements
a subset of `decodeStruct`'s logic instead of calling it.**

**Fix shape (one change, shared by every list-of-struct-with-a-nested-list field across every
Query-protocol service, not just EC2):** replace `decodeItem`'s struct branch with a call to
`decodeStruct(values, rv, "")` (the values map is already scoped/stripped to the item's own keys,
so no prefix is needed). Traced by hand against the existing algorithm: for `Filter.1.Value.1`
scoped down to `Value.1`, `decodeStruct` splits `parts = ["Value", "1"]`, hits its own
"flattened list" branch, and correctly builds a `[]string` under the `Value` field — this is not
speculative, it is the same code path already proven correct for `CreateTags`' top-level
`Tag.N.Key`/`Tag.N.Value` list. This is the **single shared-decode fix** the migration should land
first (§5, Phase 1) because every RC1 fix downstream depends on it working.

**Scope note — do not conflate with the SNS gap.** [level2-codegen.md §4](./level2-codegen.md)
separately flags SNS `Publish`/`PublishBatch`'s `MessageAttributes.entry.N.…` gap. That is a
**different** shape (a map whose *value* is itself a structured `{DataType, StringValue}` object,
decoded by the `mapEntries` branch at [query.go:188-199](../../internal/protocol/codec/query.go),
which only extracts flat `key`/`value` strings) — fixing `decodeItem` here does not fix SNS's gap,
and fixing SNS's gap would not fix this one. Both are instances of "the Query decoder doesn't
recurse into structured values," but they're different branches of the same function and need
independent test coverage; SNS's fix stays scoped to `snsLegacyOnlyOps` per that plan.

**Legacy-side observation (informational, not a blocker):** legacy handlers themselves use three
different ad hoc filter-parsing idioms — `parseIndexedParam`/`parseFilterValues`
([handler_instances.go:517-573](../../internal/services/ec2/handler_instances.go), reused by
several handlers), `collectFormValues`/`collectFormFilters`/`matchFilters`
([handler_natgw.go:236-...](../../internal/services/ec2/handler_natgw.go)), and fully inline loops
(`DescribeTags`, the ID-collection half of `DescribeVpcEndpoints`). Once RC1+RC2 land and typed
ops decode filters through struct fields, a natural (but optional, non-blocking) follow-up is a
single shared `ec2Filter`-matching helper used by every typed handler, ending this three-way
duplication. Not required for parity — flagged as a Phase-2-adjacent cleanup, not a gate.

### RC3 — Handler bodies that were never finished (stub-and-succeed)

Independent of RC1/RC2, several typed functions are simply unfinished: they validate a required
parameter, sometimes even fetch the resource from the store, then **return a success response
without calling any mutating store method**. The tell is the discard pattern at
[typed_logic.go:2562](../../internal/services/ec2/typed_logic.go): `inst, aerr :=
h.store.getInstance(ctx, req.InstanceID)` immediately followed by `_ = inst`. This affects
`TerminateInstances`, `StartInstances`, `StopInstances`, `DeleteTags`, `DeleteVpcEndpoints`,
`ModifyInstanceAttribute`, `AuthorizeSecurityGroupIngress`/`Egress`,
`RevokeSecurityGroupIngress`/`Egress`, and `DescribeInstanceTypes` (returns an empty set
unconditionally rather than the hardcoded catalog lookup legacy performs). This is not a codec or
struct problem — even with a perfect `InstanceIDs []string` field, `terminateInstancesTyped`
would still need someone to write the loop that calls `h.store.putInstance` with the new state
and schedules the async transition, mirroring
[handler_instances.go:307-361](../../internal/services/ec2/handler_instances.go).

### A fourth, orthogonal prerequisite: EC2's error wire shape

Not a divergence in the 69-op sense above, but blocking for re-enabling dispatch: EC2's documented
Query-protocol error envelope ([errors.go:376-388](../../internal/protocol/errors.go), verified
against AWS's error-response docs per the comment there) is `<Response><Errors><Error><Code/>
<Message/></Error></Errors><RequestID/></Response>` — a plural `<Errors>` wrapper around one or
more `<Error>` elements, written by
[protocol.WriteEC2QueryXMLError](../../internal/protocol/errors.go#L419). The generic Query
codec's `WriteError` (used by `op.Typed.Invoke` via `codec.QueryXML`,
[query.go:92-94](../../internal/protocol/codec/query.go)) instead calls
[protocol.WriteQueryXMLError](../../internal/protocol/errors.go#L391) — the SNS-shaped envelope
with a single `<Error><Type/><Code/><Message/></Error>` and no `<Errors>` wrapper at all
([errors.go:370-374](../../internal/protocol/errors.go)). The
[service.go:146-148](../../internal/services/ec2/service.go) comment already documents that an
`ec2ErrorCodec` wrapper existed for exactly this and was deleted with the branch. **Recovering or
re-deriving this wrapper is a Phase 0 prerequisite** — without it, every typed-op error response
(not just the 23 diverged ops — any of the 46 "correct twins" that can error, e.g. `DeleteVpc` on
a not-found VPC) would silently switch to the wrong XML error shape the moment dispatch flips,
which is its own regression class distinct from RC1-3.

---

## 4. Migration strategy

**Decision: fix the typed twins in place (RC1 struct fields + RC3 bodies), plus one shared codec
fix (RC2), rather than making typed ops thin delegates to legacy, or a full rewrite.** Three
reasons: (1) the typed bodies for all 46 correct twins are already independent, parallel,
non-duplicated implementations of the same logic as legacy — the "delegate, don't duplicate"
principle from [level2-codegen.md Track 2.2](./level2-codegen.md) is aimed at *live* duplication
(both paths receiving real traffic and rotting independently); EC2's legacy path receives 100% of
traffic today and the typed path receives none, so there is no rot-in-two-places risk to fix by
delegating — there is a one-time catch-up cost, paid once, after which typed-first dispatch makes
the typed path the only one that matters and legacy becomes deletable. (2) The typed bodies are
generally *better factored* than legacy already (e.g. `routeTableToTypedXML`,
`igwToTypedXML`, `pcxToTypedXML`, `vpnGatewayToTypedXML` helpers exist only on the typed side and
already deduplicate response-shaping code legacy repeats inline) — throwing them away for
thin shims would be a regression in code quality, not an improvement. (3) Per RC2, the missing
piece is substantially a **decoder** fix, not a duplicated-logic fix — once request structs
correctly receive `InstanceIds`/`Filters`/`IpPermissions`, most typed bodies need only the same
few lines of loop/mutation logic legacy already has, which is a small, reviewable diff per op,
not a rewrite.

This is a **hybrid** in the sense the brief anticipated: **one shared-decode fix (RC2) landed
once**, followed by **per-op-class waves** (RC1 field additions + RC3 body completions), ordered
so each wave is independently testable against the existing integration suite before the next
starts.

### Why the existing integration suite is the primary parity instrument

[tests/integration/ec2/ec2_test.go](../../tests/integration/ec2/ec2_test.go) is 4,018 lines and
~80 tests, already exercising the **legacy** path end-to-end via the real
`DispatchQuery`/`Action=` HTTP surface — including filter-specific tests already in place today:
`TestDescribeInstances_filterByState`, `TestDescribeSecurityGroups_filterByVpcId`,
`TestDescribeSubnets_filterByVpcId`/`_filterBySubnetId`, `TestDescribeImages_filterByID`,
`TestDescribeVpcPeeringConnections_filterByID`, plus full-lifecycle tests for VPC/IGW/route
table/NAT gateway/ENI/tags/addresses/VPC endpoints, and both `TestModifyInstanceAttribute_*` and
`TestDeleteVpcEndpoints_removes`/`TestDeleteTags` mutation-effect tests. This suite is what
originally surfaced the divergence during the P1 sweep (per the landing note) and is the natural
regression gate: **run it unmodified against typed dispatch, per operation class, and it must
stay green.** Where a gap exists (no filter test for `DescribeKeyPairs`/`DescribeRouteTables`/
`DescribeInternetGateways`/`DescribeNatGateways`/`DescribeVpcEndpoints`'s ID-list, or for
`RunInstances`' `SecurityGroupId.N`/`TagSpecification.N` application, or `DescribeTags`'
`resource-id` filter), a new failing test is added first per this repo's standard bug-fix
discipline, **before** the corresponding typed fix — this also converts each such addition into
permanent regression coverage for legacy, which currently has none for those cases either.

`typed_ops_test.go`'s `TestTypedOps_matchLegacyRegistry` stays as a registry-shape guard (does
every legacy op have a named, correctly-labeled typed twin) but provides **zero** behavioral
protection — it would pass unchanged for every stub in this document. It is not a substitute for
the integration suite as a gate.

**Wire-byte goldens.** [wire-byte-goldens.md](./wire-byte-goldens.md) (in progress on
`feat/wire-byte-goldens`, parallel to this work) is the intended long-term parity instrument for
exact byte-level request/response shape — once available for EC2, each phase below should also
add or update golden fixtures for the operations it touches, in addition to the integration
suite. Treat goldens as **available but not yet required**: if that branch lands and merges before
a given phase starts, that phase's gate includes golden coverage for its operations; if not, the
phase proceeds on the integration suite alone and picks up goldens as a fast-follow. Do not block
on goldens landing.

---

## 5. Phases

Each phase is independently gated and sized for one subagent session. Dispatch stays on legacy
(`DispatchQuery` unchanged) until Phase 5 — every earlier phase does correctness work on the
typed path with tests calling the typed functions/registry directly (as `typed_ops_test.go`
already does), so nothing user-facing changes until the final flip, and a phase can be paused
indefinitely without any partial-migration risk to real traffic.

### Phase 0 — Prerequisites (S, ~0.5 day)
- Recover/recreate the EC2 typed-error codec wrapper (§3, "fourth prerequisite") so typed-op
  errors render via `protocol.WriteEC2QueryXMLError`'s `<Errors><Error>` shape, not the generic
  Query codec's shape. Source from git history at the commit that deleted it (referenced in the
  service.go comment) as a starting point.
- Add a unit test asserting a typed-op error (e.g. `DeleteVpc` on an unknown VPC ID) renders the
  EC2 `<Errors>` wrapper when invoked through the typed registry directly (not yet through
  `DispatchQuery`).
- **Gate:** new test green; no dispatch change yet.

### Phase 1 — Shared codec fix (RC2) (S, ~0.5–1 day)
- Fix `decodeItem` in [internal/protocol/codec/query.go](../../internal/protocol/codec/query.go)
  to recurse via `decodeStruct` for struct-kind slice elements, instead of its current flat
  exact-tag lookup.
- New codec-package unit tests (not EC2-specific): decode a `Filter.N.Name`/`Filter.N.Value.M`-
  shaped field into `[]struct{ Name string; Values []string }`, and a doubly-nested case
  (`IpPermissions.N.IpRanges.M.CidrIp`) into a three-level nested slice-of-struct-with-slice.
  These tests should be written to fail against today's `decodeItem` first.
- Run the **full existing Query-codec test suite** (`internal/protocol/codec/...`) plus a
  representative slice of the other ~10 Query services' typed-op tests (IAM, CloudFormation, SNS,
  STS, RDS, SES, AutoScaling, ELBv2, Route53, ElastiCache) to confirm no other service's
  list-of-struct decoding regresses — this function is shared, so this is the one phase in this
  document with blast radius outside `internal/services/ec2`.
- **Gate:** new codec tests green; `go test ./internal/protocol/codec/... ./internal/services/...`
  (or at minimum every Query-protocol service's package) green.

### Phase 2 — Describe* filter/ID-list restoration (RC1, using Phase 1's fix) (M, ~1–2 days, parallelizable per sub-group)
Add the missing request-struct fields and filter-application logic for each Describe op, grouped
by resource family so each sub-group is a small, reviewable diff:
- Instances: `DescribeInstances` (`InstanceId.N`, `instance-state-name` filter).
- Networking: `DescribeSecurityGroups`, `DescribeSubnets`, `DescribeRouteTables`,
  `DescribeInternetGateways`, `DescribeVpcPeeringConnections`, `DescribeVpcEndpoints`,
  `DescribeNatGateways`.
- Misc: `DescribeImages`, `DescribeKeyPairs`, `DescribeTags`, `DescribeInstanceTypes` (restore
  the hardcoded-catalog lookup body; this one is RC3-only, no filter shape beyond
  `InstanceType.N`).
For each op: a failing integration test first (new test where the suite has a gap, per §4),
then the struct + handler fix, confirmed against the **existing** typed-registry unit test
pattern (call the typed function directly, not yet through `DispatchQuery`).
- **Gate:** every op in this phase's `Describe*Typed` function, called directly, returns the same
  filtered/ID-scoped result set as its legacy twin for the corresponding integration test's
  fixture (add a small per-op typed-vs-legacy comparison test, or extend `typed_ops_test.go`'s
  pattern, whichever is less duplicative).

### Phase 3 — Mutation-effect restoration (RC3) (M, ~1–2 days)
Complete the stub bodies so they actually call the mutating store methods, mirroring legacy
line-for-line where legacy's behavior is correct:
- `TerminateInstances`, `StartInstances`, `StopInstances` (state transitions + the async
  scheduler callback pattern already used elsewhere in `typed_logic.go`, e.g. `runInstancesTyped`'s
  own `h.scheduler.After` call at [typed_logic.go:1493](../../internal/services/ec2/typed_logic.go)).
- `DeleteTags`, `ModifyInstanceAttribute`, `DeleteVpcEndpoints`.
- `AuthorizeSecurityGroupIngress`/`Egress`, `RevokeSecurityGroupIngress`/`Egress` (depends on
  Phase 1's codec fix for `IpPermissions.N.IpRanges.M`).
- `RunInstances`: add `SecurityGroupIds []string` and a `TagSpecifications` field, apply both to
  the persisted `Instance` and the response, and restore the `h.publish` event call.
- **Gate:** the integration suite's existing mutation-effect tests
  (`TestTerminateInstances_success`/`_batch`, `TestStopInstances_success`,
  `TestModifyInstanceAttribute_instanceType`, `TestDeleteVpcEndpoints_removes`, `TestDeleteTags`,
  `TestAuthorizeSecurityGroupIngress_success`, `TestRevokeSecurityGroupIngress_success`) pass when
  invoked against the typed functions directly; new tests added first for
  `RunInstances`+SecurityGroupId.N/TagSpecification.N (no existing coverage) and
  `StartInstances`/`Egress` variants if not already covered by the batch test above.

### Phase 4 — Full-suite typed-path validation (S, ~0.5–1 day)
- Add a test-only path (or a `t.Run` matrix) that runs the **entire**
  `tests/integration/ec2/ec2_test.go` suite twice: once against `DispatchQuery` as-is (legacy,
  today's baseline) and once against a variant that routes through `s.handler.typedOp` first
  (mirroring the `cfnLegacyOnlyOps`/`snsLegacyOnlyOps` pattern with an **empty** denylist, i.e.
  "typed-first, no exceptions" — this is the dry run for Phase 5's real dispatch change without
  touching production dispatch yet).
- Any remaining failure at this point is a **new**, previously-unidentified divergence — fix
  failing-test-first per this repo's standing bug-fix discipline; do not proceed to Phase 5 with
  a known-red op.
- If `feat/wire-byte-goldens` has merged by this point, add EC2 golden fixtures for the
  operations touched in Phases 2-3 as part of this phase's gate.
- **Gate:** 100% of `tests/integration/ec2/ec2_test.go` green against the typed-first dry run.

### Phase 5 — Flip dispatch (S, ~0.5 day)
- Change `Service.DispatchQuery` ([service.go:149-160](../../internal/services/ec2/service.go))
  to the same shape CloudFormation/SNS use: typed-first via `s.handler.typedOp`, with an **empty**
  `ec2LegacyOnlyOps` denylist declared explicitly (not just an absent check) so any future
  regression is a deliberate, named, commented exception rather than a silent fallback — matching
  the pattern and self-documentation `cfnLegacyOnlyOps`/`snsLegacyOnlyOps` already establish.
- Delete the now-dead legacy `http.HandlerFunc` bodies only for operations with zero remaining
  callers — per Track 2.2's "delegate, don't duplicate," but do this as a **separate** follow-up
  PR after Phase 5 has soaked, not bundled into the dispatch flip itself, so a revert of the flip
  doesn't also need to resurrect deleted code.
- **Gate:** full `go test ./internal/services/ec2/... ./tests/integration/ec2/...` green;
  `go vet`/`gofmt` clean; `make docs` run if any capability-table-visible behavior changed (it
  should not — this phase is response-shape-invisible to a client, purely an internal routing
  change, since Phases 2-4 already proved the typed path byte-matches legacy for every covered
  case).

**Total estimate: ~4-7 days across 6 phases**, each independently revertable (dispatch doesn't
change until Phase 5, so Phases 0-4 carry no user-facing risk at all).

---

## 6. Deferred / non-goals

- **The three-way legacy filter-parsing duplication** (`parseFilterValues`/`parseIndexedParam` vs
  `collectFormFilters`/`collectFormValues`/`matchFilters` vs inline loops) is not unified by this
  plan. It's legacy-side debt that becomes moot once legacy is deleted post-Phase 5; consolidating
  it earlier would be effort spent on code this plan intends to delete.
- **Event-bus (`h.publish`) parity** for the handful of correct-twin mutations that skip it (§2)
  is not part of this plan's gates — it's invisible on the wire and orthogonal to AWS fidelity.
  Picked up opportunistically during Phase 3 if touching the same function, not tracked
  separately.
- **The SNS `MessageAttributes.entry.N` gap** ([level2-codegen.md §4](./level2-codegen.md)) is
  explicitly out of scope here (§3 RC2's scope note) — different codec branch, different service,
  its own plan.
- **New EC2 operations.** This plan is scope-limited to the 69 already-registered operations. It
  does not add coverage for operations EC2 doesn't implement today (those remain 501 per
  [service.go's doc comment](../../internal/services/ec2/service.go)).
- **Wire-byte goldens for EC2 are not a hard blocker.** Per §4, they're the better long-term
  instrument but this plan proceeds on the existing integration suite if the goldens branch isn't
  ready when a phase starts; goldens are added opportunistically per phase once available.
- **Pinning/characterization tests in this document's own PR.** Per the task brief, no production
  code changes accompany this document. Any test additions referenced in §5 land with their
  respective implementation phase, not with this design doc.

---

*Sections 1-6 above are the original 2026-07-26 proposal and describe the plan as it stood before
any of it ran. What actually shipped, and where the strategy departed from this, is §0.*
