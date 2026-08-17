package ec2

// filters_test.go — the rule an EC2 describe applies to a filter name, and the
// evidence that every describe applies the same one.

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// refused drives a describe that is expected to reject its filters, and returns
// the AWS error code and message.
func refused(t *testing.T, h *Handler, fn http.HandlerFunc, params url.Values) (code, message string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/?"+params.Encode(), nil)
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	w := httptest.NewRecorder()
	fn(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code    string `xml:"Errors>Error>Code"`
		Message string `xml:"Errors>Error>Message"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, w.Body.String())
	}
	return resp.Code, resp.Message
}

// describes names every EC2 describe that takes Filter.N, so a test can assert
// something of all of them at once. Two describes are absent because AWS gives
// them no Filters parameter at all: DescribeVpcAttribute and
// DescribeAccountAttributes.
func describes(h *Handler) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"DescribeAddresses":             h.DescribeAddresses,
		"DescribeAvailabilityZones":     h.DescribeAvailabilityZones,
		"DescribeDhcpOptions":           h.DescribeDhcpOptions,
		"DescribeImages":                h.DescribeImages,
		"DescribeInstanceTypes":         h.DescribeInstanceTypes,
		"DescribeInstances":             h.DescribeInstances,
		"DescribeInternetGateways":      h.DescribeInternetGateways,
		"DescribeKeyPairs":              h.DescribeKeyPairs,
		"DescribeNatGateways":           h.DescribeNatGateways,
		"DescribeNetworkInterfaces":     h.DescribeNetworkInterfaces,
		"DescribeRegions":               h.DescribeRegions,
		"DescribeRouteTables":           h.DescribeRouteTables,
		"DescribeSecurityGroups":        h.DescribeSecurityGroups,
		"DescribeSubnets":               h.DescribeSubnets,
		"DescribeTags":                  h.DescribeTags,
		"DescribeVpcEndpoints":          h.DescribeVpcEndpoints,
		"DescribeVpcPeeringConnections": h.DescribeVpcPeeringConnections,
		"DescribeVpcs":                  h.DescribeVpcs,
		"DescribeVpnGateways":           h.DescribeVpnGateways,
	}
}

// The point of #1032: not that one filter was missing, but that a filter
// Overcast does not implement produced a confident wrong answer, and which
// wrong answer depended on which helper the handler's author had reached for.
// One rule, every describe.
func TestEveryDescribeRefusesAnUnrecognisedFilter(t *testing.T) {
	h := defaultVPCHandler(t)
	for op, fn := range describes(h) {
		t.Run(op, func(t *testing.T) {
			code, message := refused(t, h, fn, filterParams("not-a-filter", "anything"))
			if code != "InvalidParameterValue" {
				t.Fatalf("code = %q, want InvalidParameterValue", code)
			}
			if !strings.HasPrefix(message, "The filter 'not-a-filter' is invalid.") {
				t.Fatalf("message = %q, want it to open with AWS's sentence", message)
			}
			if !strings.Contains(message, op) {
				t.Fatalf("message = %q, want it to name the operation", message)
			}
		})
	}
}

// The two idioms this replaced disagreed on an unrecognised filter, and one is
// not more equal than the other: DescribeVpcs (parseFilterValues, which ignored
// the name and returned everything) and DescribeNatGateways (matchFilters,
// which rejected it and returned nothing) now give the same answer.
func TestFormerlyOppositeIdiomsAgree(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	if aerr := h.store.putVPC(ctx, &VPC{VpcID: "vpc-mine", State: "available"}); aerr != nil {
		t.Fatalf("putVPC: %v", aerr.Message)
	}
	if aerr := h.store.putNatGateway(ctx, &NatGateway{NatGatewayID: "nat-mine", VpcID: "vpc-mine", State: "available"}); aerr != nil {
		t.Fatalf("putNatGateway: %v", aerr.Message)
	}

	for op, fn := range map[string]http.HandlerFunc{
		"DescribeVpcs":        h.DescribeVpcs,
		"DescribeNatGateways": h.DescribeNatGateways,
	} {
		t.Run(op, func(t *testing.T) {
			// A filter both operations implement still selects.
			body := describe(t, h, fn, filterParams("vpc-id", "vpc-mine"))
			if !strings.Contains(string(body), "vpc-mine") {
				t.Fatalf("vpc-id=vpc-mine selected nothing: %s", body)
			}
			// One neither implements is refused, identically.
			if code, _ := refused(t, h, fn, filterParams("owner-id", "123456789012")); code != "InvalidParameterValue" {
				t.Fatalf("code = %q, want InvalidParameterValue", code)
			}
		})
	}
}

// An empty region is where an ignored filter is most dangerous: "nothing
// matched" and "I did not understand the question" are the same response. The
// filter is validated before the collection is read, so it is refused either
// way.
func TestUnrecognisedFilterIsRefusedWithNothingToFilter(t *testing.T) {
	h := defaultVPCHandler(t)
	if code, _ := refused(t, h, h.DescribeNatGateways, filterParams("not-a-filter", "x")); code != "InvalidParameterValue" {
		t.Fatalf("code = %q, want InvalidParameterValue", code)
	}
}

// A describe that does not carry tags must refuse a tag selector rather than
// accept it and answer as though it had filtered — that is the reported bug
// with a different filter name.
func TestTagFilterIsRefusedWhereTagsAreNotMatched(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	if aerr := h.store.putVpcEndpoint(ctx, &VpcEndpoint{VpcEndpointID: "vpce-1", VpcID: "vpc-1", ServiceName: "com.amazonaws.us-east-1.s3"}); aerr != nil {
		t.Fatalf("putVpcEndpoint: %v", aerr.Message)
	}
	if code, _ := refused(t, h, h.DescribeVpcEndpoints, filterParams("tag:Name", "anything")); code != "InvalidParameterValue" {
		t.Fatalf("code = %q, want InvalidParameterValue", code)
	}
	// The same selector on a describe that does match tags is accepted.
	describe(t, h, h.DescribeVpcs, filterParams("tag:Name", "anything"))
}

// The error is the only place a caller learns what Overcast can answer, so it
// names the set rather than only what was refused.
func TestRefusalNamesTheSupportedFilters(t *testing.T) {
	h := defaultVPCHandler(t)
	_, message := refused(t, h, h.DescribeVpcs, filterParams("owner-id", "123456789012"))
	for _, want := range supportedFilters["DescribeVpcs"] {
		if !strings.Contains(message, want) {
			t.Fatalf("message = %q, want it to name the supported filter %q", message, want)
		}
	}
}

// A filter name is matched exactly, because AWS matches it exactly. Overcast
// used to fold case here, which accepted a call real EC2 refuses.
func TestFilterNamesAreCaseSensitive(t *testing.T) {
	h := defaultVPCHandler(t)
	for _, name := range []string{"VPC-ID", "isdefault", "TAG-KEY"} {
		if code, _ := refused(t, h, h.DescribeVpcs, filterParams(name, "anything")); code != "InvalidParameterValue" {
			t.Errorf("%q: code = %q, want InvalidParameterValue", name, code)
		}
	}
	// The spellings AWS uses are of course still accepted, including the one
	// EC2 spells in camel case.
	describe(t, h, h.DescribeVpcs, filterParams("isDefault", "true"))
	describe(t, h, h.DescribeVpcs, filterParams("tag-key", "Name"))
}

// wildcardMatch is the whole of AWS's filter-value pattern language, and the
// table is where its edges are pinned rather than in a describe.
func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"", "", true},
		{"", "a", false},
		{"a", "", false},
		{"a", "a", true},
		{"a", "b", false},

		{"*", "", true},
		{"*", "anything", true},
		{"a*", "a", true},
		{"a*", "abc", true},
		{"a*", "b", false},
		{"*c", "abc", true},
		{"*c", "abd", false},
		{"a*c", "ac", true},
		{"a*c", "abc", true},
		{"a*c", "abbbc", true},
		{"a*c", "ab", false},
		{"overcast-*", "overcast-local", true},
		{"overcast-*", "other-local", false},
		{"com.amazonaws.*.s3", "com.amazonaws.us-east-1.s3", true},
		{"com.amazonaws.*.s3", "com.amazonaws.us-east-1.dynamodb", false},

		{"?", "a", true},
		{"?", "", false},
		{"?", "ab", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"us-east-1?", "us-east-1a", true},
		{"us-east-1?", "us-east-1", false},

		// A backslash asks for the literal character after it.
		{`a\*c`, "a*c", true},
		{`a\*c`, "abc", false},
		{`a\?c`, "a?c", true},
		{`a\?c`, "abc", false},
		{`a\\c`, `a\c`, true},

		// Adjacent and repeated stars must not turn the scan exponential; this
		// is the classic pathological pattern for a backtracking matcher.
		{"**", "ab", true},
		{"*a*b*c*", "xxaxxbxxcxx", true},
		{"*a*b*c*", "xxaxxcxxbxx", false},
		{"a*a*a*a*a*a*a*b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},

		// path.Match would read these as a character class and a path
		// separator; an EC2 filter value is an opaque string.
		{"[abc]", "[abc]", true},
		{"[abc]", "a", false},
		{"a/*", "a/b/c", true},
	}
	for _, tc := range cases {
		if got := wildcardMatch(tc.pattern, tc.value); got != tc.want {
			t.Errorf("wildcardMatch(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}

// A value with no metacharacter stays a literal comparison — the common case,
// decided once per request rather than per resource.
func TestPlainValuesAreNotPatterns(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"vpc-mine", false},
		{"overcast-*", true},
		{"us-east-1?", true},
		{`literal\*`, true},
	} {
		if got := parseFilterValue(tc.value).wildcard; got != tc.want {
			t.Errorf("parseFilterValue(%q).wildcard = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// The reported bug's own filter, with the wildcard a find-or-create script
// would plausibly reach for.
func TestTagFilterMatchesAWildcard(t *testing.T) {
	h := defaultVPCHandler(t)
	taggedVPC(t, h, "vpc-mine", map[string]string{"Name": "overcast-local"})
	taggedVPC(t, h, "vpc-other", map[string]string{"Name": "something-else"})

	var resp xmlDescribeVpcsResponse
	body := describe(t, h, h.DescribeVpcs, filterParams("tag:Name", "overcast-*"))
	if err := xml.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.VpcSet) != 1 || resp.VpcSet[0].VpcID != "vpc-mine" {
		ids := make([]string, 0, len(resp.VpcSet))
		for _, v := range resp.VpcSet {
			ids = append(ids, v.VpcID)
		}
		t.Fatalf("tag:Name=overcast-* returned %v, want [vpc-mine]", ids)
	}
}

// CDK's AMI context provider sends `MachineImage.lookup`'s name filter, which is
// a wildcard in every documented example of it. Matching values exactly meant
// the lookup found nothing and CDK reported AmiNotFound.
func TestImageNameLookupMatchesAWildcard(t *testing.T) {
	h := defaultVPCHandler(t)
	var resp xmlDescribeImagesResponse
	body := describe(t, h, h.DescribeImages, filterParams("name", "Amazon Linux 2*"))
	if err := xml.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	names := make([]string, 0, len(resp.ImagesSet))
	for _, img := range resp.ImagesSet {
		names = append(names, img.Name)
	}
	if !slices.Equal(names, []string{"Amazon Linux 2", "Amazon Linux 2023"}) {
		t.Fatalf("name=Amazon Linux 2* returned %v, want both Amazon Linux AMIs", names)
	}
}

// An operation that implements no filters says so, rather than trailing off
// after the colon.
func TestRefusalNamesTheEmptySet(t *testing.T) {
	h := defaultVPCHandler(t)
	_, message := refused(t, h, h.DescribeDhcpOptions, filterParams("domain-name", "ec2.internal"))
	if !strings.HasSuffix(message, "filters: none") {
		t.Fatalf("message = %q, want it to name the empty set", message)
	}
}

// Filters are AND-ed with each other and their values OR-ed, on every describe
// and whether or not the values come from one filter or several.
func TestFiltersAreAndedAndValuesOred(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	for _, sub := range []*Subnet{
		{SubnetID: "subnet-a", VpcID: "vpc-1", AvailabilityZone: "us-east-1a", State: "available"},
		{SubnetID: "subnet-b", VpcID: "vpc-1", AvailabilityZone: "us-east-1b", State: "available"},
		{SubnetID: "subnet-c", VpcID: "vpc-2", AvailabilityZone: "us-east-1a", State: "available"},
	} {
		if aerr := h.store.putSubnet(ctx, sub); aerr != nil {
			t.Fatalf("putSubnet: %v", aerr.Message)
		}
	}

	subnetIDs := func(params url.Values) []string {
		t.Helper()
		var resp xmlDescribeSubnetsResponse
		if err := xml.Unmarshal(describe(t, h, h.DescribeSubnets, params), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		ids := make([]string, 0, len(resp.Subnets))
		for _, s := range resp.Subnets {
			ids = append(ids, s.SubnetID)
		}
		return ids
	}

	if got := subnetIDs(filterParams("subnet-id", "subnet-a", "subnet-c")); len(got) != 2 {
		t.Fatalf("two values of one filter selected %v, want both subnets", got)
	}

	anded := url.Values{
		"Filter.1.Name":    {"vpc-id"},
		"Filter.1.Value.1": {"vpc-1"},
		"Filter.2.Name":    {"availability-zone"},
		"Filter.2.Value.1": {"us-east-1a"},
	}
	if got := subnetIDs(anded); len(got) != 1 || got[0] != "subnet-a" {
		t.Fatalf("two filters selected %v, want [subnet-a]", got)
	}
}

// A filter can be answered by a list rather than a field — a route table's
// associations, a gateway's attachments — and matches when any member does. The
// boolean ones among them accept either casing, as the scalar boolean filters
// always did.
func TestListValuedAndBooleanFilters(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	for _, rt := range []*RouteTable{
		{RouteTableID: "rtb-main", VpcID: "vpc-1", Associations: []RouteTableAssociation{
			{AssociationID: "rtbassoc-1", RouteTableID: "rtb-main", Main: true},
		}},
		{RouteTableID: "rtb-private", VpcID: "vpc-1", Associations: []RouteTableAssociation{
			{AssociationID: "rtbassoc-2", RouteTableID: "rtb-private", SubnetID: "subnet-a"},
			{AssociationID: "rtbassoc-3", RouteTableID: "rtb-private", SubnetID: "subnet-b"},
		}},
	} {
		if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
			t.Fatalf("putRouteTable: %v", aerr.Message)
		}
	}

	only := func(params url.Values, want string) {
		t.Helper()
		var resp xmlDescribeRouteTablesResponse
		if err := xml.Unmarshal(describe(t, h, h.DescribeRouteTables, params), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.RouteTableSet) != 1 || resp.RouteTableSet[0].RouteTableID != want {
			ids := make([]string, 0, len(resp.RouteTableSet))
			for _, rt := range resp.RouteTableSet {
				ids = append(ids, rt.RouteTableID)
			}
			t.Fatalf("selected %v, want [%s]", ids, want)
		}
	}

	// The second of two associations is as good as the first.
	only(filterParams("association.subnet-id", "subnet-b"), "rtb-private")
	only(filterParams("association.main", "true"), "rtb-main")
	only(filterParams("association.main", "True"), "rtb-main")
}

// CDK's VPC context provider is the caller this rule most risks breaking: it
// sends filters on four describes, treats each response as already filtered,
// and fails the deploy if a lookup returns anything other than exactly one VPC.
// Every name it sends is one Overcast implements, so the strict rule leaves
// `Vpc.fromLookup` working. Captured from aws-cdk 2.1132.0,
// toolkit-lib/lib/context-providers/vpcs.ts.
func TestCDKVpcLookupFiltersAreAllImplemented(t *testing.T) {
	sent := map[string][]string{
		// findVpc sends whatever Vpc.fromLookup was given: vpcId, vpcName,
		// isDefault and any tags become vpc-id, tag:Name, isDefault, tag:<key>.
		"DescribeVpcs":        {"vpc-id", "isDefault", "tag:<key>"},
		"DescribeSubnets":     {"vpc-id"},
		"DescribeRouteTables": {"vpc-id"},
		"DescribeVpnGateways": {"attachment.vpc-id", "attachment.state", "state"},
	}
	for op, names := range sent {
		for _, name := range names {
			if !slices.Contains(supportedFilters[op], name) {
				t.Fatalf("%s does not implement %q, which CDK's VPC lookup sends; supported: %v",
					op, name, supportedFilters[op])
			}
		}
	}
}
