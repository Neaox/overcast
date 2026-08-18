package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/containerendpoint"
	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

// tls_settings.go — GET /_overcast/tls/status and POST /_overcast/tls/setup,
// the daemon half of the web console's Settings → HTTPS page.
//
// The console cannot flip the daemon to HTTPS itself: Overcast is configured
// entirely through environment variables (`OVERCAST_TLS=auto`), so the
// listeners' mode is fixed for the life of the process. What the daemon CAN
// do from a click is everything `overcast https enable` does — mint the local
// CA, install its certificate into the system trust store (the OS prompts the
// user; that prompt is the point), and mint the server leaf — leaving exactly
// one manual step: restart with OVERCAST_TLS=auto. The status endpoint tells
// the console which of those pieces are already in place so it renders the
// right next step, including the containerized case where the trust store is
// the HOST's and only the host-side CLI can reach it.

// Test seams. Trust-store construction shells out to platform tooling
// (certutil / security / update-ca-certificates) and the container check
// stats /.dockerenv — both are process-global facts a unit test cannot
// arrange, so tests substitute these instead.
var (
	newTrustStore      = trust.New
	runningInContainer = containerendpoint.RunningInContainer
)

// Trust-store states reported by tlsStatusResponse.TrustStore.
const (
	trustInstalled    = "installed"     // CA certificate is in the system trust store
	trustNotInstalled = "not_installed" // store is reachable, CA not (or not yet) in it
	trustUnavailable  = "unavailable"   // no trust-store backend on this platform
	trustUnknown      = "unknown"       // cannot be determined from here (containerized daemon)
)

type tlsStatusResponse struct {
	// Mode is "off", "auto" (OVERCAST_TLS=auto) or "explicit"
	// (OVERCAST_TLS_CERT/KEY). The listeners serve HTTPS exactly when it is
	// not "off" — one mode covers both the API and UI ports.
	Mode string `json:"mode"`
	// CAExists reports whether the local CA certificate exists on disk.
	CAExists bool `json:"caExists"`
	// TrustStore is one of the trust* constants above.
	TrustStore string `json:"trustStore"`
	// InContainer reports whether the daemon itself is containerized — the
	// case where trust must be installed from the host instead.
	InContainer bool `json:"inContainer"`
	// HostCommand is the exact host-side command that trusts this daemon's
	// CA when the daemon cannot do it itself (containerized, or no
	// trust-store backend). Empty when the daemon can install trust locally.
	HostCommand string `json:"hostCommand,omitempty"`
	// RestartCommand is the one manual step the daemon can never perform for
	// a native install: restarting with TLS switched on. Set while Mode is
	// "off" so the console can show it verbatim.
	RestartCommand string `json:"restartCommand,omitempty"`
	// HTTPSEndpoint is the https origin SDKs and the CLI must be pointed at
	// once TLS is on — always the https spelling, including while Mode is
	// still "off", because it is what the console tells the user to switch
	// to. The port is the one a host-side caller dials (see hostTrustCommand).
	HTTPSEndpoint string `json:"httpsEndpoint"`
	// CAEphemeral reports that this daemon's CA will not survive the
	// container being recreated: it is containerized and nobody pointed
	// OVERCAST_CA_DIR at a directory the host owns, so the CA lives in the
	// container's own layer (or in a volume it cannot distinguish from one).
	// Trust installs are per-machine and permanent; a CA that is not is a
	// re-approval prompt on every recreation, so the console leads with
	// sharing the host's CA instead.
	CAEphemeral bool `json:"caEphemeral"`
	// CAShareCommand is the docker run fragment that mounts a host-owned CA
	// into this daemon. Set alongside CAEphemeral.
	CAShareCommand string `json:"caShareCommand,omitempty"`
	// CACertPath is the daemon-side path of the CA certificate, for the
	// AWS_CA_BUNDLE / NODE_EXTRA_CA_CERTS variables SDKs need even after the
	// CA is in the system trust store — botocore and Node ship their own root
	// bundles and do not consult it. Empty for a containerized daemon, where
	// the path names a file inside the container and the host must use its own
	// copy (downloaded, or cached by `overcast https enable --endpoint`).
	CACertPath string `json:"caCertPath,omitempty"`
}

type tlsSetupResponse struct {
	OK bool `json:"ok"`
	// CAReady reports that the CA and server certificate exist on disk. It is
	// true on every success path — setup always prepares the material, even
	// when trust cannot be installed from here.
	CAReady bool `json:"caReady"`
	// TrustInstalled reports the CA certificate is now in THIS machine's
	// system trust store. False for containerized daemons and platforms
	// without a trust-store backend — HostCommand then carries the host-side
	// alternative, and the CA certificate is downloadable at /api/ca.pem for
	// the fully manual route.
	TrustInstalled bool `json:"trustInstalled"`
	// AlreadyInstalled distinguishes "installed just now — you saw the OS
	// prompt" from "nothing to do".
	AlreadyInstalled bool `json:"alreadyInstalled"`
	// RestartRequired is true while the daemon is still serving plain HTTP:
	// certificates (and possibly trust) are ready, but only a restart with
	// OVERCAST_TLS=auto turns HTTPS on.
	RestartRequired bool   `json:"restartRequired"`
	RestartCommand  string `json:"restartCommand,omitempty"`
	// HostCommand is set when trust must be installed from the host instead
	// (see TrustInstalled).
	HostCommand string `json:"hostCommand,omitempty"`
}

type tlsSettingsError struct {
	Error       string `json:"error"`
	Message     string `json:"message"`
	HostCommand string `json:"hostCommand,omitempty"`
}

func tlsMode(cfg *config.Config) string {
	switch {
	case cfg.TLSAuto():
		return "auto"
	case cfg.TLSEnabled():
		return "explicit"
	default:
		return "off"
	}
}

// hostPort is the API port a host-side caller actually dials — the published
// port when the container is remapped (`-p 4580:4566`), cfg.Port otherwise.
func hostPort(cfg *config.Config) int {
	if cfg.PublishedPort > 0 {
		return cfg.PublishedPort
	}
	return cfg.Port
}

// hostTrustCommand is the command a HOST-side shell runs to trust a daemon
// whose trust store it cannot reach (containers, unsupported platforms).
//
// The scheme tracks what the listener actually speaks right now, not what it
// will speak after the restart: run before the restart it dials plain HTTP,
// run after it dials TLS. Getting that wrong is survivable — FetchRemoteCA
// retries the https spelling — but it costs the operator a
// "client sent an HTTP request to an HTTPS server" line in the daemon log and
// the time spent working out whether it mattered.
func hostTrustCommand(cfg *config.Config) string {
	scheme := "http"
	if cfg.TLSEnabled() {
		scheme = "https"
	}
	return fmt.Sprintf("overcast https enable --endpoint %s://localhost:%d", scheme, hostPort(cfg))
}

// httpsEndpoint is the origin SDKs and the CLI must target once TLS is on.
// Always https: it is the post-switch endpoint, and the console shows it while
// the daemon is still serving plain HTTP.
func httpsEndpoint(cfg *config.Config) string {
	return fmt.Sprintf("https://localhost:%d", hostPort(cfg))
}

const restartCommand = "OVERCAST_TLS=auto overcast serve"

// caShareCommand is the host-side recipe that makes the CA outlive the
// container: create it once on the host (a trust install is per-machine
// anyway), then mount it read-only so the daemon mints leaves from an anchor
// it does not own. This is the mkcert/Caddy/step-ca division — durable layer
// holds the anchor, ephemeral layer holds the leaves — and it is the only
// arrangement in which `docker compose down` costs nothing.
const caShareCommand = `overcast https enable   # once per machine, on the host

docker run -e OVERCAST_TLS=auto \
  -e OVERCAST_CA_DIR=/ca -v ~/.overcast/data/ca:/ca:ro \
  -p 4566:4566 -p 4567:4567 ghcr.io/neaox/overcast:alpha`

// tlsStatusHandler serves GET /_overcast/tls/status.
func tlsStatusHandler(cfg *config.Config, logger *zap.Logger) http.HandlerFunc {
	caDir := cfg.CACertDir()
	certPath := filepath.Join(caDir, trust.CACertFile)
	return func(w http.ResponseWriter, r *http.Request) {
		resp := tlsStatusResponse{
			Mode:          tlsMode(cfg),
			InContainer:   runningInContainer(),
			HTTPSEndpoint: httpsEndpoint(cfg),
		}
		if _, err := os.Stat(certPath); err == nil {
			resp.CAExists = true
		}
		if resp.Mode == "off" {
			resp.RestartCommand = restartCommand
		}
		if resp.InContainer && !cfg.CADirConfigured {
			resp.CAEphemeral = true
			resp.CAShareCommand = caShareCommand
		}
		// Only meaningful where the daemon's filesystem is the caller's: in a
		// container this path names a file the host cannot open. Mode "off"
		// reports the auto-mode path the CA will live at, since that is the
		// bundle the user will need once they restart.
		if !resp.InContainer {
			if resp.Mode == "explicit" {
				resp.CACertPath = cfg.TLSCertFile
			} else {
				resp.CACertPath = certPath
			}
		}
		switch {
		case resp.InContainer:
			// The trust store that matters is the host's; the daemon can
			// neither read nor write it from inside a container.
			resp.TrustStore = trustUnknown
			resp.HostCommand = hostTrustCommand(cfg)
		default:
			store, err := newTrustStore(logger, caDir)
			switch {
			case errors.Is(err, trust.ErrUnsupported):
				resp.TrustStore = trustUnavailable
				resp.HostCommand = hostTrustCommand(cfg)
			case err != nil:
				resp.TrustStore = trustUnavailable
				logger.Warn("trust store unavailable", zap.Error(err))
			default:
				installed, err := store.Installed(r.Context())
				switch {
				case err != nil:
					// A failed check is not "not installed" — the console
					// must not offer an install button off a broken signal.
					resp.TrustStore = trustUnknown
					logger.Warn("trust store check failed", zap.Error(err))
				case installed:
					resp.TrustStore = trustInstalled
				default:
					resp.TrustStore = trustNotInstalled
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck // best-effort HTTP write.
	}
}

// tlsSetupHandler serves POST /_overcast/tls/setup — the in-daemon equivalent
// of `overcast https enable` (local flavour): create the CA if missing, mint
// or refresh the server leaf for every advertised name, and install the CA
// certificate into the system trust store. Every step is idempotent, exactly
// like the CLI command.
//
// Where the daemon cannot reach the trust store that matters — it is
// containerized (the store is the HOST's), or the platform has no backend —
// setup still prepares the certificate material and reports
// trustInstalled=false with the host-side command. That ordering is what lets
// the console offer a working "download the CA certificate" link (/api/ca.pem)
// and the `--endpoint` one-liner BEFORE the restart, so by the time the user
// first opens the https console the browser already trusts it.
func tlsSetupHandler(cfg *config.Config, logger *zap.Logger) http.HandlerFunc {
	caDir := cfg.CACertDir()
	return func(w http.ResponseWriter, r *http.Request) {
		// Installing a root CA on a click must never be reachable from a
		// hostile web page. The API port's CORS is deliberately wildcard (SDK
		// browser clients), so the guard has to be server-side: reject any
		// browser-sent Origin that is not one of this daemon's own names.
		if origin := r.Header.Get("Origin"); !originIsLocal(origin, cfg) {
			writeTLSSettingsError(w, http.StatusForbidden, tlsSettingsError{
				Error:   "ForeignOrigin",
				Message: fmt.Sprintf("cross-origin requests may not modify the system trust store (origin %q)", origin),
			})
			return
		}

		// 1. Certificate material — always, in every environment.
		if _, err := trust.LoadOrCreateCA(caDir); err != nil {
			writeTLSSettingsError(w, http.StatusInternalServerError, tlsSettingsError{
				Error: "CAError", Message: fmt.Sprintf("create local CA: %v", err),
			})
			return
		}
		if _, _, err := trust.ServerCertificate(caDir, cfg.TLSAutoSANs()); err != nil {
			writeTLSSettingsError(w, http.StatusInternalServerError, tlsSettingsError{
				Error: "CertificateError", Message: fmt.Sprintf("mint server certificate: %v", err),
			})
			return
		}

		resp := tlsSetupResponse{
			OK:              true,
			CAReady:         true,
			RestartRequired: !cfg.TLSEnabled(),
		}
		if resp.RestartRequired {
			resp.RestartCommand = restartCommand
		}

		// 2. Trust — only reachable from here for a native daemon.
		if runningInContainer() {
			resp.HostCommand = hostTrustCommand(cfg)
			logger.Info("web console prepared TLS certificates; trust must be installed from the host",
				zap.String("host_command", resp.HostCommand))
			writeTLSSettingsResponse(w, resp)
			return
		}
		store, err := newTrustStore(logger, caDir)
		if errors.Is(err, trust.ErrUnsupported) {
			// No backend (e.g. FreeBSD): material is ready, install is manual.
			resp.HostCommand = hostTrustCommand(cfg)
			writeTLSSettingsResponse(w, resp)
			return
		}
		if err != nil {
			writeTLSSettingsError(w, http.StatusInternalServerError, tlsSettingsError{
				Error: "TrustStoreError", Message: err.Error(),
			})
			return
		}

		alreadyInstalled, err := store.Installed(r.Context())
		if err != nil {
			logger.Warn("trust store check failed before install", zap.Error(err))
		}
		if err := store.Install(r.Context()); err != nil {
			// The one predictable failure here is Linux without root —
			// update-ca-certificates needs it — so the message points at the
			// command that asks for it rather than a bare permission error.
			writeTLSSettingsError(w, http.StatusInternalServerError, tlsSettingsError{
				Error:   "TrustInstallFailed",
				Message: fmt.Sprintf("install CA into the system trust store: %v — if this is a permissions error, run `sudo overcast https enable` instead", err),
			})
			return
		}
		logger.Info("web console installed the overcast CA into the system trust store",
			zap.Bool("already_installed", alreadyInstalled))

		resp.TrustInstalled = true
		resp.AlreadyInstalled = alreadyInstalled
		writeTLSSettingsResponse(w, resp)
	}
}

func writeTLSSettingsResponse(w http.ResponseWriter, resp tlsSetupResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck // best-effort HTTP write.
}

// originIsLocal reports whether a browser-sent Origin header names this
// daemon rather than a foreign site. No Origin at all is allowed — that is
// every non-browser client (the BFF proxy, curl, SDKs). A present Origin must
// have a hostname the daemon itself advertises: a loopback name/address or
// one of the TLS SANs (which are all loopback by construction —
// localhost.overcast.sh and friends resolve to 127.0.0.1).
func originIsLocal(origin string, cfg *config.Config) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, san := range cfg.TLSAutoSANs() {
		if matchesSAN(host, san) {
			return true
		}
	}
	return false
}

// matchesSAN reports whether host matches a SAN entry, honouring the
// single-label wildcard TLS semantics the certificates themselves use.
func matchesSAN(host, san string) bool {
	san = strings.ToLower(san)
	if rest, ok := strings.CutPrefix(san, "*."); ok {
		suffix := "." + rest
		label, found := strings.CutSuffix(host, suffix)
		return found && label != "" && !strings.Contains(label, ".")
	}
	return host == san
}

func writeTLSSettingsError(w http.ResponseWriter, status int, e tlsSettingsError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(e) //nolint:errcheck // best-effort HTTP write.
}
