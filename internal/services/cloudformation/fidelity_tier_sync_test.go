// Package cloudformation_test holds tests that need to import both
// internal/router and internal/services/cloudformation. A same-package
// (internal, "package cloudformation") test file cannot do that: router.go
// registers cloudformation as a service, so importing router from a file that
// is part of package cloudformation would cycle. An external test package has
// no such restriction — see fidelity.go's package comment and export_test.go,
// which exposes what this file needs to reach.
package cloudformation_test

import (
	"testing"

	"github.com/overcast-sh/overcast/internal/router"
	"github.com/overcast-sh/overcast/internal/services/cloudformation"
)

// TestResourceServiceTiers_matchRouterServiceTiers guards fidelity.go's small,
// necessarily-duplicated copy of which services are inert/stub tier against
// drifting from the real thing in internal/router/tiers.go.
func TestResourceServiceTiers_matchRouterServiceTiers(t *testing.T) {
	for svc, tier := range cloudformation.ResourceServiceTiersForTest {
		t.Run(svc, func(t *testing.T) {
			routerTier, ok := router.ServiceTiers[svc]
			if !ok {
				t.Fatalf("service %q is not a registered router service tier at all — "+
					"remove it from internal/services/cloudformation/fidelity.go's resourceServiceTiers", svc)
			}
			want := ""
			switch routerTier {
			case router.TierInert:
				want = "inert"
			case router.TierStub:
				want = "stub"
			}
			if want == "" {
				t.Errorf("router.ServiceTiers[%q] = %q, which is neither inert nor stub tier — "+
					"remove %q from fidelity.go's resourceServiceTiers, its resources are no longer worth flagging",
					svc, routerTier, svc)
				return
			}
			if tier != want {
				t.Errorf("fidelity.go's resourceServiceTiers[%q] = %q, but router.ServiceTiers says %q — "+
					"keep internal/services/cloudformation/fidelity.go in sync with internal/router/tiers.go",
					svc, tier, want)
			}
		})
	}
}

// TestResourceServiceTiers_coverEveryRealHandlerOnAnInertOrStubService is the
// opposite tripwire: a resource type backed by a real (non-stub) handler on a
// service router.ServiceTiers reports as inert or stub, but which fidelity.go's
// resourceServiceTiers has no entry for, would silently get no fidelity
// notice at all — exactly the drift the issue's "no per-type table" design
// depends on not happening unnoticed.
func TestResourceServiceTiers_coverEveryRealHandlerOnAnInertOrStubService(t *testing.T) {
	for resType, isStub := range cloudformation.ResourceHandlerTypesForTest() {
		if isStub {
			continue
		}
		svc, ok := cloudformation.CFNResourceServiceForTest(resType)
		if !ok {
			continue
		}
		routerTier, ok := router.ServiceTiers[svc]
		if !ok || (routerTier != router.TierInert && routerTier != router.TierStub) {
			continue
		}
		t.Run(resType, func(t *testing.T) {
			if _, ok := cloudformation.ResourceServiceTiersForTest[svc]; !ok {
				t.Errorf("%s is backed by a real handler on service %q, which router.ServiceTiers reports as %q tier, "+
					"but fidelity.go's resourceServiceTiers has no entry for %q — add one, or this resource silently "+
					"gets no fidelity notice on deploy", resType, svc, routerTier, svc)
			}
		})
	}
}
