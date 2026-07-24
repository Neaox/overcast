package apigateway

// Tests for storage-plan.md item 3.1 (List+Get N+1 sweep), the apigateway
// portion. The list* functions below (listResources, listStages,
// listDeployments, listV2Routes, listV2Integrations, listV2Stages,
// listV2Deployments) already used a single Scan call before this pass; what
// they were missing was the malformed-persisted-state isolation half of the
// rule (CLAUDE.md): a single undecodable record must not fail the whole
// list. The deleteAll* helpers sitting next to each of them are a different
// shape entirely — List+Delete, no Get, nothing to unmarshal — so they were
// converted to prefer state.PrefixDeleter (a single ranged delete) with a
// List+Delete fallback, mirroring sqs.deleteMessagesByQueuePrefix and
// cloudformation.deleteStackEvents.

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// ---- Malformed-record isolation: list* functions --------------------------

func TestListResources_malformedRecord_skippedNotFatal(t *testing.T) {
	// Given: two well-formed resources and one malformed record under the same API
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()
	apiID := "api1"

	mustPutResource(t, s, ctx, apiID, "res1")
	mustPutResource(t, s, ctx, apiID, "res2")
	seedMalformed(t, store, ctx, nsResources, s.region(ctx), resourceKey(apiID, "res-corrupt"))

	// When: listResources is called
	resources, aerr := s.listResources(ctx, apiID)

	// Then: it succeeds and returns only the well-formed resources
	if aerr != nil {
		t.Fatalf("listResources: %v", aerr)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 well-formed resources, got %d: %+v", len(resources), resources)
	}
}

func TestListStages_malformedRecord_skippedNotFatal(t *testing.T) {
	// Given: two well-formed stages and one malformed record under the same API
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()
	apiID := "api1"

	mustPutStage(t, s, ctx, apiID, "dev")
	mustPutStage(t, s, ctx, apiID, "prod")
	seedMalformed(t, store, ctx, nsStages, s.region(ctx), stageKey(apiID, "corrupt"))

	// When: listStages is called
	stages, aerr := s.listStages(ctx, apiID)

	// Then: it succeeds and returns only the well-formed stages
	if aerr != nil {
		t.Fatalf("listStages: %v", aerr)
	}
	if len(stages) != 2 {
		t.Fatalf("expected 2 well-formed stages, got %d: %+v", len(stages), stages)
	}
}

func TestListDeployments_malformedRecord_skippedNotFatal(t *testing.T) {
	// Given: two well-formed deployments and one malformed record under the same API
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()
	apiID := "api1"

	mustPutDeployment(t, s, ctx, apiID, "dep1")
	mustPutDeployment(t, s, ctx, apiID, "dep2")
	seedMalformed(t, store, ctx, nsDeployments, s.region(ctx), deploymentKey(apiID, "corrupt"))

	// When: listDeployments is called
	deps, aerr := s.listDeployments(ctx, apiID)

	// Then: it succeeds and returns only the well-formed deployments
	if aerr != nil {
		t.Fatalf("listDeployments: %v", aerr)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 well-formed deployments, got %d: %+v", len(deps), deps)
	}
}

func TestListV2Routes_malformedRecord_skippedNotFatal(t *testing.T) {
	// Given: two well-formed routes and one malformed record under the same API
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()
	apiID := "api1"

	mustPutV2Route(t, s, ctx, apiID, "route1")
	mustPutV2Route(t, s, ctx, apiID, "route2")
	seedMalformed(t, store, ctx, nsV2Routes, s.region(ctx), apiID+"/route-corrupt")

	// When: listV2Routes is called
	routes, aerr := s.listV2Routes(ctx, apiID)

	// Then: it succeeds and returns only the well-formed routes
	if aerr != nil {
		t.Fatalf("listV2Routes: %v", aerr)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 well-formed routes, got %d: %+v", len(routes), routes)
	}
}

func TestListV2Integrations_malformedRecord_skippedNotFatal(t *testing.T) {
	// Given: two well-formed integrations and one malformed record under the same API
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()
	apiID := "api1"

	mustPutV2Integration(t, s, ctx, apiID, "integ1")
	mustPutV2Integration(t, s, ctx, apiID, "integ2")
	seedMalformed(t, store, ctx, nsV2Integ, s.region(ctx), apiID+"/integ-corrupt")

	// When: listV2Integrations is called
	integs, aerr := s.listV2Integrations(ctx, apiID)

	// Then: it succeeds and returns only the well-formed integrations
	if aerr != nil {
		t.Fatalf("listV2Integrations: %v", aerr)
	}
	if len(integs) != 2 {
		t.Fatalf("expected 2 well-formed integrations, got %d: %+v", len(integs), integs)
	}
}

func TestListV2Stages_malformedRecord_skippedNotFatal(t *testing.T) {
	// Given: two well-formed v2 stages and one malformed record under the same API
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()
	apiID := "api1"

	mustPutV2Stage(t, s, ctx, apiID, "dev")
	mustPutV2Stage(t, s, ctx, apiID, "prod")
	seedMalformed(t, store, ctx, nsV2Stages, s.region(ctx), stageKey(apiID, "corrupt"))

	// When: listV2Stages is called
	stages, aerr := s.listV2Stages(ctx, apiID)

	// Then: it succeeds and returns only the well-formed stages
	if aerr != nil {
		t.Fatalf("listV2Stages: %v", aerr)
	}
	if len(stages) != 2 {
		t.Fatalf("expected 2 well-formed stages, got %d: %+v", len(stages), stages)
	}
}

func TestListV2Deployments_malformedRecord_skippedNotFatal(t *testing.T) {
	// Given: two well-formed v2 deployments and one malformed record under the same API
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()
	apiID := "api1"

	mustPutV2Deployment(t, s, ctx, apiID, "dep1")
	mustPutV2Deployment(t, s, ctx, apiID, "dep2")
	seedMalformed(t, store, ctx, nsV2Deploys, s.region(ctx), deploymentKey(apiID, "corrupt"))

	// When: listV2Deployments is called
	deps, aerr := s.listV2Deployments(ctx, apiID)

	// Then: it succeeds and returns only the well-formed deployments
	if aerr != nil {
		t.Fatalf("listV2Deployments: %v", aerr)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 well-formed deployments, got %d: %+v", len(deps), deps)
	}
}

// ---- deleteAll*: prefix scoping preserved, malformed rows still deleted ----

func TestDeleteAllResources_removesOnlyMatchingPrefixIncludingMalformedRows(t *testing.T) {
	// Given: resources (one malformed) under api1, and one resource under api2
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()

	mustPutResource(t, s, ctx, "api1", "res1")
	seedMalformed(t, store, ctx, nsResources, s.region(ctx), resourceKey("api1", "res-corrupt"))
	mustPutResource(t, s, ctx, "api2", "res1")

	// When: deleteAllResources is called for api1
	if aerr := s.deleteAllResources(ctx, "api1"); aerr != nil {
		t.Fatalf("deleteAllResources: %v", aerr)
	}

	// Then: every api1 row (including the malformed one) is gone, api2's remains
	assertNamespaceEmptyForPrefix(t, store, ctx, nsResources, s.region(ctx), "api1/")
	assertNamespaceHasKey(t, store, ctx, nsResources, s.region(ctx), resourceKey("api2", "res1"))
}

func TestDeleteAllStages_removesOnlyMatchingPrefixIncludingMalformedRows(t *testing.T) {
	// Given: stages (one malformed) under api1, and one stage under api2
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()

	mustPutStage(t, s, ctx, "api1", "dev")
	seedMalformed(t, store, ctx, nsStages, s.region(ctx), stageKey("api1", "corrupt"))
	mustPutStage(t, s, ctx, "api2", "dev")

	// When: deleteAllStages is called for api1
	if aerr := s.deleteAllStages(ctx, "api1"); aerr != nil {
		t.Fatalf("deleteAllStages: %v", aerr)
	}

	// Then: every api1 row (including the malformed one) is gone, api2's remains
	assertNamespaceEmptyForPrefix(t, store, ctx, nsStages, s.region(ctx), "api1/")
	assertNamespaceHasKey(t, store, ctx, nsStages, s.region(ctx), stageKey("api2", "dev"))
}

func TestDeleteAllDeployments_removesOnlyMatchingPrefixIncludingMalformedRows(t *testing.T) {
	// Given: deployments (one malformed) under api1, and one deployment under api2
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()

	mustPutDeployment(t, s, ctx, "api1", "dep1")
	seedMalformed(t, store, ctx, nsDeployments, s.region(ctx), deploymentKey("api1", "corrupt"))
	mustPutDeployment(t, s, ctx, "api2", "dep1")

	// When: deleteAllDeployments is called for api1
	if aerr := s.deleteAllDeployments(ctx, "api1"); aerr != nil {
		t.Fatalf("deleteAllDeployments: %v", aerr)
	}

	// Then: every api1 row (including the malformed one) is gone, api2's remains
	assertNamespaceEmptyForPrefix(t, store, ctx, nsDeployments, s.region(ctx), "api1/")
	assertNamespaceHasKey(t, store, ctx, nsDeployments, s.region(ctx), deploymentKey("api2", "dep1"))
}

func TestDeleteAllV2Routes_removesOnlyMatchingPrefixIncludingMalformedRows(t *testing.T) {
	// Given: routes (one malformed) under api1, and one route under api2
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()

	mustPutV2Route(t, s, ctx, "api1", "route1")
	seedMalformed(t, store, ctx, nsV2Routes, s.region(ctx), "api1/route-corrupt")
	mustPutV2Route(t, s, ctx, "api2", "route1")

	// When: deleteAllV2Routes is called for api1
	if aerr := s.deleteAllV2Routes(ctx, "api1"); aerr != nil {
		t.Fatalf("deleteAllV2Routes: %v", aerr)
	}

	// Then: every api1 row (including the malformed one) is gone, api2's remains
	assertNamespaceEmptyForPrefix(t, store, ctx, nsV2Routes, s.region(ctx), "api1/")
	assertNamespaceHasKey(t, store, ctx, nsV2Routes, s.region(ctx), "api2/route1")
}

func TestDeleteAllV2Integrations_removesOnlyMatchingPrefixIncludingMalformedRows(t *testing.T) {
	// Given: integrations (one malformed) under api1, and one integration under api2
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()

	mustPutV2Integration(t, s, ctx, "api1", "integ1")
	seedMalformed(t, store, ctx, nsV2Integ, s.region(ctx), "api1/integ-corrupt")
	mustPutV2Integration(t, s, ctx, "api2", "integ1")

	// When: deleteAllV2Integrations is called for api1
	if aerr := s.deleteAllV2Integrations(ctx, "api1"); aerr != nil {
		t.Fatalf("deleteAllV2Integrations: %v", aerr)
	}

	// Then: every api1 row (including the malformed one) is gone, api2's remains
	assertNamespaceEmptyForPrefix(t, store, ctx, nsV2Integ, s.region(ctx), "api1/")
	assertNamespaceHasKey(t, store, ctx, nsV2Integ, s.region(ctx), "api2/integ1")
}

func TestDeleteAllV2Stages_removesOnlyMatchingPrefixIncludingMalformedRows(t *testing.T) {
	// Given: v2 stages (one malformed) under api1, and one stage under api2
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()

	mustPutV2Stage(t, s, ctx, "api1", "dev")
	seedMalformed(t, store, ctx, nsV2Stages, s.region(ctx), stageKey("api1", "corrupt"))
	mustPutV2Stage(t, s, ctx, "api2", "dev")

	// When: deleteAllV2Stages is called for api1
	if aerr := s.deleteAllV2Stages(ctx, "api1"); aerr != nil {
		t.Fatalf("deleteAllV2Stages: %v", aerr)
	}

	// Then: every api1 row (including the malformed one) is gone, api2's remains
	assertNamespaceEmptyForPrefix(t, store, ctx, nsV2Stages, s.region(ctx), "api1/")
	assertNamespaceHasKey(t, store, ctx, nsV2Stages, s.region(ctx), stageKey("api2", "dev"))
}

func TestDeleteAllV2Deployments_removesOnlyMatchingPrefixIncludingMalformedRows(t *testing.T) {
	// Given: v2 deployments (one malformed) under api1, and one deployment under api2
	store := state.NewMemoryStore()
	s := newAPIGatewayStore(store, "us-east-1")
	ctx := context.Background()

	mustPutV2Deployment(t, s, ctx, "api1", "dep1")
	seedMalformed(t, store, ctx, nsV2Deploys, s.region(ctx), deploymentKey("api1", "corrupt"))
	mustPutV2Deployment(t, s, ctx, "api2", "dep1")

	// When: deleteAllV2Deployments is called for api1
	if aerr := s.deleteAllV2Deployments(ctx, "api1"); aerr != nil {
		t.Fatalf("deleteAllV2Deployments: %v", aerr)
	}

	// Then: every api1 row (including the malformed one) is gone, api2's remains
	assertNamespaceEmptyForPrefix(t, store, ctx, nsV2Deploys, s.region(ctx), "api1/")
	assertNamespaceHasKey(t, store, ctx, nsV2Deploys, s.region(ctx), deploymentKey("api2", "dep1"))
}

// ---- PrefixDeleter fallback path -------------------------------------------

// noPrefixDeleteStore embeds the state.Store *interface*, not a concrete
// type, so it never promotes DeletePrefix even though the wrapped MemoryStore
// implements state.PrefixDeleter — this is the "store without ranged
// deletes" fixture, mirroring cloudformation's failingStore in
// internal/services/cloudformation/store_test.go.
type noPrefixDeleteStore struct {
	state.Store
}

func TestDeleteAllResources_fallbackPath_noPrefixDeleter(t *testing.T) {
	// Given: a store that does not implement state.PrefixDeleter
	fs := &noPrefixDeleteStore{Store: state.NewMemoryStore()}
	s := newAPIGatewayStore(fs, "us-east-1")
	ctx := context.Background()

	mustPutResource(t, s, ctx, "api1", "res1")
	mustPutResource(t, s, ctx, "api1", "res2")
	mustPutResource(t, s, ctx, "api2", "res1")

	// When: deleteAllResources is called for api1
	if aerr := s.deleteAllResources(ctx, "api1"); aerr != nil {
		t.Fatalf("deleteAllResources: %v", aerr)
	}

	// Then: the List+Delete fallback still removes every api1 resource, and
	// leaves api2's alone
	remaining, aerr := s.listResources(ctx, "api1")
	if aerr != nil {
		t.Fatalf("listResources: %v", aerr)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected no resources left for api1, got %+v", remaining)
	}
	other, aerr := s.listResources(ctx, "api2")
	if aerr != nil {
		t.Fatalf("listResources: %v", aerr)
	}
	if len(other) != 1 {
		t.Fatalf("expected api2's resource to survive, got %+v", other)
	}
}

// ---- Test helpers -----------------------------------------------------------

// seedMalformed writes a value that is not valid JSON directly into the
// store, bypassing the normal put* marshaling path, to simulate a persisted
// record that was corrupted or written by an incompatible version.
func seedMalformed(t *testing.T, store state.Store, ctx context.Context, ns, region, key string) {
	t.Helper()
	if err := store.Set(ctx, ns, serviceutil.RegionKey(region, key), "{not valid json"); err != nil {
		t.Fatalf("seed malformed record: %v", err)
	}
}

// assertNamespaceEmptyForPrefix asserts no keys remain under prefix in ns.
func assertNamespaceEmptyForPrefix(t *testing.T, store state.Store, ctx context.Context, ns, region, prefix string) {
	t.Helper()
	pairs, err := store.Scan(ctx, ns, serviceutil.RegionKey(region, prefix))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("expected no remaining keys under prefix %q in %q, got %+v", prefix, ns, pairs)
	}
}

// assertNamespaceHasKey asserts a specific key is still present in ns.
func assertNamespaceHasKey(t *testing.T, store state.Store, ctx context.Context, ns, region, key string) {
	t.Helper()
	_, found, err := store.Get(ctx, ns, serviceutil.RegionKey(region, key))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatalf("expected key %q to still be present in %q", key, ns)
	}
}

func mustPutResource(t *testing.T, s *apigatewayStore, ctx context.Context, apiID, id string) {
	t.Helper()
	if aerr := s.putResource(ctx, apiID, &Resource{ID: id, PathPart: id, Path: "/" + id}); aerr != nil {
		t.Fatalf("putResource: %v", aerr)
	}
}

func mustPutStage(t *testing.T, s *apigatewayStore, ctx context.Context, apiID, name string) {
	t.Helper()
	if aerr := s.putStage(ctx, apiID, &Stage{StageName: name}); aerr != nil {
		t.Fatalf("putStage: %v", aerr)
	}
}

func mustPutDeployment(t *testing.T, s *apigatewayStore, ctx context.Context, apiID, id string) {
	t.Helper()
	if aerr := s.putDeployment(ctx, apiID, &Deployment{ID: id}); aerr != nil {
		t.Fatalf("putDeployment: %v", aerr)
	}
}

func mustPutV2Route(t *testing.T, s *apigatewayStore, ctx context.Context, apiID, id string) {
	t.Helper()
	if aerr := s.putV2Route(ctx, apiID, &RouteV2{RouteID: id, RouteKey: "GET /" + id}); aerr != nil {
		t.Fatalf("putV2Route: %v", aerr)
	}
}

func mustPutV2Integration(t *testing.T, s *apigatewayStore, ctx context.Context, apiID, id string) {
	t.Helper()
	if aerr := s.putV2Integration(ctx, apiID, &IntegrationV2{IntegrationID: id, IntegrationType: "AWS_PROXY"}); aerr != nil {
		t.Fatalf("putV2Integration: %v", aerr)
	}
}

func mustPutV2Stage(t *testing.T, s *apigatewayStore, ctx context.Context, apiID, name string) {
	t.Helper()
	if aerr := s.putV2Stage(ctx, apiID, &StageV2{StageName: name}); aerr != nil {
		t.Fatalf("putV2Stage: %v", aerr)
	}
}

func mustPutV2Deployment(t *testing.T, s *apigatewayStore, ctx context.Context, apiID, id string) {
	t.Helper()
	if aerr := s.putV2Deployment(ctx, apiID, &DeploymentV2{DeploymentID: id}); aerr != nil {
		t.Fatalf("putV2Deployment: %v", aerr)
	}
}
