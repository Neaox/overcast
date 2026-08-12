package elasticache

// replication_subnet_group_test.go — a replication group belongs to the VPC its
// subnet group belongs to.
//
// CacheSubnetGroupName was accepted on the wire and dropped before it reached
// the record, so a replication group always landed on the default plane. While
// placement was additive that was invisible; now that naming a VPC restricts,
// it is the one container-backed resource that cannot be placed in a VPC at
// all — reachable from everywhere, and impossible to isolate.
//
// Both create paths are covered because they are separate code: the Query
// handler reads a form, the typed path unmarshals a request, and either one
// dropping the field reintroduces the gap on its own.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// ecTestService builds a service with a subnet group already in place, which is
// the shape CloudFormation produces: the group is created before anything that
// names it.
func ecTestService(t *testing.T, groupName, vpcID string) *Service {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012", Network: "overcast"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if groupName != "" {
		if aerr := s.handler.store.putCacheSubnetGroup(context.Background(), &CacheSubnetGroup{
			CacheSubnetGroupName: groupName,
			VpcId:                vpcID,
		}); aerr != nil {
			t.Fatalf("putCacheSubnetGroup: %s", aerr.Message)
		}
	}
	return s
}

// postForm drives the Query handler the way a signed AWS client would.
func postForm(t *testing.T, h http.HandlerFunc, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestCreateReplicationGroup_queryPathKeepsTheSubnetGroup(t *testing.T) {
	s := ecTestService(t, "cache-subnets", "vpc-0abc")

	rec := postForm(t, s.handler.CreateReplicationGroup, url.Values{
		"ReplicationGroupId":          {"app-cache"},
		"ReplicationGroupDescription": {"app cache"},
		"CacheSubnetGroupName":        {"cache-subnets"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateReplicationGroup: status %d, body %s", rec.Code, rec.Body.String())
	}

	rg, aerr := s.handler.store.getReplicationGroup(context.Background(), "app-cache")
	if aerr != nil {
		t.Fatalf("getReplicationGroup: %s", aerr.Message)
	}
	if rg.CacheSubnetGroupName != "cache-subnets" {
		t.Errorf("CacheSubnetGroupName = %q, want %q — the group cannot be placed in a VPC without it",
			rg.CacheSubnetGroupName, "cache-subnets")
	}
}

func TestCreateReplicationGroup_typedPathKeepsTheSubnetGroup(t *testing.T) {
	s := ecTestService(t, "cache-subnets", "vpc-0abc")

	if _, aerr := s.handler.createReplicationGroupTyped(context.Background(), &ecCreateReplicationGroupReq{
		ReplicationGroupId:          "app-cache",
		ReplicationGroupDescription: "app cache",
		CacheSubnetGroupName:        "cache-subnets",
	}); aerr != nil {
		t.Fatalf("createReplicationGroupTyped: %s", aerr.Message)
	}

	rg, _ := s.handler.store.getReplicationGroup(context.Background(), "app-cache")
	if rg.CacheSubnetGroupName != "cache-subnets" {
		t.Errorf("CacheSubnetGroupName = %q, want %q", rg.CacheSubnetGroupName, "cache-subnets")
	}
}

// The stored name is only worth carrying if it resolves to a VPC, which is what
// the container attachment actually places on.
func TestReplicationGroup_subnetGroupResolvesToItsVPC(t *testing.T) {
	s := ecTestService(t, "cache-subnets", "vpc-0abc")

	if _, aerr := s.handler.createReplicationGroupTyped(context.Background(), &ecCreateReplicationGroupReq{
		ReplicationGroupId:   "app-cache",
		CacheSubnetGroupName: "cache-subnets",
	}); aerr != nil {
		t.Fatalf("createReplicationGroupTyped: %s", aerr.Message)
	}

	rg, _ := s.handler.store.getReplicationGroup(context.Background(), "app-cache")
	if got := s.handler.vpcForSubnetGroup(context.Background(), rg.CacheSubnetGroupName); got != "vpc-0abc" {
		t.Errorf("vpcForSubnetGroup = %q, want %q — the container would land on the default plane", got, "vpc-0abc")
	}
}

// A group naming no subnet group is unchanged: it stays on the default plane,
// which is where AWS puts a cache outside a VPC too.
func TestCreateReplicationGroup_withoutASubnetGroupStaysOnTheDefaultPlane(t *testing.T) {
	s := ecTestService(t, "", "")

	if _, aerr := s.handler.createReplicationGroupTyped(context.Background(), &ecCreateReplicationGroupReq{
		ReplicationGroupId: "bare-cache",
	}); aerr != nil {
		t.Fatalf("createReplicationGroupTyped: %s", aerr.Message)
	}

	rg, _ := s.handler.store.getReplicationGroup(context.Background(), "bare-cache")
	if rg.CacheSubnetGroupName != "" {
		t.Errorf("CacheSubnetGroupName = %q, want empty", rg.CacheSubnetGroupName)
	}
	if got := s.handler.vpcForSubnetGroup(context.Background(), rg.CacheSubnetGroupName); got != "" {
		t.Errorf("vpcForSubnetGroup = %q, want empty", got)
	}
}
