//go:build !slim

// Package overcast exposes embedded web assets for use by the overcast binary.
// This file lives at the module root so it can reach both web/dist and docs
// via straight descendant paths (//go:embed cannot use ../ to climb the tree).
package overcast

import "embed"

// WebDistFS contains the pre-built SPA static files (web/dist/).
// Build the web UI before compiling: cd web && npm run build
//
//go:embed all:web/dist
var WebDistFS embed.FS

// DocsServicesFS contains published docs served by the BFF docs endpoints.
// Developer-only planning notes under docs/plans are intentionally excluded.
//
//go:embed docs/*.md docs/cdk docs/compatibility docs/perf-baselines docs/services
var DocsServicesFS embed.FS
