//go:build dev

package sts

import (
	"sort"
	"testing"

	"github.com/Neaox/overcast/internal/capabilities"
)

// TestCapabilities_MatchDispatchInventory asserts that the ops dispatch table
// and the capabilities declaration are kept in sync: every capability must have
// a dispatch entry, and every dispatch entry must be declared as a capability.
func TestCapabilities_MatchDispatchInventory(t *testing.T) {
	t.Helper()

	h := &Handler{}
	h.initOps()

	dispatchSet := make(map[string]struct{}, len(h.ops))
	for op := range h.ops {
		dispatchSet[op] = struct{}{}
	}

	caps := capabilities.Default.ForService("sts")
	capsSet := make(map[string]struct{}, len(caps))
	// docOnly holds capabilities that document AWS operations Overcast does not
	// dispatch (unsupported operations that fall through to the router's
	// NotImplemented handler). They are metadata only and are exempt from the
	// dispatch cross-check.
	docOnly := make(map[string]struct{})
	for _, c := range caps {
		capsSet[c.Operation] = struct{}{}
		if c.DocOnly {
			docOnly[c.Operation] = struct{}{}
		}
	}

	var inDispatchNotCaps []string
	for op := range dispatchSet {
		if _, ok := capsSet[op]; !ok {
			inDispatchNotCaps = append(inDispatchNotCaps, op)
		}
	}
	sort.Strings(inDispatchNotCaps)

	var inCapsNotDispatch []string
	for op := range capsSet {
		if _, ok := dispatchSet[op]; ok {
			continue
		}
		if _, ok := docOnly[op]; ok {
			continue
		}
		inCapsNotDispatch = append(inCapsNotDispatch, op)
	}
	sort.Strings(inCapsNotDispatch)

	if len(inDispatchNotCaps) > 0 {
		t.Errorf("operations in dispatch but not in capabilities.go (add them to capabilities_dev.go):\n  %v", inDispatchNotCaps)
	}
	if len(inCapsNotDispatch) > 0 {
		t.Errorf("operations in capabilities.go but not in dispatch (add them to initOps() or remove from capabilities_dev.go):\n  %v", inCapsNotDispatch)
	}
}
