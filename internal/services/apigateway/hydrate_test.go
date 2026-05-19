package apigateway

import (
	"context"
	"sync"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/domainregistry"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
)

// registryHas checks whether the registry contains a record with the given domain name.
func registryHas(reg *domainregistry.Registry, name string) bool {
	for _, r := range reg.Snapshot() {
		if r.Name == name {
			return true
		}
	}
	return false
}

func TestEnsureRegistryHydrated_CrossRegion(t *testing.T) {
	// Given: domain names stored in non-default regions.
	store := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1"}
	h := &Handler{
		cfg:            cfg,
		store:          newAPIGatewayStore(store, cfg.Region),
		domainRegistry: domainregistry.New(),
	}

	// Put a v1 domain name in ap-southeast-2 (non-default region).
	ctx := middleware.ContextWithRegion(context.Background(), "ap-southeast-2")
	dn := &DomainName{DomainName: "api.example.com"}
	if aerr := h.store.putDomainName(ctx, dn); aerr != nil {
		t.Fatalf("putDomainName: %v", aerr)
	}

	// Put a v2 domain name in eu-west-1 (another non-default region).
	ctx2 := middleware.ContextWithRegion(context.Background(), "eu-west-1")
	dnv2 := &DomainNameV2{DomainName: "v2.example.com"}
	if aerr := h.store.putV2DomainName(ctx2, dnv2); aerr != nil {
		t.Fatalf("putV2DomainName: %v", aerr)
	}

	// When: ensureRegistryHydrated is called (uses context.Background internally).
	h.ensureRegistryHydrated()

	// Then: both cross-region domains should be in the registry.
	if !registryHas(h.domainRegistry, "api.example.com") {
		t.Error("expected api.example.com in registry, but not found")
	}
	if !registryHas(h.domainRegistry, "v2.example.com") {
		t.Error("expected v2.example.com in registry, but not found")
	}
}

func TestEnsureRegistryHydrated_DefaultRegion(t *testing.T) {
	// Given: domain names stored in the default region.
	store := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1"}
	h := &Handler{
		cfg:            cfg,
		store:          newAPIGatewayStore(store, cfg.Region),
		domainRegistry: domainregistry.New(),
	}

	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	dn := &DomainName{DomainName: "default.example.com"}
	if aerr := h.store.putDomainName(ctx, dn); aerr != nil {
		t.Fatalf("putDomainName: %v", aerr)
	}

	// Reset hydrateOnce so it fires.
	h.hydrateOnce = sync.Once{}
	h.ensureRegistryHydrated()

	if !registryHas(h.domainRegistry, "default.example.com") {
		t.Error("expected default.example.com in registry, but not found")
	}
}
