package ec2

// tags_test.go — the EC2 tag read path: what a describe surfaces, and what a
// tag filter selects.

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/protocol"
)

// describe drives a Query-protocol handler the way DispatchQuery does — form
// parsed, params in the query string — and returns the response body.
func describe(t *testing.T, h *Handler, fn http.HandlerFunc, params url.Values) []byte {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/?"+params.Encode(), nil)
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	w := httptest.NewRecorder()
	fn(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

// filterParams builds the Filter.N.Name / Filter.N.Value.M shape the AWS CLI
// sends for `--filters Name=<name>,Values=<v1>,<v2>`.
func filterParams(name string, values ...string) url.Values {
	p := url.Values{}
	p.Set("Filter.1.Name", name)
	for i, v := range values {
		p.Set(fmt.Sprintf("Filter.1.Value.%d", i+1), v)
	}
	return p
}

// tagged creates a VPC carrying the given tags.
func taggedVPC(t *testing.T, h *Handler, id string, tags map[string]string) {
	t.Helper()
	ctx := context.Background()
	if aerr := h.store.putVPC(ctx, &VPC{VpcID: id, State: "available", CidrBlock: "10.42.0.0/16"}); aerr != nil {
		t.Fatalf("putVPC: %v", aerr.Message)
	}
	if aerr := h.store.putTags(ctx, id, tags); aerr != nil {
		t.Fatalf("putTags: %v", aerr.Message)
	}
}

// A find-or-create script asks for its own VPC by name and creates one when the
// answer is empty. DescribeVpcs ignored `tag:Name` entirely and answered with
// every VPC in the region, so the script read Vpcs[0] — the seeded default VPC,
// public by construction — and adopted it instead of creating its own.
func TestDescribeVpcs_selectsByTagFilter(t *testing.T) {
	h := defaultVPCHandler(t)
	taggedVPC(t, h, "vpc-mine", map[string]string{"Name": "overcast-local"})
	taggedVPC(t, h, "vpc-other", map[string]string{"Name": "something-else"})

	body := describe(t, h, h.DescribeVpcs, filterParams("tag:Name", "overcast-local"))

	var resp xmlDescribeVpcsResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.VpcSet) != 1 || resp.VpcSet[0].VpcID != "vpc-mine" {
		ids := make([]string, 0, len(resp.VpcSet))
		for _, v := range resp.VpcSet {
			ids = append(ids, v.VpcID)
		}
		t.Fatalf("tag:Name=overcast-local returned %v, want [vpc-mine]", ids)
	}
}

// A filter that matches nothing must return nothing. Returning everything is
// what makes a find-or-create check take the "found" branch forever.
func TestDescribeVpcs_tagFilterThatMatchesNothingReturnsNothing(t *testing.T) {
	h := defaultVPCHandler(t)
	taggedVPC(t, h, "vpc-mine", map[string]string{"Name": "overcast-local"})

	body := describe(t, h, h.DescribeVpcs, filterParams("tag:Name", "no-such-vpc"))

	var resp xmlDescribeVpcsResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.VpcSet) != 0 {
		t.Fatalf("VpcSet = %d VPCs, want none", len(resp.VpcSet))
	}
}

// tag-key and tag-value select on one half of a tag each.
func TestDescribeVpcs_tagKeyAndTagValueFilters(t *testing.T) {
	h := defaultVPCHandler(t)
	taggedVPC(t, h, "vpc-mine", map[string]string{"Name": "overcast-local", "owner": "scripts"})
	taggedVPC(t, h, "vpc-other", map[string]string{"Name": "something-else"})

	cases := []struct {
		name   string
		filter url.Values
		want   string
	}{
		{"tag-key", filterParams("tag-key", "owner"), "vpc-mine"},
		{"tag-value", filterParams("tag-value", "something-else"), "vpc-other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp xmlDescribeVpcsResponse
			if err := xml.Unmarshal(describe(t, h, h.DescribeVpcs, tc.filter), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(resp.VpcSet) != 1 || resp.VpcSet[0].VpcID != tc.want {
				t.Fatalf("got %#v, want just %s", resp.VpcSet, tc.want)
			}
		})
	}
}

// The tags a caller set have to come back, or client-side filtering — the
// obvious workaround when a server-side filter is missing — cannot work either.
// DescribeVpcs replaced them wholesale with its own diagnostic tag.
func TestDescribeVpcs_returnsTheTagsTheResourceHas(t *testing.T) {
	h := defaultVPCHandler(t)
	taggedVPC(t, h, "vpc-mine", map[string]string{"Name": "overcast-local"})

	var resp xmlDescribeVpcsResponse
	if err := xml.Unmarshal(describe(t, h, h.DescribeVpcs, url.Values{}), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var vpc *xmlVpc
	for i := range resp.VpcSet {
		if resp.VpcSet[i].VpcID == "vpc-mine" {
			vpc = &resp.VpcSet[i]
		}
	}
	if vpc == nil {
		t.Fatal("vpc-mine missing from DescribeVpcs")
	}
	got := map[string]string{}
	for _, tag := range vpc.TagSet {
		got[tag.Key] = tag.Value
	}
	if got["Name"] != "overcast-local" {
		t.Fatalf("tagSet = %#v, want it to carry Name=overcast-local", got)
	}
	// The diagnostic tag is Overcast's own and must survive alongside the real ones.
	if _, ok := got["overcast:network-status"]; !ok {
		t.Errorf("tagSet = %#v, want it to keep overcast:network-status", got)
	}
}

// Every describe that renders tags, in one table, asserting all three things
// that were wrong somewhere: the tags come back, a matching tag filter selects,
// and a non-matching one excludes.
//
// It is table-driven across all nine because the bugs were not uniform.
// DescribeInstances, DescribeNatGateways and DescribeVpnGateways read a `Tags`
// field on their own record that only a create call wrote, so `CreateTags` on
// an existing resource was invisible to them. DescribeVpcs rendered only
// Overcast's own diagnostic tag. Security groups, route tables, internet
// gateways and network interfaces returned no tagSet at all. A per-resource
// test would have been written against whichever of those the author had in
// mind, which is how the service ended up with four different answers.
func TestEveryTaggableDescribeRendersAndFiltersOnTags(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		id       string
		seed     func(t *testing.T, h *Handler)
		describe func(h *Handler) http.HandlerFunc
	}{
		{
			name: "vpc", id: "vpc-tagtest",
			seed: func(t *testing.T, h *Handler) {
				mustPut(t, h.store.putVPC(ctx, &VPC{VpcID: "vpc-tagtest", State: "available"}))
			},
			describe: func(h *Handler) http.HandlerFunc { return h.DescribeVpcs },
		},
		{
			name: "subnet", id: "subnet-tagtest",
			seed: func(t *testing.T, h *Handler) {
				mustPut(t, h.store.putSubnet(ctx, &Subnet{SubnetID: "subnet-tagtest", State: "available"}))
			},
			describe: func(h *Handler) http.HandlerFunc { return h.DescribeSubnets },
		},
		{
			name: "instance", id: "i-tagtest",
			seed: func(t *testing.T, h *Handler) {
				mustPut(t, h.store.putInstance(ctx, &Instance{
					InstanceID: "i-tagtest", State: InstanceState{Code: 16, Name: "running"},
				}))
			},
			describe: func(h *Handler) http.HandlerFunc { return h.DescribeInstances },
		},
		{
			name: "nat gateway", id: "nat-tagtest",
			seed: func(t *testing.T, h *Handler) {
				mustPut(t, h.store.putNatGateway(ctx, &NatGateway{NatGatewayID: "nat-tagtest", State: "available"}))
			},
			describe: func(h *Handler) http.HandlerFunc { return h.DescribeNatGateways },
		},
		{
			name: "vpn gateway", id: "vgw-tagtest",
			seed: func(t *testing.T, h *Handler) {
				mustPut(t, h.store.putVpnGateway(ctx, &VpnGateway{VpnGatewayID: "vgw-tagtest", State: "available"}))
			},
			describe: func(h *Handler) http.HandlerFunc { return h.DescribeVpnGateways },
		},
		{
			name: "security group", id: "sg-tagtest",
			seed: func(t *testing.T, h *Handler) {
				mustPut(t, h.store.putSecurityGroup(ctx, &SecurityGroup{GroupID: "sg-tagtest", GroupName: "tagtest"}))
			},
			describe: func(h *Handler) http.HandlerFunc { return h.DescribeSecurityGroups },
		},
		{
			name: "route table", id: "rtb-tagtest",
			seed: func(t *testing.T, h *Handler) {
				mustPut(t, h.store.putRouteTable(ctx, &RouteTable{RouteTableID: "rtb-tagtest", VpcID: "vpc-x"}))
			},
			describe: func(h *Handler) http.HandlerFunc { return h.DescribeRouteTables },
		},
		{
			name: "internet gateway", id: "igw-tagtest",
			seed: func(t *testing.T, h *Handler) {
				mustPut(t, h.store.putInternetGateway(ctx, &InternetGateway{InternetGatewayID: "igw-tagtest"}))
			},
			describe: func(h *Handler) http.HandlerFunc { return h.DescribeInternetGateways },
		},
		{
			name: "network interface", id: "eni-tagtest",
			seed: func(t *testing.T, h *Handler) {
				mustPut(t, h.store.putNetworkInterface(ctx, &NetworkInterface{NetworkInterfaceID: "eni-tagtest"}))
			},
			describe: func(h *Handler) http.HandlerFunc { return h.DescribeNetworkInterfaces },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := defaultVPCHandler(t)
			tc.seed(t, h)
			// What CreateTags does, through the store it writes to.
			mustPut(t, h.store.putTags(ctx, tc.id, map[string]string{"Name": "tagged-later"}))
			fn := tc.describe(h)

			got := tagsInResponse(t, describe(t, h, fn, url.Values{}))
			if got["Name"] != "tagged-later" {
				t.Errorf("unfiltered describe rendered %#v, want Name=tagged-later", got)
			}

			got = tagsInResponse(t, describe(t, h, fn, filterParams("tag:Name", "tagged-later")))
			if got["Name"] != "tagged-later" {
				t.Errorf("tag:Name=tagged-later returned %#v, want the resource", got)
			}

			got = tagsInResponse(t, describe(t, h, fn, filterParams("tag:Name", "no-such-thing")))
			if len(got) != 0 {
				t.Errorf("tag:Name=no-such-thing returned %#v, want nothing", got)
			}
		})
	}
}

func mustPut(t *testing.T, aerr *protocol.AWSError) {
	t.Helper()
	if aerr != nil {
		t.Fatalf("seed: %v", aerr.Message)
	}
}

// tagsInResponse pulls every tagSet out of a Query-protocol response, whatever
// resource element wraps it, so one test can cover nine describes without
// knowing nine response shapes.
func tagsInResponse(t *testing.T, body []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "tagSet" {
			continue
		}
		var set struct {
			Items []struct {
				Key   string `xml:"key"`
				Value string `xml:"value"`
			} `xml:"item"`
		}
		if err := dec.DecodeElement(&set, &se); err != nil {
			t.Fatalf("decode tagSet: %v", err)
		}
		for _, item := range set.Items {
			out[item.Key] = item.Value
		}
	}
}

// The handlers that match filters against an attribute map reject any filter
// name the map has no entry for. A tag filter is never in that map — tags are
// matched separately — so routing one through it excluded every resource, which
// is the opposite failure to the one DescribeVpcs had and just as wrong.
func TestTagFilterWorksOnAttributeMapHandlers(t *testing.T) {
	ctx := context.Background()
	h := defaultVPCHandler(t)

	for _, id := range []string{"nat-keep", "nat-drop"} {
		if aerr := h.store.putNatGateway(ctx, &NatGateway{NatGatewayID: id, State: "available"}); aerr != nil {
			t.Fatalf("putNatGateway: %v", aerr.Message)
		}
	}
	if aerr := h.store.putTags(ctx, "nat-keep", map[string]string{"Name": "wanted"}); aerr != nil {
		t.Fatalf("putTags: %v", aerr.Message)
	}

	var resp xmlDescribeNatGatewaysResponse
	body := describe(t, h, h.DescribeNatGateways, filterParams("tag:Name", "wanted"))
	if err := xml.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.NatGateways) != 1 || resp.NatGateways[0].NatGatewayID != "nat-keep" {
		ids := make([]string, 0, len(resp.NatGateways))
		for _, n := range resp.NatGateways {
			ids = append(ids, n.NatGatewayID)
		}
		t.Fatalf("tag:Name=wanted returned %v, want [nat-keep]", ids)
	}
}

// Tags live in a namespace of their own, keyed by resource ID, so nothing tied
// them to the lifetime of the record. Deleting a resource left them behind:
// DescribeTags went on reporting them, and the store grew for the life of the
// session.
func TestDeletingAResourceTakesItsTagsWithIt(t *testing.T) {
	ctx := context.Background()
	h := defaultVPCHandler(t)
	taggedVPC(t, h, "vpc-doomed", map[string]string{"Name": "goes-away"})

	if aerr := h.store.deleteVPC(ctx, "vpc-doomed"); aerr != nil {
		t.Fatalf("deleteVPC: %v", aerr.Message)
	}

	got, aerr := h.store.getTags(ctx, "vpc-doomed")
	if aerr != nil {
		t.Fatalf("getTags: %v", aerr.Message)
	}
	if len(got) != 0 {
		t.Fatalf("tags outlived the VPC: %#v", got)
	}

	all, aerr := h.store.listAllTags(ctx)
	if aerr != nil {
		t.Fatalf("listAllTags: %v", aerr.Message)
	}
	if _, ok := all["vpc-doomed"]; ok {
		t.Fatal("DescribeTags would still report the deleted VPC's tags")
	}
}

// Tags were rendered straight out of a Go map, so a client polling a describe
// saw them reorder between identical calls. Anything diffing the response —
// CloudFormation drift, a golden file, a test — sees churn that is not there.
func TestDescribeVpcs_tagOrderIsStable(t *testing.T) {
	h := defaultVPCHandler(t)
	taggedVPC(t, h, "vpc-mine", map[string]string{
		"Name": "overcast-local", "owner": "scripts", "env": "dev", "team": "infra",
	})

	var first []string
	for range 8 {
		var resp xmlDescribeVpcsResponse
		if err := xml.Unmarshal(describe(t, h, h.DescribeVpcs, url.Values{}), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		var keys []string
		for _, v := range resp.VpcSet {
			if v.VpcID != "vpc-mine" {
				continue
			}
			for _, tag := range v.TagSet {
				keys = append(keys, tag.Key)
			}
		}
		if first == nil {
			first = keys
			continue
		}
		if strings.Join(keys, ",") != strings.Join(first, ",") {
			t.Fatalf("tag order changed between calls: %v then %v", first, keys)
		}
	}
}
