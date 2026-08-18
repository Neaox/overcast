package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

// fakeTrustStore is a trust.Store double: unit tests must never touch the
// real system trust store (that is a machine-global, prompt-raising write).
type fakeTrustStore struct {
	installed    bool
	installErr   error
	installedErr error
	installCalls int
}

func (f *fakeTrustStore) Install(context.Context) error {
	f.installCalls++
	if f.installErr != nil {
		return f.installErr
	}
	f.installed = true
	return nil
}
func (f *fakeTrustStore) Uninstall(context.Context) error { f.installed = false; return nil }
func (f *fakeTrustStore) Installed(context.Context) (bool, error) {
	return f.installed, f.installedErr
}

// withTLSSeams swaps the package seams for the duration of one test. These
// tests must not run in parallel with each other for that reason.
func withTLSSeams(t *testing.T, store trust.Store, storeErr error, inContainer bool) {
	t.Helper()
	prevStore, prevContainer := newTrustStore, runningInContainer
	newTrustStore = func(*zap.Logger, string) (trust.Store, error) { return store, storeErr }
	runningInContainer = func() bool { return inContainer }
	t.Cleanup(func() { newTrustStore, runningInContainer = prevStore, prevContainer })
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	return v
}

func TestTLSStatus_plainHTTPNativeNothingPrepared(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{}, nil, false)
	handler := tlsStatusHandler(&config.Config{DataDir: t.TempDir(), Port: 4566}, zap.NewNop())
	rec := httptest.NewRecorder()

	handler(rec, httptest.NewRequest(http.MethodGet, "/_overcast/tls/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	got := decodeJSON[tlsStatusResponse](t, rec)
	if got.Mode != "off" {
		t.Errorf("mode = %q, want off", got.Mode)
	}
	if got.CAExists {
		t.Error("caExists = true, want false for a fresh data dir")
	}
	if got.TrustStore != trustNotInstalled {
		t.Errorf("trustStore = %q, want %q", got.TrustStore, trustNotInstalled)
	}
	if got.RestartCommand == "" {
		t.Error("restartCommand should be set while TLS is off")
	}
	if got.InContainer {
		t.Error("inContainer = true, want false")
	}
}

func TestTLSStatus_autoModeWithTrustInstalled(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{installed: true}, nil, false)
	dataDir := t.TempDir()
	if _, err := trust.LoadOrCreateCA(trust.DirFor(dataDir)); err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	cfg := &config.Config{DataDir: dataDir, Port: 4566, TLSMode: config.TLSModeAuto}
	rec := httptest.NewRecorder()

	tlsStatusHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodGet, "/_overcast/tls/status", nil))

	got := decodeJSON[tlsStatusResponse](t, rec)
	if got.Mode != "auto" {
		t.Errorf("mode = %q, want auto", got.Mode)
	}
	if !got.CAExists {
		t.Error("caExists = false, want true after minting")
	}
	if got.TrustStore != trustInstalled {
		t.Errorf("trustStore = %q, want %q", got.TrustStore, trustInstalled)
	}
	if got.RestartCommand != "" {
		t.Errorf("restartCommand = %q, want empty while TLS is already on", got.RestartCommand)
	}
}

func TestTLSStatus_containerReportsUnknownTrustAndHostCommand(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{}, nil, true)
	// Published on a remapped host port: the host command must name the port
	// a host-side shell can actually dial, not the in-container listen port.
	cfg := &config.Config{DataDir: t.TempDir(), Port: 4566, PublishedPort: 4580}
	rec := httptest.NewRecorder()

	tlsStatusHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodGet, "/_overcast/tls/status", nil))

	got := decodeJSON[tlsStatusResponse](t, rec)
	if !got.InContainer {
		t.Error("inContainer = false, want true")
	}
	if got.TrustStore != trustUnknown {
		t.Errorf("trustStore = %q, want %q", got.TrustStore, trustUnknown)
	}
	want := "overcast https enable --endpoint http://localhost:4580"
	if got.HostCommand != want {
		t.Errorf("hostCommand = %q, want %q", got.HostCommand, want)
	}
	// The endpoint to switch SDKs to is always the https spelling, on the same
	// host-dialable port — it is what the console tells the user to move to,
	// and it is needed before the restart, not after.
	if got.HTTPSEndpoint != "https://localhost:4580" {
		t.Errorf("httpsEndpoint = %q, want https://localhost:4580", got.HTTPSEndpoint)
	}
	// A containerized daemon's CA path names a file the host cannot open.
	if got.CACertPath != "" {
		t.Errorf("caCertPath = %q, want empty for a containerized daemon", got.CACertPath)
	}
}

// A containerized daemon that minted its own CA is telling the user to
// install a trust root that dies with the container. The console leads with
// the host-owned alternative, so the status has to say which case this is.
func TestTLSStatus_containerWithOwnCAReportsEphemeral(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{}, nil, true)
	cfg := &config.Config{DataDir: t.TempDir(), CADir: t.TempDir(), Port: 4566}
	rec := httptest.NewRecorder()

	tlsStatusHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodGet, "/_overcast/tls/status", nil))

	got := decodeJSON[tlsStatusResponse](t, rec)
	if !got.CAEphemeral {
		t.Error("caEphemeral = false; a container-minted CA does not survive recreation")
	}
	if got.CAShareCommand == "" {
		t.Error("caShareCommand should carry the host-owned-CA recipe")
	}
}

// OVERCAST_CA_DIR set is the signal that someone deliberately placed the CA —
// mounted from the host, on a dedicated volume. Nagging then would be wrong.
func TestTLSStatus_containerWithSharedCAIsNotEphemeral(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{}, nil, true)
	cfg := &config.Config{DataDir: t.TempDir(), CADir: t.TempDir(), CADirConfigured: true, Port: 4566}
	rec := httptest.NewRecorder()

	tlsStatusHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodGet, "/_overcast/tls/status", nil))

	got := decodeJSON[tlsStatusResponse](t, rec)
	if got.CAEphemeral {
		t.Error("caEphemeral = true despite OVERCAST_CA_DIR naming a host-owned directory")
	}
	if got.CAShareCommand != "" {
		t.Errorf("caShareCommand = %q, want empty once the CA is already shared", got.CAShareCommand)
	}
}

// A native daemon's CA is already as durable as the machine.
func TestTLSStatus_nativeDaemonIsNeverEphemeral(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{}, nil, false)
	cfg := &config.Config{DataDir: t.TempDir(), CADir: t.TempDir(), Port: 4566}
	rec := httptest.NewRecorder()

	tlsStatusHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodGet, "/_overcast/tls/status", nil))

	if got := decodeJSON[tlsStatusResponse](t, rec); got.CAEphemeral {
		t.Error("caEphemeral = true for a native daemon")
	}
}

// Once TLS is on, the host command has to dial https. Spelled http it still
// works — trust.FetchRemoteCA retries — but the daemon logs a
// "client sent an HTTP request to an HTTPS server" handshake error for a
// command the console itself handed the user.
func TestTLSStatus_hostCommandUsesHTTPSOnceTLSIsOn(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{}, nil, true)
	cfg := &config.Config{DataDir: t.TempDir(), Port: 4566, TLSMode: config.TLSModeAuto}
	rec := httptest.NewRecorder()

	tlsStatusHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodGet, "/_overcast/tls/status", nil))

	got := decodeJSON[tlsStatusResponse](t, rec)
	want := "overcast https enable --endpoint https://localhost:4566"
	if got.HostCommand != want {
		t.Errorf("hostCommand = %q, want %q", got.HostCommand, want)
	}
}

// Native daemons share a filesystem with the caller, so the CA path is usable
// as AWS_CA_BUNDLE — which the AWS CLI needs even after the CA is installed
// into the system trust store, since botocore ships its own root bundle.
func TestTLSStatus_nativeDaemonReportsCACertPath(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{}, nil, false)
	dir := t.TempDir()
	rec := httptest.NewRecorder()

	tlsStatusHandler(&config.Config{DataDir: dir, Port: 4566}, zap.NewNop())(
		rec, httptest.NewRequest(http.MethodGet, "/_overcast/tls/status", nil))

	got := decodeJSON[tlsStatusResponse](t, rec)
	want := filepath.Join(trust.DirFor(dir), trust.CACertFile)
	if got.CACertPath != want {
		t.Errorf("caCertPath = %q, want %q", got.CACertPath, want)
	}
	if got.HTTPSEndpoint != "https://localhost:4566" {
		t.Errorf("httpsEndpoint = %q, want https://localhost:4566", got.HTTPSEndpoint)
	}
}

func TestTLSStatus_failedTrustCheckReportsUnknownNotUninstalled(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{installedErr: errors.New("certutil exploded")}, nil, false)
	rec := httptest.NewRecorder()

	tlsStatusHandler(&config.Config{DataDir: t.TempDir(), Port: 4566}, zap.NewNop())(
		rec, httptest.NewRequest(http.MethodGet, "/_overcast/tls/status", nil))

	got := decodeJSON[tlsStatusResponse](t, rec)
	if got.TrustStore != trustUnknown {
		t.Errorf("trustStore = %q, want %q — a broken check must not offer an install button", got.TrustStore, trustUnknown)
	}
}

func TestTLSSetup_nativePreparesEverything(t *testing.T) {
	store := &fakeTrustStore{}
	withTLSSeams(t, store, nil, false)
	dataDir := t.TempDir()
	cfg := &config.Config{DataDir: dataDir, Port: 4566}
	rec := httptest.NewRecorder()

	tlsSetupHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodPost, "/_overcast/tls/setup", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	got := decodeJSON[tlsSetupResponse](t, rec)
	if !got.OK || !got.CAReady || !got.TrustInstalled {
		t.Errorf("ok/caReady/trustInstalled = %v/%v/%v, want all true", got.OK, got.CAReady, got.TrustInstalled)
	}
	if got.AlreadyInstalled {
		t.Error("alreadyInstalled = true, want false on first install")
	}
	if !got.RestartRequired || got.RestartCommand == "" {
		t.Errorf("restartRequired/restartCommand = %v/%q — TLS is off, a restart is the remaining step", got.RestartRequired, got.RestartCommand)
	}
	if store.installCalls != 1 {
		t.Errorf("trust store Install calls = %d, want 1", store.installCalls)
	}
	// The CLI-equivalent side effects: CA and leaf on disk.
	for _, f := range []string{trust.CACertFile, "cert.pem", "key.pem"} {
		if _, err := os.Stat(filepath.Join(trust.DirFor(dataDir), f)); err != nil {
			t.Errorf("expected %s to exist after setup: %v", f, err)
		}
	}
}

func TestTLSSetup_secondRunReportsAlreadyInstalled(t *testing.T) {
	store := &fakeTrustStore{installed: true}
	withTLSSeams(t, store, nil, false)
	cfg := &config.Config{DataDir: t.TempDir(), Port: 4566}
	rec := httptest.NewRecorder()

	tlsSetupHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodPost, "/_overcast/tls/setup", nil))

	got := decodeJSON[tlsSetupResponse](t, rec)
	if !got.OK || !got.AlreadyInstalled {
		t.Errorf("ok/alreadyInstalled = %v/%v, want both true on re-run", got.OK, got.AlreadyInstalled)
	}
}

func TestTLSSetup_containerPreparesCertificatesButNotTrust(t *testing.T) {
	store := &fakeTrustStore{}
	withTLSSeams(t, store, nil, true)
	dataDir := t.TempDir()
	cfg := &config.Config{DataDir: dataDir, Port: 4566}
	rec := httptest.NewRecorder()

	tlsSetupHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodPost, "/_overcast/tls/setup", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	got := decodeJSON[tlsSetupResponse](t, rec)
	if !got.OK || !got.CAReady {
		t.Errorf("ok/caReady = %v/%v, want both true — the CA must exist so /api/ca.pem can serve it pre-restart", got.OK, got.CAReady)
	}
	if got.TrustInstalled {
		t.Error("trustInstalled = true, want false — a containerized daemon cannot reach the host trust store")
	}
	if got.HostCommand == "" {
		t.Error("hostCommand should carry the host-side alternative")
	}
	if store.installCalls != 0 {
		t.Errorf("trust store Install calls = %d, want 0 — installing into the container's store would be misleading", store.installCalls)
	}
	if _, err := os.Stat(filepath.Join(trust.DirFor(dataDir), trust.CACertFile)); err != nil {
		t.Errorf("CA certificate should exist on disk after container setup: %v", err)
	}
}

func TestTLSSetup_unsupportedPlatformStillPreparesCertificates(t *testing.T) {
	withTLSSeams(t, nil, trust.ErrUnsupported, false)
	cfg := &config.Config{DataDir: t.TempDir(), Port: 4566}
	rec := httptest.NewRecorder()

	tlsSetupHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodPost, "/_overcast/tls/setup", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	got := decodeJSON[tlsSetupResponse](t, rec)
	if !got.CAReady || got.TrustInstalled {
		t.Errorf("caReady/trustInstalled = %v/%v, want true/false", got.CAReady, got.TrustInstalled)
	}
}

func TestTLSSetup_trustInstallFailureSurfacesSudoHint(t *testing.T) {
	withTLSSeams(t, &fakeTrustStore{installErr: errors.New("permission denied")}, nil, false)
	cfg := &config.Config{DataDir: t.TempDir(), Port: 4566}
	rec := httptest.NewRecorder()

	tlsSetupHandler(cfg, zap.NewNop())(rec, httptest.NewRequest(http.MethodPost, "/_overcast/tls/setup", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	got := decodeJSON[tlsSettingsError](t, rec)
	if got.Error != "TrustInstallFailed" {
		t.Errorf("error = %q, want TrustInstallFailed", got.Error)
	}
}

func TestTLSSetup_rejectsForeignOrigin(t *testing.T) {
	store := &fakeTrustStore{}
	withTLSSeams(t, store, nil, false)
	cfg := &config.Config{DataDir: t.TempDir(), Port: 4566}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/_overcast/tls/setup", nil)
	req.Header.Set("Origin", "https://evil.example.com")

	tlsSetupHandler(cfg, zap.NewNop())(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — the API port's wildcard CORS makes this guard the only CSRF defence", rec.Code)
	}
	if store.installCalls != 0 {
		t.Errorf("trust store Install calls = %d, want 0", store.installCalls)
	}
}

func TestTLSSetup_allowsDaemonsOwnOrigins(t *testing.T) {
	for _, origin := range []string{
		"http://localhost:4567",
		"http://127.0.0.1:4567",
		"https://localhost.overcast.sh:4567",
		"https://app.localhost.overcast.sh",
	} {
		t.Run(origin, func(t *testing.T) {
			withTLSSeams(t, &fakeTrustStore{}, nil, false)
			cfg := &config.Config{DataDir: t.TempDir(), Port: 4566}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/_overcast/tls/setup", nil)
			req.Header.Set("Origin", origin)

			tlsSetupHandler(cfg, zap.NewNop())(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 for origin %s (body: %s)", rec.Code, origin, rec.Body.String())
			}
		})
	}
}

func TestOriginIsLocal_wildcardMatchesOneLabelOnly(t *testing.T) {
	cfg := &config.Config{}
	if originIsLocal("https://a.b.localhost.overcast.sh", cfg) {
		t.Error("two-label subdomain should not match the one-label TLS wildcard")
	}
	if originIsLocal("https://evillocalhost.overcast.sh", cfg) {
		t.Error("suffix-only lookalike host must not match")
	}
	if !originIsLocal("", cfg) {
		t.Error("absent Origin (non-browser clients, the BFF proxy) must be allowed")
	}
}
