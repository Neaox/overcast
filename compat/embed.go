// compat/embed.go — embeds the pre-built compatibility UI into the package.
//
// The compat UI must be built before `go build ./cmd/compat`:
//
//	cd compat/ui && npm install && npm run build
//
// The resulting compat/ui/dist/ tree is embedded here and exported as UIFS.
// The compat server serves it at GET /.
package compat

import (
	"embed"
	"io/fs"
)

//go:embed all:ui/dist
var rawUIFS embed.FS

// UIFS is the embedded compat UI sub-tree, rooted at the dist build output.
// Served at / by the compat HTTP server.
var UIFS, _ = fs.Sub(rawUIFS, "ui/dist")
