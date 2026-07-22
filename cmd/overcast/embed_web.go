//go:build !slim

package main

import (
	"io/fs"
	"net/http"

	overcast "github.com/Neaox/overcast"
	"github.com/Neaox/overcast/internal/bff"
)

func newUIHandler(apiPort int, region string, debug bool) (http.Handler, error) {
	staticFS, err := fs.Sub(overcast.WebDistFS, "web/dist")
	if err != nil {
		return nil, err
	}
	docsFS, err := fs.Sub(overcast.DocsServicesFS, "docs")
	if err != nil {
		return nil, err
	}
	return bff.NewHandler(staticFS, docsFS, bff.UIConfig{APIPort: apiPort, Region: region, Debug: debug}), nil
}
