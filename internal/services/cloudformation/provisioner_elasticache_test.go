package cloudformation

// provisioner_elasticache_test.go — what crosses the template boundary, in both
// directions: which properties reach ElastiCache, and which attributes a GetAtt
// can read back.
//
// CacheSubnetGroupName is what places a replication group's container in a VPC.
// The provisioner dropped it at the template boundary, so a stack that put its
// cache in a VPC got one on the default plane instead — invisible while
// placement was additive, and a cache that cannot be isolated once naming a VPC
// restricts what a resource can reach.
//
// ClusterName and the endpoint attributes are the same boundary, and neither
// side of it fails loudly: a property read under a name the resource does not
// have simply never arrives, and an attribute never exported resolves to the
// physical ID rather than to nothing.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeElastiCache is an ElastiCache Query endpoint that answers the create and
// describe calls a cache resource makes, records the create form, and walks the
// cache through a scripted sequence of statuses — one per describe, with the
// last repeating. Real ElastiCache creates asynchronously and answers with the
// cache in "creating", which is the shape the stabilization wait exists for.
type fakeElastiCache struct {
	mu   sync.Mutex
	form url.Values

	// script drives DescribeCacheClusters / DescribeReplicationGroups /
	// DescribeServerlessCaches. Empty means "available on the first ask".
	script statusScript
	// gone answers describe with an empty result, as a cache deleted from
	// under the stack does.
	gone bool
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

	case "DescribeReplicationGroups":
		if f.gone {
			fmt.Fprint(w, `<DescribeReplicationGroupsResponse><DescribeReplicationGroupsResult>`+
				`<ReplicationGroups></ReplicationGroups>`+
				`</DescribeReplicationGroupsResult></DescribeReplicationGroupsResponse>`)
			return
		}
		fmt.Fprintf(w, `<DescribeReplicationGroupsResponse><DescribeReplicationGroupsResult>`+
			`<ReplicationGroups><ReplicationGroup>`+
			`<ReplicationGroupId>%s</ReplicationGroupId><Status>%s</Status>`+
			`</ReplicationGroup></ReplicationGroups>`+
			`</DescribeReplicationGroupsResult></DescribeReplicationGroupsResponse>`,
			r.Form.Get("ReplicationGroupId"), f.script.next("available"))

	case "CreateCacheCluster":
		f.mu.Lock()
		f.form = r.Form
		f.mu.Unlock()
		id := r.Form.Get("CacheClusterId")
		fmt.Fprintf(w, `<CreateCacheClusterResponse><CreateCacheClusterResult><CacheCluster>`+
			`<CacheClusterId>%s</CacheClusterId>`+
			`<CacheClusterStatus>creating</CacheClusterStatus>`+
			`<ARN>arn:aws:elasticache:us-east-1:000000000000:cluster:%s</ARN>`+
			`<ConfigurationEndpoint><Address>%s.cfg.cache.overcast.test</Address><Port>11211</Port></ConfigurationEndpoint>`+
			`</CacheCluster></CreateCacheClusterResult></CreateCacheClusterResponse>`, id, id, id)

	case "DescribeCacheClusters":
		if f.gone {
			fmt.Fprint(w, `<DescribeCacheClustersResponse><DescribeCacheClustersResult>`+
				`<CacheClusters></CacheClusters>`+
				`</DescribeCacheClustersResult></DescribeCacheClustersResponse>`)
			return
		}
		fmt.Fprintf(w, `<DescribeCacheClustersResponse><DescribeCacheClustersResult>`+
			`<CacheClusters><CacheCluster>`+
			`<CacheClusterId>%s</CacheClusterId><CacheClusterStatus>%s</CacheClusterStatus>`+
			`</CacheCluster></CacheClusters>`+
			`</DescribeCacheClustersResult></DescribeCacheClustersResponse>`,
			r.Form.Get("CacheClusterId"), f.script.next("available"))

	case "CreateServerlessCache":
		f.mu.Lock()
		f.form = r.Form
		f.mu.Unlock()
		name := r.Form.Get("ServerlessCacheName")
		fmt.Fprintf(w, `<CreateServerlessCacheResponse><CreateServerlessCacheResult><ServerlessCache>`+
			`<ServerlessCacheName>%s</ServerlessCacheName><Status>CREATING</Status>`+
			`<ARN>arn:aws:elasticache:us-east-1:000000000000:serverlesscache:%s</ARN>`+
			`</ServerlessCache></CreateServerlessCacheResult></CreateServerlessCacheResponse>`, name, name)

	case "DescribeServerlessCaches":
		fmt.Fprintf(w, `<DescribeServerlessCachesResponse><DescribeServerlessCachesResult>`+
			`<ServerlessCaches><ServerlessCache>`+
			`<ServerlessCacheName>%s</ServerlessCacheName><Status>%s</Status>`+
			`</ServerlessCache></ServerlessCaches>`+
			`</DescribeServerlessCachesResult></DescribeServerlessCachesResponse>`,
			r.Form.Get("ServerlessCacheName"), f.script.next("AVAILABLE"))

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

// ── naming and endpoint attributes ─────────────────────────────────────────

// AWS::ElastiCache::CacheCluster names its cluster with ClusterName. The
// handler read CacheClusterId — the name of the *API* parameter, and not a
// property the resource has at all — so every template that named its cluster
// got a generated name instead, and the endpoint the stack advertised was for a
// cluster nobody had asked for.
func TestElastiCacheCacheClusterCreate_namesTheClusterFromClusterName(t *testing.T) {
	// Given: a template that names its cluster
	f := &fakeElastiCache{}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::CacheCluster"}, cacheClusterProps("app-cache"), rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}

	// Then: that is the cluster's name, service-side and as the physical ID
	if got := f.createForm().Get("CacheClusterId"); got != "app-cache" {
		t.Errorf("CacheClusterId = %q, want %q — ClusterName was dropped at the template boundary", got, "app-cache")
	}
	if id != "app-cache" {
		t.Errorf("physical ID = %q, want %q", id, "app-cache")
	}
}

// A template that names no cluster gets CloudFormation's generated name: the
// stack, the logical ID and a random suffix. It got "<stack>-cache", which
// carried neither of the last two — so two unnamed clusters in one stack were
// both handed the same ID, and the second create failed on a name the template
// never mentions.
func TestElastiCacheCacheClusterCreate_generatesACloudFormationShapedName(t *testing.T) {
	// Given: two clusters in one stack, neither of them named
	f := &fakeElastiCache{}
	p, rCtx := newTestProvisioner(t, f)
	props := cacheClusterProps("")
	delete(props, "ClusterName")

	// When: CloudFormation provisions both
	first, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::CacheCluster"}, props, rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	second, err := p.provisionResource(context.Background(), "Sessions",
		TemplateResource{Type: "AWS::ElastiCache::CacheCluster"}, props, rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}

	// Then: each is named after its own resource, and ElastiCache would accept
	// it — a cluster ID is lowercase and at most 50 characters.
	suffix := `-[a-z0-9]{` + strconv.Itoa(physicalIDSuffixLen) + `}$`
	for logicalID, id := range map[string]string{"Cache": first, "Sessions": second} {
		want := regexp.MustCompile(`^` + regexp.QuoteMeta(strings.ToLower(rCtx.StackName+"-"+logicalID)) + suffix)
		if !want.MatchString(id) {
			t.Errorf("%s physical ID = %q, want %s", logicalID, id, want)
		}
		if len(id) > maxNameLenCache {
			t.Errorf("%s physical ID %q is %d characters, over ElastiCache's %d",
				logicalID, id, len(id), maxNameLenCache)
		}
	}
	if first == second {
		t.Errorf("both clusters were named %q; the second create collides with the first", first)
	}
}

// Fn::GetAtt on RedisEndpoint.Address is how a Redis cluster's host reaches the
// code that dials it — CDK's attrRedisEndpointAddress, baked into an ECS task
// definition as REDIS_HOST. The handler exported the ConfigurationEndpoint pair
// alone, so the GetAtt fell through to resolveGetAtt's physical-ID fallback and
// every task came up pointed at a bare cluster ID, which is a name no container
// advertises and nothing can resolve.
//
// Which pair carries the endpoint follows the engine, as AWS populates them.
// The other pair is exported empty rather than left out, because "left out" is
// exactly what produced the bare cluster ID.
func TestElastiCacheCacheClusterCreate_exportsTheEndpointPairItsEngineHas(t *testing.T) {
	for _, tc := range []struct{ engine, populated, absent string }{
		{"redis", "RedisEndpoint", "ConfigurationEndpoint"},
		{"valkey", "RedisEndpoint", "ConfigurationEndpoint"},
		{"memcached", "ConfigurationEndpoint", "RedisEndpoint"},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			// Given: a cluster whose endpoint the emulator mints at create time
			f := &fakeElastiCache{}
			p, rCtx := newTestProvisioner(t, f)
			props := cacheClusterProps("appcache")
			props["Engine"] = tc.engine

			// When: CloudFormation provisions it
			id, err := p.provisionResource(context.Background(), "Cache",
				TemplateResource{Type: "AWS::ElastiCache::CacheCluster"}, props, rCtx)
			if err != nil {
				t.Fatalf("provisionResource: %v", err)
			}
			rCtx.Resources = map[string]string{"Cache": id}

			// Then: the pair this engine has carries the endpoint
			for attr, want := range map[string]string{
				tc.populated + ".Address": "appcache.cfg.cache.overcast.test",
				tc.populated + ".Port":    "11211",
			} {
				if got := resolveGetAtt("Cache."+attr, rCtx); got != want {
					t.Errorf("GetAtt %s = %v, want %q", attr, got, want)
				}
			}

			// And: the pair it does not have resolves empty rather than to the
			// physical ID, which is a hostname nothing can resolve and which
			// reads as a working value all the way to the failed connection
			for _, attr := range []string{tc.absent + ".Address", tc.absent + ".Port"} {
				if got := resolveGetAtt("Cache."+attr, rCtx); got != "" {
					t.Errorf("GetAtt %s = %v, want empty — a %s cluster has no %s on AWS either",
						attr, got, tc.engine, tc.absent)
				}
			}
		})
	}
}

// ── stabilization ──────────────────────────────────────────────────────────
//
// CreateCacheCluster is asynchronous: it answers with the cluster in "creating"
// and the engine comes up behind it. The handler returned there and exported
// ConfigurationEndpoint.Address out of the create response, so a stack reported
// CREATE_COMPLETE — and everything with a GetAtt on that endpoint ran — while
// there was nothing listening on it.

func cacheClusterProps(id string) map[string]any {
	return map[string]any{
		"ClusterName":   id,
		"Engine":        "redis",
		"CacheNodeType": "cache.t3.micro",
		"NumCacheNodes": 1,
	}
}

// The resource is not done until the cache answers. It must keep asking rather
// than take CreateCacheCluster's own "creating" as the end of it.
func TestElastiCacheCacheClusterCreate_waitsForTheClusterToBecomeAvailable(t *testing.T) {
	// Given: a cache that is still creating for its first two status checks
	f := &fakeElastiCache{script: statusScript{statuses: []string{"creating", "creating", "available"}}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::CacheCluster"}, cacheClusterProps("appcache"), rCtx)

	// Then: it completed only once the cluster was available
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if id != "appcache" {
		t.Errorf("physical ID = %q, want %q", id, "appcache")
	}
	if got := f.script.count(); got != 3 {
		t.Errorf("DescribeCacheClusters calls = %d, want 3 — the resource completed before the "+
			"cache was available", got)
	}
	// The endpoint minted at create time has to survive the wait: a GetAtt on
	// it is the most common reason a resource depends on this one.
	if addr := rCtx.Attributes["Cache"]["RedisEndpoint.Address"]; addr == "" {
		t.Errorf("RedisEndpoint.Address is empty; got %v", rCtx.Attributes["Cache"])
	}
}

// A cache that reaches a status ElastiCache documents as terminal must fail the
// resource with that status, not be waited out and blamed on the clock.
//
// The status matters: a cache cluster has no "create-failed" — botocore's
// CacheClusterAvailable waiter fails on "deleted", "deleting",
// "incompatible-network" and "restore-failed", and API_CacheCluster documents
// no other. Testing against a status the service cannot produce would prove
// nothing about the classification a real cluster is read with.
func TestElastiCacheCacheClusterCreate_failsWithTheStatusTheClusterReached(t *testing.T) {
	// Given: a cache that cannot be placed on the network it was asked for
	f := &fakeElastiCache{script: statusScript{statuses: []string{"creating", "incompatible-network"}}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::CacheCluster"}, cacheClusterProps("brokencache"), rCtx)

	// Then: the resource fails, naming the status the cache reached
	if err == nil {
		t.Fatal("expected the resource to fail for a cache that went to incompatible-network")
	}
	if !strings.Contains(err.Error(), "incompatible-network") {
		t.Errorf("expected the cache's own status in the reason, got %v", err)
	}
	if strings.Contains(err.Error(), "did not become available within") {
		t.Errorf("a terminal status was reported as a timeout: %v", err)
	}
	// The cache exists — create named it before it failed — and rollback
	// deletes it by that name.
	if id != "brokencache" {
		t.Errorf("physical ID = %q, want %q — a cache that failed to come up still exists", id, "brokencache")
	}
	if got := f.script.count(); got != 2 {
		t.Errorf("DescribeCacheClusters calls = %d, want 2 — the wait kept polling past a terminal status", got)
	}
}

// A cache that never becomes available must not leave the stack CREATE_COMPLETE
// around it. The stack fails and rolls back, and the reason says what it was
// waiting for.
func TestElastiCacheCacheClusterCreate_neverAvailableRollsTheStackBack(t *testing.T) {
	// Given: a cache stuck in "creating" for as long as anyone asks
	f := &fakeElastiCache{script: statusScript{statuses: []string{"creating"}}}
	p, _ := newTestProvisioner(t, f, newPollDrivenClock())
	stack := &Stack{
		StackName: "cache-stack",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/cache-stack/1111",
		Region:    "us-east-1",
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"Cache": {Type: "AWS::ElastiCache::CacheCluster", Properties: cacheClusterProps("stuckcache")},
	}}

	// When: the stack is provisioned
	p.provisionStackResources(stack, tmpl)

	// Then: it does not complete — it rolls back, saying what stalled
	if stack.Status != StatusRollbackComplete {
		t.Fatalf("stack status = %q, want %q — a stack completed around a cache that never came up",
			stack.Status, StatusRollbackComplete)
	}
	if len(stack.Resources) == 0 {
		t.Fatal("no resource recorded for the failed cache")
	}
	reason := stack.Resources[0].StatusReason
	for _, want := range []string{"stuckcache", "become available", "creating"} {
		if !strings.Contains(reason, want) {
			t.Errorf("resource status reason %q does not mention %q", reason, want)
		}
	}
}

// A cache deleted from under the stack is not going to become available, and
// waiting out the budget for one only delays saying so.
func TestElastiCacheCacheClusterCreate_failsWhenTheClusterDisappears(t *testing.T) {
	f := &fakeElastiCache{gone: true}
	p, rCtx := newTestProvisioner(t, f)

	_, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::CacheCluster"}, cacheClusterProps("vanishingcache"), rCtx)
	if err == nil {
		t.Fatal("expected the resource to fail for a cache that no longer exists")
	}
	if !strings.Contains(err.Error(), "vanishingcache") {
		t.Errorf("expected the failure to name the cache, got %v", err)
	}
}

// A deployment with no ElastiCache container runtime — the default for a test
// server and for anyone running Overcast without a daemon — is not a special
// case. The wait used to be skipped there, because ElastiCache left a cache
// with no container coming in "creating" and waiting for it would have held
// every cache stack open for the full budget before rolling it back.
// ElastiCache now marks such a cache available at once — there is no engine
// being claimed — so the wait runs everywhere and gets its answer immediately.
func TestElastiCacheCacheClusterCreate_withoutAContainerRuntimeStillWaitsAndCompletes(t *testing.T) {
	// Given: a deployment with no ElastiCache container runtime, where the
	// service answers "available" because nothing is coming that could change it
	f := &fakeElastiCache{script: statusScript{statuses: []string{"available"}}}
	p, rCtx := newTestProvisioner(t, f, newPollDrivenClock())
	p.cfg.ElastiCacheDockerSocket = ""

	// When: CloudFormation provisions a cache
	id, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::CacheCluster"}, cacheClusterProps("mockcache"), rCtx)

	// Then: it completes, having actually asked
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if id != "mockcache" {
		t.Errorf("physical ID = %q, want %q", id, "mockcache")
	}
	if got := f.script.count(); got != 1 {
		t.Errorf("DescribeCacheClusters calls = %d, want 1 — the wait no longer skips a socketless deployment", got)
	}
}

// A replication group settles on the same rule, reading ReplicationGroup's
// "Status" rather than CacheCluster's "CacheClusterStatus".
func TestElastiCacheReplicationGroupCreate_waitsForTheGroupToBecomeAvailable(t *testing.T) {
	f := &fakeElastiCache{script: statusScript{statuses: []string{"creating", "available"}}}
	p, rCtx := newTestProvisioner(t, f)

	if _, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::ReplicationGroup"}, map[string]any{
			"ReplicationGroupId":          "app-cache",
			"ReplicationGroupDescription": "app cache",
		}, rCtx); err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if got := f.script.count(); got != 2 {
		t.Errorf("DescribeReplicationGroups calls = %d, want 2 — the resource completed before the "+
			"group was available", got)
	}
}

// A replication group whose engine never answers reaches "create-failed",
// which API_ReplicationGroup documents and Overcast now produces. AWS's
// ReplicationGroupAvailable waiter predates that status and fails only on
// "deleted", so classifying by the waiter alone would treat a group that has
// already given up as one still working — the stack would spend its whole
// budget and then report a timeout instead of the failure the group named.
func TestElastiCacheReplicationGroupCreate_failsOnCreateFailed(t *testing.T) {
	// Given: a replication group that gives up while it is being created
	f := &fakeElastiCache{script: statusScript{statuses: []string{"creating", "create-failed"}}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	_, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::ReplicationGroup"}, map[string]any{
			"ReplicationGroupId":          "broken-group",
			"ReplicationGroupDescription": "broken group",
		}, rCtx)

	// Then: the resource fails on the status, not on the clock
	if err == nil {
		t.Fatal("expected the resource to fail for a group that went to create-failed")
	}
	if !strings.Contains(err.Error(), "create-failed") {
		t.Errorf("expected the group's own status in the reason, got %v", err)
	}
	if strings.Contains(err.Error(), "did not become available within") {
		t.Errorf("a terminal status was reported as a timeout: %v", err)
	}
	if got := f.script.count(); got != 2 {
		t.Errorf("DescribeReplicationGroups calls = %d, want 2 — the wait kept polling past a "+
			"terminal status", got)
	}
}

// A serverless cache reports the same vocabulary in upper case, which is AWS's
// own inconsistency and not something a template author can do anything about.
// Matching literally would wait out the whole budget over a cache that was
// ready on the first ask.
func TestElastiCacheServerlessCacheCreate_acceptsTheUppercaseStatus(t *testing.T) {
	f := &fakeElastiCache{script: statusScript{statuses: []string{"CREATING", "AVAILABLE"}}}
	p, rCtx := newTestProvisioner(t, f)

	if _, err := p.provisionResource(context.Background(), "Cache",
		TemplateResource{Type: "AWS::ElastiCache::ServerlessCache"}, map[string]any{
			"ServerlessCacheName": "app-serverless",
			"Engine":              "redis",
		}, rCtx); err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if got := f.script.count(); got != 2 {
		t.Errorf("DescribeServerlessCaches calls = %d, want 2", got)
	}
}
