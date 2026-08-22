package ec2

// tag_validation_test.go — EC2 validates its tags.
//
// EC2 was one of the services that hand-rolled tag storage and never reached
// for the shared validator, so it accepted a 300-character key and a reserved
// aws: prefix that AWS refuses. Both wire paths validate now, and a create call
// is refused before it makes anything.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// createTags drives the Query-protocol CreateTags the way DispatchQuery does,
// and returns the recorded response.
func createTags(t *testing.T, h *Handler, resourceID string, tags map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	p := url.Values{}
	p.Set("ResourceId.1", resourceID)
	i := 1
	for k, v := range tags {
		p.Set(fmt.Sprintf("Tag.%d.Key", i), k)
		p.Set(fmt.Sprintf("Tag.%d.Value", i), v)
		i++
	}
	r := httptest.NewRequest(http.MethodGet, "/?"+p.Encode(), nil)
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	w := httptest.NewRecorder()
	h.CreateTags(w, r)
	return w
}

func TestCreateTags_rejectsWhatAWSWouldReject(t *testing.T) {
	cases := []struct {
		name     string
		tags     map[string]string
		wantCode string
	}{
		{name: "an ordinary tag", tags: map[string]string{"Name": "overcast-local"}},
		{name: "key over 128 characters", tags: map[string]string{strings.Repeat("k", 129): "v"}, wantCode: "InvalidParameterValue"},
		{name: "value over 256 characters", tags: map[string]string{"k": strings.Repeat("v", 257)}, wantCode: "InvalidParameterValue"},
		{name: "the reserved aws: prefix", tags: map[string]string{"aws:owner": "v"}, wantCode: "InvalidParameterValue"},
		{name: "a character the model forbids", tags: map[string]string{"env!": "v"}, wantCode: "InvalidParameterValue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a VPC to tag
			h := defaultVPCHandler(t)
			taggedVPC(t, h, "vpc-target", nil)

			// When: the tags are written
			w := createTags(t, h, "vpc-target", tc.tags)

			// Then: a rejected set is refused, and nothing is stored
			if tc.wantCode == "" {
				if w.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
				}
				return
			}
			if w.Code == http.StatusOK {
				t.Fatalf("status = 200, want a refusal: %s", w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.wantCode) {
				t.Fatalf("body = %s, want %s", w.Body.String(), tc.wantCode)
			}
			stored, aerr := h.store.getTags(context.Background(), "vpc-target")
			if aerr != nil {
				t.Fatalf("getTags: %v", aerr.Message)
			}
			if len(stored) != 0 {
				t.Fatalf("stored tags = %v, want none — a rejected set was written anyway", stored)
			}
		})
	}
}

// The aws: prefix is reserved from callers, not from AWS. Auto Scaling stamps
// aws:autoscaling:groupName on every instance it launches, through the same
// RunInstances call a customer makes — so refusing the prefix outright stopped
// every Auto Scaling launch dead.
func TestRunInstances_acceptsTheTagAutoScalingStamps(t *testing.T) {
	// Given: a launch carrying the key Auto Scaling propagates
	h := defaultVPCHandler(t)
	p := url.Values{}
	p.Set("ImageId", "ami-12345678")
	p.Set("MinCount", "1")
	p.Set("MaxCount", "1")
	p.Set("TagSpecification.1.ResourceType", "instance")
	p.Set("TagSpecification.1.Tag.1.Key", "aws:autoscaling:groupName")
	p.Set("TagSpecification.1.Tag.1.Value", "web")
	r := httptest.NewRequest(http.MethodGet, "/?"+p.Encode(), nil)
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	// When: it is run
	w := httptest.NewRecorder()
	h.RunInstances(w, r)

	// Then: the instance launches and carries the tag
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	instances, aerr := h.store.listInstances(context.Background())
	if aerr != nil {
		t.Fatalf("listInstances: %v", aerr.Message)
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(instances))
	}
	stored, aerr := h.store.getTags(context.Background(), instances[0].InstanceID)
	if aerr != nil {
		t.Fatalf("getTags: %v", aerr.Message)
	}
	if stored["aws:autoscaling:groupName"] != "web" {
		t.Fatalf("tags = %v, want aws:autoscaling:groupName=web", stored)
	}
}

// doForm drives a Query-protocol handler the way DispatchQuery does, without
// asserting on the response — the caller decides what a pass or a refusal
// looks like.
func doForm(t *testing.T, fn http.HandlerFunc, params url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/?"+params.Encode(), nil)
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	w := httptest.NewRecorder()
	fn(w, r)
	return w
}

// tagSpecParams builds the TagSpecification.1.ResourceType /
// TagSpecification.1.Tag.1.{Key,Value} shape every EC2 create call accepts.
func tagSpecParams(resourceType, key, value string) url.Values {
	p := url.Values{}
	p.Set("TagSpecification.1.ResourceType", resourceType)
	p.Set("TagSpecification.1.Tag.1.Key", key)
	p.Set("TagSpecification.1.Tag.1.Value", value)
	return p
}

// TestCreate_tagsAppliedAtCreate covers issue #1196 (Axis B) for the nine EC2
// create operations that used to ignore TagSpecification.N entirely: a tag
// carried at create must be stored immediately, and an invalid tag must
// refuse the whole call rather than leave the resource behind (or, for
// CreateVpcPeeringConnection, leave a second VPC pair created too).
func TestCreate_tagsAppliedAtCreate(t *testing.T) {
	cases := []struct {
		name         string
		setup        func(h *Handler) url.Values // returns the base params, prereqs already created
		invoke       func(h *Handler) http.HandlerFunc
		resourceType string
		countAfter   func(h *Handler) int // count of the resource after a valid create
	}{
		{
			name:         "CreateVpc",
			setup:        func(h *Handler) url.Values { p := url.Values{}; p.Set("CidrBlock", "10.50.0.0/16"); return p },
			invoke:       func(h *Handler) http.HandlerFunc { return h.CreateVpc },
			resourceType: "vpc",
			countAfter: func(h *Handler) int {
				vpcs, _ := h.store.listVPCs(context.Background())
				return len(vpcs)
			},
		},
		{
			name: "CreateSubnet",
			setup: func(h *Handler) url.Values {
				mustPutVPC(t, h, "vpc-for-subnet")
				p := url.Values{}
				p.Set("VpcId", "vpc-for-subnet")
				p.Set("CidrBlock", "10.51.0.0/24")
				return p
			},
			invoke:       func(h *Handler) http.HandlerFunc { return h.CreateSubnet },
			resourceType: "subnet",
			countAfter: func(h *Handler) int {
				subs, _ := h.store.listSubnets(context.Background())
				return len(subs)
			},
		},
		{
			name:         "CreateSecurityGroup",
			setup:        func(h *Handler) url.Values { p := url.Values{}; p.Set("GroupName", "sg-test"); return p },
			invoke:       func(h *Handler) http.HandlerFunc { return h.CreateSecurityGroup },
			resourceType: "security-group",
			countAfter: func(h *Handler) int {
				sgs, _ := h.store.listSecurityGroups(context.Background())
				return len(sgs)
			},
		},
		{
			name:         "CreateInternetGateway",
			setup:        func(h *Handler) url.Values { return url.Values{} },
			invoke:       func(h *Handler) http.HandlerFunc { return h.CreateInternetGateway },
			resourceType: "internet-gateway",
			countAfter: func(h *Handler) int {
				igws, _ := h.store.listInternetGateways(context.Background())
				return len(igws)
			},
		},
		{
			name: "CreateRouteTable",
			setup: func(h *Handler) url.Values {
				mustPutVPC(t, h, "vpc-for-rt")
				p := url.Values{}
				p.Set("VpcId", "vpc-for-rt")
				return p
			},
			invoke:       func(h *Handler) http.HandlerFunc { return h.CreateRouteTable },
			resourceType: "route-table",
			countAfter: func(h *Handler) int {
				rts, _ := h.store.listRouteTables(context.Background())
				return len(rts)
			},
		},
		{
			name: "CreateVpcEndpoint",
			setup: func(h *Handler) url.Values {
				mustPutVPC(t, h, "vpc-for-vpce")
				p := url.Values{}
				p.Set("VpcId", "vpc-for-vpce")
				p.Set("ServiceName", "com.amazonaws.us-east-1.s3")
				return p
			},
			invoke:       func(h *Handler) http.HandlerFunc { return h.CreateVpcEndpoint },
			resourceType: "vpc-endpoint",
			countAfter: func(h *Handler) int {
				eps, _ := h.store.listVpcEndpoints(context.Background())
				return len(eps)
			},
		},
		{
			name: "CreateVpcPeeringConnection",
			setup: func(h *Handler) url.Values {
				mustPutVPC(t, h, "vpc-pcx-requester")
				mustPutVPC(t, h, "vpc-pcx-accepter")
				p := url.Values{}
				p.Set("VpcId", "vpc-pcx-requester")
				p.Set("PeerVpcId", "vpc-pcx-accepter")
				return p
			},
			invoke:       func(h *Handler) http.HandlerFunc { return h.CreateVpcPeeringConnection },
			resourceType: "vpc-peering-connection",
			countAfter: func(h *Handler) int {
				pcxs, _ := h.store.listVpcPeeringConnections(context.Background())
				return len(pcxs)
			},
		},
		{
			name: "CreateNetworkInterface",
			setup: func(h *Handler) url.Values {
				mustPutVPC(t, h, "vpc-for-eni")
				if aerr := h.store.putSubnet(context.Background(), &Subnet{
					SubnetID: "subnet-for-eni", VpcID: "vpc-for-eni", CidrBlock: "10.52.0.0/24", State: "available",
				}); aerr != nil {
					t.Fatalf("putSubnet: %v", aerr.Message)
				}
				p := url.Values{}
				p.Set("SubnetId", "subnet-for-eni")
				return p
			},
			invoke:       func(h *Handler) http.HandlerFunc { return h.CreateNetworkInterface },
			resourceType: "network-interface",
			countAfter: func(h *Handler) int {
				enis, _ := h.store.listNetworkInterfaces(context.Background())
				return len(enis)
			},
		},
		{
			name:         "CreateKeyPair",
			setup:        func(h *Handler) url.Values { p := url.Values{}; p.Set("KeyName", "kp-test"); return p },
			invoke:       func(h *Handler) http.HandlerFunc { return h.CreateKeyPair },
			resourceType: "key-pair",
			countAfter: func(h *Handler) int {
				kps, _ := h.store.listKeyPairs(context.Background())
				return len(kps)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"_valid", func(t *testing.T) {
			// Given: a create call whose TagSpecification carries a valid tag
			h := defaultVPCHandler(t)
			p := tagSpecParams(tc.resourceType, "env", "prod")
			for k, v := range tc.setup(h) {
				p[k] = v
			}

			// When: it is invoked
			w := doForm(t, tc.invoke(h), p)

			// Then: it succeeds and exactly one resource carries the tag
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
			}
			if got := tc.countAfter(h); got != 1 {
				t.Fatalf("resource count = %d, want 1", got)
			}
			all, aerr := h.store.listAllTags(context.Background())
			if aerr != nil {
				t.Fatalf("listAllTags: %v", aerr.Message)
			}
			found := false
			for _, tags := range all {
				if tags["env"] == "prod" {
					found = true
				}
			}
			if !found {
				t.Fatalf("no resource carries env=prod after create: %v", all)
			}
		})

		t.Run(tc.name+"_invalidTagRejected", func(t *testing.T) {
			// Given: a create call whose TagSpecification carries a reserved key
			h := defaultVPCHandler(t)
			p := tagSpecParams(tc.resourceType, "aws:reserved", "x")
			for k, v := range tc.setup(h) {
				p[k] = v
			}

			// When: it is invoked
			w := doForm(t, tc.invoke(h), p)

			// Then: it is refused, and no resource was created
			if w.Code == http.StatusOK {
				t.Fatalf("status = 200, want a refusal: %s", w.Body.String())
			}
			if got := tc.countAfter(h); got != 0 {
				t.Fatalf("resource count = %d, want 0 — a rejected tag still created one", got)
			}
		})
	}
}

// mustPutVPC seeds an untagged VPC directly in the store, bypassing
// CreateVpc's Docker network strategy — the tests above only need the VPC to
// exist for the resource-under-test's own prerequisite check.
func mustPutVPC(t *testing.T, h *Handler, id string) {
	t.Helper()
	if aerr := h.store.putVPC(context.Background(), &VPC{VpcID: id, State: "available", CidrBlock: "10.49.0.0/16"}); aerr != nil {
		t.Fatalf("putVPC: %v", aerr.Message)
	}
}

// A create call carrying a bad tag must fail before it makes anything: on AWS
// the instance is never launched, so Overcast must not leave one running with
// the tags the caller asked for missing.
func TestRunInstances_refusesABadTagBeforeLaunching(t *testing.T) {
	// Given: a run request whose TagSpecification carries a reserved key
	h := defaultVPCHandler(t)
	p := url.Values{}
	p.Set("ImageId", "ami-12345678")
	p.Set("MinCount", "1")
	p.Set("MaxCount", "1")
	p.Set("TagSpecification.1.ResourceType", "instance")
	p.Set("TagSpecification.1.Tag.1.Key", "aws:owner")
	p.Set("TagSpecification.1.Tag.1.Value", "someone")
	r := httptest.NewRequest(http.MethodGet, "/?"+p.Encode(), nil)
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	// When: it is run
	w := httptest.NewRecorder()
	h.RunInstances(w, r)

	// Then: it is refused, and no instance exists
	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want a refusal: %s", w.Body.String())
	}
	instances, aerr := h.store.listInstances(context.Background())
	if aerr != nil {
		t.Fatalf("listInstances: %v", aerr.Message)
	}
	if len(instances) != 0 {
		t.Fatalf("instances = %d, want none — a rejected tag still launched one", len(instances))
	}
}
