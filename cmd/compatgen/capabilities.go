//go:build dev

package main

import (
	"github.com/overcast-sh/overcast/internal/capabilities"
)

// The emulator's own statement of what it implements, read from the same
// generated table the docs and the dashboard read. This is the only emulator
// code the generator consults, and it consults it at build time: nothing under
// compat/ imports it.

// capabilityTable maps operation name to declared status for one service. An
// operation absent from the map has no capability row at all.
type capabilityTable map[string]capabilities.Status

// capabilitiesFor collects the declared rows for a service, skipping DocOnly
// rows, which document behaviour that is not an operation.
func capabilitiesFor(service string) capabilityTable {
	table := make(capabilityTable)
	for _, cap := range capabilities.AllCapabilities {
		if cap.Service != service || cap.DocOnly {
			continue
		}
		table[cap.Operation] = cap.Status
	}
	return table
}

// implemented is the probe-group boundary (§3.5): an operation the emulator
// declares with any status other than Unsupported is implemented, and may
// never sit in a probe group. Everything else — Unsupported, or no row at all
// — is what a probe exists to record.
func (t capabilityTable) implemented(op string) bool {
	status, declared := t[op]
	return declared && status != capabilities.StatusUnsupported
}

// statusLabel renders a status for the report; "" means no row.
func (t capabilityTable) statusLabel(op string) string {
	status, declared := t[op]
	if !declared {
		return "undeclared"
	}
	return status.String()
}
