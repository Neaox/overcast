// cmd/compat/embed.go — wires the embedded compat UI into the binary.
//
// The UI is embedded in the compat package (compat/embed.go) which sits
// alongside compat/ui/dist/.  This file simply aliases it for use in main.go.
package main

import (
	"io/fs"

	"github.com/Neaox/overcast/compat"
)

// uiFS is the embedded compat UI, served at / by the compat server.
var uiFS fs.FS = compat.UIFS
