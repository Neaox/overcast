// remote.go — trusting a daemon you cannot share a filesystem with.
//
// A containerized daemon mints its CA inside the container; the host-side
// CLI cannot read it without a shared volume. Instead the daemon serves its
// CA certificate (public half only) at CAPemPath, and the CLI fetches it,
// validates it actually is a CA certificate, caches it under the local data
// dir (in a directory that can never be confused with a locally-minted CA),
// and installs it with the same trust-store backends the local flow uses.
//
// Fetching over TLS is deliberately unverified: the whole point is that
// trust is not established yet. That is safe only because the caller gates
// on the endpoint being loopback (or an explicit user acknowledgement) and
// because the payload is validated as a CA certificate — the trust decision
// is the user's command, not the transport.

package trust

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CAPemPath is the daemon endpoint serving the CA certificate (PEM, public
// half only). Registered in internal/router; fetched by FetchRemoteCA.
const CAPemPath = "/_overcast/ca.pem"

// maxCAPemBytes bounds the fetch: a CA certificate PEM is ~1-2 KiB; anything
// approaching this limit is not one.
const maxCAPemBytes = 64 * 1024

// fetchTimeout bounds the whole fetch — the daemon is on loopback in every
// supported flow, so a slow answer is a hung daemon, not distance.
const fetchTimeout = 10 * time.Second

// FetchRemoteCA fetches and validates the CA certificate of the daemon at
// endpoint (an origin such as "http://localhost:4566"). It returns the PEM
// exactly as served plus the parsed certificate.
//
// A TLS-serving daemon (OVERCAST_TLS=auto — the very case this exists for)
// answers only https, while every doc and log line spells the endpoint
// http://; an http:// endpoint that fails is therefore retried once as
// https. TLS verification is skipped (see the file comment for why that is
// sound here); the payload validation below is the real gate.
func FetchRemoteCA(ctx context.Context, endpoint string) ([]byte, *x509.Certificate, error) {
	endpoint = strings.TrimRight(endpoint, "/")
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, nil, fmt.Errorf("trust: invalid endpoint %q", endpoint)
	}

	pemBytes, err := fetchCAPem(ctx, endpoint)
	if err != nil && u.Scheme == "http" {
		// The daemon may be TLS-only. Retry the https spelling before
		// giving up; keep the original error if that also fails.
		if pemHTTPS, errHTTPS := fetchCAPem(ctx, "https://"+u.Host); errHTTPS == nil {
			pemBytes, err = pemHTTPS, nil
		}
	}
	if err != nil {
		return nil, nil, err
	}

	cert, err := parsePEMCertificate(pemBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("trust: %s did not return a certificate: %w", endpoint+CAPemPath, err)
	}
	if !cert.IsCA {
		return nil, nil, fmt.Errorf("trust: certificate from %s is not a CA certificate (subject %q) — refusing to install it as a trust root", endpoint, cert.Subject.CommonName)
	}
	return pemBytes, cert, nil
}

// fetchCAPem performs one GET of origin+CAPemPath.
func fetchCAPem(ctx context.Context, origin string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+CAPemPath, nil)
	if err != nil {
		return nil, fmt.Errorf("trust: build request: %w", err)
	}
	client := &http.Client{Transport: &http.Transport{
		// Unverified by design — see the file comment. The payload is
		// validated as a CA certificate and the caller gates on loopback.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trust: fetch %s: %w", origin+CAPemPath, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("trust: %s has no CA to serve — the daemon is not running with OVERCAST_TLS=auto", origin)
	default:
		return nil, fmt.Errorf("trust: fetch %s: unexpected status %s", origin+CAPemPath, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCAPemBytes+1))
	if err != nil {
		return nil, fmt.Errorf("trust: read %s: %w", origin+CAPemPath, err)
	}
	if len(body) > maxCAPemBytes {
		return nil, fmt.Errorf("trust: response from %s exceeds %d bytes — not a CA certificate", origin, maxCAPemBytes)
	}
	return body, nil
}

// LoopbackEndpoint reports whether endpoint's host is a loopback name or
// address. Only literal loopback counts: a name that merely resolves to
// 127.0.0.1 (localhost.overcast.sh) is still a remote-controlled DNS answer,
// so it does not qualify for the silent path.
func LoopbackEndpoint(endpoint string) (bool, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return false, fmt.Errorf("trust: invalid endpoint %q", endpoint)
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true, nil
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback(), nil
}

// RemoteDirFor returns the cache directory for a CA fetched from endpoint:
// <dataDir>/ca-remote/<host_port>. Deterministic, so uninstall/status find
// the same cache install used; scheme-agnostic, since http and https name
// the same daemon; and disjoint from DirFor's <dataDir>/ca, so a fetched CA
// can never be mistaken for (or overwrite) a locally-minted one.
func RemoteDirFor(dataDir, endpoint string) string {
	name := "invalid-endpoint"
	if u, err := url.Parse(strings.TrimRight(endpoint, "/")); err == nil && u.Host != "" {
		// ':' is not portable in file names (Windows); '[]' from IPv6
		// literals are noise.
		name = strings.NewReplacer(":", "_", "[", "", "]", "").Replace(strings.ToLower(u.Host))
	}
	return filepath.Join(dataDir, "ca-remote", name)
}

// SaveRemoteCA caches a fetched CA certificate under dir (see RemoteDirFor).
// Only the certificate is ever written — a remote CA has no key here.
func SaveRemoteCA(dir string, certPEM []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("trust: create remote CA cache: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, CACertFile), certPEM, 0o644); err != nil {
		return fmt.Errorf("trust: cache remote CA certificate: %w", err)
	}
	return nil
}
