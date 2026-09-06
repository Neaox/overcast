package elbv2

// describe_identifiers.go — what a Describe* does with an explicit ARN or Name
// list, as opposed to describing everything the region holds.
//
// ELBv2 has two selectors and treats them as opposites, which is the
// distinction #1718 was filed about, and the same one #1708 named for EC2. A
// Describe that names *no* identifier is a listing: "none of them" is a
// legitimate empty 200. A Describe that names an ARN — or a Name, which ELBv2
// accepts in the same position — asserts the resource exists, and it is the
// exception, not the list length, that carries the answer. Terraform's and
// CloudFormation's refresh logic reads LoadBalancerNotFound /
// TargetGroupNotFound / ListenerNotFound to mean "gone, drop it from state";
// handed a 200 with an empty list instead, a provider that only inspects the
// exception concludes the resource is fine and then indexes into an empty list.
//
// Overcast applied the listing rule to both, because identifierFilter.matches
// treats an ARN the region does not hold exactly the way it treats a resource
// no identifier selected. This file is the other half of that type: the
// selection is now also *resolved*, once, in one place.
//
// Shape is checked before existence, and across the whole ARN list before any
// of it is resolved, because that is where AWS checks it: a malformed value
// fails request validation ahead of any lookup, so a request naming an
// unknown-but-well-formed ARN *and* a malformed one answers ValidationError.
//
// This deliberately stays local to elbv2 rather than reaching for the EC2
// helper of the same shape (internal/services/ec2/describe_ids.go, PR #1846).
// The two services agree on the rule and on nothing else: EC2 resolves bare
// resource IDs against a per-resource prefix and answers a different
// Invalid*.NotFound / .Malformed pair per operation, while ELBv2 resolves ARNs
// and names against three codes and one ValidationError.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// identifierScope is one resource type's explicit-identifier contract: the ARN
// segment that names it, the code AWS answers for one that does not resolve,
// and the two nouns its messages use.
//
// The nouns are fields rather than something derived from the segment because
// AWS's own wording is not derivable from it: the not-found message names the
// resource in the plural ("Load balancers '[…]' not found"), the validation
// message in the singular ("… is not a valid load balancer ARN"), and neither
// spells the resource the way the ARN segment does.
type identifierScope struct {
	// segment is the ARN's resource segment — "loadbalancer", "targetgroup",
	// "listener".
	segment string

	// notFound is the code for an identifier the region does not hold.
	notFound string

	// plural names the resource in notFound's message.
	plural string

	// singular names the resource in a malformed ARN's message.
	singular string

	// shape matches the ARNs that name this resource, built by
	// newIdentifierScope.
	shape *regexp.Regexp
}

// newIdentifierScope compiles the ARN shape this resource answers to.
//
// The match is deliberately loose after the resource segment. What AWS rejects
// with a ValidationError is a value that is not an ARN for this resource at
// all — a name passed where an ARN belongs, a truncated ARN, a target group
// ARN handed to DescribeLoadBalancers. A syntactically fine ARN whose trailing
// id names nothing is a *not-found*, not a validation error, so pinning the
// id's own shape here would answer the wrong code for an ARN minted by an
// older Overcast, by another emulator, or by a real account.
func newIdentifierScope(segment, notFound, plural, singular string) identifierScope {
	return identifierScope{
		segment:  segment,
		notFound: notFound,
		plural:   plural,
		singular: singular,
		shape: regexp.MustCompile(`^arn:[^:]*:elasticloadbalancing:[^:]*:[^:]*:` +
			regexp.QuoteMeta(segment) + `/.+`),
	}
}

// errNotFound is the answer to an identifier — an ARN or a Name — that is
// shaped right and is not there.
//
// The bracketed list is AWS's own wording: it reports the identifiers it could
// not resolve as a set, e.g. "Load balancers '[arn:aws:elasticloadbalancing:…]'
// not found". HTTP 400, as every documented ELBv2 error is; the API reference
// gives LoadBalancerNotFound, TargetGroupNotFound and ListenerNotFound a 400
// each.
// https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeLoadBalancers.html
func (s identifierScope) errNotFound(identifier string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       s.notFound,
		Message:    fmt.Sprintf("%s '[%s]' not found", s.plural, identifier),
		HTTPStatus: http.StatusBadRequest,
	}
}

// errMalformedARN is the answer to a value that is not an ARN for this
// resource. AWS keeps this apart from not-found so a typo, a truncated value or
// an ARN for the wrong resource fails as a bad request rather than reading as
// "it has been deleted".
func (s identifierScope) errMalformedARN(value string) *protocol.AWSError {
	return errValidation(fmt.Sprintf("'%s' is not a valid %s ARN", value, s.singular))
}

// The three scopes. Every code below is AWS's, from the ELBv2 API reference's
// per-operation error tables.
var (
	loadBalancerScope = newIdentifierScope("loadbalancer",
		"LoadBalancerNotFound", "Load balancers", "load balancer")

	targetGroupScope = newIdentifierScope("targetgroup",
		"TargetGroupNotFound", "Target groups", "target group")

	listenerScope = newIdentifierScope("listener",
		"ListenerNotFound", "Listeners", "listener")
)

// scopeForARN picks the scope an ARN names, for the operations that take a
// mixed list — AddTags, RemoveTags and DescribeTags accept load balancer and
// target group ARNs in one ResourceArns, and AWS answers each with its own
// resource's not-found code. Anything else falls back to the load balancer
// scope, which is the code those operations have always answered.
func scopeForARN(arn string) identifierScope {
	for _, scope := range []identifierScope{targetGroupScope, listenerScope} {
		if strings.Contains(arn, ":"+scope.segment+"/") {
			return scope
		}
	}
	return loadBalancerScope
}

// resolveIdentifiers is the rule this file exists for: a Describe*'s explicit
// ARN and Name lists, checked against what the region actually holds.
//
// Empty lists are not a selection at all — they mean "everything in the
// region" — so they keep today's semantics untouched, which is the regression
// risk #1718 names: a Describe naming nothing must still be an empty 200.
//
// A non-empty list is resolved in the order the caller sent it and fails on the
// first identifier that does not resolve, ARNs before Names. `all` is the
// collection the Describe has already read, and arnOf/nameOf read one record's
// identifiers out of it, so no operation needs a second store round trip to
// answer this. nameOf may be nil for a resource that has no Names parameter.
func resolveIdentifiers[T any](scope identifierScope, arns, names []string, all []T,
	arnOf func(T) string, nameOf func(T) string) *protocol.AWSError {
	for _, arn := range arns {
		if !scope.shape.MatchString(arn) {
			return scope.errMalformedARN(arn)
		}
	}
	if len(arns) > 0 {
		known := make(map[string]bool, len(all))
		for _, res := range all {
			known[arnOf(res)] = true
		}
		for _, arn := range arns {
			if !known[arn] {
				return scope.errNotFound(arn)
			}
		}
	}
	if len(names) > 0 && nameOf != nil {
		known := make(map[string]bool, len(all))
		for _, res := range all {
			known[nameOf(res)] = true
		}
		for _, name := range names {
			if !known[name] {
				return scope.errNotFound(name)
			}
		}
	}
	return nil
}

// requireLoadBalancer, requireTargetGroup and requireListener resolve the
// single explicit ARN that every operation outside the Describe* list takes —
// Delete*, Modify*, the two *Attributes reads, CreateListener's
// LoadBalancerArn and DescribeListeners' own. They apply the same two rules in
// the same order as resolveIdentifiers, so naming a resource that is not there
// answers identically whether it was named one at a time or in a list.
func (h *Handler) requireLoadBalancer(ctx context.Context, region, arn string) (*LoadBalancer, *protocol.AWSError) {
	if !loadBalancerScope.shape.MatchString(arn) {
		return nil, loadBalancerScope.errMalformedARN(arn)
	}
	lb, found, err := h.getLB(ctx, region, arn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, loadBalancerScope.errNotFound(arn)
	}
	return lb, nil
}

func (h *Handler) requireTargetGroup(ctx context.Context, region, arn string) (*TargetGroup, *protocol.AWSError) {
	if !targetGroupScope.shape.MatchString(arn) {
		return nil, targetGroupScope.errMalformedARN(arn)
	}
	tg, found, err := h.getTG(ctx, region, arn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, targetGroupScope.errNotFound(arn)
	}
	return tg, nil
}

func (h *Handler) requireListener(ctx context.Context, region, arn string) (*Listener, *protocol.AWSError) {
	if !listenerScope.shape.MatchString(arn) {
		return nil, listenerScope.errMalformedARN(arn)
	}
	l, found, err := h.getListener(ctx, region, arn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, listenerScope.errNotFound(arn)
	}
	return l, nil
}
