//go:build tools

// Keeps go.mod honest about what the build-ignored scripts in this directory
// import.
//
// Every script here is `//go:build ignore` and run as `go run ./scripts/x.go`,
// and `go mod tidy` does not read ignore-tagged files — so a library that only
// a script imports looks unused to tidy and gets dropped from go.mod. That is
// exactly what happened to github.com/yuin/goldmark (docs-index.go) the first
// time Renovate ran `go mod tidy` on a dependency PR (#1212–#1215): go.mod lost
// the requirement, `make docs-check` could no longer build the script, and
// every Go update PR went red on the Docs job.
//
// The `tools` tag is never set by anything, so this file is never compiled;
// tidy still reads it, which is all it is for. Add an import here whenever a
// script starts importing a module that nothing in the buildable tree needs.
package scripts

import (
	_ "github.com/yuin/goldmark"
	_ "github.com/yuin/goldmark/ast"
	_ "github.com/yuin/goldmark/extension"
	_ "github.com/yuin/goldmark/text"
)
