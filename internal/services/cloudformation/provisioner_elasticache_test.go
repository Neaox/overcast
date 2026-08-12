package cloudformation

// provisioner_elasticache_test.go — which template properties reach ElastiCache.
//
// CacheSubnetGroupName is what places a replication group's container in a VPC.
// The provisioner dropped it at the template boundary, so a stack that put its
// cache in a VPC got one on the default plane instead — invisible while
// placement was additive, and a cache that cannot be isolated once naming a VPC
// restricts what a resource can reach.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
)

// fakeElastiCache is an ElastiCache Query endpoint that answers
// CreateReplicationGroup and records the form it was called with.
type fakeElastiCache struct {
	mu   sync.Mutex
	form url.Values
}

func (f *fakeElastiCache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/xml")

	switch action := r.Form.Get("Action"); action {
	case "CreateReplicationGroup":
		f.mu.Lock()
		f.form = r.Form
		f.mu.Unlock()
		id := r.Form.Get("ReplicationGroupId")
		fmt.Fprintf(w, `<CreateReplicationGroupResponse><CreateReplicationGroupResult><ReplicationGroup>`+
			`<ReplicationGroupId>%s</ReplicationGroupId>`+
			`<Status>creating</Status>`+
			`<ARN>arn:aws:elasticache:us-east-1:000000000000:replicationgroup:%s</ARN>`+
			`<ConfigurationEndpoint><Address>%s.cache.overcast.test</Address><Port>6379</Port></ConfigurationEndpoint>`+
			`</ReplicationGroup></CreateReplicationGroupResult></CreateReplicationGroupResponse>`, id, id, id)

	default:
		http.Error(w, "unexpected action "+action, http.StatusBadRequest)
	}
}

func (f *fakeElastiCache) createForm() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.form
}

func TestElastiCacheReplicationGroupCreate_forwardsCacheSubnetGroupName(t *testing.T) {
	// Given: a template that puts the replication group in a subnet group
	f := &fakeElastiCache{}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::ReplicationGroup"}, map[string]any{
			"ReplicationGroupId":          "app-cache",
			"ReplicationGroupDescription": "app cache",
			"CacheSubnetGroupName":        "cache-subnets",
		}, rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if id != "app-cache" {
		t.Errorf("physical ID = %q, want %q", id, "app-cache")
	}

	// Then: ElastiCache was told which subnet group to place it in
	if got := f.createForm().Get("CacheSubnetGroupName"); got != "cache-subnets" {
		t.Errorf("CacheSubnetGroupName = %q, want %q — the cache lands outside the stack's VPC without it",
			got, "cache-subnets")
	}
}

// A template that names no subnet group must not invent one: the group belongs
// on the default plane, and sending an empty value would be a lookup that fails.
func TestElastiCacheReplicationGroupCreate_omitsAnAbsentSubnetGroup(t *testing.T) {
	f := &fakeElastiCache{}
	p, rCtx := newTestProvisioner(t, f)

	if _, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::ReplicationGroup"}, map[string]any{
			"ReplicationGroupId":          "bare-cache",
			"ReplicationGroupDescription": "bare cache",
		}, rCtx); err != nil {
		t.Fatalf("provisionResource: %v", err)
	}

	if _, present := f.createForm()["CacheSubnetGroupName"]; present {
		t.Error("CacheSubnetGroupName was sent, want it absent entirely")
	}
}
