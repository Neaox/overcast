//go:build dev

package mcp

import (
	"strings"

	"github.com/Neaox/overcast/internal/capabilities"
)

// runtimeCapabilitiesDetail returns per-operation capability info from the
// dev-only static snapshot.
func runtimeCapabilitiesDetail(services []string) []map[string]any {
	// Index capabilities by service.
	capsByService := make(map[string][]capabilities.Capability, len(services))
	for _, c := range capabilities.AllCapabilities {
		svc := strings.ToLower(c.Service)
		capsByService[svc] = append(capsByService[svc], c)
	}

	results := make([]map[string]any, 0, len(services))
	for _, svc := range services {
		caps := capsByService[svc]
		type opEntry struct {
			Operation string `json:"operation"`
			Category  string `json:"category,omitempty"`
			Status    string `json:"status"`
			Notes     string `json:"notes,omitempty"`
		}
		ops := make([]opEntry, 0, len(caps))
		for _, c := range caps {
			ops = append(ops, opEntry{
				Operation: c.Operation,
				Category:  c.Category,
				Status:    c.Status.String(),
				Notes:     c.Notes,
			})
		}
		results = append(results, map[string]any{
			"service":    svc,
			"enabled":    true,
			"op_count":   len(caps),
			"operations": ops,
		})
	}
	return results
}
