// Package config loads and validates all runtime configuration from environment
// variables. All other packages receive a *Config value — they never read
// os.Getenv directly. This makes configuration explicit and testable.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// StateBackend identifies which storage implementation to use.
type StateBackend string

const (
	StateBackendMemory StateBackend = "memory"
	StateBackendSQLite StateBackend = "sqlite"
)

// Config holds all runtime configuration for the emulator.
// Zero value is not valid — always construct via Load().
type Config struct {
	// Host is the hostname or IP address to bind to.
	// Equivalent to LocalStack's LOCALSTACK_HOST.
	// Use "127.0.0.1" to restrict to localhost only.
	// Defaults to "0.0.0.0" (all interfaces).
	Host string

	// Port is the TCP port the HTTP server listens on.
	Port int

	// Services is the set of AWS services to enable.
	// Map key is the lowercase service name, e.g. "s3", "sqs".
	Services map[string]bool

	// State controls which storage backend is used.
	State StateBackend

	// DataDir is the root directory for the SQLite file and any
	// on-disk state (analogous to LocalStack's DATA_DIR).
	DataDir string

	// Region is the default AWS region reported in ARNs and responses.
	Region string

	// AccountID is the fake AWS account ID embedded in ARNs.
	AccountID string

	// SigV4Validate enables SigV4 signature verification.
	// TODO: implement full SigV4 validation — currently always false.
	SigV4Validate bool

	// ShutdownTimeout is how long the server waits for in-flight
	// requests to complete before forcibly closing.
	ShutdownTimeout time.Duration

	// LogLevel controls log verbosity: "debug", "info", "warn", "error".
	LogLevel string

	// LambdaNodeBin is the path to the node binary used to execute
	// Lambda functions. Defaults to "node" (resolved via PATH).
	LambdaNodeBin string

	// Debug enables the /_debug/* endpoint namespace.
	// These endpoints expose internal state and should never be enabled
	// in shared or production environments.
	Debug bool

	// TLSCertFile is the path to the TLS certificate file.
	// When set (together with TLSKeyFile), the server uses HTTPS.
	TLSCertFile string

	// TLSKeyFile is the path to the TLS private key file.
	TLSKeyFile string
}

// Addr returns the "host:port" string for the server to listen on.
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// TLSEnabled returns true when both TLS cert and key are configured.
func (c *Config) TLSEnabled() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

// allServices is the canonical list of supported service names.
var allServices = []string{"s3", "sqs", "sns", "dynamodb", "lambda"}

// Load reads configuration from environment variables and returns a validated
// Config. Returns an error if any required value is invalid.
//
// Environment variables (all optional, defaults shown):
//
//	OVERCAST_HOST             0.0.0.0
//	OVERCAST_PORT             4566
//	OVERCAST_SERVICES         s3,sqs,sns,dynamodb,lambda
//	OVERCAST_STATE            memory
//	OVERCAST_DATA_DIR         ~/.overcast/data
//	OVERCAST_REGION           us-east-1
//	OVERCAST_ACCOUNT_ID       000000000000
//	OVERCAST_SIGV4_VALIDATE   false  (TODO: not yet implemented)
//	OVERCAST_LOG_LEVEL        info
//	OVERCAST_SHUTDOWN_TIMEOUT 30s
//	OVERCAST_LAMBDA_NODE_BIN  node
//	OVERCAST_DEBUG            false
//	OVERCAST_TLS_CERT         ""
//	OVERCAST_TLS_KEY          ""
func Load() (*Config, error) {
	cfg := &Config{}

	// Host
	cfg.Host = envOr("OVERCAST_HOST", "0.0.0.0")

	// Port
	portStr := envOr("OVERCAST_PORT", "4566")
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("config: OVERCAST_PORT %q is not a valid port number", portStr)
	}
	cfg.Port = port

	// Services
	svcStr := envOr("OVERCAST_SERVICES", strings.Join(allServices, ","))
	cfg.Services = make(map[string]bool)
	for _, s := range strings.Split(svcStr, ",") {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			continue
		}
		if !isKnownService(s) {
			return nil, fmt.Errorf("config: unknown service %q in OVERCAST_SERVICES", s)
		}
		cfg.Services[s] = true
	}

	// State backend
	backend := StateBackend(strings.ToLower(envOr("OVERCAST_STATE", "memory")))
	if backend != StateBackendMemory && backend != StateBackendSQLite {
		return nil, fmt.Errorf("config: OVERCAST_STATE must be 'memory' or 'sqlite', got %q", backend)
	}
	cfg.State = backend

	// Data directory
	cfg.DataDir = envOr("OVERCAST_DATA_DIR", defaultDataDir())

	// AWS identity defaults
	cfg.Region = envOr("OVERCAST_REGION", "us-east-1")
	cfg.AccountID = envOr("OVERCAST_ACCOUNT_ID", "000000000000")

	// SigV4 — stub; validation not yet implemented
	cfg.SigV4Validate = envBool("OVERCAST_SIGV4_VALIDATE", false)

	// Logging
	cfg.LogLevel = strings.ToLower(envOr("OVERCAST_LOG_LEVEL", "info"))

	// Shutdown timeout
	timeoutStr := envOr("OVERCAST_SHUTDOWN_TIMEOUT", "30s")
	cfg.ShutdownTimeout, err = time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, fmt.Errorf("config: OVERCAST_SHUTDOWN_TIMEOUT %q is not a valid duration", timeoutStr)
	}

	// Lambda node binary
	cfg.LambdaNodeBin = envOr("OVERCAST_LAMBDA_NODE_BIN", "node")

	// Debug endpoints
	cfg.Debug = envBool("OVERCAST_DEBUG", false)

	// TLS
	cfg.TLSCertFile = os.Getenv("OVERCAST_TLS_CERT")
	cfg.TLSKeyFile = os.Getenv("OVERCAST_TLS_KEY")
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return nil, fmt.Errorf("config: OVERCAST_TLS_CERT and OVERCAST_TLS_KEY must both be set or both be empty")
	}

	return cfg, nil
}

// envOr returns the value of the named environment variable, or fallback if
// the variable is unset or empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envBool parses a boolean environment variable. Accepts "true", "1", "yes".
func envBool(key string, fallback bool) bool {
	v := strings.ToLower(os.Getenv(key))
	switch v {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return fallback
	}
}

// defaultDataDir returns ~/.overcast/data, mirroring LocalStack's DATA_DIR
// convention so existing tooling works without reconfiguration.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "overcast", "data")
	}
	return home + "/.overcast/data"
}

func isKnownService(s string) bool {
	for _, known := range allServices {
		if s == known {
			return true
		}
	}
	return false
}
