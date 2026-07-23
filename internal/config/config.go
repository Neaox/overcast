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
	// StateBackendMemory stores all state in-process. Fastest; nothing persists
	// across restarts. Best for unit tests and CI pipelines.
	StateBackendMemory StateBackend = "memory"

	// StateBackendPersistent writes every mutation synchronously to SQLite.
	// Slowest; fully durable. Previously named "sqlite" (still accepted as alias).
	StateBackendPersistent StateBackend = "persistent"

	// StateBackendHybrid serves all reads from an in-memory map and flushes
	// writes to SQLite asynchronously at a configurable interval. Fast reads,
	// durable across restarts, with a small window of potential data loss on
	// unclean exit. This is the default — best for general local development.
	StateBackendHybrid StateBackend = "hybrid"

	// StateBackendWAL uses the append-log WALStore (memory reads, write-ahead
	// durability with replay on startup and periodic compaction).
	StateBackendWAL StateBackend = "wal"
)

// EKSMode identifies how the EKS service behaves.
type EKSMode string

const (
	// EKSModeMock keeps EKS metadata-only and does not start a Kubernetes API server.
	EKSModeMock EKSMode = "mock"

	// EKSModeLive enables the future live k3s-backed control plane path.
	EKSModeLive EKSMode = "live"
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

	// Hostname is the externally-reachable hostname or IP that services embed
	// in URLs returned to clients (e.g. SQS QueueUrl, SNS UnsubscribeURL,
	// RDS Endpoint.Address). When empty, defaults to "localhost".
	// Set OVERCAST_HOSTNAME when Overcast runs in Docker Compose alongside
	// app containers that need to reach it by its service name.
	Hostname string

	// Services is the set of AWS services to enable.
	// Map key is the lowercase service name, e.g. "s3", "sqs".
	Services map[string]bool

	// State controls the global storage backend used for all services.
	// Individual services may override this via ServiceStates.
	State StateBackend

	// ServiceStates overrides the global State for individual services.
	// Keys are lowercase service names ("s3", "sqs", "dynamodb", etc.).
	// Entries missing from the map inherit the global State.
	ServiceStates map[string]StateBackend

	// HybridFlushInterval controls how often the hybrid store flushes
	// in-memory state to disk. Only meaningful when State or a per-service
	// mode is "hybrid".
	HybridFlushInterval time.Duration

	// HybridSyncMode controls fsync policy for the hybrid store's pending
	// log. Valid values: always, interval, never. Corresponds to
	// state.WALSyncMode (the hybrid pending log reuses the same sync-mode
	// mechanism as the WAL backend).
	HybridSyncMode string

	// HybridSyncInterval controls periodic fsync cadence for the hybrid
	// pending log when HybridSyncMode is "interval".
	HybridSyncInterval time.Duration

	// HybridDirtyEntryThreshold triggers an early, out-of-band hybrid flush
	// once this many unflushed pending-log operations have accumulated,
	// ahead of the next HybridFlushInterval tick. A value <= 0 disables the
	// entry-count trigger (the byte threshold still applies unless it is
	// also disabled).
	HybridDirtyEntryThreshold int

	// HybridDirtyByteThreshold triggers an early, out-of-band hybrid flush
	// once the approximate byte size of unflushed writes exceeds this many
	// bytes. A value <= 0 disables the byte-size trigger.
	HybridDirtyByteThreshold int64

	// HybridMaintenanceInterval controls how often the hybrid store's
	// background loop runs routine SQLite housekeeping (a passive WAL
	// checkpoint plus a conditional incremental vacuum — see
	// docs/storage-plan.md item 3.5). Never runs on the request path.
	HybridMaintenanceInterval time.Duration

	// WALFsyncMode controls fsync policy for the WAL backend.
	// Valid values: always, interval, never.
	WALFsyncMode string

	// WALFsyncInterval controls periodic fsync cadence when WALFsyncMode is
	// set to interval.
	WALFsyncInterval time.Duration

	// WALMaxLogBytes triggers log compaction when the append log grows past
	// this size.
	WALMaxLogBytes int64

	// DataDir is the root directory for the SQLite file and any
	// on-disk state (analogous to LocalStack's DATA_DIR).
	DataDir string

	// Region is the default AWS region reported in ARNs and responses.
	Region string

	// AccountID is the fake AWS account ID embedded in ARNs.
	AccountID string

	// EKSMode controls whether the EKS service stays metadata-only (`mock`) or
	// enables the live k3s-backed control plane path (`live`).
	EKSMode EKSMode

	// SigV4Validate enables SigV4 signature verification.
	SigV4Validate bool

	// EnforceIAM enables opt-in IAM authorization enforcement middleware.
	// Default false.
	EnforceIAM bool

	// CFNSyncWait is the bounded time CloudFormation waits for fast stack
	// create/update/delete provisioning to reach a terminal state before returning.
	// A zero value disables the wait and restores fully asynchronous behaviour.
	// Corresponds to env var OVERCAST_CFN_SYNC_WAIT_MS. Default 1000ms.
	CFNSyncWait time.Duration

	// ProtocolDispatch enables the typed wire-protocol dispatcher and the

	// ShutdownTimeout is how long the server waits for in-flight
	// requests to complete before forcibly closing.
	ShutdownTimeout time.Duration

	// LogLevel controls log verbosity: "debug", "info", "warn", "error".
	LogLevel string

	// LambdaDockerSocket is the path to the Docker daemon socket used to
	// manage Lambda container siblings. Defaults to the platform Docker socket
	// (/var/run/docker.sock on Linux/macOS, npipe:////./pipe/docker_engine on Windows).
	LambdaDockerSocket string

	// LambdaNetwork is the Docker network name that Lambda containers are
	// attached to. Must be reachable from the Overcast container.
	// Defaults to "overcast_lambda".
	LambdaNetwork string

	// LambdaRuntimeAPIPort is the port on which Overcast exposes the Lambda
	// Runtime API to containers. Each container connects back on this port.
	// Defaults to 9001.
	LambdaRuntimeAPIPort int

	// LambdaDockerMaxConcurrentStarts bounds concurrent Docker-backed Lambda
	// environment starts. This is local Docker backpressure, not an AWS-facing
	// Lambda concurrency quota. Corresponds to env var
	// LAMBDA_DOCKER_MAX_CONCURRENT_STARTS. Default 4.
	LambdaDockerMaxConcurrentStarts int

	// LambdaSeedRuntimeImages controls whether Overcast pre-pulls every known
	// managed Lambda runtime image when the Docker runtime starts. Disabled by
	// default to avoid Docker Desktop/containerd pressure during frequent restarts;
	// runtime images are still pulled lazily on first use.
	// Corresponds to env var LAMBDA_SEED_RUNTIME_IMAGES. Default false.
	LambdaSeedRuntimeImages bool

	// LambdaInitTimeout is the maximum time to wait for a Docker-backed Lambda
	// runtime to finish INIT and poll the Runtime API for its first invocation.
	// This is separate from the function invocation timeout. Corresponds to env
	// var LAMBDA_INIT_TIMEOUT_SECONDS. Default 10s.
	LambdaInitTimeout time.Duration

	// LambdaKeepContainers controls whether Docker containers are removed when
	// a Lambda instance expires (idle timeout) or the function is deleted.
	// Set to true to keep stopped containers for post-mortem inspection.
	// Corresponds to env var LAMBDA_KEEP_CONTAINERS. Default false.
	LambdaKeepContainers bool

	// LambdaHotReload enables bind-mount based source reload for functions that
	// opt in via the overcast:hot-reload-path function tag.
	// Corresponds to env var OVERCAST_LAMBDA_HOT_RELOAD. Default false.
	LambdaHotReload bool

	// LambdaFetchRemoteLayers enables downloading layer content from real AWS
	// when a layer ARN is not found locally. Requires valid AWS credentials.
	// Downloaded layers are cached on disk and have /opt/extensions/ stripped
	// (extensions can't run locally without the full Lambda platform).
	// Corresponds to env var LAMBDA_FETCH_REMOTE_LAYERS. Default false.
	LambdaFetchRemoteLayers bool

	// LambdaLayerCacheDir overrides the directory used to look up and cache
	// layer zip files. When empty, defaults to {DataDir}/layers (typically
	// /data/layers in the standard Docker image).
	// Users can pre-download layers and mount this directory to avoid needing
	// AWS credentials at runtime. Files are named {sha256(arn)}.zip.
	// Corresponds to env var LAMBDA_LAYER_CACHE_DIR.
	LambdaLayerCacheDir string

	// LambdaRemoteAWSAccessKeyID is the AWS access key used for fetching
	// remote layers. Read from LAMBDA_REMOTE_AWS_ACCESS_KEY_ID.
	LambdaRemoteAWSAccessKeyID string

	// LambdaRemoteAWSSecretAccessKey is the AWS secret key used for fetching
	// remote layers. Read from LAMBDA_REMOTE_AWS_SECRET_ACCESS_KEY.
	LambdaRemoteAWSSecretAccessKey string

	// LambdaRemoteAWSSessionToken is the optional session token for fetching
	// remote layers. Read from LAMBDA_REMOTE_AWS_SESSION_TOKEN.
	LambdaRemoteAWSSessionToken string

	// ECSDockerSocket is the path to the Docker daemon socket used to manage
	// ECS task containers. Defaults to the same value as LambdaDockerSocket.
	ECSDockerSocket string

	// ECSNetwork is the Docker network name that ECS task containers are
	// attached to. Defaults to "overcast_ecs".
	ECSNetwork string

	// ECSKeepContainers controls whether Docker containers are removed when
	// an ECS task stops. Set to true for post-mortem inspection.
	// Corresponds to env var ECS_KEEP_CONTAINERS. Default false.
	ECSKeepContainers bool

	// RDSDockerSocket is the path to the Docker daemon socket used to manage
	// RDS database containers. Defaults to the same value as LambdaDockerSocket.
	RDSDockerSocket string

	// RDSNetwork is the Docker network name that RDS database containers are
	// attached to. Defaults to "overcast_rds".
	RDSNetwork string

	// RDSPortBase is the starting host port for RDS database containers.
	// Each DB instance gets a sequential port starting from this base.
	// Defaults to 33060.
	RDSPortBase int

	// RDSKeepContainers controls whether Docker containers are removed when
	// an RDS instance is deleted. Set to true for post-mortem inspection.
	// Corresponds to env var RDS_KEEP_CONTAINERS. Default false.
	RDSKeepContainers bool

	// ElastiCacheDockerSocket is the path to the Docker daemon socket used to
	// manage ElastiCache Redis/Valkey containers. Defaults to the same value as
	// LambdaDockerSocket.
	ElastiCacheDockerSocket string

	// ElastiCacheNetwork is the Docker network name that ElastiCache containers
	// are attached to. Defaults to "overcast_elasticache".
	ElastiCacheNetwork string

	// ElastiCachePortBase is the starting host port for ElastiCache containers.
	// Each cache cluster gets a sequential port starting from this base.
	// Defaults to 63790.
	ElastiCachePortBase int

	// ElastiCacheKeepContainers controls whether Docker containers are removed
	// when a cache cluster is deleted. Set to true for post-mortem inspection.
	// Corresponds to env var ELASTICACHE_KEEP_CONTAINERS. Default false.
	ElastiCacheKeepContainers bool

	// MSKDockerSocket is the path to the Docker daemon socket for MSK Redpanda containers.
	MSKDockerSocket string

	// MSKNetwork is the Docker network name for MSK containers.
	MSKNetwork string

	// MSKPortBase is the starting host port for MSK containers.
	MSKPortBase int

	// MSKKeepContainers controls whether Docker containers are removed on delete.
	MSKKeepContainers bool

	// EKSDockerSocket is the path to the Docker daemon socket used to manage
	// EKS live-mode control-plane containers. Defaults to the same value as
	// LambdaDockerSocket.
	EKSDockerSocket string

	// EKSNetwork is the Docker network name that EKS live-mode containers are
	// attached to. Defaults to "overcast_eks".
	EKSNetwork string

	// EC2VPCNetworkStrategy selects the policy used to map stored VPCs onto
	// Docker networks. Docker bridges share one host address space, so two
	// VPCs with overlapping CIDRs cannot both back real networks. Valid
	// values:
	//
	//   shared   (default) — overlapping VPCs share one Docker network.
	//                        Fastest, isolation leaks between sharers.
	//   strict              — reject overlapping CIDRs at CreateVpc; startup
	//                        tolerates existing overlaps. (future)
	//   remapped            — allocate a shadow CIDR from 100.64.0.0/10
	//                        when the requested range collides. (future)
	//   netns               — per-VPC Linux netns for true overlap. (future)
	//
	// Values other than "shared" currently fall back to "shared" with a
	// startup warning. Corresponds to env var OVERCAST_EC2_VPC_STRATEGY.
	EC2VPCNetworkStrategy string

	// Debug enables the /_debug/* endpoint namespace.
	// These endpoints expose internal state and should never be enabled
	// in shared or production environments.
	Debug bool

	// TLSCertFile is the path to the TLS certificate file.
	// When set (together with TLSKeyFile), the server uses HTTPS.
	TLSCertFile string

	// TLSKeyFile is the path to the TLS private key file.
	TLSKeyFile string

	// SMTPMock enables the built-in SMTP capture server. When true, all
	// outbound SNS email/email-json notifications are delivered to the local
	// capture server and are browseable in the web UI under /mail.
	// Automatically set to false when SMTPHost is configured.
	SMTPMock bool

	// SMTPPort is the TCP port the mock SMTP server listens on, and also the
	// default port the mailer dials when SMTPHost is unset.
	SMTPPort int

	// SMTPHost is the hostname of an external SMTP relay. When set, SMTPMock
	// is automatically false and the built-in capture server is not started.
	SMTPHost string

	// SMTPFrom is the envelope From address used when sending SNS email notifications.
	SMTPFrom string

	// SMTPUsername and SMTPPassword are the credentials for SMTP AUTH PLAIN
	// when connecting to an external relay. Leave empty for no authentication.
	SMTPUsername string
	SMTPPassword string

	// SMTPTLS enables implicit TLS (port 465). For STARTTLS (port 587) leave
	// this false — Go's net/smtp client upgrades automatically.
	SMTPTLS bool

	// SMTPInboxMax is the maximum number of messages the capture store retains.
	// When exceeded, the oldest message is evicted.
	SMTPInboxMax int

	// InitEnabled controls whether init hook scripts are executed.
	// Defaults to true.
	InitEnabled bool

	// InitDirs is the list of base directories to scan for init hook scripts.
	// Each directory should contain stage subdirs: boot.d/, start.d/, ready.d/,
	// shutdown.d/. Scripts are executed in the order directories appear, then
	// alphabetically within each directory.
	// Defaults to ["/etc/localstack/init", "/etc/overcast/init"].
	InitDirs []string

	// InitTimeout is the maximum time allowed for each individual init script.
	// Defaults to 30s.
	InitTimeout time.Duration

	// Version is the build version string, injected via -ldflags at build time.
	// Not loaded from environment — set by the caller after Load().
	Version string

	// MCPReplayLimit bounds in-memory MCP notification replay history for
	// Last-Event-ID reconnect support. A value of 0 disables replay retention.
	// Default: 256.
	MCPReplayLimit int

	// MCPRemoteExposure explicitly enables remote/runtime MCP exposure mode.
	// When true, MCPAuthToken must be configured.
	MCPRemoteExposure bool

	// MCPAuthToken is the bearer token required for HTTP access to runtime MCP
	// when MCPRemoteExposure is enabled.
	MCPAuthToken string
}

// Addr returns the "host:port" string for the server to listen on.
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// ExternalHostname returns the hostname that should appear in client-facing
// URLs. Returns Hostname if set, otherwise "localhost".
func (c *Config) ExternalHostname() string {
	if c.Hostname != "" {
		return c.Hostname
	}
	return "localhost"
}

// ExternalBaseURL returns the base URL for client-facing links, e.g.
// "http://localhost:4566" or "http://overcast:4566".
func (c *Config) ExternalBaseURL() string {
	scheme := "http"
	if c.TLSEnabled() {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, c.ExternalHostname(), c.Port)
}

// TLSEnabled returns true when both TLS cert and key are configured.
func (c *Config) TLSEnabled() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

// allServices is the canonical list of supported service names.
var allServices = []string{"s3", "sqs", "sns", "ses", "dynamodb", "dynamodbstreams", "lambda", "pipes", "logs", "secretsmanager", "sts", "ssm", "kms", "iam", "cloudformation", "ec2", "rds", "ecs", "ecr", "eks", "cognito", "stepfunctions", "waf", "shield", "appsync", "apigateway", "cloudfront", "eventbridge", "kinesis", "appregistry", "cloudwatch", "acm", "opensearch", "appconfig", "appconfigdata", "bedrock", "glue", "firehose", "athena", "elasticache", "msk", "scheduler", "route53", "elbv2", "organizations", "autoscaling", "cloudtrail", "backup", "transfer"}

// Load reads configuration from environment variables and returns a validated
// Config. Returns an error if any required value is invalid.
//
// Environment variables (all optional, defaults shown):
//
//	OVERCAST_HOST                      0.0.0.0
//	OVERCAST_HOSTNAME                  (empty — defaults to localhost in URLs)
//	OVERCAST_PORT                      4566
//	OVERCAST_SERVICES                  s3,sqs,sns,ses,dynamodb,dynamodbstreams,lambda
//	OVERCAST_STATE                     hybrid  (memory | persistent | hybrid | wal)
//	OVERCAST_STATE_<SERVICE>           <mode>  (per-service override, e.g. OVERCAST_STATE_S3=memory)
//	OVERCAST_HYBRID_FLUSH_INTERVAL     5s
//	OVERCAST_HYBRID_SYNC                interval (always | interval | never)
//	OVERCAST_HYBRID_SYNC_INTERVAL       100ms
//	OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD 10000
//	OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD  8388608
//	OVERCAST_HYBRID_MAINTENANCE_INTERVAL  5m
//	OVERCAST_WAL_FSYNC                 interval (always | interval | never)
//	OVERCAST_WAL_FSYNC_INTERVAL        100ms
//	OVERCAST_WAL_MAX_LOG_BYTES         67108864
//	OVERCAST_DATA_DIR                  ~/.overcast/data
//	OVERCAST_DEFAULT_REGION             us-east-1
//	OVERCAST_ACCOUNT_ID                000000000000
//	OVERCAST_EKS_MODE                  mock    (mock | live)
//	OVERCAST_SIGV4_VALIDATE            false
//	OVERCAST_ENFORCE_IAM              false
//	OVERCAST_CFN_SYNC_WAIT_MS          1000
//	OVERCAST_LOG_LEVEL                 info
//	OVERCAST_SHUTDOWN_TIMEOUT          5s
//	OVERCAST_LAMBDA_NODE_BIN           node
//	OVERCAST_LAMBDA_HOT_RELOAD         false
//	OVERCAST_DEBUG                     false
//	OVERCAST_TLS_CERT                  ""
//	OVERCAST_TLS_KEY                   ""
//	LAMBDA_DOCKER_MAX_CONCURRENT_STARTS 4
//	LAMBDA_INIT_TIMEOUT_SECONDS       10
//	LAMBDA_KEEP_CONTAINERS             false (true = keep stopped containers after expiry/delete)
//	LAMBDA_FETCH_REMOTE_LAYERS         false (true = download missing layers from real AWS)
//	ECS_DOCKER_SOCKET                  <LAMBDA_DOCKER_SOCKET> (default: same as Lambda)
//	ECS_NETWORK                        overcast_ecs
//	ECS_KEEP_CONTAINERS                false
//	RDS_DOCKER_SOCKET                  <LAMBDA_DOCKER_SOCKET> (default: same as Lambda)
//	RDS_NETWORK                        overcast_rds
//	RDS_PORT_BASE                      33060
//	RDS_KEEP_CONTAINERS                false
//	ELASTICACHE_DOCKER_SOCKET          <LAMBDA_DOCKER_SOCKET> (default: same as Lambda)
//	ELASTICACHE_NETWORK                overcast_elasticache
//	ELASTICACHE_PORT_BASE              63790
//	ELASTICACHE_KEEP_CONTAINERS        false
//	MSK_DOCKER_SOCKET                  <LAMBDA_DOCKER_SOCKET>
//	MSK_NETWORK                        overcast_msk
//	MSK_PORT_BASE                      49092
//	MSK_KEEP_CONTAINERS                false
//	EKS_DOCKER_SOCKET                  <LAMBDA_DOCKER_SOCKET>
//	EKS_NETWORK                        overcast_eks
//	OVERCAST_SMTP_MOCK                 true  (false when SMTP_HOST is set)
//	OVERCAST_SMTP_PORT                 1025
//	OVERCAST_SMTP_HOST                 ""    (set to use an external relay)
//	OVERCAST_SMTP_FROM                 overcast@localhost
//	OVERCAST_SMTP_USERNAME             ""
//	OVERCAST_SMTP_PASSWORD             ""
//	OVERCAST_SMTP_TLS                  false
//	OVERCAST_SMTP_INBOX_MAX            500
//	OVERCAST_INIT_ENABLED              true  (set false to disable init hooks)
//	OVERCAST_INIT_DIRS                 /etc/localstack/init,/etc/overcast/init
//	OVERCAST_INIT_TIMEOUT              30s   (per-script timeout)
//	OVERCAST_MCP_REPLAY_LIMIT          256
//	OVERCAST_MCP_REMOTE_EXPOSURE       false
//	OVERCAST_MCP_AUTH_TOKEN            "" (required when OVERCAST_MCP_REMOTE_EXPOSURE=true)
func Load() (*Config, error) {
	cfg := &Config{}

	// Host
	cfg.Host = envOr("OVERCAST_HOST", "0.0.0.0")

	// Hostname (external — for client-facing URLs)
	cfg.Hostname = os.Getenv("OVERCAST_HOSTNAME")

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

	// State backend — accept "sqlite" as a deprecated alias for "persistent"
	rawBackend := strings.ToLower(envOr("OVERCAST_STATE", string(StateBackendHybrid)))
	if rawBackend == "sqlite" {
		rawBackend = string(StateBackendPersistent)
	}
	backend := StateBackend(rawBackend)
	if err := validateStateBackend(backend, "OVERCAST_STATE"); err != nil {
		return nil, err
	}
	cfg.State = backend

	// Hybrid flush interval
	flushStr := envOr("OVERCAST_HYBRID_FLUSH_INTERVAL", "5s")
	cfg.HybridFlushInterval, err = time.ParseDuration(flushStr)
	if err != nil {
		return nil, fmt.Errorf("config: OVERCAST_HYBRID_FLUSH_INTERVAL %q is not a valid duration", flushStr)
	}

	// Hybrid pending-log sync mode — mirrors the WAL sync settings below,
	// applied to the hybrid store's own pending log instead.
	cfg.HybridSyncMode = strings.ToLower(strings.TrimSpace(envOr("OVERCAST_HYBRID_SYNC", "interval")))
	switch cfg.HybridSyncMode {
	case "always", "interval", "never":
	default:
		return nil, fmt.Errorf("config: OVERCAST_HYBRID_SYNC must be 'always', 'interval', or 'never', got %q", cfg.HybridSyncMode)
	}
	hybridSyncIntervalStr := envOr("OVERCAST_HYBRID_SYNC_INTERVAL", "100ms")
	cfg.HybridSyncInterval, err = time.ParseDuration(hybridSyncIntervalStr)
	if err != nil || cfg.HybridSyncInterval <= 0 {
		return nil, fmt.Errorf("config: OVERCAST_HYBRID_SYNC_INTERVAL %q is not a valid positive duration", hybridSyncIntervalStr)
	}

	// Hybrid size-triggered flush thresholds.
	cfg.HybridDirtyEntryThreshold = envInt("OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD", 10000)
	hybridByteThresholdStr := envOr("OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD", "8388608")
	hybridByteThreshold, err := strconv.ParseInt(hybridByteThresholdStr, 10, 64)
	if err != nil || hybridByteThreshold <= 0 {
		return nil, fmt.Errorf("config: OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD %q must be a positive integer", hybridByteThresholdStr)
	}
	cfg.HybridDirtyByteThreshold = hybridByteThreshold

	// Hybrid background maintenance loop interval (3.5: passive WAL
	// checkpoint + conditional incremental vacuum).
	maintenanceIntervalStr := envOr("OVERCAST_HYBRID_MAINTENANCE_INTERVAL", "5m")
	cfg.HybridMaintenanceInterval, err = time.ParseDuration(maintenanceIntervalStr)
	if err != nil || cfg.HybridMaintenanceInterval <= 0 {
		return nil, fmt.Errorf("config: OVERCAST_HYBRID_MAINTENANCE_INTERVAL %q is not a valid positive duration", maintenanceIntervalStr)
	}

	// WAL settings
	cfg.WALFsyncMode = strings.ToLower(strings.TrimSpace(envOr("OVERCAST_WAL_FSYNC", "interval")))
	switch cfg.WALFsyncMode {
	case "always", "interval", "never":
	default:
		return nil, fmt.Errorf("config: OVERCAST_WAL_FSYNC must be 'always', 'interval', or 'never', got %q", cfg.WALFsyncMode)
	}
	walSyncIntervalStr := envOr("OVERCAST_WAL_FSYNC_INTERVAL", "100ms")
	cfg.WALFsyncInterval, err = time.ParseDuration(walSyncIntervalStr)
	if err != nil || cfg.WALFsyncInterval <= 0 {
		return nil, fmt.Errorf("config: OVERCAST_WAL_FSYNC_INTERVAL %q is not a valid positive duration", walSyncIntervalStr)
	}
	walMaxLogBytesStr := envOr("OVERCAST_WAL_MAX_LOG_BYTES", "67108864")
	walMaxLogBytes, err := strconv.ParseInt(walMaxLogBytesStr, 10, 64)
	if err != nil || walMaxLogBytes <= 0 {
		return nil, fmt.Errorf("config: OVERCAST_WAL_MAX_LOG_BYTES %q must be a positive integer", walMaxLogBytesStr)
	}
	cfg.WALMaxLogBytes = walMaxLogBytes

	// Per-service state overrides
	cfg.ServiceStates = make(map[string]StateBackend)
	for _, svc := range allServices {
		envKey := "OVERCAST_STATE_" + strings.ToUpper(strings.ReplaceAll(svc, "-", "_"))
		if v := os.Getenv(envKey); v != "" {
			raw := strings.ToLower(v)
			if raw == "sqlite" {
				raw = string(StateBackendPersistent)
			}
			svcBackend := StateBackend(raw)
			if err := validateStateBackend(svcBackend, envKey); err != nil {
				return nil, err
			}
			cfg.ServiceStates[svc] = svcBackend
		}
	}

	// Data directory
	cfg.DataDir = envOr("OVERCAST_DATA_DIR", defaultDataDir())

	// AWS identity defaults
	cfg.Region = envOr("OVERCAST_DEFAULT_REGION", "us-east-1")
	cfg.AccountID = envOr("OVERCAST_ACCOUNT_ID", "000000000000")

	// EKS mode
	rawEKSMode := strings.ToLower(strings.TrimSpace(envOr("OVERCAST_EKS_MODE", string(EKSModeMock))))
	cfg.EKSMode = EKSMode(rawEKSMode)
	if cfg.EKSMode != EKSModeMock && cfg.EKSMode != EKSModeLive {
		return nil, fmt.Errorf("config: OVERCAST_EKS_MODE %q is invalid (expected mock or live)", rawEKSMode)
	}

	// SigV4 validation
	cfg.SigV4Validate = envBool("OVERCAST_SIGV4_VALIDATE", false)

	// Optional IAM enforcement middleware (default off).
	cfg.EnforceIAM = envBool("OVERCAST_ENFORCE_IAM", false)

	// CloudFormation synchronous fast-path wait budget.
	cfg.CFNSyncWait = time.Duration(envInt("OVERCAST_CFN_SYNC_WAIT_MS", 1000)) * time.Millisecond
	if cfg.CFNSyncWait < 0 {
		cfg.CFNSyncWait = 0
	}

	// Logging
	cfg.LogLevel = strings.ToLower(envOr("OVERCAST_LOG_LEVEL", "info"))

	// Shutdown timeout
	timeoutStr := envOr("OVERCAST_SHUTDOWN_TIMEOUT", "5s")
	cfg.ShutdownTimeout, err = time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, fmt.Errorf("config: OVERCAST_SHUTDOWN_TIMEOUT %q is not a valid duration", timeoutStr)
	}

	// Lambda container runtime
	cfg.LambdaDockerSocket = envOr("LAMBDA_DOCKER_SOCKET", defaultDockerSocket)
	cfg.LambdaNetwork = envOr("LAMBDA_NETWORK", "overcast_lambda")
	cfg.LambdaRuntimeAPIPort = envInt("LAMBDA_RUNTIME_API_PORT", 9001)
	cfg.LambdaDockerMaxConcurrentStarts = envInt("LAMBDA_DOCKER_MAX_CONCURRENT_STARTS", 4)
	if cfg.LambdaDockerMaxConcurrentStarts < 1 {
		cfg.LambdaDockerMaxConcurrentStarts = 1
	}
	cfg.LambdaSeedRuntimeImages = envBool("LAMBDA_SEED_RUNTIME_IMAGES", false)
	cfg.LambdaInitTimeout = time.Duration(envInt("LAMBDA_INIT_TIMEOUT_SECONDS", 10)) * time.Second
	if cfg.LambdaInitTimeout <= 0 {
		cfg.LambdaInitTimeout = 10 * time.Second
	}
	cfg.LambdaKeepContainers = envBool("LAMBDA_KEEP_CONTAINERS", false)
	cfg.LambdaHotReload = envBool("OVERCAST_LAMBDA_HOT_RELOAD", false)
	cfg.LambdaFetchRemoteLayers = envBool("LAMBDA_FETCH_REMOTE_LAYERS", false)
	cfg.LambdaLayerCacheDir = envOr("LAMBDA_LAYER_CACHE_DIR", "")
	cfg.LambdaRemoteAWSAccessKeyID = envOr("LAMBDA_REMOTE_AWS_ACCESS_KEY_ID", "")
	cfg.LambdaRemoteAWSSecretAccessKey = envOr("LAMBDA_REMOTE_AWS_SECRET_ACCESS_KEY", "")
	cfg.LambdaRemoteAWSSessionToken = envOr("LAMBDA_REMOTE_AWS_SESSION_TOKEN", "")

	// ECS container runtime — defaults fall back to Lambda socket
	cfg.ECSDockerSocket = envOr("ECS_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.ECSNetwork = envOr("ECS_NETWORK", "overcast_ecs")
	cfg.ECSKeepContainers = envBool("ECS_KEEP_CONTAINERS", false)

	// RDS container runtime — defaults fall back to Lambda socket
	cfg.RDSDockerSocket = envOr("RDS_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.RDSNetwork = envOr("RDS_NETWORK", "overcast_rds")
	cfg.RDSPortBase = envInt("RDS_PORT_BASE", 33060)
	cfg.RDSKeepContainers = envBool("RDS_KEEP_CONTAINERS", false)

	// ElastiCache container runtime — defaults fall back to Lambda socket
	cfg.ElastiCacheDockerSocket = envOr("ELASTICACHE_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.ElastiCacheNetwork = envOr("ELASTICACHE_NETWORK", "overcast_elasticache")
	cfg.ElastiCachePortBase = envInt("ELASTICACHE_PORT_BASE", 63790)
	cfg.ElastiCacheKeepContainers = envBool("ELASTICACHE_KEEP_CONTAINERS", false)

	// MSK container runtime — defaults fall back to Lambda socket
	cfg.MSKDockerSocket = envOr("MSK_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.MSKNetwork = envOr("MSK_NETWORK", "overcast_msk")
	cfg.MSKPortBase = envInt("MSK_PORT_BASE", 49092)
	cfg.MSKKeepContainers = envBool("MSK_KEEP_CONTAINERS", false)

	// EKS live-mode container runtime — defaults fall back to Lambda socket
	cfg.EKSDockerSocket = envOr("EKS_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.EKSNetwork = envOr("EKS_NETWORK", "overcast_eks")

	// EC2 VPC network strategy — unknown values fall back to "shared" at
	// service construction with a logged warning. "netns" is explicitly
	// rejected here because that strategy is not implemented yet.
	cfg.EC2VPCNetworkStrategy = envOr("OVERCAST_EC2_VPC_STRATEGY", "shared")
	if strings.EqualFold(strings.TrimSpace(cfg.EC2VPCNetworkStrategy), "netns") {
		return nil, fmt.Errorf("config: OVERCAST_EC2_VPC_STRATEGY=netns is not supported yet; use shared, strict, or remapped")
	}

	// Debug endpoints
	cfg.Debug = envBool("OVERCAST_DEBUG", false)

	// TLS
	cfg.TLSCertFile = os.Getenv("OVERCAST_TLS_CERT")
	cfg.TLSKeyFile = os.Getenv("OVERCAST_TLS_KEY")
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return nil, fmt.Errorf("config: OVERCAST_TLS_CERT and OVERCAST_TLS_KEY must both be set or both be empty")
	}

	// SMTP
	cfg.SMTPHost = os.Getenv("OVERCAST_SMTP_HOST")
	cfg.SMTPFrom = envOr("OVERCAST_SMTP_FROM", "overcast@localhost")
	cfg.SMTPUsername = os.Getenv("OVERCAST_SMTP_USERNAME")
	cfg.SMTPPassword = os.Getenv("OVERCAST_SMTP_PASSWORD")
	cfg.SMTPTLS = envBool("OVERCAST_SMTP_TLS", false)

	smtpPortStr := envOr("OVERCAST_SMTP_PORT", "1025")
	smtpPort, err := strconv.Atoi(smtpPortStr)
	if err != nil || smtpPort < 1 || smtpPort > 65535 {
		return nil, fmt.Errorf("config: OVERCAST_SMTP_PORT %q is not a valid port number", smtpPortStr)
	}
	cfg.SMTPPort = smtpPort

	smtpInboxMaxStr := envOr("OVERCAST_SMTP_INBOX_MAX", "500")
	smtpInboxMax, err := strconv.Atoi(smtpInboxMaxStr)
	if err != nil || smtpInboxMax < 1 {
		return nil, fmt.Errorf("config: OVERCAST_SMTP_INBOX_MAX %q must be a positive integer", smtpInboxMaxStr)
	}
	cfg.SMTPInboxMax = smtpInboxMax

	// Mock mode: default true unless an external host is configured.
	if cfg.SMTPHost != "" {
		cfg.SMTPMock = false
	} else {
		cfg.SMTPMock = envBool("OVERCAST_SMTP_MOCK", true)
	}

	// Init hooks
	cfg.InitEnabled = envBool("OVERCAST_INIT_ENABLED", true)

	initDirsStr := envOr("OVERCAST_INIT_DIRS", "/etc/localstack/init,/etc/overcast/init")
	for _, d := range strings.Split(initDirsStr, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			cfg.InitDirs = append(cfg.InitDirs, d)
		}
	}

	initTimeoutStr := envOr("OVERCAST_INIT_TIMEOUT", "30s")
	cfg.InitTimeout, err = time.ParseDuration(initTimeoutStr)
	if err != nil {
		return nil, fmt.Errorf("config: OVERCAST_INIT_TIMEOUT %q is not a valid duration", initTimeoutStr)
	}

	mcpReplayStr := envOr("OVERCAST_MCP_REPLAY_LIMIT", "256")
	cfg.MCPReplayLimit, err = strconv.Atoi(mcpReplayStr)
	if err != nil || cfg.MCPReplayLimit < 0 {
		return nil, fmt.Errorf("config: OVERCAST_MCP_REPLAY_LIMIT %q must be a non-negative integer", mcpReplayStr)
	}

	cfg.MCPRemoteExposure = envBool("OVERCAST_MCP_REMOTE_EXPOSURE", false)
	cfg.MCPAuthToken = strings.TrimSpace(os.Getenv("OVERCAST_MCP_AUTH_TOKEN"))
	if cfg.MCPRemoteExposure && cfg.MCPAuthToken == "" {
		return nil, fmt.Errorf("config: OVERCAST_MCP_AUTH_TOKEN is required when OVERCAST_MCP_REMOTE_EXPOSURE=true")
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

// envInt parses an integer environment variable, returning fallback if the
// variable is unset, empty, or not a valid integer.
func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// defaultDataDir returns the path where persistent state is stored.
//
// Inside a dev container (detected via OVERCAST_DEVCONTAINER or the presence
// of /workspace) the data directory is placed inside the workspace mount so
// that it survives container rebuilds. Outside a dev container it falls back
// to ~/.overcast/data, mirroring LocalStack's DATA_DIR convention.
func defaultDataDir() string {
	// Dev container: /workspace is bind-mounted from the host.
	if _, err := os.Stat("/workspace"); err == nil {
		return "/workspace/.overcast/data"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "overcast", "data")
	}
	return filepath.Join(home, ".overcast", "data")
}

func isKnownService(s string) bool {
	for _, known := range allServices {
		if s == known {
			return true
		}
	}
	return false
}

// validateStateBackend returns an error if b is not one of the four recognised
// storage modes. envKey is included in the error message for context.
func validateStateBackend(b StateBackend, envKey string) error {
	switch b {
	case StateBackendMemory, StateBackendPersistent, StateBackendHybrid, StateBackendWAL:
		return nil
	default:
		return fmt.Errorf("config: %s must be 'memory', 'persistent', 'hybrid' or 'wal', got %q", envKey, b)
	}
}
