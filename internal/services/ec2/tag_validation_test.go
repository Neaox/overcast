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
