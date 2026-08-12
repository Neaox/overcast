package router

import (
	"sort"
	"testing"

	"github.com/Neaox/overcast/internal/config"
)

// registeredServices returns every service name the emulator registers —
// config's canonical list, which is exactly the set New walks when wiring
// routes.
func registeredServices(t *testing.T) []string {
	t.Helper()

	names := config.AllServices()
	if len(names) == 0 {
		t.Fatal("config.AllServices returned no services")
	}
	sort.Strings(names)
	return names
}

// TestServiceTiers_coverEveryRegisteredService is a tripwire, not a sanity
// check. New falls back to TierStub for any registered service missing from
// ServiceTiers, so a service that is enabled and answering requests is
// silently reported as "stub" in /_overcast/health — and the web UI files it under
// "not emulated". The fallback is invisible: nothing fails, the label is just
// wrong. Every registered service must therefore carry an explicit tier.
func TestServiceTiers_coverEveryRegisteredService(t *testing.T) {
	for _, svc := range registeredServices(t) {
		t.Run(svc, func(t *testing.T) {
			if _, ok := ServiceTiers[svc]; !ok {
				t.Errorf("service %q has no ServiceTiers entry: New defaults it to %q in /_overcast/health, mislabelling it if any operation is implemented — add an explicit tier in tiers.go", svc, TierStub)
			}
		})
	}
}

// TestServiceTiers_haveNoUnregisteredEntries catches the opposite drift: an
// entry for a service that was renamed or removed, which would keep claiming
// a tier for something the emulator never registers.
func TestServiceTiers_haveNoUnregisteredEntries(t *testing.T) {
	registered := make(map[string]bool)
	for _, svc := range registeredServices(t) {
		registered[svc] = true
	}

	for svc := range ServiceTiers {
		if !registered[svc] {
			t.Errorf("ServiceTiers has an entry for %q, which is not a registered service — remove it or fix the name", svc)
		}
	}
}

// TestServiceTiers_useRegisteredTierValues rejects typos and the one tier that
// is meaningless here: TierUnsupported means "not registered in Overcast", so
// it can never describe a service that is.
func TestServiceTiers_useRegisteredTierValues(t *testing.T) {
	valid := map[EmulationTier]bool{
		TierFull:    true,
		TierPartial: true,
		TierInert:   true,
		TierStub:    true,
	}

	for svc, tier := range ServiceTiers {
		t.Run(svc, func(t *testing.T) {
			if !valid[tier] {
				t.Errorf("ServiceTiers[%q] = %q, want one of %q/%q/%q/%q", svc, tier, TierFull, TierPartial, TierInert, TierStub)
			}
		})
	}
}

// TestServiceGoalTiers_describeKnownServices keeps the aspirational map from
// drifting away from the current one — a goal for a service with no current
// tier can never surface a WIP indicator.
func TestServiceGoalTiers_describeKnownServices(t *testing.T) {
	for svc, goal := range ServiceGoalTiers {
		t.Run(svc, func(t *testing.T) {
			current, ok := ServiceTiers[svc]
			if !ok {
				t.Fatalf("ServiceGoalTiers has a goal for %q, which has no ServiceTiers entry", svc)
			}
			if current == goal {
				t.Errorf("ServiceGoalTiers[%q] = %q, same as its current tier — drop the entry instead of claiming work in progress", svc, goal)
			}
		})
	}
}
