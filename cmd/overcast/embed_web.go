//go:build !slim

package main

import (
	"io/fs"
	"net/http"

	overcast "github.com/Neaox/overcast"
	"github.com/Neaox/overcast/internal/bff"
	"github.com/Neaox/overcast/internal/containerendpoint"
)

func newUIHandler(apiPort, browserAPIPort int, region string, debug bool, tlsEnabled bool, tlsTrustPEM []byte) (http.Handler, error) {
	staticFS, err := fs.Sub(overcast.WebDistFS, "web/dist")
	if err != nil {
		return nil, err
	}
	docsFS, err := fs.Sub(overcast.DocsServicesFS, "docs")
	if err != nil {
		return nil, err
	}
	return bff.NewHandler(staticFS, docsFS, bff.UIConfig{
		APIPort:        apiPort,
		BrowserAPIPort: browserAPIPort,
		Region:         region,
		Debug:          debug,
		TLS:            tlsEnabled,
		TLSTrustPEM:    tlsTrustPEM,
		InDocker:       containerendpoint.RunningInContainer(),
	}), nil
}
