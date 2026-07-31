package containerendpoint

import (
	"archive/tar"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

// This file makes TLS survive the container boundary. Under OVERCAST_TLS the
// API listener serves only TLS, so a container handed an http endpoint gets
// Go's plaintext rejection ("Client sent an HTTP request to an HTTPS server")
// surfaced as SDK deserialization garbage. Two things fix that: the endpoint
// scheme follows the listener (BaseURL), and the CA that minted the server
// certificate is trusted inside the container (CABundleEnv + CABundleTar).
//
// The bundle travels by CopyToContainer — the same mechanism as function code
// — because when Overcast itself runs in Docker its data dir is not a host
// path, so a bind mount cannot carry the file to a sibling container.

// ContainerCABundlePath is where the CA bundle lands inside a container. Under
// /opt so it coexists with Lambda layer content, which is copied to the same
// tree.
const ContainerCABundlePath = "/opt/overcast/ca.pem"

// BaseURL is the origin containers use to reach Overcast on host — the single
// place the container-facing scheme is decided, shared by the Lambda and ECS
// runtimes. The scheme follows the server's listener: with TLS on, an http
// endpoint is not a degraded mode, it is a hard failure.
func BaseURL(cfg *config.Config, host string) string {
	scheme := "http"
	if cfg != nil && cfg.TLSEnabled() {
		scheme = "https"
	}
	port := 4566
	if cfg != nil && cfg.Port > 0 {
		port = cfg.Port
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// CABundleEnv returns the environment variables that point a container's TLS
// stacks at the injected bundle, or nil when Overcast serves plain HTTP.
//
// Coverage: AWS_CA_BUNDLE (botocore/CLI, Go SDK), NODE_EXTRA_CA_CERTS (Node —
// the Lambda runtimes Overcast ships), SSL_CERT_FILE (OpenSSL-based stacks,
// Ruby), REQUESTS_CA_BUNDLE (python-requests). The Java SDK reads only its own
// truststore and cannot be reached this way — documented in docs/https.md.
func (m *Mapper) CABundleEnv() map[string]string {
	if m == nil || m.cfg == nil || !m.cfg.TLSEnabled() {
		return nil
	}
	return map[string]string{
		"AWS_CA_BUNDLE":       ContainerCABundlePath,
		"NODE_EXTRA_CA_CERTS": ContainerCABundlePath,
		"SSL_CERT_FILE":       ContainerCABundlePath,
		"REQUESTS_CA_BUNDLE":  ContainerCABundlePath,
	}
}

// CABundleTar returns a tar archive (for CopyToContainer with destPath "/")
// carrying the PEM roots a container needs to verify Overcast's certificate:
// the local CA in auto mode, the operator's own certificate chain in explicit
// mode — the same split cmd/overcast uses for init-hook processes. (nil, nil)
// when TLS is off.
func (m *Mapper) CABundleTar() ([]byte, error) {
	if m == nil || m.cfg == nil || !m.cfg.TLSEnabled() {
		return nil, nil
	}
	// The trust root is minted before any container-running service starts
	// and never rotates within a process, so the tar (a disk read + build
	// otherwise) is produced once per Mapper and reused for every container
	// launch — Lambda cold starts and ECS tasks alike.
	m.caTarOnce.Do(func() {
		m.caTar, m.caTarErr = m.buildCABundleTar()
	})
	return m.caTar, m.caTarErr
}

func (m *Mapper) buildCABundleTar() ([]byte, error) {
	pemPath := m.cfg.TLSCertFile
	if m.cfg.TLSAuto() {
		pemPath = filepath.Join(trust.DirFor(m.cfg.DataDir), trust.CACertFile)
	}
	pem, err := os.ReadFile(pemPath)
	if err != nil {
		return nil, fmt.Errorf("read TLS trust root %s: %w", pemPath, err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	name := strings.TrimPrefix(ContainerCABundlePath, "/")
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o444, Size: int64(len(pem))}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(pem); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// rewriteTarget is what RewriteURLs substitutes for an Overcast loopback
// origin. Plain HTTP keeps the resolved address — resolution-free, always
// dialable. Under TLS the address would trade a scheme error for a
// certificate error (the leaf's SANs cover Overcast's names, never a
// container-network IP), so the target is the SAN-covered client endpoint.
func (m *Mapper) rewriteTarget() string {
	if m.cfg != nil && m.cfg.TLSEnabled() {
		if named := m.ClientEndpoint(); named != "" {
			return named
		}
	}
	return m.endpoint
}
