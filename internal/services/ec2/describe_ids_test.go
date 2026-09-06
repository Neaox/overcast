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
func TestDescribeIDScopes_carryAWSsOwnCodes(t *testing.T) {
	want := map[string][3]string{
		"DescribeVpcs":              {"vpc", "InvalidVpcID.NotFound", "InvalidVpcID.Malformed"},
		"DescribeSubnets":           {"subnet", "InvalidSubnetID.NotFound", "InvalidSubnetID.Malformed"},
		"DescribeSecurityGroups":    {"sg", "InvalidGroup.NotFound", "InvalidGroupId.Malformed"},
		"DescribeRouteTables":       {"rtb", "InvalidRouteTableID.NotFound", "InvalidRouteTableId.Malformed"},
		"DescribeInternetGateways":  {"igw", "InvalidInternetGatewayID.NotFound", "InvalidInternetGatewayId.Malformed"},
		"DescribeNetworkInterfaces": {"eni", "InvalidNetworkInterfaceID.NotFound", "InvalidNetworkInterfaceId.Malformed"},
		"DescribeInstances":         {"i", "InvalidInstanceID.NotFound", "InvalidInstanceID.Malformed"},
	}
	if len(describeIDScopes) != len(want) {
		t.Fatalf("describeIDScopes has %d entries, this table has %d", len(describeIDScopes), len(want))
	}
	for op, w := range want {
		scope, ok := describeIDScopes[op]
		if !ok {
			t.Errorf("%s resolves no ID list", op)
			continue
		}
		if scope.prefix != w[0] || scope.notFound != w[1] || scope.malformed != w[2] {
			t.Errorf("%s = {%s %s %s}, want {%s %s %s}",
				op, scope.prefix, scope.notFound, scope.malformed, w[0], w[1], w[2])
		}
	}
}
