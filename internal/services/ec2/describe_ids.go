package ec2

// describe_ids.go — what a Describe* does with an explicit `<Resource>Id.N`
// list, as opposed to a filter.
//
// EC2 has two selectors and treats them as opposites, which is the distinction
// #1708 was filed about. A *filter* that matches nothing is a legitimate empty
// 200: the caller asked which resources look like this, and none do. An
// explicitly named *id* that does not resolve is an error, because the caller
// asserted the resource exists — and it is the exception, not the list length,
// that carries the answer. Terraform's and CloudFormation's refresh logic reads
// `Invalid*.NotFound` to mean "gone, drop it from state"; a waiter treats it as
// its terminal condition. Handed a 200 with an empty list instead, a provider
// that only inspects the exception concludes the resource is fine, and a waiter
// spins to its timeout.
//
// Every describe here used to apply the filter rule to both, because
// idSelection.has treats an id the region does not hold exactly the way it
// treats a resource a filter excluded. This file is the second half of that
// type: the selection is now also *resolved*, once, in one place. A per-
// operation copy is how AWS's casing quirks would get normalised by accident —
// see idScope below.
//
// Shape is checked before existence, and across the whole list before any of
// it is resolved, because that is where AWS checks it: `.Malformed` comes out
// of request parsing, ahead of any lookup, so a request naming an unknown-but-
// well-formed id *and* a malformed one answers `.Malformed`.

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// idScope is one Describe*'s explicit-id contract: the prefix its ids carry,
// and the two codes AWS answers a bad one with.
//
// The codes are fields rather than something derived from the prefix because
// AWS's own spelling is not derivable. It is `InvalidVpcID.Malformed` but
// `InvalidGroupId.Malformed`; `InvalidRouteTableID.NotFound` but
// `InvalidRouteTableId.Malformed` — the same resource, two casings, in two
// rows of the same table. Clients match on the exact string, so each one is
// copied from the EC2 API reference's error list verbatim and none of them is
// normalised:
// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/errors-overview.html
type idScope struct {
	// prefix is the id prefix, without the dash — "vpc", "sg", "i". It is
	// empty for the one selector EC2 does not spell that way, the public
	// address DescribeAddresses takes in PublicIp.N.
	prefix string

	// expect is what the malformed message tells the caller to send instead —
	// "vpc-..." for a prefixed id, "xx.xx.xx.xx" for an address.
	expect string

	// notFound is the code for a well-formed id the region does not hold.
	notFound string

	// notFoundMessage is that code's message, with one %s for the id. AWS
	// words it per resource — "The vpc ID '…' does not exist", but "The
	// security group '…' does not exist" with no "ID" at all — so it is
	// spelled out rather than built from the prefix.
	notFoundMessage string

	// malformed is the code for an id whose shape AWS rejects before it looks
	// anything up.
	//
	// It is empty for a resource the reference documents no Malformed code
	// for — allocation IDs and virtual private gateways, as of #1847. That is
	// a fact about AWS's table rather than an omission here, and inventing a
	// plausible `InvalidAllocationID.Malformed` would be the one thing worse
	// than not having one: a client matching AWS's set would never see it, and
	// a client matching ours would learn a code AWS never sends. Where it is
	// empty, checksShape is false and a wrong-shaped id falls through to the
	// existence pass, so the caller is told the id is not there — which is
	// true, and is a code AWS does send.
	malformed string

	// shape matches the ids AWS accepts for this scope, built by newIDScope
	// (or supplied whole by newShapedScope). Only read when checksShape.
	shape *regexp.Regexp
}

// checksShape reports whether this scope can refuse an id on its shape, which
// it can only do when AWS documents a code to refuse it with.
func (s idScope) checksShape() bool { return s.malformed != "" }

// newIDScope compiles the id shape AWS accepts for a prefix.
//
// Eight or seventeen hex characters: the short form EC2 issued before the move
// to long ids and still accepts, and the long form it issues now. Both are
// spelled out in AWS's own `.Malformed` messages ("in the form i-xxxxxxxx or
// i-xxxxxxxxxxxxxxxxx"), and `shortID`/`longID` in handler.go mint exactly
// those two.
func newIDScope(prefix, notFound, notFoundMessage, malformed string) idScope {
	return idScope{
		prefix:          prefix,
		expect:          prefix + "-...",
		notFound:        notFound,
		notFoundMessage: notFoundMessage,
		malformed:       malformed,
		shape:           regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `-([0-9a-f]{8}|[0-9a-f]{17})$`),
	}
}

// newShapedScope is newIDScope for a selector that is not a `<prefix>-<hex>`
// id at all.
//
// There is exactly one: DescribeAddresses' PublicIp.N, which names an Elastic
// IP by its address. It is still a resolved selection rather than a filter —
// AWS answers InvalidAddress.NotFound for an address it does not hold — so it
// belongs to this file rather than to filters.go, and only the shape and the
// wording of "what you should have sent" differ.
func newShapedScope(shape *regexp.Regexp, expect, notFound, notFoundMessage, malformed string) idScope {
	return idScope{
		expect:          expect,
		notFound:        notFound,
		notFoundMessage: notFoundMessage,
		malformed:       malformed,
		shape:           shape,
	}
}

// errNotFoundID is the answer to an id that is shaped right and is not there.
func (s idScope) errNotFoundID(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       s.notFound,
		Message:    fmt.Sprintf(s.notFoundMessage, id),
		HTTPStatus: http.StatusBadRequest,
	}
}

// errMalformedID is the answer to an id that is not an id.
//
// The message follows the shape real EC2 answers with rather than the prose in
// the reference's description column, which reads as guidance to a human
// ("Ensure that you provide the full security group ID…") and is not what comes
// back on the wire. The wire shape is reported verbatim in
// hashicorp/terraform-provider-aws#6104 —
// `Invalid id: "arn:aws:ec2:…:security-group/sg-0226c17131bc870f0" (expecting "sg-...")`
// — and in hashicorp/terraform-provider-aws#9693 for a different prefix, so it
// is the same sentence with the prefix substituted.
func (s idScope) errMalformedID(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       s.malformed,
		Message:    fmt.Sprintf("Invalid id: %q (expecting %q)", id, s.expect),
		HTTPStatus: http.StatusBadRequest,
	}
}

// resolveIDShapes is the first of resolveIDs' two passes on its own: the shape
// of every id in one selector, refused with the scope's Malformed code.
//
// It is separate because an operation with two selectors has to run every
// shape check before any existence check, not both passes per selector. AWS
// raises `.Malformed` from request parsing, which sees the whole request, so a
// DescribeAddresses naming an allocation the region does not hold *and* a
// public address that is not an address answers about the address. Nothing
// else in EC2 has two id selectors; a scope with no Malformed code checks
// nothing here and refuses the id in the existence pass instead.
func resolveIDShapes(scope idScope, sel idSelection) *protocol.AWSError {
	if !scope.checksShape() {
		return nil
	}
	for _, id := range sel.all() {
		if !scope.shape.MatchString(id) {
			return scope.errMalformedID(id)
		}
	}
	return nil
}

// resolveIDs is the rule this file exists for: a Describe*'s explicit id list,
// checked against what the region actually holds.
//
// An empty list is not a selection at all — it means "everything the filters
// allow" — so it keeps today's semantics untouched, which is the regression
// risk #1708 names: a filter matching nothing must still be an empty 200.
//
// A non-empty list is resolved in two passes, in the order the caller sent the
// ids, and fails on the first bad one. Shape first, over the whole list, then
// existence: see the file comment.
//
// `all` is the collection the describe has already read, and `idOf` reads one
// record's id out of it, so no operation needs a second store round trip to
// answer this.
func resolveIDs[T any](scope idScope, sel idSelection, all []T, idOf func(T) string) *protocol.AWSError {
	requested := sel.all()
	if len(requested) == 0 {
		return nil
	}
	if aerr := resolveIDShapes(scope, sel); aerr != nil {
		return aerr
	}
	known := make(map[string]bool, len(all))
	for _, res := range all {
		known[idOf(res)] = true
	}
	for _, id := range requested {
		if !known[id] {
			return scope.errNotFoundID(id)
		}
	}
	return nil
}

// ── The scopes ───────────────────────────────────────────────────────────────
//
// Every code and every message below is quoted from the EC2 API reference's
// client error table (errors-overview.html); the message wordings are the ones
// real EC2 answers with, cited per scope where a public capture was used
// instead of the reference's own prose.

// vpcIDScope — InvalidVpcID.NotFound / InvalidVpcID.Malformed. Both spell the
// resource "VpcID"; the message wording matches store.go's own errNotFound,
// which has answered "The vpc ID '…' does not exist" since before this change.
var vpcIDScope = newIDScope("vpc",
	"InvalidVpcID.NotFound", "The vpc ID '%s' does not exist",
	"InvalidVpcID.Malformed")

// subnetIDScope — InvalidSubnetID.NotFound (the reference lists
// `InvalidSubnetID.NotFound or InvalidSubnetId.NotFound`; the first spelling is
// the one AWS answers with, and the one reported in
// terraform-aws-modules/terraform-aws-ec2-instance#20: "The subnet ID
// 'subnet-ed4ddcb6' does not exist") / InvalidSubnetID.Malformed.
var subnetIDScope = newIDScope("subnet",
	"InvalidSubnetID.NotFound", "The subnet ID '%s' does not exist",
	"InvalidSubnetID.Malformed")

// securityGroupIDScope — InvalidGroup.NotFound / InvalidGroupId.Malformed.
// The pair AWS spells most inconsistently: the not-found code names no "Id" at
// all and the malformed one spells it "Id", in adjacent rows. Its message is
// the one place a resource is not called "<noun> ID" either.
var securityGroupIDScope = newIDScope("sg",
	"InvalidGroup.NotFound", "The security group '%s' does not exist",
	"InvalidGroupId.Malformed")

// routeTableIDScope — InvalidRouteTableID.NotFound / InvalidRouteTableId.Malformed.
// #1708's table left the malformed column blank for this resource; the
// reference does document one, spelled with a lowercase "d". The message
// matches store.go's getRouteTable, which already answers "The routeTable ID
// '…' does not exist".
var routeTableIDScope = newIDScope("rtb",
	"InvalidRouteTableID.NotFound", "The routeTable ID '%s' does not exist",
	"InvalidRouteTableId.Malformed")

// internetGatewayIDScope — InvalidInternetGatewayID.NotFound /
// InvalidInternetGatewayId.Malformed. Blank in #1708's table too, and
// documented, with the same ID/Id split as the route table. Message matches
// store.go's getInternetGateway.
var internetGatewayIDScope = newIDScope("igw",
	"InvalidInternetGatewayID.NotFound", "The internetGateway ID '%s' does not exist",
	"InvalidInternetGatewayId.Malformed")

// networkInterfaceIDScope — InvalidNetworkInterfaceID.NotFound /
// InvalidNetworkInterfaceId.Malformed. Third of the three #1708 left blank,
// and documented. The reference also carries a bare
// InvalidNetworkInterface.NotFound; the ID-suffixed code is the one whose
// description names the id parameter, and the one an id list answers with.
var networkInterfaceIDScope = newIDScope("eni",
	"InvalidNetworkInterfaceID.NotFound", "The networkInterface ID '%s' does not exist",
	"InvalidNetworkInterfaceId.Malformed")

// instanceIDScope — InvalidInstanceID.NotFound / InvalidInstanceID.Malformed.
// The reference's own example error response (errors-overview.html,
// "Example error response") is an InvalidInstanceID.NotFound, which is where
// the envelope this is written into comes from.
var instanceIDScope = newIDScope("i",
	"InvalidInstanceID.NotFound", "The instance ID '%s' does not exist",
	"InvalidInstanceID.Malformed")

// ── #1847's five describes ───────────────────────────────────────────────────

// allocationIDScope — InvalidAllocationID.NotFound, and no Malformed.
//
// The reference's row is worded for exactly this call: "The specified
// allocation ID you are trying to describe or associate does not exist". Its
// neighbours are about other things — InvalidAddressID.NotFound is the release
// wording (real EC2 answers InvalidAllocationID.NotFound there too, see
// store.go's getElasticIP), and InvalidAddress.Malformed is about the public
// address below, not about an allocation. There is no
// InvalidAllocationID.Malformed anywhere in the table, so this scope does not
// check shape and an `eipalloc-`-less id is reported as one that is not there.
// The message matches store.go's getElasticIP, which already answers it.
var allocationIDScope = newIDScope("eipalloc",
	"InvalidAllocationID.NotFound", "The allocation ID '%s' does not exist",
	"")

// publicIPv4 is the shape AWS's InvalidAddress.Malformed row asks for: "the
// address in the form xx.xx.xx.xx; for example, 55.123.45.67". Dotted quad,
// each octet 0-255, which is what makes "203.0.113.999" malformed rather than
// merely absent.
var publicIPv4 = regexp.MustCompile(`^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])$`)

// publicIPScope — InvalidAddress.NotFound / InvalidAddress.Malformed, the
// second selector DescribeAddresses takes.
//
// "The specified Elastic IP address that you are describing cannot be found"
// is the reference's own wording for the first, and "describing" is the only
// operation it names. The not-found message is not this file's usual "The
// <noun> ID '…' does not exist", because addresses are the one thing EC2 does
// not call an ID: getmoto/moto's tests/test_ec2/test_elastic_ip_addresses.py
// asserts `Address '<ip>' not found.` against a released address, which is the
// shape a re-implementation would only have written down by copying AWS.
//
// Moto answers that same code for `256.256.256.256` rather than a Malformed —
// but moto/ec2/exceptions.py declares no Malformed class for addresses at all,
// so that is its own coverage rather than evidence about AWS. The reference
// does document InvalidAddress.Malformed, and its wording ("Ensure that you
// provide the address in the form xx.xx.xx.xx; for example, 55.123.45.67") is
// address-shape validation, which AWS does at request parsing — ahead of any
// lookup, for every operation taking the parameter. So the shape is checked,
// and errMalformedID's usual sentence is kept with only its "expecting" half
// changed, because "eipalloc-..." would be nonsense advice for an address.
var publicIPScope = newShapedScope(publicIPv4, "xx.xx.xx.xx",
	"InvalidAddress.NotFound", "Address '%s' not found.",
	"InvalidAddress.Malformed")

// natGatewayIDScope — NatGatewayNotFound / NatGatewayMalformed. The one
// resource whose two codes carry neither the "Invalid" prefix nor a dot; they
// are copied as they are spelled, like every other pair here. The message
// matches store.go's getNatGateway, which already answers NatGatewayNotFound.
var natGatewayIDScope = newIDScope("nat",
	"NatGatewayNotFound", "The natGateway ID '%s' does not exist",
	"NatGatewayMalformed")

// vpnGatewayIDScope — InvalidVpnGatewayID.NotFound, and no Malformed. The
// table's only other vpn-gateway rows are InvalidVpnGatewayAttachment.NotFound
// and InvalidVpnGatewayState, neither of which is about the shape of an id, so
// this scope does not check shape either. Message matches store.go's
// getVpnGateway.
var vpnGatewayIDScope = newIDScope("vgw",
	"InvalidVpnGatewayID.NotFound", "The vpnGateway ID '%s' does not exist",
	"")

// vpcEndpointIDScope — InvalidVpcEndpointId.NotFound /
// InvalidVpcEndpointId.Malformed. The table also carries a bare
// InvalidVpcEndpoint.NotFound with the same description; the Id-suffixed pair
// is the one whose Malformed sibling names the `vpce-` parameter, so a
// describe selecting on VpcEndpointId.N answers with it.
var vpcEndpointIDScope = newIDScope("vpce",
	"InvalidVpcEndpointId.NotFound", "The vpcEndpoint ID '%s' does not exist",
	"InvalidVpcEndpointId.Malformed")

// vpcPeeringIDScope — InvalidVpcPeeringConnectionID.NotFound /
// InvalidVpcPeeringConnectionId.Malformed. Another ID/Id split inside one
// resource, and one the reference itself is inconsistent about: the not-found
// row's anchor is spelled "…ConnectionId.NotFound" while the cell that renders
// is "…ConnectionID.NotFound". The rendered spelling is the one real EC2 sends
// and the one store.go's getVpcPeeringConnection has always answered, so it is
// the one kept. Message matches that store path.
var vpcPeeringIDScope = newIDScope("pcx",
	"InvalidVpcPeeringConnectionID.NotFound", "The vpcPeeringConnection ID '%s' does not exist",
	"InvalidVpcPeeringConnectionId.Malformed")

// describeIDScopes is which operation resolves an explicit id list, and with
// which scopes. The handlers name their scope directly — a map lookup keyed by
// a string is one typo away from a nil regexp — so this exists as the audit:
// "which describes enforce this" is a question answered here rather than by
// grepping a dozen files.
//
// The value is a list because one operation has two selectors that are not the
// same resource: DescribeAddresses takes AllocationId.N and PublicIp.N and
// answers them with different codes. Everything else has exactly one.
//
// It is what the dev-tagged checks are driven from, so an operation added to
// this map without a parity row naming an unknown and a malformed id, or
// without capability notes saying what it raises, fails the build rather than
// being noticed in review.
var describeIDScopes = map[string][]idScope{
	"DescribeVpcs":                  {vpcIDScope},
	"DescribeSubnets":               {subnetIDScope},
	"DescribeSecurityGroups":        {securityGroupIDScope},
	"DescribeRouteTables":           {routeTableIDScope},
	"DescribeInternetGateways":      {internetGatewayIDScope},
	"DescribeNetworkInterfaces":     {networkInterfaceIDScope},
	"DescribeInstances":             {instanceIDScope},
	"DescribeAddresses":             {allocationIDScope, publicIPScope},
	"DescribeNatGateways":           {natGatewayIDScope},
	"DescribeVpnGateways":           {vpnGatewayIDScope},
	"DescribeVpcEndpoints":          {vpcEndpointIDScope},
	"DescribeVpcPeeringConnections": {vpcPeeringIDScope},
}
