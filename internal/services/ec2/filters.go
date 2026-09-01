package ec2

// filters.go — one answer, for every EC2 describe, to "what does this filter
// mean?".
//
// A Describe* is handed Filter.N.Name / Filter.N.Value.M and has to decide three
// things: which names it understands, how a name it understands is matched, and
// what a name it does not understand means. EC2 answered the third differently
// depending on which handler you asked. Three idioms had grown up in one
// package:
//
//   - parseFilterValues (vpcs, subnets, security groups, route tables, internet
//     gateways, VPC endpoints, peering connections) looked filters up by name
//     and never saw the rest, so an unrecognised filter was ignored and the call
//     returned everything.
//   - matchFilters (NAT gateways, network interfaces) compared every supplied
//     filter against an attribute map and returned false on a name it did not
//     know, so an unrecognised filter returned nothing.
//   - vpnGatewayMatchesFilters switched on the name with no default, ignoring
//     what it did not recognise — parseFilterValues' answer, written a third
//     time.
//
// Two of those are silently wrong in opposite directions, so a caller could not
// even work around them consistently. Both are worse than they look: the
// reported bug (#1032) was a find-or-create script whose `tag:Name` filter was
// ignored, so DescribeVpcs answered with every VPC in the region, the script
// read Vpcs[0], adopted the seeded default VPC and never created the private one
// it was written to use. Hours went into suspecting CDK. The emulator was asked
// a question it could not answer, and answered anyway.
//
// The rule now, everywhere: a filter name an operation does not implement is
// refused with AWS's InvalidParameterValue, before the collection is read. That
// is what real AWS does with a name it does not model, and for a name AWS models
// that Overcast has not implemented it is a deliberate, louder divergence — a
// caller told "this filter is not supported" loses a minute, a caller silently
// given the wrong resources loses an afternoon. The alternatives were weighed on
// the reported bug: `tag:Name` is a filter AWS models, so "ignore the ones AWS
// models, refuse only typos" would have left that bug exactly as it was.
//
// filterSpec is where an operation declares the names it implements. The same
// declaration matches the filter, writes the error message, and is what the
// operation's capability note is held to — so what Overcast claims and what it
// does cannot drift apart. See TestCapabilityNotesNameTheFiltersOnly.
//
// Names and values are held to opposite standards, deliberately. A *name* is
// matched exactly, because AWS matches it exactly and because a name is
// something the caller either knows or does not. A *value* is a pattern: AWS
// reads `*` as any run of characters and `?` as exactly one, so `tag:Name` of
// `overcast-*` selects what a caller means by it. See filterValue.

import (
	"fmt"
	"iter"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// ── The contract ─────────────────────────────────────────────────────────────

// filterSpec is a Describe*'s filter contract: the filter names it implements,
// and how each one is read off a resource.
type filterSpec[T any] struct {
	// op is the AWS operation name. It appears in the error message and keys
	// the supported-filter set the capability notes are checked against.
	op string

	// tagged reports whether the operation matches tag selectors against the
	// resource's tags, through tagView. When it does not, `tag:<key>`,
	// `tag-key` and `tag-value` are unrecognised names like any other:
	// accepting a tag filter and then ignoring it is the reported bug.
	tagged bool

	// attrs maps each implemented filter name to the accessor that answers it.
	attrs map[string]filterAttr[T]
}

// filterAttr answers one filter name for one resource.
type filterAttr[T any] struct {
	// values returns the resource's values for this filter. The filter matches
	// when any of them is one of the caller's values.
	values func(T) []string

	// fold compares values case-insensitively. AWS spells boolean filter
	// values "true" and "false"; EC2 accepted either casing before the idioms
	// were converged, and there is no reason to start refusing "True".
	// Identifiers, names and tag values stay case-sensitive.
	fold bool
}

// attr declares a filter answered by a single field.
func attr[T any](get func(T) string) filterAttr[T] {
	return filterAttr[T]{values: func(res T) []string { return []string{get(res)} }}
}

// listAttr declares a filter answered by several values on one resource — the
// VPC IDs of a gateway's attachments, say — any of which may match.
func listAttr[T any](get func(T) []string) filterAttr[T] {
	return filterAttr[T]{values: get}
}

// boolAttr declares a boolean filter, spelled the way AWS spells it.
func boolAttr[T any](get func(T) bool) filterAttr[T] {
	return folded(attr(func(res T) string { return strconv.FormatBool(get(res)) }))
}

// folded makes a filter compare its values case-insensitively. It is what
// boolAttr is built from, and what a boolean read off a list of sub-records
// needs so that `association.main=True` matches wherever `isDefault=True` does.
func folded[T any](a filterAttr[T]) filterAttr[T] {
	a.fold = true
	return a
}

// intAttr declares a filter answered by an integer field.
func intAttr[T any](get func(T) int64) filterAttr[T] {
	return attr(func(res T) string { return strconv.FormatInt(get(res), 10) })
}

// supported names every filter the operation implements, in a stable order:
// its own names sorted, then the tag selectors it shares with every other
// taggable describe.
func (spec filterSpec[T]) supported() []string {
	names := slices.Sorted(maps.Keys(spec.attrs))
	if spec.tagged {
		names = append(names, tagFilterPrefix+"<key>", tagKeyFilter, tagValueFilter)
	}
	return names
}

// invalidFilter is AWS's answer to a filter an operation does not accept.
//
// AWS's sentence comes first and unaltered, so a client matching on it still
// works. What follows is Overcast's own: AWS refuses a name because it does not
// model it, while Overcast may also be refusing one AWS does model but Overcast
// has not implemented, and nothing in the AWS message tells those apart.
func (spec filterSpec[T]) invalidFilter(name string) *protocol.AWSError {
	return ec2err("InvalidParameterValue", fmt.Sprintf(
		"The filter '%s' is invalid. Overcast implements these %s filters: %s",
		name, spec.op, filterList(spec.supported())),
		http.StatusBadRequest)
}

// filterList renders a supported-filter set for a human, naming the empty set
// rather than trailing off after the colon.
func filterList(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// ── The request side ─────────────────────────────────────────────────────────

// filterQuery is a request's filters, validated against a filterSpec and
// resolved to the accessors that answer them — once per request rather than
// once per resource.
type filterQuery[T any] struct {
	terms []filterTerm[T]
}

// filterTerm is one Filter.N: an accessor, and the values the caller accepts
// from it.
type filterTerm[T any] struct {
	attr   filterAttr[T]
	values []filterValue
}

// filterValue is one Filter.N.Value.M.
//
// AWS treats a filter value as a pattern, not a literal: `*` stands for any run
// of characters and `?` for exactly one, and a backslash escapes either. Almost
// every value is still a plain string, so which kind this is gets decided once
// per request — the per-resource comparison of a literal stays a string
// equality rather than a walk.
type filterValue struct {
	pattern  string
	wildcard bool
}

// parseFilterValue classifies a caller's value.
//
// A backslash counts as making it a pattern even with no wildcard beside it,
// because the escape still has to be stripped before anything can match — and
// stripping it is what wildcardMatch does.
func parseFilterValue(v string) filterValue {
	return filterValue{pattern: v, wildcard: strings.ContainsAny(v, `*?\`)}
}

// parseFilterValues classifies every value of one filter.
func parseFilterValues(values []string) []filterValue {
	out := make([]filterValue, 0, len(values))
	for _, v := range values {
		out = append(out, parseFilterValue(v))
	}
	return out
}

// matchesAny reports whether have satisfies any of the caller's values — the OR
// inside a single filter. It is the one place a filter value is compared, for
// the resource's own attributes and for its tags alike.
func matchesAny(values []filterValue, have string, fold bool) bool {
	return slices.ContainsFunc(values, func(fv filterValue) bool {
		return fv.matches(have, fold)
	})
}

// matches reports whether a resource's value satisfies this one.
func (fv filterValue) matches(have string, fold bool) bool {
	if fv.wildcard {
		if fold {
			return wildcardMatch(strings.ToLower(fv.pattern), strings.ToLower(have))
		}
		return wildcardMatch(fv.pattern, have)
	}
	if fold {
		return strings.EqualFold(have, fv.pattern)
	}
	return have == fv.pattern
}

// wildcardMatch reports whether value satisfies an AWS filter pattern, in which
// `*` stands for any run of characters (including none), `?` for exactly one,
// and `\` escapes the next character so a caller can ask for a literal `*`,
// `?` or `\`.
//
// This is not path.Match: that reads `[a-z]` as a character class and refuses
// to let `*` cross a `/`, neither of which is true of an EC2 filter — a value
// is an opaque string, and `com.amazonaws.*.s3` has to match a service name.
// The scan backtracks to the last `*` rather than recursing, so a pattern of
// many stars cannot turn a describe into an exponential walk.
func wildcardMatch(pattern, value string) bool {
	pat, val := []rune(pattern), []rune(value)
	// star is where the most recent `*` sits, and mark how much of the value it
	// has absorbed so far.
	p, v, star, mark := 0, 0, -1, 0
	for v < len(val) {
		switch {
		case p+1 < len(pat) && pat[p] == '\\':
			if val[v] == pat[p+1] {
				p, v = p+2, v+1
				continue
			}
		case p < len(pat) && (pat[p] == '?' || pat[p] == val[v]):
			p, v = p+1, v+1
			continue
		case p < len(pat) && pat[p] == '*':
			star, mark = p, v
			p++
			continue
		}
		if star < 0 {
			return false
		}
		// Give the last `*` one more character and try again from there.
		p, mark = star+1, mark+1
		v = mark
	}
	// Only trailing stars can still match, having nothing left to consume.
	for p < len(pat) && pat[p] == '*' {
		p++
	}
	return p == len(pat)
}

// parse reads a request's filters and refuses any name the operation does not
// implement.
//
// Names are matched exactly, as AWS matches them: `VPC-ID` is not `vpc-id` and
// real EC2 refuses it. Values are the lenient half — see filterValue.
//
// Handlers call this before reading their collection, so an unrecognised filter
// is refused whether or not the region holds anything to filter. The empty case
// is where an ignored filter is most dangerous: "no match" and "I did not
// understand you" are indistinguishable in an empty response.
//
// It takes a filterSeq rather than the request so that the same rule answers a
// request decoded either way — see filterSeq.
func (spec filterSpec[T]) parse(filters filterSeq) (filterQuery[T], *protocol.AWSError) {
	var q filterQuery[T]
	for name, values := range filters {
		if spec.tagged && isTagFilter(name) {
			continue // matched against the resource's tags by tagFilters
		}
		a, ok := spec.attrs[name]
		if !ok {
			return filterQuery[T]{}, spec.invalidFilter(name)
		}
		q.terms = append(q.terms, filterTerm[T]{attr: a, values: parseFilterValues(values)})
	}
	return q, nil
}

// matches reports whether a resource passes every filter.
//
// Filters are AND-ed with each other and the values within one are OR-ed, which
// is AWS's rule. A filter carrying no values therefore matches nothing: the
// caller asked for a value drawn from an empty set.
func (q filterQuery[T]) matches(res T) bool {
	for _, term := range q.terms {
		if !slices.ContainsFunc(term.attr.values(res), func(have string) bool {
			return matchesAny(term.values, have, term.attr.fold)
		}) {
			return false
		}
	}
	return true
}

// idSelection is a describe's `<Resource>Id.N` parameters — the other, older
// way a caller names what it wants, which every describe combines with its
// filters.
//
// An empty selection matches everything, which is AWS's rule and the reason
// this is a type rather than a bare set lookup: every describe spelled out
// `len(ids) > 0 && !ids[id]` for itself, and one of them getting it wrong would
// have hidden every resource in the region.
type idSelection map[string]bool

// requestedIDs reads `<param>.1`, `<param>.2`, … from the request.
func requestedIDs(r *http.Request, param string) idSelection {
	return selectedIDs(parseIndexedParam(r, param))
}

// selectedIDs turns an already-decoded ID list into a selection.
//
// It stops at the first empty entry, which is what parseIndexedParam does to a
// gap in `<param>.N` and therefore what the two decode paths have to agree on:
// the Query codec fills a gap with the zero string rather than truncating, and
// an ID a caller never sent must not become a selection of "".
func selectedIDs(values []string) idSelection {
	sel := idSelection{}
	for _, v := range values {
		if v == "" {
			break
		}
		sel[v] = true
	}
	return sel
}

// has reports whether a resource was asked for. An empty selection asks for all
// of them.
func (sel idSelection) has(id string) bool {
	return len(sel) == 0 || sel[id]
}

// ── The two ways a filter reaches a handler ──────────────────────────────────
//
// A Describe* is reached two ways: through the legacy http.HandlerFunc, which
// reads Filter.N.Name / Filter.N.Value.M straight off the form, and through the
// typed operation registry, which is handed a []ec2Filter the Query codec
// already decoded. filterSeq is the seam between them, so filterSpec.parse and
// parseTagFilters are written once and answer both identically — the property
// #754 turns on, since the legacy path's filter rules (#1032) must survive the
// switch byte for byte.

// filterSeq yields every Filter.N.Name and its Filter.N.Value.M values, in
// index order.
//
// Index order is not cosmetic: it is what makes the filter a request is refused
// for the same one AWS would name, rather than whichever a map iteration
// happened to reach first. Both producers below preserve it.
type filterSeq = iter.Seq2[string, []string]

// ec2Filter is one Filter.N of a typed request, in the shape the Query codec
// decodes `Filter.1.Name` / `Filter.1.Value.1` into.
type ec2Filter struct {
	Name   string   `json:"Name"`
	Values []string `json:"Value"`
}

// eachFilter yields a request's filters straight off the form, in a single
// pass.
func eachFilter(r *http.Request) filterSeq {
	return func(yield func(string, []string) bool) {
		for i := 1; ; i++ {
			name := r.FormValue(fmt.Sprintf("Filter.%d.Name", i))
			if name == "" {
				return
			}
			var values []string
			for j := 1; ; j++ {
				v, ok := r.Form[fmt.Sprintf("Filter.%d.Value.%d", i, j)]
				if !ok || len(v) == 0 {
					break
				}
				values = append(values, v[0])
			}
			if !yield(name, values) {
				return
			}
		}
	}
}

// eachDecodedFilter yields a typed request's already-decoded filters.
//
// It stops at the first unnamed filter for the same reason eachFilter stops at
// the first absent Filter.N.Name: the codec renders a gap in the index as a
// zero-valued element, and a filter the caller never sent must not be read as a
// filter named "" and refused as unrecognised.
func eachDecodedFilter(filters []ec2Filter) filterSeq {
	return func(yield func(string, []string) bool) {
		for _, f := range filters {
			if f.Name == "" {
				return
			}
			if !yield(f.Name, f.Values) {
				return
			}
		}
	}
}

// ── The per-operation sets ───────────────────────────────────────────────────
//
// Each declaration below is the whole of what its operation implements: what it
// matches on, what it refuses, and what its capability note advertises. AWS
// models more filters than these for every one of them; a name Overcast cannot
// answer off the record it holds is refused rather than accepted and ignored.

// supportedFilters names, per operation, the filters that operation implements.
// declareFilters is its only writer, so an operation cannot advertise a filter
// its handler does not apply, and the capability tables are checked against it.
var supportedFilters = map[string][]string{}

// declareFilters records a spec's filter names and returns the spec, so one
// declaration serves the handler, the error message and the documentation.
func declareFilters[T any](spec filterSpec[T]) filterSpec[T] {
	supportedFilters[spec.op] = spec.supported()
	return spec
}

var vpcFilters = declareFilters(filterSpec[*VPC]{
	op:     "DescribeVpcs",
	tagged: true,
	attrs: map[string]filterAttr[*VPC]{
		"cidr":      attr(func(v *VPC) string { return v.CidrBlock }),
		"isDefault": boolAttr(func(v *VPC) bool { return v.IsDefault }),
		"state":     attr(func(v *VPC) string { return v.State }),
		"vpc-id":    attr(func(v *VPC) string { return v.VpcID }),
	},
})

var subnetFilters = declareFilters(filterSpec[*Subnet]{
	op:     "DescribeSubnets",
	tagged: true,
	attrs: map[string]filterAttr[*Subnet]{
		"availability-zone": attr(func(s *Subnet) string { return s.AvailabilityZone }),
		"cidr-block":        attr(func(s *Subnet) string { return s.CidrBlock }),
		"state":             attr(func(s *Subnet) string { return s.State }),
		"subnet-id":         attr(func(s *Subnet) string { return s.SubnetID }),
		"vpc-id":            attr(func(s *Subnet) string { return s.VpcID }),
	},
})

var securityGroupFilters = declareFilters(filterSpec[*SecurityGroup]{
	op:     "DescribeSecurityGroups",
	tagged: true,
	attrs: map[string]filterAttr[*SecurityGroup]{
		"description": attr(func(sg *SecurityGroup) string { return sg.Description }),
		"group-id":    attr(func(sg *SecurityGroup) string { return sg.GroupID }),
		"group-name":  attr(func(sg *SecurityGroup) string { return sg.GroupName }),
		"vpc-id":      attr(func(sg *SecurityGroup) string { return sg.VpcID }),
	},
})

var routeTableFilters = declareFilters(filterSpec[*RouteTable]{
	op:     "DescribeRouteTables",
	tagged: true,
	attrs: map[string]filterAttr[*RouteTable]{
		"association.main": folded(listAttr(func(rt *RouteTable) []string {
			return mapped(rt.Associations, func(a RouteTableAssociation) string {
				return strconv.FormatBool(a.Main)
			})
		})),
		"association.route-table-association-id": listAttr(func(rt *RouteTable) []string {
			return mapped(rt.Associations, func(a RouteTableAssociation) string { return a.AssociationID })
		}),
		"association.subnet-id": listAttr(func(rt *RouteTable) []string {
			return mapped(rt.Associations, func(a RouteTableAssociation) string { return a.SubnetID })
		}),
		"route-table-id": attr(func(rt *RouteTable) string { return rt.RouteTableID }),
		"vpc-id":         attr(func(rt *RouteTable) string { return rt.VpcID }),
	},
})

var internetGatewayFilters = declareFilters(filterSpec[*InternetGateway]{
	op:     "DescribeInternetGateways",
	tagged: true,
	attrs: map[string]filterAttr[*InternetGateway]{
		"attachment.state": listAttr(func(igw *InternetGateway) []string {
			return mapped(igw.Attachments, func(a IGWAttachment) string { return a.State })
		}),
		"attachment.vpc-id": listAttr(func(igw *InternetGateway) []string {
			return mapped(igw.Attachments, func(a IGWAttachment) string { return a.VpcID })
		}),
		"internet-gateway-id": attr(func(igw *InternetGateway) string { return igw.InternetGatewayID }),
	},
})

var natGatewayFilters = declareFilters(filterSpec[*NatGateway]{
	op:     "DescribeNatGateways",
	tagged: true,
	attrs: map[string]filterAttr[*NatGateway]{
		"nat-gateway-id": attr(func(ngw *NatGateway) string { return ngw.NatGatewayID }),
		"state":          attr(func(ngw *NatGateway) string { return ngw.State }),
		"subnet-id":      attr(func(ngw *NatGateway) string { return ngw.SubnetID }),
		"vpc-id":         attr(func(ngw *NatGateway) string { return ngw.VpcID }),
	},
})

var networkInterfaceFilters = declareFilters(filterSpec[*NetworkInterface]{
	op:     "DescribeNetworkInterfaces",
	tagged: true,
	attrs: map[string]filterAttr[*NetworkInterface]{
		"availability-zone":    attr(func(eni *NetworkInterface) string { return eni.AvailabilityZone }),
		"description":          attr(func(eni *NetworkInterface) string { return eni.Description }),
		"mac-address":          attr(func(eni *NetworkInterface) string { return eni.MacAddress }),
		"network-interface-id": attr(func(eni *NetworkInterface) string { return eni.NetworkInterfaceID }),
		"status":               attr(func(eni *NetworkInterface) string { return eni.Status }),
		"subnet-id":            attr(func(eni *NetworkInterface) string { return eni.SubnetID }),
		"vpc-id":               attr(func(eni *NetworkInterface) string { return eni.VpcID }),
	},
})

var vpnGatewayFilters = declareFilters(filterSpec[*VpnGateway]{
	op:     "DescribeVpnGateways",
	tagged: true,
	attrs: map[string]filterAttr[*VpnGateway]{
		"amazon-side-asn":   intAttr(func(vgw *VpnGateway) int64 { return vgw.AmazonSideAsn }),
		"availability-zone": attr(func(vgw *VpnGateway) string { return vgw.AvailabilityZone }),
		"attachment.state": listAttr(func(vgw *VpnGateway) []string {
			return mapped(vgw.Attachments, func(a VpnGatewayAttachment) string { return a.State })
		}),
		"attachment.vpc-id": listAttr(func(vgw *VpnGateway) []string {
			return mapped(vgw.Attachments, func(a VpnGatewayAttachment) string { return a.VpcID })
		}),
		"state":          attr(func(vgw *VpnGateway) string { return vgw.State }),
		"type":           attr(func(vgw *VpnGateway) string { return vgw.Type }),
		"vpn-gateway-id": attr(func(vgw *VpnGateway) string { return vgw.VpnGatewayID }),
	},
})

var instanceFilters = declareFilters(filterSpec[*Instance]{
	op:     "DescribeInstances",
	tagged: true,
	attrs: map[string]filterAttr[*Instance]{
		"availability-zone":           attr(func(i *Instance) string { return i.Placement.AvailabilityZone }),
		"image-id":                    attr(func(i *Instance) string { return i.ImageID }),
		"instance-id":                 attr(func(i *Instance) string { return i.InstanceID }),
		"instance-state-code":         intAttr(func(i *Instance) int64 { return int64(i.State.Code) }),
		"instance-state-name":         attr(func(i *Instance) string { return i.State.Name }),
		"instance-type":               attr(func(i *Instance) string { return i.InstanceType }),
		"placement.availability-zone": attr(func(i *Instance) string { return i.Placement.AvailabilityZone }),
		"subnet-id":                   attr(func(i *Instance) string { return i.SubnetID }),
		"vpc-id":                      attr(func(i *Instance) string { return i.VpcID }),
	},
})

var vpcEndpointFilters = declareFilters(filterSpec[*VpcEndpoint]{
	op: "DescribeVpcEndpoints",
	attrs: map[string]filterAttr[*VpcEndpoint]{
		"service-name":       attr(func(ep *VpcEndpoint) string { return ep.ServiceName }),
		"vpc-endpoint-id":    attr(func(ep *VpcEndpoint) string { return ep.VpcEndpointID }),
		"vpc-endpoint-state": attr(func(ep *VpcEndpoint) string { return ep.State }),
		"vpc-endpoint-type":  attr(func(ep *VpcEndpoint) string { return ep.VpcEndpointType }),
		"vpc-id":             attr(func(ep *VpcEndpoint) string { return ep.VpcID }),
	},
})

var vpcPeeringFilters = declareFilters(filterSpec[*VpcPeeringConnection]{
	op: "DescribeVpcPeeringConnections",
	attrs: map[string]filterAttr[*VpcPeeringConnection]{
		"accepter-vpc-info.vpc-id":  attr(func(p *VpcPeeringConnection) string { return p.AccepterVpcInfo.VpcID }),
		"requester-vpc-info.vpc-id": attr(func(p *VpcPeeringConnection) string { return p.RequesterVpcInfo.VpcID }),
		"status-code":               attr(func(p *VpcPeeringConnection) string { return p.Status.Code }),
		"vpc-peering-connection-id": attr(func(p *VpcPeeringConnection) string { return p.VpcPeeringConnectionID }),
	},
})

var addressFilters = declareFilters(filterSpec[*ElasticIP]{
	op: "DescribeAddresses",
	attrs: map[string]filterAttr[*ElasticIP]{
		"allocation-id":        attr(func(a *ElasticIP) string { return a.AllocationID }),
		"association-id":       attr(func(a *ElasticIP) string { return a.AssociationID }),
		"domain":               attr(func(a *ElasticIP) string { return a.Domain }),
		"instance-id":          attr(func(a *ElasticIP) string { return a.InstanceID }),
		"network-interface-id": attr(func(a *ElasticIP) string { return a.NetworkInterfaceID }),
		"private-ip-address":   attr(func(a *ElasticIP) string { return a.PrivateIP }),
		"public-ip":            attr(func(a *ElasticIP) string { return a.PublicIP }),
	},
})

var keyPairFilters = declareFilters(filterSpec[*KeyPair]{
	op: "DescribeKeyPairs",
	attrs: map[string]filterAttr[*KeyPair]{
		"fingerprint": attr(func(kp *KeyPair) string { return kp.KeyFingerprint }),
		"key-name":    attr(func(kp *KeyPair) string { return kp.KeyName }),
		"key-pair-id": attr(func(kp *KeyPair) string { return kp.KeyPairID }),
	},
})

var imageFilters = declareFilters(filterSpec[xmlImage]{
	op: "DescribeImages",
	attrs: map[string]filterAttr[xmlImage]{
		"architecture":        attr(func(i xmlImage) string { return i.Architecture }),
		"description":         attr(func(i xmlImage) string { return i.Description }),
		"image-id":            attr(func(i xmlImage) string { return i.ImageID }),
		"image-type":          attr(func(i xmlImage) string { return i.ImageType }),
		"is-public":           boolAttr(func(i xmlImage) bool { return i.IsPublic }),
		"name":                attr(func(i xmlImage) string { return i.Name }),
		"owner-id":            attr(func(i xmlImage) string { return i.OwnerID }),
		"root-device-type":    attr(func(i xmlImage) string { return i.RootDeviceType }),
		"state":               attr(func(i xmlImage) string { return i.ImageState }),
		"virtualization-type": attr(func(i xmlImage) string { return i.VirtualizationType }),
	},
})

// tagItemFilters selects tags rather than the resources carrying them, so its
// names describe a tag: DescribeTags is the one describe whose filters are not
// about a resource's own attributes.
var tagItemFilters = declareFilters(filterSpec[xmlTagItem]{
	op: "DescribeTags",
	attrs: map[string]filterAttr[xmlTagItem]{
		"key":           attr(func(t xmlTagItem) string { return t.Key }),
		"resource-id":   attr(func(t xmlTagItem) string { return t.ResourceID }),
		"resource-type": attr(func(t xmlTagItem) string { return t.ResourceType }),
		"value":         attr(func(t xmlTagItem) string { return t.Value }),
	},
})

var regionFilters = declareFilters(filterSpec[xmlRegion]{
	op: "DescribeRegions",
	attrs: map[string]filterAttr[xmlRegion]{
		"endpoint":      attr(func(r xmlRegion) string { return r.RegionEndpoint }),
		"opt-in-status": attr(func(r xmlRegion) string { return r.OptInStatus }),
		"region-name":   attr(func(r xmlRegion) string { return r.RegionName }),
	},
})

var azFilters = declareFilters(filterSpec[xmlAZ]{
	op: "DescribeAvailabilityZones",
	attrs: map[string]filterAttr[xmlAZ]{
		"region-name": attr(func(z xmlAZ) string { return z.RegionName }),
		"state":       attr(func(z xmlAZ) string { return z.ZoneState }),
		"zone-name":   attr(func(z xmlAZ) string { return z.ZoneName }),
	},
})

var instanceTypeFilters = declareFilters(filterSpec[xmlInstanceTypeItem]{
	op: "DescribeInstanceTypes",
	attrs: map[string]filterAttr[xmlInstanceTypeItem]{
		"current-generation":      boolAttr(func(it xmlInstanceTypeItem) bool { return it.CurrentGeneration }),
		"instance-type":           attr(func(it xmlInstanceTypeItem) string { return it.InstanceType }),
		"memory-info.size-in-mib": intAttr(func(it xmlInstanceTypeItem) int64 { return int64(it.MemoryInfo.SizeInMiB) }),
		"vcpu-info.default-vcpus": intAttr(func(it xmlInstanceTypeItem) int64 { return int64(it.VCpuInfo.DefaultVCpus) }),
	},
})

// dhcpOptionsFilters implements nothing, deliberately. DescribeDhcpOptions
// fabricates an option set per call rather than storing one, so there is no
// record for a filter to select against and every AWS filter name here would be
// a name Overcast cannot answer.
var dhcpOptionsFilters = declareFilters(filterSpec[xmlDhcpOption]{
	op: "DescribeDhcpOptions",
})

// ── Helpers ──────────────────────────────────────────────────────────────────

// mapped projects a field out of a resource's sub-records, which is how the
// `attachment.*` and `association.*` filters read a list of attachments.
func mapped[E any](items []E, get func(E) string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, get(item))
	}
	return out
}
