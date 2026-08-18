package router

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

// caCertHandler serves GET /_overcast/ca.pem — the daemon's local CA
// *certificate* (public half only; the key never leaves the CA directory).
//
// This is what makes the Docker HTTPS flow one command: a containerized
// daemon mints its CA inside the container where the host CLI cannot read
// it, so `overcast https enable --endpoint https://localhost:4566` fetches it
// from here instead of requiring a shared volume. Registered under /_
// (exempt from the NotReady middleware like every internal endpoint) and
// read from disk per request, so it needs no startup work and always serves
// whatever CA currently exists.
//
// 404 means "no CA minted" — the daemon is not running with
// OVERCAST_TLS=auto and has never done so with this data dir.
func caCertHandler(cfg *config.Config) http.HandlerFunc {
	certPath := filepath.Join(cfg.CACertDir(), trust.CACertFile)
	return func(w http.ResponseWriter, _ *http.Request) {
		pem, err := os.ReadFile(certPath)
		if err != nil {
			http.Error(w, "no CA certificate — this daemon is not serving TLS (set OVERCAST_TLS=auto)", http.StatusNotFound)
			return
		}
		// Never serve bytes that are not a certificate: a corrupt or
		// tampered file must not propagate into anyone's trust store.
		if _, err := trust.ParseCertificatePEM(pem); err != nil {
			http.Error(w, "CA certificate on disk is unreadable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(pem) //nolint:errcheck // best-effort HTTP write.
	}
}
