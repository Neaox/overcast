//go:build !slim

// Package overcast exposes embedded web assets for use by the overcast binary.
// This file lives at the module root so it can reach both web/dist and docs/services
// via straight descendant paths (//go:embed cannot use ../ to climb the tree).
package overcast

import "embed"

// WebDistFS contains the pre-built SPA static files (web/dist/).
// Build the web UI before compiling: cd web && npm run build
//
//go:embed all:web/dist
var WebDistFS embed.FS

// DocsServicesFS contains docs/services/*.md — served by the BFF at GET /api/docs/:service.
//
//go:embed docs/services
var DocsServicesFS embed.FS
