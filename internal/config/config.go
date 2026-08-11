// Package config loads and validates all runtime configuration from environment
// variables. All other packages receive a *Config value — they never read
// os.Getenv directly. This makes configuration explicit and testable.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
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

// EFSMode identifies how the EFS service behaves.
type EFSMode string

const (
	// EFSModeMock keeps EFS metadata-only: file systems have no backing storage.
	// An explicit opt-out — the default is live.
	EFSModeMock EFSMode = "mock"

	// EFSModeLive backs each file system with a named Docker volume so
	// emulated compute (Lambda, ECS) can share real file data. The default,
	// because it costs nothing where it cannot be used: volume operations run
	// only while a Docker daemon is reachable, and without one live mode
	// behaves exactly like mock.
	EFSModeLive EFSMode = "live"
)

// RDSMode identifies how the RDS service behaves.
type RDSMode string

const (
	// RDSModeMock keeps RDS metadata-only: instances move through the status
	// model on a timer and no engine container is ever started, so nothing
	// listens on the reported endpoint. An explicit opt-out — the default is
	// live — for environments that have a Docker daemon but cannot afford the
	// tens of seconds a real engine takes to accept its first connection.
	RDSModeMock RDSMode = "mock"

	// RDSModeLive runs a real engine container per DB instance, which is what
	// makes the endpoint connectable. The default. Live mode needs no Docker to
	// be safe: without a reachable daemon it behaves exactly like mock.
	RDSModeLive RDSMode = "live"
)

// DefaultEFSNFSImage is the digest-pinned NFS-Ganesha image used for
// mount-target exports when OVERCAST_EFS_NFS is on. Pinned rather than
// floating so an upstream retag cannot change what a mount target runs.
//
// The image is Kubernetes SIG-Storage's NFS provisioner build; Overcast uses
// it only as a carrier for ganesha.nfsd and its VFS FSAL, replacing the
// entrypoint with its own configuration. It is multi-arch (amd64, arm64,
// ppc64le, s390x) and runs unprivileged.
const DefaultEFSNFSImage = "registry.k8s.io/sig-storage/nfs-provisioner@sha256:c825f3d5e28bde099bd7a3daace28772d412c9157ad47fa752a9ad0baafc118d"

// Config holds all runtime configuration for the emulator.
// Zero value is not valid — always construct via Load().
type Config struct {
	// Host is the hostname or IP address to bind to.
	// Equivalent to LocalStack's LOCALSTACK_HOST.
	// Use "127.0.0.1" to restrict to localhost only.
	// Defaults to "0.0.0.0" (all interfaces).
	// When several addresses are configured this is the first of them — see
	// Hosts.
	Host string

	// Hosts are every address the API listener binds, in order, with Hosts[0]
	// mirrored into Host. Load populates it; a Config built in code may leave
	// it nil, which Addrs() reads as "just Host".
	//
	// More than one address is for reaching the same emulator from two places
	// that no single address covers. The case that drove it: a throwaway
	// instance wants to be on loopback and nowhere routable, but a test suite
	// running in a sibling container reaches the host over the Docker bridge,
	// where loopback is invisible. Binding "127.0.0.1,172.17.0.1" serves both
	// without ever putting the emulator on a network the machine is attached
	// to. A wildcard cannot be combined with a specific address.
	//
	// The web UI listener binds Host only: the extra addresses exist so
	// container-side clients can reach the API, and the UI is a browser
	// surface on the machine itself.
	Hosts []string

	// Port is the TCP port the HTTP server listens on.
	Port int

	// Hostname is the externally-reachable hostname or IP that services embed
	// in URLs returned to clients (e.g. SQS QueueUrl, SNS UnsubscribeURL,
	// RDS Endpoint.Address). When empty, defaults to "localhost".
	// Set OVERCAST_HOSTNAME when Overcast runs in Docker Compose alongside
	// app containers that need to reach it by its service name.
	Hostname string

	// SplitHorizonHosts are extra hostnames that resolve to 127.0.0.1 in public
	// DNS and are remapped to Overcast's address inside containers Overcast
	// starts, so one URL is dialable from both the host and a sibling container.
	// Appended to the built-in set (localhost.overcast.sh,
	// localhost.localstack.cloud, localhost.floci.io) — see
	// internal/containerendpoint.
	SplitHorizonHosts []string

	// TestOnlyServiceSubset restricts which services router.New registers.
	// nil — the normal case, and the only case any production path produces —
	// registers every service.
	//
	// Load never populates this: there is no environment variable for it and
	// no way for a user to set it. It exists because a handful of router tests
	// need a server where nothing but the service under test is registered.
	// S3's bucket/object routes are deliberately broad, so proving that no
	// modeled operation falls through to them, or that an S3 bucket whose name
	// collides with another service's REST literal stays reachable, requires
	// removing the other claimants — there is no way to assert it against a
	// fully-registered router. See tests/integration/router and
	// docs/plans/host-routing-precedence.md.
	TestOnlyServiceSubset map[string]bool

	// State controls the global storage backend used for all services. This
	// is always a concrete backend (memory, persistent, hybrid, or wal) —
	// when OVERCAST_STATE is unset or "auto", it holds the *resolved* value
	// (see resolveAutoState); the raw configured value ("auto" or whatever
	// the user set) is preserved separately in StateConfigured.
	// Individual services may override this via ServiceStates.
	State StateBackend

	// StateConfigured is the raw value of OVERCAST_STATE as configured, or
	// "auto" if the variable was unset (auto is also the default). Unlike
	// State, this is never resolved — it exists so /_health and startup logs
	// can report what was actually configured, not just what it resolved to.
	StateConfigured string

	// StateSource reports whether State was chosen explicitly (OVERCAST_STATE
	// set to a concrete backend) or resolved by the OVERCAST_STATE=auto
	// heuristic. Drives the actionable variant of the memory-mode advisory
	// (internal/router/advisories.go checkMemoryMode) and the startup log
	// line below.
	StateSource StateSource

	// StateAutoReason is a short, human-readable explanation of why
	// resolveAutoState chose State. Empty when StateSource is "explicit".
	StateAutoReason string

	// StateAutoSignal is the stable code identifying which auto-detection
	// signal fired: "mountpoint", "explicit-data-dir", "existing-database",
	// or "" when none fired (State resolved to memory). Empty when
	// StateSource is "explicit". Safe to key tests off of, unlike
	// StateAutoReason's free text.
	StateAutoSignal string

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
	// docs/plans/storage-plan.md item 3.5). Never runs on the request path.
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

	// EFSMode controls whether the EFS service backs each file system with a
	// named Docker volume (`live`, the default) or stays metadata-only
	// (`mock`). Live mode needs no Docker to be safe: it falls back to
	// metadata-only whenever a daemon is unreachable.
	EFSMode EFSMode

	// RDSMode controls whether the RDS service starts a real engine container
	// per DB instance (`live`, the default) or stays metadata-only (`mock`).
	// Mock mode makes an instance reach `available` in a moment rather than in
	// the tens of seconds a real engine takes, at the cost of nothing listening
	// on the endpoint it reports.
	RDSMode RDSMode

	// SigV4Validate enables SigV4 signature verification.
	SigV4Validate bool

	// EnforceIAM enables opt-in IAM authorization enforcement middleware.
	// Default false.
	EnforceIAM bool

	// EnforceAPIGatewayThrottle turns API Gateway usage-plan throttle and
	// quota limits from evaluate-and-report into actual rejection: an
	// over-limit request gets AWS's 429 instead of being served. Usage is
	// always measured and readable through GetUsage regardless — only the
	// rejection is gated, so a stack that works today keeps working.
	// Default false. Corresponds to env var
	// OVERCAST_ENFORCE_APIGATEWAY_THROTTLE.
	EnforceAPIGatewayThrottle bool

	// ProtocolStrict restores strict rejection of "claimed-but-undeclared"
	// wire protocols: when a request's identified protocol isn't one a
	// service's SupportedProtocols() lists, the service returns 415
	// UnsupportedProtocol instead of attempting the decode anyway. Default
	// false (lenient): the emulator logs a "protocol drift" warning and
	// attempts dispatch regardless, since AWS SDKs may switch a service's
	// wire protocol without notice (see docs/plans/level2-codegen.md
	// Track 1.2). Corresponds to env var OVERCAST_PROTOCOL_STRICT.
	ProtocolStrict bool

	// CFNSyncWait is the bounded time CloudFormation waits for fast stack
	// create/update/delete provisioning to reach a terminal state before returning.
	// A zero value disables the wait and restores fully asynchronous behaviour.
	// Corresponds to env var OVERCAST_CFN_SYNC_WAIT_MS. Default 1000ms.
	CFNSyncWait time.Duration

	// StepFunctionsExecutionTimeout is the ceiling on how long one Step
	// Functions execution may run. It is a runaway guard, not a request
	// timeout: StartExecution accepts and returns while the execution is still
	// RUNNING, so this never sits on the wire and is set high enough not to
	// rule out ordinary Wait states. A state machine's own top-level
	// TimeoutSeconds can lower the budget but never raise it. Exceeding it
	// ends the execution TIMED_OUT with AWS's States.Timeout.
	// Corresponds to env var OVERCAST_STEPFUNCTIONS_EXECUTION_TIMEOUT.
	// Default 15m; values below 1s are raised to 1s.
	StepFunctionsExecutionTimeout time.Duration

	// ShutdownTimeout is how long the server waits for in-flight
	// requests to complete before forcibly closing.
	ShutdownTimeout time.Duration

	// LogLevel controls log verbosity: "trace", "debug", "info", "warn", "error"
	// (case-insensitive). "trace" is Overcast-specific — it sits below zap's
	// built-in "debug" and covers periodic machine chatter (health-check and
	// /_debug/* request logs, flush/checkpoint/maintenance/sweep cycle logs,
	// pool internals); see serviceutil.ParseLevel and CONTRIBUTING.md § Log
	// levels. Default "info".
	LogLevel string

	// Network is the Docker network every container Overcast starts is
	// reachable on by name when it belongs to no VPC — the *default data
	// plane*, Overcast's stand-in for the account's default VPC. A resource
	// that names a VPC joins that VPC's network instead; see
	// docs/dev/container-networking.md for the two-plane model and
	// ControlNetwork below for the other half of it.
	//
	// Corresponds to env var OVERCAST_NETWORK. Defaults to "overcast".
	Network string

	// LambdaDockerSocket is the path to the Docker daemon socket used to
	// manage Lambda container siblings. Defaults to the platform Docker socket
	// (/var/run/docker.sock on Linux/macOS, npipe:////./pipe/docker_engine on Windows).
	//
	// The per-service socket overrides below exist for unusual setups, but
	// every one of them must address the *same* daemon: containers are attached
	// to shared networks across service boundaries, which one daemon cannot do
	// on another's behalf.
	LambdaDockerSocket string

	// LambdaRuntimeAPIPort is the port on which Overcast exposes the Lambda
	// Runtime API to containers. Each container connects back on this port.
	// Defaults to 9001.
	LambdaRuntimeAPIPort int

	// LambdaDockerMaxConcurrentStarts bounds concurrent Docker-backed Lambda
	// environment starts. This is local Docker backpressure, not an AWS-facing
	// Lambda concurrency quota. Corresponds to env var
	// LAMBDA_DOCKER_MAX_CONCURRENT_STARTS. 0 (the default) means unset: the
	// Lambda runtime derives a value from the Docker host's CPU count —
	// clamp(NCPU/2, 2, 8), because every start bursts to ~2 CPUs during INIT —
	// falling back to 4 when Docker /info is unavailable.
	LambdaDockerMaxConcurrentStarts int

	// LambdaMaxInstances bounds how many Lambda containers Overcast runs at
	// once across all functions — warm and executing combined. When the limit
	// is reached, a new invocation first reclaims the least-recently-used idle
	// container; if none can be reclaimed it queues until the function's
	// timeout and is then throttled. This protects the host, it is not an
	// emulation of the AWS account concurrency quota. Corresponds to env var
	// LAMBDA_MAX_INSTANCES. 0 (the default) means unset: the Lambda runtime
	// derives a value from the Docker host's memory —
	// clamp(MemTotal*0.65 / 256 MiB, 4, 32) — falling back to 25 when Docker
	// /info is unavailable.
	LambdaMaxInstances int

	// LambdaMaxInstancesPerFunction bounds concurrent containers for a single
	// function, independent of any AWS reserved concurrency the function has.
	// Clamped to LambdaMaxInstances. Corresponds to env var
	// LAMBDA_MAX_INSTANCES_PER_FUNCTION. 0 (the default) means unset: the
	// Lambda runtime derives clamp(maxInstances/2, 2, maxInstances) from the
	// effective global limit, falling back to 10 when Docker /info is
	// unavailable.
	LambdaMaxInstancesPerFunction int

	// LambdaMaxMemoryMB bounds the aggregate memory of live Lambda containers,
	// in MB: Σ MemorySize over warm and executing containers. Each container is
	// hard-capped at its function's MemorySize with swap disabled, so this is a
	// real bound on what Lambda can take from the host, not an estimate. When
	// the budget is exhausted a new invocation reclaims idle containers, then
	// queues, then throttles at the function's timeout — the same ladder as
	// LambdaMaxInstances. Corresponds to env var LAMBDA_MAX_MEMORY_MB. 0 (the
	// default) means unset: the Lambda runtime derives 65% of the Docker host's
	// MemTotal, or leaves the budget unlimited when Docker /info is unavailable.
	LambdaMaxMemoryMB int

	// LambdaMaxWarmInstances bounds how many idle containers one function keeps
	// after a concurrency burst. Surplus instances are destroyed on release
	// rather than waiting out the 15-minute idle sweep. A provisioned
	// concurrency allocation raises this floor for that function. Corresponds
	// to env var LAMBDA_MAX_WARM_INSTANCES. Default 10.
	LambdaMaxWarmInstances int

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

	// LambdaTarCacheMB bounds the in-memory cache of pre-built cold-start
	// artifacts (code and layer tars), in megabytes. 0 disables the cache.
	// Corresponds to env var LAMBDA_TAR_CACHE_MB. Default 256.
	LambdaTarCacheMB int

	// LambdaProactiveInit pre-initializes one execution environment after a
	// function's configuration settles, mirroring AWS's documented proactive
	// initialization, so the next request lands warm. Corresponds to env var
	// LAMBDA_PROACTIVE_INIT. Default false while the feature beds in.
	LambdaProactiveInit bool

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

	// ECSKeepContainers controls whether Docker containers are removed when
	// an ECS task stops. Set to true for post-mortem inspection.
	// Corresponds to env var ECS_KEEP_CONTAINERS. Default false.
	ECSKeepContainers bool

	// ECRRegistryPort is the host port the shared ECR registry container asks
	// for. A fixed, well-known port keeps repositoryUri stable across restarts
	// (a repository re-mints it from the registry on every read, so it follows
	// the port rather than fixing it), gives daemon configuration such as
	// insecure-registries something nameable, and matches the port LocalStack
	// serves its registry on — worth real money for drop-in migration. When
	// the port is taken the registry falls back to an ephemeral one rather
	// than staying down. 0 skips straight to ephemeral.
	// Corresponds to env var OVERCAST_ECR_REGISTRY_PORT. Default 4510.
	ECRRegistryPort int

	// RDSDockerSocket is the path to the Docker daemon socket used to manage
	// RDS database containers. Defaults to the same value as LambdaDockerSocket.
	RDSDockerSocket string

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

	// MSKPortBase is the starting host port for MSK containers.
	MSKPortBase int

	// MSKKeepContainers controls whether Docker containers are removed on delete.
	MSKKeepContainers bool

	// EKSDockerSocket is the path to the Docker daemon socket used to manage
	// EKS live-mode control-plane containers. Defaults to the same value as
	// LambdaDockerSocket.
	EKSDockerSocket string

	// EFSDockerSocket is the path to the Docker daemon socket used to manage
	// EFS live-mode volumes. Defaults to the same value as LambdaDockerSocket.
	EFSDockerSocket string

	// EFSNFSExport opts into the NFS data plane: in live mode each mount
	// target starts an NFS-Ganesha container exporting its file system's
	// volume. Off by default — the volume mounts Lambda and ECS use need no
	// NFS hop, and an export costs a container plus a published port per
	// mount target. Corresponds to OVERCAST_EFS_NFS.
	EFSNFSExport bool

	// EFSNFSPortBase is the starting host port for NFS-Ganesha export
	// containers. Each mount target publishes container port 2049 on the
	// first free port at or above this.
	EFSNFSPortBase int

	// EFSNFSImage is the NFS-Ganesha image used for mount-target exports,
	// pinned by digest. Override to run a different build.
	EFSNFSImage string

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

	// DebugTraceBuffer is the capacity of the request-trace ring buffer
	// (OVERCAST_DEBUG_TRACE_BUFFER). Only read when Debug is true.
	DebugTraceBuffer int

	// TLSCertFile is the path to the TLS certificate file.
	// When set (together with TLSKeyFile), the server uses HTTPS.
	TLSCertFile string

	// TLSKeyFile is the path to the TLS private key file.
	TLSKeyFile string

	// TLSMode selects how TLS is provisioned (OVERCAST_TLS). Empty means
	// plain HTTP unless TLSCertFile/TLSKeyFile are set; TLSModeAuto mints a
	// server certificate from Overcast's local CA at startup and serves both
	// the API and the web UI over HTTPS (which also unlocks browser HTTP/2
	// via ALPN). Mutually exclusive with TLSCertFile/TLSKeyFile.
	TLSMode string

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

	// PublishedPort is the host port the API is actually reachable on, which
	// differs from Port only when Overcast runs in a container that remaps it
	// (`docker run -p 4580:4566`). A container cannot see its own port mapping
	// from the inside, so this is recovered by asking Docker about our own
	// container; it is 0 when there is nothing to recover — a native binary, no
	// Docker socket, or a 1:1 mapping.
	//
	// Consumers use it wherever an address has to make sense *outside* the
	// container: the port the web UI hands the browser, and the origins
	// containerendpoint recognises as Overcast's own when rewriting URLs a
	// host-side deploy minted.
	//
	// Not loaded from environment — set by the caller after Load().
	PublishedPort int

	// DNSEnabled turns on the resolver that serves Overcast's split-horizon
	// hostnames to the containers it starts (OVERCAST_DNS, default true).
	//
	// It exists because /etc/hosts is an exact-match table: it can shadow
	// localhost.overcast.sh but not bucket.s3.localhost.overcast.sh, and the
	// URLs AWS SDKs build are overwhelmingly subdomains. See internal/dns.
	DNSEnabled bool

	// DNSPort is the port the resolver listens on (OVERCAST_DNS_PORT, default
	// 53). Docker's --dns cannot express a port, so anything other than 53 is
	// only useful for tests.
	DNSPort int

	// DNSListening reports that the resolver actually bound, which is what
	// makes it safe to point containers at it: a container told to use a
	// resolver that is not there loses all name resolution. Binding port 53
	// needs privilege outside a container, so this is false more often than
	// DNSEnabled is.
	//
	// Not loaded from environment — set by the caller after Load().
	DNSListening bool

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

// Addr returns the "host:port" string for the server to listen on. When
// several addresses are configured this is the first — see Addrs.
//
// JoinHostPort rather than "%s:%d" so an IPv6 literal comes back bracketed and
// dialable ("[::1]:4566"); every other host formats identically to before.
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Addrs returns every "host:port" the API listener binds, in configured
// order, with Addr() first. A Config assembled in code rather than by Load
// leaves Hosts nil; that reads as the single Addr(), so a struct literal that
// only sets Host behaves exactly as it did before OVERCAST_HOST took a list.
func (c *Config) Addrs() []string {
	if len(c.Hosts) == 0 {
		return []string{c.Addr()}
	}
	addrs := make([]string, 0, len(c.Hosts))
	for _, host := range c.Hosts {
		addrs = append(addrs, net.JoinHostPort(host, strconv.Itoa(c.Port)))
	}
	return addrs
}

// parseHosts splits OVERCAST_HOST into the addresses to bind. Blank entries
// are dropped and repeats collapsed — binding the same address twice on one
// port fails the second listen with EADDRINUSE, and a trailing comma is not
// worth failing a startup over.
//
// A wildcard alongside a specific address is refused rather than collapsed.
// It reads as a widening ("loopback, and also everything") when it is the
// opposite of what the specific address was asking for, and on Linux the
// second bind fails anyway — as an opaque listen error at startup rather than
// as the configuration mistake it is.
func parseHosts(raw string) ([]string, error) {
	var hosts []string
	for _, host := range strings.Split(raw, ",") {
		host = strings.TrimSpace(host)
		if host == "" || slices.Contains(hosts, host) {
			continue
		}
		hosts = append(hosts, host)
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("config: OVERCAST_HOST %q names no address to bind", raw)
	}
	if len(hosts) > 1 {
		for _, host := range hosts {
			if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
				return nil, fmt.Errorf(
					"config: OVERCAST_HOST %q combines the wildcard %q with a specific address; "+
						"the wildcard already covers every address, so drop either it or the rest",
					raw, host)
			}
		}
	}
	return hosts, nil
}

// WildcardDNSDomains are the public domains whose every subdomain resolves to
// 127.0.0.1, so an SDK-generated hostname reaches Overcast with no hosts-file
// edit. They are "split horizon": inside containers Overcast starts, the same
// names are remapped to its container address via /etc/hosts, so one URL is
// dialable from both sides of the container boundary.
//
// This is the single source of truth for the set.
// internal/middleware reads it for S3 virtual-hosted addressing — adding plain
// "localhost", which is not a public domain and so belongs only there — and
// internal/containerendpoint reads it for the /etc/hosts mapping. They were
// previously two hand-maintained lists and they drifted: containerendpoint
// knew localhost.floci.io while the S3 matcher did not, so a bucket was
// unreachable on a domain Overcast itself advertises.
//
// Extend per-deployment with OVERCAST_SPLIT_HORIZON_HOSTS rather than editing
// this list.
var WildcardDNSDomains = []string{
	"localhost.overcast.sh",
	"localhost.localstack.cloud",
	"localhost.floci.io",
}

// ControlNetwork returns the Docker network that carries the channel between
// Overcast and every container it starts: the Lambda Runtime API, and the
// AWS_ENDPOINT_URL calls function and task code makes back into the emulator.
//
// It is deliberately *not* the data plane. On AWS the Runtime API is inside the
// execution sandbox — reachable whatever VPC a function joins, because it is
// the mechanism by which the function runs at all — and the emulator's own API
// stands in for a VPC endpoint per service. Both must survive VPC placement;
// reaching a database must not. Keeping them on separate networks is what lets
// a VPC restrict the second without severing the first (which would strand
// every invocation at INIT).
//
// Derived from Network rather than separately configurable: one name to set,
// no way to configure half a pair.
func (c *Config) ControlNetwork() string {
	return c.Network + "_control"
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

// TLSModeAuto is the OVERCAST_TLS value that mints a certificate from the
// local Overcast CA at startup.
const TLSModeAuto = "auto"

// TLSEnabled returns true when the server should serve HTTPS — either via an
// explicitly configured cert/key pair or via the auto-minted local CA leaf.
func (c *Config) TLSEnabled() bool {
	return c.TLSAuto() || (c.TLSCertFile != "" && c.TLSKeyFile != "")
}

// TLSAuto returns true when OVERCAST_TLS=auto: the server certificate is
// minted from Overcast's local CA at startup.
func (c *Config) TLSAuto() bool {
	return c.TLSMode == TLSModeAuto
}

// TLSAutoSANs returns every subject alternative name the auto-minted server
// certificate must cover: the loopback names and addresses, each wildcard
// DNS domain (apex, one-level wildcard, and the "*.s3." level used by S3
// virtual-hosted addressing), any extra split-horizon hosts, and the
// configured hostname.
//
// One-level wildcards are a TLS constraint, not a choice: "*.<base>" matches
// exactly one label, so deeper host-routed names with a variable middle
// (e.g. "{id}.execute-api.{region}.<base>") cannot be enumerated here and
// are not covered. The S3 level is included explicitly because
// "{bucket}.s3.<base>" is the common two-label shape.
func (c *Config) TLSAutoSANs() []string {
	sans := []string{"localhost", "127.0.0.1", "::1"}
	seen := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	add := func(name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		sans = append(sans, name)
	}
	addDomain := func(base string) {
		base = strings.TrimSpace(base)
		if base == "" {
			return
		}
		if net.ParseIP(base) != nil {
			add(base) // an IP literal gets an IP SAN; wildcards over IPs are meaningless
			return
		}
		add(base)
		add("*." + base)
		add("*.s3." + base)
	}
	for _, base := range WildcardDNSDomains {
		addDomain(base)
	}
	for _, base := range c.SplitHorizonHosts {
		addDomain(base)
	}
	addDomain(c.Hostname)
	return sans
}

// allServices is the canonical list of supported service names.
var allServices = []string{"s3", "sqs", "sns", "ses", "dynamodb", "dynamodbstreams", "lambda", "pipes", "logs", "secretsmanager", "sts", "ssm", "kms", "iam", "cloudformation", "ec2", "rds", "ecs", "ecr", "efs", "eks", "cognito", "stepfunctions", "waf", "shield", "appsync", "apigateway", "cloudfront", "eventbridge", "kinesis", "appregistry", "cloudwatch", "acm", "opensearch", "appconfig", "appconfigdata", "bedrock", "glue", "firehose", "athena", "elasticache", "msk", "scheduler", "route53", "elbv2", "organizations", "autoscaling", "cloudtrail", "backup", "transfer"}

// AllServices returns the canonical list of supported service names in
// declaration order. The result is a copy, so callers cannot mutate package
// state through it.
//
// These are the names OVERCAST_STATE_<SERVICE> is keyed by, and cmd/capgen
// generates the service-name table in docs/README.md from this list, which is
// what keeps the documented names from drifting away from the real ones when a
// service is added or renamed.
func AllServices() []string {
	out := make([]string, len(allServices))
	copy(out, allServices)
	return out
}

// ServiceNamespacePrefix returns the canonical mapping from a config service
// name (as used in OVERCAST_STATE_<SERVICE> and allServices) to the
// storage-namespace prefix that service actually writes
// under — the segment before the first ":" in namespaces like "cfn:stacks" or
// "sqs:queues".
//
// Most services use their config name as their namespace prefix unchanged
// (e.g. "sqs" → "sqs"), but a few historical namespace prefixes are shorter
// than the config name:
//
//	cloudformation → cfn
//	apigateway     → apigw
//	eventbridge    → eb
//
// This function is the single source of truth for that mapping. It is used
// by both the per-service storage override routing in cmd/overcast's serve
// command and the /_debug endpoints in internal/router — state.NamespacedStore
// (internal/state/namespaced.go) routes purely on namespace prefix, not on
// config service name, so any caller that keys a NamespacedStore route map
// (or unwraps one) by config service name instead of this prefix will build
// an override that silently never takes effect.
//
// "dynamodbstreams" is intentionally left identity-mapped here — it must
// NOT be remapped to "dynamodb". cfg.ServiceStates is an unordered map: if
// both OVERCAST_STATE_DYNAMODB and OVERCAST_STATE_DYNAMODBSTREAMS were ever
// set to different modes, remapping dynamodbstreams to "dynamodb" would make
// both resolve to the same route-map key and collide, nondeterministically
// redirecting real DynamoDB item/table storage depending on map iteration
// order. It's also moot: dynamodbstreams.New (internal/services/dynamodbstreams/service.go)
// takes no state.Store parameter at all — it's a store-less facade over the
// dynamodb service, so an override for it can never have any effect
// regardless of prefix. See ServiceOverrideIneffective, which documents this
// (and a few other services) as a known no-op override.
func ServiceNamespacePrefix(service string) string {
	switch service {
	case "cloudformation":
		return "cfn"
	case "apigateway":
		return "apigw"
	case "eventbridge":
		return "eb"
	default:
		return service
	}
}

// ServiceOverrideIneffective reports whether an OVERCAST_STATE_<SERVICE>
// override for service is accepted by config validation but has no
// observable effect, and if so, a short human-readable reason why.
//
// These services fall into two groups, both of which mean the service never
// reads or writes through the store instance that a per-service override
// would substitute:
//
//   - "dynamodbstreams": a store-less facade over the dynamodb service.
//     dynamodbstreams.New(ddb DynamoDBService, logger *zap.Logger) — see
//     internal/services/dynamodbstreams/service.go — takes no state.Store
//     parameter; all persisted data actually lives under the dynamodb
//     service's own store.
//   - "sts": STS session state is written under the IAM namespace, not
//     "sts" — see internal/services/sts/handler.go, which calls
//     `h.st.Set(ctx, "iam:sessions", accessKeyID, ...)`. An OVERCAST_STATE_STS
//     override builds a store that is registered under the "sts" prefix,
//     but NamespacedStore.storeFor never routes "iam:sessions" there.
//   - "bedrock": a stateless stub. bedrock.New's state.Store parameter is
//     the blank identifier (see internal/services/bedrock/service.go) — the
//     service struct holds no store field and never persists anything.
//   - "organizations": a stateless stub. organizations.New accepts a
//     state.Store parameter but never assigns or otherwise uses it (see
//     internal/services/organizations/service.go) — the service struct
//     holds no store field.
//
// Callers that build per-service storage overrides (cmd/overcast's serve
// command) should still build and route the override store for these
// services when configured — it's harmless — but should log a warning so
// the no-op isn't silently misread as "the override took effect".
func ServiceOverrideIneffective(service string) (reason string, ok bool) {
	switch service {
	case "dynamodbstreams":
		return "store-less facade over the dynamodb service; dynamodbstreams.New takes no state.Store and owns no stored state of its own", true
	case "sts":
		return `STS session state is written under the "iam:sessions" namespace, not "sts"; a per-service override for sts is never consulted`, true
	case "bedrock":
		return "stateless stub; bedrock.New's state.Store parameter is discarded and never persists anything", true
	case "organizations":
		return "stateless stub; organizations.New's state.Store parameter is accepted but never used", true
	default:
		return "", false
	}
}

// Load reads configuration from environment variables and returns a validated
// Config. Returns an error if any required value is invalid.
//
// Environment variables (all optional, defaults shown):
//
//	OVERCAST_HOST                      0.0.0.0 (comma-separated for several)
//	OVERCAST_HOSTNAME                  (empty — defaults to localhost in URLs)
//	OVERCAST_SPLIT_HORIZON_HOSTS       (empty — extra names remapped to Overcast
//	                                           inside containers, comma-separated)
//	OVERCAST_PORT                      4566
//	OVERCAST_STATE                     auto    (auto | memory | persistent | hybrid | wal)
//	                                           auto resolves to hybrid when the data directory
//	                                           is a mounted volume/bind mount, OVERCAST_DATA_DIR
//	                                           was explicitly set, or an existing database is
//	                                           found there — otherwise memory. See
//	                                           resolveAutoState (state_auto.go).
//	OVERCAST_STATE_<SERVICE>           <mode>  (per-service override, e.g. OVERCAST_STATE_S3=memory;
//	                                           does not accept "auto")
//	OVERCAST_DATA_DIR_SOURCE           (empty) internal provenance marker — the Docker image sets this
//	                                           to "image" alongside its baked-in OVERCAST_DATA_DIR=/data,
//	                                           so the auto-state resolver doesn't mistake the image
//	                                           default for user intent. Not for end users to set.
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
//	OVERCAST_EFS_MODE                  live    (mock | live; live is inert without Docker)
//	OVERCAST_RDS_MODE                  live    (mock | live; mock starts no engine container)
//	OVERCAST_SIGV4_VALIDATE            false
//	OVERCAST_ENFORCE_IAM              false
//	OVERCAST_ENFORCE_APIGATEWAY_THROTTLE false
//	OVERCAST_PROTOCOL_STRICT           false   (true = 415 on a protocol a service does not
//	                                           declare, instead of attempting the decode anyway)
//	OVERCAST_CFN_SYNC_WAIT_MS          1000
//	OVERCAST_STEPFUNCTIONS_EXECUTION_TIMEOUT 15m (runaway guard on one execution, not a request
//	                                           timeout; values below 1s are raised to 1s)
//	OVERCAST_LOG_LEVEL                 info    (trace | debug | info | warn | error)
//	OVERCAST_SHUTDOWN_TIMEOUT          5s
//	OVERCAST_LAMBDA_HOT_RELOAD         false
//	OVERCAST_DEBUG                     false
//	OVERCAST_DEBUG_TRACE_BUFFER        1000    (request-trace ring buffer; only read when
//	                                           OVERCAST_DEBUG is on)
//	OVERCAST_TLS                       ""    (auto = mint from the local overcast CA; serves API + web UI over HTTPS/h2)
//	OVERCAST_TLS_CERT                  ""
//	OVERCAST_TLS_KEY                   ""
//	OVERCAST_DNS                       true    (resolver serving the split-horizon names to the
//	                                           containers Overcast starts; failing to bind is not fatal)
//	OVERCAST_DNS_PORT                  53      (Docker's --dns cannot express a port, so anything
//	                                           other than 53 is only useful for tests)
//	OVERCAST_EC2_VPC_STRATEGY          shared  (shared | strict | remapped; strict and remapped fall
//	                                           back to shared with a startup warning, netns is rejected)
//	OVERCAST_ECR_REGISTRY_PORT         4510    (host port the shared registry container asks for;
//	                                           0, or a port already taken, falls back to ephemeral)
//	LAMBDA_DOCKER_SOCKET               (platform default) /var/run/docker.sock on Linux/macOS,
//	                                           npipe:////./pipe/docker_engine on Windows. Every
//	                                           per-service socket override below must address the
//	                                           same daemon.
//	LAMBDA_RUNTIME_API_PORT            9001    (port containers call back on for the Runtime API)
//	LAMBDA_DOCKER_MAX_CONCURRENT_STARTS (auto) derived from Docker host CPUs: clamp(NCPU/2, 2, 8);
//	                                           4 when Docker /info is unavailable
//	LAMBDA_MAX_INSTANCES               (auto)  derived from Docker host memory:
//	                                           clamp(MemTotal*0.65 / 256MiB, 4, 32); 25 when /info is unavailable
//	LAMBDA_MAX_INSTANCES_PER_FUNCTION  (auto)  clamp(maxInstances/2, 2, maxInstances); 10 when /info is unavailable
//	LAMBDA_MAX_MEMORY_MB               (auto)  aggregate Σ MemorySize budget for live containers, in MB;
//	                                           derived as MemTotal*0.65; unlimited when /info is unavailable
//	LAMBDA_MAX_WARM_INSTANCES          10
//	LAMBDA_SEED_RUNTIME_IMAGES         false (true = pre-pull every managed runtime image at
//	                                           startup, instead of lazily on first use)
//	LAMBDA_INIT_TIMEOUT_SECONDS       10
//	LAMBDA_KEEP_CONTAINERS             false (true = keep stopped containers after expiry/delete)
//	LAMBDA_TAR_CACHE_MB                256   (in-memory cache of pre-built cold-start code and
//	                                           layer tars; 0 disables it)
//	LAMBDA_PROACTIVE_INIT              false (true = pre-initialize one execution environment once
//	                                           a function's configuration settles)
//	LAMBDA_FETCH_REMOTE_LAYERS         false (true = download missing layers from real AWS)
//	LAMBDA_LAYER_CACHE_DIR             <OVERCAST_DATA_DIR>/layers (where layer zips are looked up
//	                                           and cached, named {sha256(arn)}.zip)
//	LAMBDA_REMOTE_AWS_ACCESS_KEY_ID    ""    (credentials for LAMBDA_FETCH_REMOTE_LAYERS)
//	LAMBDA_REMOTE_AWS_SECRET_ACCESS_KEY ""
//	LAMBDA_REMOTE_AWS_SESSION_TOKEN    ""    (optional)
//	OVERCAST_NETWORK                   overcast (the data plane every container Overcast starts
//	                                           shares, unless the resource names a VPC, in which
//	                                           case it joins that VPC's network. The control plane
//	                                           is derived from this rather than configured — see
//	                                           Config.ControlNetwork. One variable for every
//	                                           service: the per-service LAMBDA_NETWORK,
//	                                           ECS_NETWORK, RDS_NETWORK, ELASTICACHE_NETWORK,
//	                                           MSK_NETWORK, EKS_NETWORK and EFS_NETWORK are gone
//	                                           and are no longer read.)
//	ECS_DOCKER_SOCKET                  <LAMBDA_DOCKER_SOCKET> (default: same as Lambda)
//	ECS_KEEP_CONTAINERS                false
//	RDS_DOCKER_SOCKET                  <LAMBDA_DOCKER_SOCKET> (default: same as Lambda)
//	RDS_PORT_BASE                      33060
//	RDS_KEEP_CONTAINERS                false
//	ELASTICACHE_DOCKER_SOCKET          <LAMBDA_DOCKER_SOCKET> (default: same as Lambda)
//	ELASTICACHE_PORT_BASE              63790
//	ELASTICACHE_KEEP_CONTAINERS        false
//	MSK_DOCKER_SOCKET                  <LAMBDA_DOCKER_SOCKET>
//	MSK_PORT_BASE                      49092
//	MSK_KEEP_CONTAINERS                false
//	EKS_DOCKER_SOCKET                  <LAMBDA_DOCKER_SOCKET>
//	EFS_DOCKER_SOCKET                  <LAMBDA_DOCKER_SOCKET>
//	OVERCAST_EFS_NFS                   false (true = one NFS-Ganesha export container per mount target, live mode only)
//	EFS_NFS_PORT_BASE                  22049
//	EFS_NFS_IMAGE                      registry.k8s.io/sig-storage/nfs-provisioner@sha256:c825f3d5… (digest-pinned)
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
	hosts, err := parseHosts(envOr("OVERCAST_HOST", "0.0.0.0"))
	if err != nil {
		return nil, err
	}
	cfg.Hosts = hosts
	cfg.Host = hosts[0]

	// Hostname (external — for client-facing URLs)
	cfg.Hostname = os.Getenv("OVERCAST_HOSTNAME")

	// Split-horizon hostnames (extra names remapped to Overcast inside containers)
	for _, host := range strings.Split(os.Getenv("OVERCAST_SPLIT_HORIZON_HOSTS"), ",") {
		if host = strings.TrimSpace(host); host != "" {
			cfg.SplitHorizonHosts = append(cfg.SplitHorizonHosts, host)
		}
	}

	// Port
	portStr := envOr("OVERCAST_PORT", "4566")
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("config: OVERCAST_PORT %q is not a valid port number", portStr)
	}
	cfg.Port = port

	// Data directory — resolved before the state backend below, since
	// OVERCAST_STATE=auto inspects it (mountpoint check, existing-database
	// check). dataDirEnvRaw/dataDirSource are read raw (undefaulted) because
	// detecting "was this actually configured" requires knowing whether the
	// env var was empty before envOr fills in the default.
	dataDirEnvRaw := os.Getenv("OVERCAST_DATA_DIR")
	dataDirSource := os.Getenv("OVERCAST_DATA_DIR_SOURCE")
	cfg.DataDir = envOr("OVERCAST_DATA_DIR", defaultDataDir())

	// State backend — accept "sqlite" as a deprecated alias for "persistent",
	// and "auto" (also the default when unset) as a request to resolve the
	// backend from evidence of persistence intent — see resolveAutoState.
	cfg.StateConfigured = strings.ToLower(strings.TrimSpace(os.Getenv("OVERCAST_STATE")))
	if cfg.StateConfigured == "" {
		cfg.StateConfigured = "auto"
	}
	if cfg.StateConfigured == "sqlite" {
		cfg.StateConfigured = string(StateBackendPersistent)
	}
	if cfg.StateConfigured == "auto" {
		cfg.StateSource = StateSourceAuto
		sig := detectAutoStateSignals(cfg.DataDir, dataDirEnvRaw, dataDirSource)
		backend, signal, reason := resolveAutoState(sig)
		cfg.State = backend
		cfg.StateAutoSignal = signal
		cfg.StateAutoReason = reason
	} else {
		backend := StateBackend(cfg.StateConfigured)
		if err := validateStateBackend(backend, "OVERCAST_STATE"); err != nil {
			return nil, err
		}
		cfg.State = backend
		cfg.StateSource = StateSourceExplicit
	}

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

	// Per-service state overrides.
	//
	// Scanned out of the environment rather than probed per known service.
	// The probe form — building "OVERCAST_STATE_"+svc for each service and
	// reading that — cannot see a variable it does not think to construct, so
	// OVERCAST_STATE_CLOUDWATCH_LOGS (the plausible guess for a service whose
	// name is "logs"), or any typo, or a name that used to be right before a
	// rename, was silently ignored: no error, no warning, and the service
	// quietly kept using the global backend. Scanning means an override that
	// names nothing real is a startup error instead.
	serviceByEnvSuffix := make(map[string]string, len(allServices))
	for _, svc := range allServices {
		serviceByEnvSuffix[strings.ToUpper(strings.ReplaceAll(svc, "-", "_"))] = svc
	}

	const statePrefix = "OVERCAST_STATE_"
	cfg.ServiceStates = make(map[string]StateBackend)
	for _, entry := range os.Environ() {
		envKey, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(envKey, statePrefix) {
			continue
		}
		// An empty value means "unset", matching every other variable here.
		if value == "" {
			continue
		}
		svc, known := serviceByEnvSuffix[envKey[len(statePrefix):]]
		if !known {
			return nil, fmt.Errorf("config: %s does not name a known service (see the service names table in docs/README.md)", envKey)
		}

		raw := strings.ToLower(value)
		if raw == "sqlite" {
			raw = string(StateBackendPersistent)
		}
		svcBackend := StateBackend(raw)
		if err := validateStateBackend(svcBackend, envKey); err != nil {
			return nil, err
		}
		cfg.ServiceStates[svc] = svcBackend
	}

	// AWS identity defaults
	cfg.Region = envOr("OVERCAST_DEFAULT_REGION", "us-east-1")
	cfg.AccountID = envOr("OVERCAST_ACCOUNT_ID", "000000000000")

	// EKS mode
	rawEKSMode := strings.ToLower(strings.TrimSpace(envOr("OVERCAST_EKS_MODE", string(EKSModeMock))))
	cfg.EKSMode = EKSMode(rawEKSMode)
	if cfg.EKSMode != EKSModeMock && cfg.EKSMode != EKSModeLive {
		return nil, fmt.Errorf("config: OVERCAST_EKS_MODE %q is invalid (expected mock or live)", rawEKSMode)
	}

	// EFS mode. Live by default, unlike EKS: EKS live mode runs a k3s
	// container per cluster whether or not anything needs one, while EFS live
	// mode only creates a volume for a file system someone asked for, and
	// creates nothing at all when Docker is out of reach.
	rawEFSMode := strings.ToLower(strings.TrimSpace(envOr("OVERCAST_EFS_MODE", string(EFSModeLive))))
	cfg.EFSMode = EFSMode(rawEFSMode)
	if cfg.EFSMode != EFSModeMock && cfg.EFSMode != EFSModeLive {
		return nil, fmt.Errorf("config: OVERCAST_EFS_MODE %q is invalid (expected mock or live)", rawEFSMode)
	}

	// RDS mode. Live by default: a DB instance whose endpoint nothing answers
	// on is not much of a database, and live mode costs nothing where it cannot
	// be used — without a reachable daemon RDS is metadata-only anyway.
	rawRDSMode := strings.ToLower(strings.TrimSpace(envOr("OVERCAST_RDS_MODE", string(RDSModeLive))))
	cfg.RDSMode = RDSMode(rawRDSMode)
	if cfg.RDSMode != RDSModeMock && cfg.RDSMode != RDSModeLive {
		return nil, fmt.Errorf("config: OVERCAST_RDS_MODE %q is invalid (expected mock or live)", rawRDSMode)
	}

	// SigV4 validation
	cfg.SigV4Validate = envBool("OVERCAST_SIGV4_VALIDATE", false)

	// Optional IAM enforcement middleware (default off).
	cfg.EnforceIAM = envBool("OVERCAST_ENFORCE_IAM", false)

	// Optional API Gateway usage-plan throttle/quota rejection (default off:
	// limits are measured and reported, but never turned into a 429).
	cfg.EnforceAPIGatewayThrottle = envBool("OVERCAST_ENFORCE_APIGATEWAY_THROTTLE", false)

	// Protocol drift strictness (default off — lenient "attempt anyway" posture).
	cfg.ProtocolStrict = envBool("OVERCAST_PROTOCOL_STRICT", false)

	// CloudFormation synchronous fast-path wait budget.
	cfg.CFNSyncWait = time.Duration(envInt("OVERCAST_CFN_SYNC_WAIT_MS", 1000)) * time.Millisecond
	if cfg.CFNSyncWait < 0 {
		cfg.CFNSyncWait = 0
	}

	// Step Functions runaway-execution guard. Executions run off the request
	// path, so this bounds the run only.
	sfnTimeoutStr := envOr("OVERCAST_STEPFUNCTIONS_EXECUTION_TIMEOUT", "15m")
	cfg.StepFunctionsExecutionTimeout, err = time.ParseDuration(sfnTimeoutStr)
	if err != nil {
		return nil, fmt.Errorf("config: OVERCAST_STEPFUNCTIONS_EXECUTION_TIMEOUT %q is not a duration: %w", sfnTimeoutStr, err)
	}
	if cfg.StepFunctionsExecutionTimeout < time.Second {
		cfg.StepFunctionsExecutionTimeout = time.Second
	}

	// Logging
	cfg.LogLevel = strings.ToLower(envOr("OVERCAST_LOG_LEVEL", "info"))

	// Shutdown timeout
	timeoutStr := envOr("OVERCAST_SHUTDOWN_TIMEOUT", "5s")
	cfg.ShutdownTimeout, err = time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, fmt.Errorf("config: OVERCAST_SHUTDOWN_TIMEOUT %q is not a valid duration", timeoutStr)
	}

	// The default data plane. Every container Overcast starts is reachable by
	// name here unless it belongs to a VPC, in which case it is reachable on
	// that VPC's network instead. ControlNetwork() derives the other plane.
	cfg.Network = envOr("OVERCAST_NETWORK", "overcast")

	// Lambda container runtime
	cfg.LambdaDockerSocket = envOr("LAMBDA_DOCKER_SOCKET", DefaultDockerSocket())
	cfg.LambdaRuntimeAPIPort = envInt("LAMBDA_RUNTIME_API_PORT", 9001)
	// For the three derivable limits, 0 is a sentinel meaning "unset — derive
	// from the Docker host when the Lambda runtime initialises" (see
	// internal/services/lambda/host_limits.go). A negative or zero env value is
	// normalised to the sentinel rather than clamped to 1: pinning a limit
	// means setting a positive integer.
	cfg.LambdaDockerMaxConcurrentStarts = envInt("LAMBDA_DOCKER_MAX_CONCURRENT_STARTS", 0)
	if cfg.LambdaDockerMaxConcurrentStarts < 0 {
		cfg.LambdaDockerMaxConcurrentStarts = 0
	}
	cfg.LambdaMaxInstances = envInt("LAMBDA_MAX_INSTANCES", 0)
	if cfg.LambdaMaxInstances < 0 {
		cfg.LambdaMaxInstances = 0
	}
	cfg.LambdaMaxInstancesPerFunction = envInt("LAMBDA_MAX_INSTANCES_PER_FUNCTION", 0)
	if cfg.LambdaMaxInstancesPerFunction < 0 {
		cfg.LambdaMaxInstancesPerFunction = 0
	}
	cfg.LambdaMaxMemoryMB = envInt("LAMBDA_MAX_MEMORY_MB", 0)
	if cfg.LambdaMaxMemoryMB < 0 {
		cfg.LambdaMaxMemoryMB = 0
	}
	cfg.LambdaMaxWarmInstances = envInt("LAMBDA_MAX_WARM_INSTANCES", 10)
	if cfg.LambdaMaxWarmInstances < 1 {
		cfg.LambdaMaxWarmInstances = 1
	}
	cfg.LambdaSeedRuntimeImages = envBool("LAMBDA_SEED_RUNTIME_IMAGES", false)
	cfg.LambdaInitTimeout = time.Duration(envInt("LAMBDA_INIT_TIMEOUT_SECONDS", 10)) * time.Second
	if cfg.LambdaInitTimeout <= 0 {
		cfg.LambdaInitTimeout = 10 * time.Second
	}
	cfg.LambdaKeepContainers = envBool("LAMBDA_KEEP_CONTAINERS", false)
	cfg.LambdaTarCacheMB = envInt("LAMBDA_TAR_CACHE_MB", 256)
	if cfg.LambdaTarCacheMB < 0 {
		cfg.LambdaTarCacheMB = 0
	}
	cfg.LambdaProactiveInit = envBool("LAMBDA_PROACTIVE_INIT", false)
	cfg.LambdaHotReload = envBool("OVERCAST_LAMBDA_HOT_RELOAD", false)
	cfg.LambdaFetchRemoteLayers = envBool("LAMBDA_FETCH_REMOTE_LAYERS", false)
	cfg.LambdaLayerCacheDir = envOr("LAMBDA_LAYER_CACHE_DIR", "")
	cfg.LambdaRemoteAWSAccessKeyID = envOr("LAMBDA_REMOTE_AWS_ACCESS_KEY_ID", "")
	cfg.LambdaRemoteAWSSecretAccessKey = envOr("LAMBDA_REMOTE_AWS_SECRET_ACCESS_KEY", "")
	cfg.LambdaRemoteAWSSessionToken = envOr("LAMBDA_REMOTE_AWS_SESSION_TOKEN", "")

	// Container-facing resolver for the split-horizon hostnames. On by default:
	// out of the box a Lambda must be able to reach Overcast by every name
	// Overcast advertises, including the subdomain forms /etc/hosts cannot
	// express. Failing to bind is not fatal — see DNSListening.
	cfg.DNSEnabled = envBool("OVERCAST_DNS", true)
	cfg.DNSPort = envInt("OVERCAST_DNS_PORT", 53)

	// ECS container runtime — defaults fall back to Lambda socket
	cfg.ECSDockerSocket = envOr("ECS_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.ECSKeepContainers = envBool("ECS_KEEP_CONTAINERS", false)

	// ECR registry — see the field's comment for why the default is pinned.
	cfg.ECRRegistryPort = envInt("OVERCAST_ECR_REGISTRY_PORT", 4510)

	// RDS container runtime — defaults fall back to Lambda socket
	cfg.RDSDockerSocket = envOr("RDS_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.RDSPortBase = envInt("RDS_PORT_BASE", 33060)
	cfg.RDSKeepContainers = envBool("RDS_KEEP_CONTAINERS", false)

	// ElastiCache container runtime — defaults fall back to Lambda socket
	cfg.ElastiCacheDockerSocket = envOr("ELASTICACHE_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.ElastiCachePortBase = envInt("ELASTICACHE_PORT_BASE", 63790)
	cfg.ElastiCacheKeepContainers = envBool("ELASTICACHE_KEEP_CONTAINERS", false)

	// MSK container runtime — defaults fall back to Lambda socket
	cfg.MSKDockerSocket = envOr("MSK_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.MSKPortBase = envInt("MSK_PORT_BASE", 49092)
	cfg.MSKKeepContainers = envBool("MSK_KEEP_CONTAINERS", false)

	// EKS live-mode container runtime — defaults fall back to Lambda socket
	cfg.EKSDockerSocket = envOr("EKS_DOCKER_SOCKET", cfg.LambdaDockerSocket)

	// EFS live-mode volume runtime — defaults fall back to Lambda socket
	cfg.EFSDockerSocket = envOr("EFS_DOCKER_SOCKET", cfg.LambdaDockerSocket)
	cfg.EFSNFSExport = envBool("OVERCAST_EFS_NFS", false)
	cfg.EFSNFSPortBase = envInt("EFS_NFS_PORT_BASE", 22049)
	cfg.EFSNFSImage = envOr("EFS_NFS_IMAGE", DefaultEFSNFSImage)

	// EC2 VPC network strategy — unknown values fall back to "shared" at
	// service construction with a logged warning. "netns" is explicitly
	// rejected here because that strategy is not implemented yet.
	cfg.EC2VPCNetworkStrategy = envOr("OVERCAST_EC2_VPC_STRATEGY", "shared")
	if strings.EqualFold(strings.TrimSpace(cfg.EC2VPCNetworkStrategy), "netns") {
		return nil, fmt.Errorf("config: OVERCAST_EC2_VPC_STRATEGY=netns is not supported yet; use shared, strict, or remapped")
	}

	// Debug endpoints
	cfg.Debug = envBool("OVERCAST_DEBUG", false)
	cfg.DebugTraceBuffer = envInt("OVERCAST_DEBUG_TRACE_BUFFER", 1000)

	// TLS
	cfg.TLSCertFile = os.Getenv("OVERCAST_TLS_CERT")
	cfg.TLSKeyFile = os.Getenv("OVERCAST_TLS_KEY")
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return nil, fmt.Errorf("config: OVERCAST_TLS_CERT and OVERCAST_TLS_KEY must both be set or both be empty")
	}
	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv("OVERCAST_TLS"))); mode {
	case "", "off", "false", "0":
		cfg.TLSMode = ""
	case TLSModeAuto:
		cfg.TLSMode = TLSModeAuto
	default:
		return nil, fmt.Errorf("config: OVERCAST_TLS must be 'auto' or unset, got %q", mode)
	}
	if cfg.TLSAuto() && (cfg.TLSCertFile != "" || cfg.TLSKeyFile != "") {
		return nil, fmt.Errorf("config: OVERCAST_TLS=auto and OVERCAST_TLS_CERT/OVERCAST_TLS_KEY are mutually exclusive — use one or the other")
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

// validateStateBackend returns an error if b is not one of the four recognised
// concrete storage modes. envKey is included in the error message for
// context. Callers peel off "auto" (and the "sqlite" alias) before reaching
// here, so this only ever validates a concrete backend name — for the global
// OVERCAST_STATE var specifically, "auto" is also accepted upstream and is
// mentioned in the error for completeness even though this function itself
// never returns nil for it.
func validateStateBackend(b StateBackend, envKey string) error {
	switch b {
	case StateBackendMemory, StateBackendPersistent, StateBackendHybrid, StateBackendWAL:
		return nil
	default:
		if envKey == "OVERCAST_STATE" {
			return fmt.Errorf("config: %s must be 'auto', 'memory', 'persistent', 'hybrid' or 'wal', got %q", envKey, b)
		}
		return fmt.Errorf("config: %s must be 'memory', 'persistent', 'hybrid' or 'wal', got %q", envKey, b)
	}
}
