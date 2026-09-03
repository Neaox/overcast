//go:build !slim

// Package overcast exposes embedded web assets for use by the overcast binary.
// This file lives at the module root so it can reach both web/dist and docs
// via straight descendant paths (//go:embed cannot use ../ to climb the tree).
package overcast

import "embed"

// WebDistFS contains the pre-built SPA static files (web/dist/).
// Build the web UI before compiling: cd web && pnpm run build
//
//go:embed all:web/dist
var WebDistFS embed.FS

// DocsServicesFS contains published docs served by the BFF docs endpoints.
// Developer-only planning notes under docs/plans and contributor-only docs
// under docs/dev are intentionally excluded.
//
// Every directory a published page lives in has to be named here: the pattern
// takes no wildcard across directories, and internal/docsindex builds its index
// by walking the tree on disk, so a directory missing from this line is a page
// that is searchable in the console and 404s when opened. TestDocsEmbed in
// embed_test.go fails when the two sets diverge.
//
//go:embed docs/*.md docs/cdk docs/cli docs/configuration docs/networking docs/services
var DocsServicesFS embed.FS
