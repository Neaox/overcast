//go:build slim

// Package overcast provides stub embedded-asset vars for slim builds.
// Slim builds omit the web UI; these zero-value FSes satisfy the package
// so that `go build -tags slim ./...` does not error on the root package.
package overcast

import "embed"

var WebDistFS embed.FS
var DocsServicesFS embed.FS
