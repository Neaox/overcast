//go:build dev

package ssm

import (
	"sort"
	"testing"

	"github.com/Neaox/overcast/internal/capabilities"
)

// TestCapabilities_MatchDispatchInventory asserts that the capabilities
// declared in capabilities_dev.go exactly cover the operations registered in
// initOps — no more, no less.  This prevents the two inventories from drifting
// when operations are added, promoted out of stubs, or removed.
func TestCapabilities_MatchDispatchInventory(t *testing.T) {
	t.Helper()

	// Populate the dispatch table by calling initOps on a zero-value Handler.
	// initOps only assigns method-value closures to the map; it never
	// dereferences h's pointer fields, so nil fields are safe here.
	h := &Handler{}
	h.initOps()

	dispatchSet := make(map[string]struct{}, len(h.ops))
	for op := range h.ops {
		dispatchSet[op] = struct{}{}
	}

	// capabilities.Default is populated by the init() in capabilities_dev.go
	// (same package, so it runs before any test).
	caps := capabilities.Default.ForService("ssm")
	capsSet := make(map[string]struct{}, len(caps))
	for _, c := range caps {
		capsSet[c.Operation] = struct{}{}
	}

	// Every dispatch key must have a capability declaration.
	var missing []string
	for op := range dispatchSet {
		if _, ok := capsSet[op]; !ok {
			missing = append(missing, op)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("operations in initOps but missing from capabilities_dev.go:\n  %v\nAdd a Capability entry for each.", missing)
	}

	// Every capability declaration must correspond to a real dispatch entry.
	var phantom []string
	for op := range capsSet {
		if _, ok := dispatchSet[op]; !ok {
			phantom = append(phantom, op)
		}
	}
	sort.Strings(phantom)
	if len(phantom) > 0 {
		t.Errorf("operations declared in capabilities_dev.go but absent from initOps:\n  %v\nEither add them to initOps (as stubs) or remove the capability entry.", phantom)
	}
}
