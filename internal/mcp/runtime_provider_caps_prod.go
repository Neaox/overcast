//go:build !dev

package mcp

// runtimeCapabilitiesDetail returns only the enabled-service list in prod
// builds where the capability snapshot is not embedded.
func runtimeCapabilitiesDetail(services []string) []map[string]any {
	results := make([]map[string]any, 0, len(services))
	for _, svc := range services {
		results = append(results, map[string]any{
			"service":  svc,
			"enabled":  true,
			"op_count": 0,
		})
	}
	return results
}
