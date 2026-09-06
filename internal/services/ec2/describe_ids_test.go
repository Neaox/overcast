package ec2

// describe_ids_test.go — the rule in describe_ids.go, checked at the seam
// rather than through seven handlers: an empty list selects everything, a
// non-empty one is resolved, shape before existence, first bad ID wins.

import (
	"testing"
)

// held is a stand-in for whatever collection a describe has already read.
type held struct{ id string }

func heldIDs(ids ...string) []held {
	out := make([]held, 0, len(ids))
	for _, id := range ids {
		out = append(out, held{id})
	}
	return out
}

func heldID(h held) string { return h.id }

func TestResolveIDs(t *testing.T) {
	region := heldIDs("vpc-0a0a0a0a", "vpc-0b0b0b0b")

	cases := []struct {
		name        string
		requested   []string
		wantCode    string
		wantMessage string
	}{
		// The regression risk #1708 names: no IDs means "everything the
		// filters allow", and a filter matching nothing is a legitimate empty
		// 200 rather than an error.
		{name: "no ids selects everything", requested: nil},
		{name: "one known id", requested: []string{"vpc-0a0a0a0a"}},
		{name: "every id known", requested: []string{"vpc-0b0b0b0b", "vpc-0a0a0a0a"}},
		{
			name:        "unknown id",
			requested:   []string{"vpc-0c0c0c0c"},
			wantCode:    "InvalidVpcID.NotFound",
			wantMessage: "The vpc ID 'vpc-0c0c0c0c' does not exist",
		},
		{
			// AWS fails the whole call rather than answering with the part
			// that resolved.
			name:        "a known id does not excuse an unknown one",
			requested:   []string{"vpc-0a0a0a0a", "vpc-0c0c0c0c"},
			wantCode:    "InvalidVpcID.NotFound",
			wantMessage: "The vpc ID 'vpc-0c0c0c0c' does not exist",
		},
		{
			name:        "malformed id",
			requested:   []string{"not-an-id"},
			wantCode:    "InvalidVpcID.Malformed",
			wantMessage: `Invalid id: "not-an-id" (expecting "vpc-...")`,
		},
		{
			// Shape is checked across the whole list first, because AWS checks
			// it at request parsing, ahead of any lookup.
			name:      "malformed beats unknown wherever it sits",
			requested: []string{"vpc-0c0c0c0c", "not-an-id"},
			wantCode:  "InvalidVpcID.Malformed",
		},
		{
			// The first bad ID in the caller's order is the one named, which
			// is why idSelection keeps that order.
			name:        "the first malformed id is the one named",
			requested:   []string{"vpc-nope", "also-nope"},
			wantCode:    "InvalidVpcID.Malformed",
			wantMessage: `Invalid id: "vpc-nope" (expecting "vpc-...")`,
		},
		// AWS issued eight-character IDs before the move to long IDs and still
		// accepts them; seventeen is what it issues now. Nothing else is an ID.
		{name: "seventeen hex characters", requested: []string{"vpc-0000000000000000a"},
			wantCode: "InvalidVpcID.NotFound"},
		{name: "sixteen is not an id", requested: []string{"vpc-000000000000000a"},
			wantCode: "InvalidVpcID.Malformed"},
		{name: "nine is not an id", requested: []string{"vpc-000000000"},
			wantCode: "InvalidVpcID.Malformed"},
		{name: "uppercase hex is not an id", requested: []string{"vpc-0A0A0A0A"},
			wantCode: "InvalidVpcID.Malformed"},
		{name: "another resource's id is not this one's", requested: []string{"subnet-0a0a0a0a"},
			wantCode: "InvalidVpcID.Malformed"},
		{name: "an empty id is not an id", requested: []string{"vpc-0a0a0a0a", ""},
			wantCode: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aerr := resolveIDs(vpcIDScope, selectedIDs(tc.requested), region, heldID)
			if tc.wantCode == "" {
				if aerr != nil {
					t.Fatalf("resolveIDs = %s: %s, want no error", aerr.Code, aerr.Message)
				}
				return
			}
			if aerr == nil {
				t.Fatalf("resolveIDs = nil, want %s", tc.wantCode)
			}
			if aerr.Code != tc.wantCode {
				t.Errorf("code = %q, want %q (message %q)", aerr.Code, tc.wantCode, aerr.Message)
			}
			if tc.wantMessage != "" && aerr.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", aerr.Message, tc.wantMessage)
			}
			if aerr.HTTPStatus != 400 {
				t.Errorf("status = %d, want 400", aerr.HTTPStatus)
			}
		})
	}
}

// The casing is AWS's and is not consistent between resources — InvalidVpcID
// but InvalidGroupId, InvalidRouteTableID.NotFound but
// InvalidRouteTableId.Malformed. A client matches the string exactly, so this
// pins every one of them against being tidied up.
// A blank Malformed column is deliberate and is pinned here too: the EC2
// reference documents no Malformed code for an allocation ID or a virtual
// private gateway, so those scopes check no shape and a wrong-shaped ID comes
// back as NotFound (TestResolveIDs_scopeWithNoMalformedCode below).
func TestDescribeIDScopes_carryAWSsOwnCodes(t *testing.T) {
	want := map[string][][3]string{
		"DescribeVpcs":              {{"vpc", "InvalidVpcID.NotFound", "InvalidVpcID.Malformed"}},
		"DescribeSubnets":           {{"subnet", "InvalidSubnetID.NotFound", "InvalidSubnetID.Malformed"}},
		"DescribeSecurityGroups":    {{"sg", "InvalidGroup.NotFound", "InvalidGroupId.Malformed"}},
		"DescribeRouteTables":       {{"rtb", "InvalidRouteTableID.NotFound", "InvalidRouteTableId.Malformed"}},
		"DescribeInternetGateways":  {{"igw", "InvalidInternetGatewayID.NotFound", "InvalidInternetGatewayId.Malformed"}},
		"DescribeNetworkInterfaces": {{"eni", "InvalidNetworkInterfaceID.NotFound", "InvalidNetworkInterfaceId.Malformed"}},
		"DescribeInstances":         {{"i", "InvalidInstanceID.NotFound", "InvalidInstanceID.Malformed"}},
		"DescribeAddresses": {
			{"eipalloc", "InvalidAllocationID.NotFound", ""},
			{"", "InvalidAddress.NotFound", "InvalidAddress.Malformed"},
		},
		"DescribeNatGateways":           {{"nat", "NatGatewayNotFound", "NatGatewayMalformed"}},
		"DescribeVpnGateways":           {{"vgw", "InvalidVpnGatewayID.NotFound", ""}},
		"DescribeVpcEndpoints":          {{"vpce", "InvalidVpcEndpointId.NotFound", "InvalidVpcEndpointId.Malformed"}},
		"DescribeVpcPeeringConnections": {{"pcx", "InvalidVpcPeeringConnectionID.NotFound", "InvalidVpcPeeringConnectionId.Malformed"}},
	}
	if len(describeIDScopes) != len(want) {
		t.Fatalf("describeIDScopes has %d entries, this table has %d", len(describeIDScopes), len(want))
	}
	for op, w := range want {
		scopes, ok := describeIDScopes[op]
		if !ok {
			t.Errorf("%s resolves no ID list", op)
			continue
		}
		if len(scopes) != len(w) {
			t.Errorf("%s has %d scopes, this table has %d", op, len(scopes), len(w))
			continue
		}
		for i, scope := range scopes {
			if scope.prefix != w[i][0] || scope.notFound != w[i][1] || scope.malformed != w[i][2] {
				t.Errorf("%s[%d] = {%s %s %s}, want {%s %s %s}",
					op, i, scope.prefix, scope.notFound, scope.malformed, w[i][0], w[i][1], w[i][2])
			}
		}
	}
}

// A scope the reference documents no Malformed code for resolves on existence
// alone: an ID of the wrong shape is one the region does not hold, which is
// true and is a code AWS does send. Inventing InvalidAllocationID.Malformed
// would be a code no AWS client ever matches on.
func TestResolveIDs_scopeWithNoMalformedCode(t *testing.T) {
	region := heldIDs("eipalloc-0a0a0a0a")

	for _, id := range []string{"not-an-id", "eipalloc-0A0A0A0A", "eipalloc-000"} {
		t.Run(id, func(t *testing.T) {
			aerr := resolveIDs(allocationIDScope, selectedIDs([]string{id}), region, heldID)
			if aerr == nil {
				t.Fatalf("resolveIDs(%q) = nil, want InvalidAllocationID.NotFound", id)
			}
			if aerr.Code != "InvalidAllocationID.NotFound" {
				t.Errorf("code = %q, want InvalidAllocationID.NotFound", aerr.Code)
			}
		})
	}

	if aerr := resolveIDs(allocationIDScope, selectedIDs([]string{"eipalloc-0a0a0a0a"}), region, heldID); aerr != nil {
		t.Errorf("a known allocation still resolves: %s: %s", aerr.Code, aerr.Message)
	}
}

// DescribeAddresses' second selector is an address rather than a
// `<prefix>-<hex>` ID, so "malformed" means "not an IPv4 address" — which is
// what AWS's own InvalidAddress.Malformed row asks for ("in the form
// xx.xx.xx.xx; for example, 55.123.45.67").
func TestResolveIDs_publicIPScope(t *testing.T) {
	region := heldIDs("203.0.113.7")

	cases := []struct {
		address  string
		wantCode string
	}{
		{"203.0.113.7", ""},
		{"203.0.113.8", "InvalidAddress.NotFound"},
		{"0.0.0.0", "InvalidAddress.NotFound"},
		{"255.255.255.255", "InvalidAddress.NotFound"},
		{"not-an-ip", "InvalidAddress.Malformed"},
		{"203.0.113.999", "InvalidAddress.Malformed"},
		{"203.0.113", "InvalidAddress.Malformed"},
		{"203.0.113.7.7", "InvalidAddress.Malformed"},
		{"eipalloc-0a0a0a0a", "InvalidAddress.Malformed"},
		{"2001:db8::1", "InvalidAddress.Malformed"},
	}

	for _, tc := range cases {
		t.Run(tc.address, func(t *testing.T) {
			aerr := resolveIDs(publicIPScope, selectedIDs([]string{tc.address}), region, heldID)
			if tc.wantCode == "" {
				if aerr != nil {
					t.Fatalf("resolveIDs = %s: %s, want no error", aerr.Code, aerr.Message)
				}
				return
			}
			if aerr == nil {
				t.Fatalf("resolveIDs = nil, want %s", tc.wantCode)
			}
			if aerr.Code != tc.wantCode {
				t.Errorf("code = %q, want %q (message %q)", aerr.Code, tc.wantCode, aerr.Message)
			}
		})
	}
}

// The malformed message tells the caller what to send instead, and for an
// address that is not a prefix.
func TestResolveIDs_malformedMessageNamesTheShapeExpected(t *testing.T) {
	aerr := resolveIDs(publicIPScope, selectedIDs([]string{"not-an-ip"}), heldIDs(), heldID)
	if aerr == nil {
		t.Fatal("resolveIDs = nil, want InvalidAddress.Malformed")
	}
	if want := `Invalid id: "not-an-ip" (expecting "xx.xx.xx.xx")`; aerr.Message != want {
		t.Errorf("message = %q, want %q", aerr.Message, want)
	}
}
