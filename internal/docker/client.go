// Package docker provides a thin Docker Engine API client.
//
// Supported endpoints:
//   - Unix socket: "/var/run/docker.sock" (Linux / macOS default)
//   - Named pipe: "npipe:////./pipe/docker_engine" (Windows default)
//   - TCP: "tcp://host:port" (DinD sidecars, all platforms)
//
// This avoids pulling in the massive github.com/docker/docker SDK with its
// transitive dependencies (otel, protobuf, etc.). We only need a handful of
// API calls: create/start/stop/remove container, pull image, create/inspect
// network. The Docker Engine API is stable REST over a Unix socket or TCP.
//
// Reference: https://docs.docker.com/engine/api/v1.45/
package docker

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Client is a lightweight Docker Engine API client.
type Client struct {
	httpClient *http.Client
	host       string // base URL for API requests
	logger     *zap.Logger
	sem        chan struct{} // bounds concurrent mutating Docker operations
}

// maxConcurrentOps limits how many container create/start/stop/remove
// operations run concurrently.  Under high load (4 compat suites), the
// Docker daemon becomes overwhelmed by hundreds of simultaneous requests.
// This semaphore provides natural backpressure — each operation waits for
// a slot, keeping the daemon responsive.
const maxConcurrentOps = 8

// NewClient creates a Docker client for the given endpoint.
//
// The endpoint can be:
//   - A Unix socket path: "/var/run/docker.sock" (Linux / macOS)
//   - A Windows named pipe: "npipe:////./pipe/docker_engine" (Windows)
//   - A TCP address: "tcp://host:port" (for DinD sidecars, all platforms)
//
// Use the package-level defaultDockerSocket constant for the platform default.
func NewClient(endpoint string, logger *zap.Logger) *Client {
	dialFn, host := dialEndpoint(endpoint)
	transport := &http.Transport{
		MaxConnsPerHost:       8,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	if dialFn != nil {
		transport.DialContext = dialFn
	}
	return &Client{
		httpClient: &http.Client{Transport: transport},
		host:       host,
		logger:     logger,
		sem:        make(chan struct{}, maxConcurrentOps),
	}
}

// acquireOp blocks until a concurrent-operation slot is available.  Call
// before mutating Docker state (create/start/stop/remove).  The caller
// MUST call releaseOp when done.  Uses the context for cancellation.
func (d *Client) acquireOp(ctx context.Context) error {
	select {
	case d.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Client) releaseOp() {
	<-d.sem
}

// ─── Container types ───────────────────────────────────────────────────────

// ContainerConfig describes the container's runtime configuration.
type ContainerConfig struct {
	Image        string              `json:"Image"`
	Env          []string            `json:"Env,omitempty"`
	Cmd          []string            `json:"Cmd,omitempty"`
	Entrypoint   []string            `json:"Entrypoint,omitempty"`
	WorkingDir   string              `json:"WorkingDir,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
}

// Standard labels applied by Overcast services to Docker resources (containers
// and networks). The Docker watcher filters on LabelManaged so it only sees
// our resources.
const (
	// LabelManaged marks a resource as Overcast-managed.
	LabelManaged = "overcast.managed"
	// LabelService identifies which Overcast service owns the resource
	// (e.g. "lambda", "ecs", "rds", "ec2").
	LabelService = "overcast.service"
	// LabelResourceID identifies the logical resource that owns the
	// Docker resource (e.g. function name, ECS task ID, VPC ID).
	LabelResourceID = "overcast.resource-id"
)

// ManagedLabels returns the standard Overcast labels for a Docker resource.
// All services should use this instead of constructing the map inline.
func ManagedLabels(service, resourceID string) map[string]string {
	return map[string]string{
		LabelManaged:    "true",
		LabelService:    service,
		LabelResourceID: resourceID,
	}
}

// HostConfig describes the host-side container configuration.
type HostConfig struct {
	Binds        []string                 `json:"Binds,omitempty"`
	NetworkMode  string                   `json:"NetworkMode,omitempty"`
	Memory       int64                    `json:"Memory,omitempty"`     // bytes
	MemorySwap   int64                    `json:"MemorySwap,omitempty"` // bytes (-1 = unlimited)
	NanoCPUs     int64                    `json:"NanoCPUs,omitempty"`   // 1e9 = 1 CPU
	AutoRemove   bool                     `json:"AutoRemove,omitempty"`
	PortBindings map[string][]PortBinding `json:"PortBindings,omitempty"`
	Privileged   bool                     `json:"Privileged,omitempty"` // required by k3s
	Tmpfs        map[string]string        `json:"Tmpfs,omitempty"`      // tmpfs mounts (path → options)
}

// PortBinding represents a host-to-container port mapping.
type PortBinding struct {
	HostIP   string `json:"HostIp,omitempty"`
	HostPort string `json:"HostPort,omitempty"`
}

// NetworkingConfig specifies the container's networking configuration.
type NetworkingConfig struct {
	EndpointsConfig map[string]*EndpointSettings `json:"EndpointsConfig,omitempty"`
}

// EndpointSettings describes a container's attachment to a Docker network.
type EndpointSettings struct {
	// Empty struct is enough to attach to a network.
}

// CreateContainerRequest combines all container creation parameters.
type CreateContainerRequest struct {
	*ContainerConfig
	HostConfig       *HostConfig       `json:"HostConfig,omitempty"`
	NetworkingConfig *NetworkingConfig `json:"NetworkingConfig,omitempty"`
}

// CreateContainerResponse is the response from container creation.
type CreateContainerResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings,omitempty"`
}

// ContainerInspect holds container state and networking details.
type ContainerInspect struct {
	ID     string            `json:"Id"`
	Name   string            `json:"Name"` // e.g. "/overcast-rds-mydb"
	Labels map[string]string `json:"Labels"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status     string `json:"Status"` // "created", "running", "exited", etc.
		Running    bool   `json:"Running"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`     // runtime error, e.g. "OCI runtime create failed: ..."
		OOMKilled  bool   `json:"OOMKilled"` // true if the kernel OOM-killer terminated the container
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
	HostConfig struct {
		Binds []string `json:"Binds"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
		// Ports maps "containerPort/proto" → list of host bindings.
		// e.g. "3306/tcp" → [{"HostIp":"0.0.0.0","HostPort":"33060"}]
		Ports map[string][]PortBinding `json:"Ports"`
	} `json:"NetworkSettings"`
}

// HasOvercastLabels reports whether the container was created by Overcast with
// the given service name and resource ID. Use this before reusing a container
// found by name to avoid accidentally attaching to a user-created container
// that happens to share the same name.
func (c *ContainerInspect) HasOvercastLabels(service, resourceID string) bool {
	labels := c.Config.Labels
	if len(labels) == 0 {
		labels = c.Labels // fallback for older inspect responses
	}
	return labels[LabelManaged] == "true" &&
		labels[LabelService] == service &&
		labels[LabelResourceID] == resourceID
}

// NetworkInspect holds Docker network details.
type NetworkInspect struct {
	ID       string            `json:"Id"`
	Name     string            `json:"Name"`
	Internal bool              `json:"Internal"`
	Labels   map[string]string `json:"Labels"`
	IPAM     NetworkIPAM       `json:"IPAM"`
}

// NetworkIPAM describes IP address management for a Docker network.
type NetworkIPAM struct {
	Config []NetworkIPAMConfig `json:"Config"`
}

// NetworkIPAMConfig describes one IPAM pool.
type NetworkIPAMConfig struct {
	Subnet  string `json:"Subnet"`
	Gateway string `json:"Gateway"`
}

// NetworkSummary is a lightweight network representation used by ListNetworks.
type NetworkSummary struct {
	ID     string            `json:"Id"`
	Name   string            `json:"Name"`
	Labels map[string]string `json:"Labels"`
	IPAM   NetworkIPAM       `json:"IPAM"`
}

// Subnet returns the first IPAM subnet for the network, or empty if unset.
func (n *NetworkSummary) Subnet() string {
	if len(n.IPAM.Config) == 0 {
		return ""
	}
	return n.IPAM.Config[0].Subnet
}

// Service returns the overcast.service label value (empty string if not set).
func (n *NetworkSummary) Service() string { return n.Labels[LabelService] }

// ResourceID returns the overcast.resource-id label value (empty string if not set).
func (n *NetworkSummary) ResourceID() string { return n.Labels[LabelResourceID] }

// ContainerSummary is the lightweight container representation returned by
// GET /containers/json (list endpoint), as opposed to the full ContainerInspect
// returned by GET /containers/{id}/json.
type ContainerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"` // e.g. ["/overcast-rds-mydb"]
	Image  string            `json:"Image"`
	State  string            `json:"State"`  // "running", "exited", "created", etc.
	Status string            `json:"Status"` // human-readable, e.g. "Up 2 hours"
	Labels map[string]string `json:"Labels"`
	Ports  []struct {
		HostPort      int    `json:"PublicPort"`
		ContainerPort int    `json:"PrivatePort"`
		Type          string `json:"Type"`
	} `json:"Ports"`
}

// Service returns the overcast.service label value (empty string if not set).
func (c *ContainerSummary) Service() string { return c.Labels[LabelService] }

// ResourceID returns the overcast.resource-id label value (empty string if not set).
func (c *ContainerSummary) ResourceID() string { return c.Labels[LabelResourceID] }

// FirstName returns the primary container name without the leading slash.
func (c *ContainerSummary) FirstName() string {
	if len(c.Names) == 0 {
		return ""
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// ─── API helpers ───────────────────────────────────────────────────────────

func (d *Client) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.host+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return d.httpClient.Do(req)
}

func (d *Client) doJSON(ctx context.Context, method, path string, reqBody interface{}, respBody interface{}) error {
	var body io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = strings.NewReader(string(data))
	}
	resp, err := d.doRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("docker %s %s: %d: %s", method, path, resp.StatusCode, string(errBody))
	}

	if respBody != nil {
		return json.NewDecoder(resp.Body).Decode(respBody)
	}
	return nil
}

// ─── Container operations ──────────────────────────────────────────────────

// Ping checks Docker daemon connectivity.
func (d *Client) Ping(ctx context.Context) error {
	resp, err := d.doRequest(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker ping: status %d", resp.StatusCode)
	}
	return nil
}

// CreateContainer creates a container (does not start it).
func (d *Client) CreateContainer(ctx context.Context, name string, req *CreateContainerRequest) (string, error) {
	path := "/v1.45/containers/create"
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}
	var resp CreateContainerResponse
	if err := d.doJSON(ctx, http.MethodPost, path, req, &resp); err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	return resp.ID, nil
}

// StartContainer starts a previously created container.
func (d *Client) StartContainer(ctx context.Context, id string) error {
	resp, err := d.doRequest(ctx, http.MethodPost, "/v1.45/containers/"+id+"/start", nil)
	if err != nil {
		return fmt.Errorf("start container %s: %w", id, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		return fmt.Errorf("start container %s: status %d", id, resp.StatusCode)
	}
	return nil
}

// StopContainer stops a running container with a timeout.
func (d *Client) StopContainer(ctx context.Context, id string, timeoutSec int) error {
	path := fmt.Sprintf("/v1.45/containers/%s/stop?t=%d", id, timeoutSec)
	resp, err := d.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("stop container %s: %w", id, err)
	}
	resp.Body.Close()
	// 204 = stopped, 304 = already stopped, 404 = not found (already removed)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("stop container %s: status %d", id, resp.StatusCode)
	}
	return nil
}

// CopyFileFromContainer returns the raw bytes of a file path from inside a
// container using Docker's archive endpoint.
func (d *Client) CopyFileFromContainer(ctx context.Context, id, path string) ([]byte, error) {
	endpoint := "/v1.45/containers/" + id + "/archive?path=" + url.QueryEscape(path)
	resp, err := d.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("copy file from container %s: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("copy file from container %s: status %d: %s", id, resp.StatusCode, string(errBody))
	}

	tr := tar.NewReader(resp.Body)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read container archive: %w", err)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read container file %s: %w", path, err)
		}
		return data, nil
	}

	return nil, fmt.Errorf("file %s not found in container archive", path)
}

// RemoveContainer removes a container. force=true kills it first if running.
func (d *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	path := fmt.Sprintf("/v1.45/containers/%s?force=%t", id, force)
	resp, err := d.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("remove container %s: %w", id, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("remove container %s: status %d", id, resp.StatusCode)
	}
	return nil
}

// RemoveContainerForce removes a container using a background context with a
// deadline, ensuring cleanup always succeeds even when the request context is
// cancelled. Use this for teardown/cleanup paths only.
func (d *Client) RemoveContainerForce(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.RemoveContainer(ctx, id, true)
}

// InspectContainer returns container details.
func (d *Client) InspectContainer(ctx context.Context, id string) (*ContainerInspect, error) {
	var info ContainerInspect
	if err := d.doJSON(ctx, http.MethodGet, "/v1.45/containers/"+id+"/json", nil, &info); err != nil {
		return nil, fmt.Errorf("inspect container %s: %w", id, err)
	}
	return &info, nil
}

// UpdateContainerResources updates resource limits on a running container.
// Only the non-zero fields in the request are applied; zero values are ignored
// by the Docker daemon. Mirrors POST /containers/{id}/update.
func (d *Client) UpdateContainerResources(ctx context.Context, id string, update *UpdateResourcesRequest) error {
	path := "/v1.45/containers/" + id + "/update"
	if err := d.doJSON(ctx, http.MethodPost, path, update, nil); err != nil {
		return fmt.Errorf("update container %s resources: %w", id, err)
	}
	return nil
}

// UpdateResourcesRequest contains the resource fields that can be changed on a
// running container via the Docker Engine API POST /containers/{id}/update.
type UpdateResourcesRequest struct {
	NanoCPUs   int64 `json:"NanoCPUs,omitempty"`   // 1e9 = 1 CPU
	Memory     int64 `json:"Memory,omitempty"`     // bytes
	MemorySwap int64 `json:"MemorySwap,omitempty"` // bytes (-1 = unlimited)
}

// GetContainerByName looks up a container by its name (without the leading "/").
// Returns (nil, nil) if no container with that name exists.
func (d *Client) GetContainerByName(ctx context.Context, name string) (*ContainerInspect, error) {
	// Docker accept inspect by name directly.
	resp, err := d.doRequest(ctx, http.MethodGet, "/v1.45/containers/"+url.PathEscape(name)+"/json", nil)
	if err != nil {
		return nil, fmt.Errorf("inspect container by name %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("inspect container by name %s: %d: %s", name, resp.StatusCode, string(errBody))
	}
	var info ContainerInspect
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("inspect container by name %s: decode: %w", name, err)
	}
	return &info, nil
}

// IsConflict reports whether an error is a Docker 409 Conflict response
// (e.g. container name already in use).
func IsConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), ": 409:")
}

// IsNotFound reports whether an error is a Docker 404 Not Found response.
func IsNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), ": 404:")
}

// ListContainers returns all containers (running and stopped) that carry
// overcast.managed=true and optionally overcast.service=<service>.
// Pass an empty service string to list across all services.
func (d *Client) ListContainers(ctx context.Context, service string) ([]ContainerSummary, error) {
	// Docker filter JSON: all=true gives stopped containers too.
	filterMap := map[string][]string{
		"label": {LabelManaged + "=true"},
	}
	if service != "" {
		filterMap["label"] = append(filterMap["label"], LabelService+"="+service)
	}
	filterJSON, err := json.Marshal(filterMap)
	if err != nil {
		return nil, fmt.Errorf("list containers: marshal filters: %w", err)
	}
	path := "/v1.45/containers/json?all=true&filters=" + url.QueryEscape(string(filterJSON))
	var containers []ContainerSummary
	if err := d.doJSON(ctx, http.MethodGet, path, nil, &containers); err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	return containers, nil
}

// ContainerLogs fetches container stdout+stderr logs (non-streaming).
func (d *Client) ContainerLogs(ctx context.Context, id string, tail string) ([]byte, error) {
	path := fmt.Sprintf("/v1.45/containers/%s/logs?stdout=true&stderr=true&tail=%s",
		id, url.QueryEscape(tail))
	resp, err := d.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("container logs %s: %w", id, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 64*1024))
}

// ContainerLogsSince fetches the full stdout+stderr log payload for a container
// starting from a given Unix timestamp (seconds). Used for reconciliation —
// after a streaming follower fails or on container teardown — to backfill any
// log frames that the streaming connection may have missed. Output includes
// per-line RFC3339Nano timestamps (timestamps=true) so the caller can
// deduplicate against events already delivered.
//
// The response is a multiplexed Docker log stream identical in shape to
// ContainerLogsStream's body; pass it through dockerLogStripper to extract
// payload bytes.
func (d *Client) ContainerLogsSince(ctx context.Context, id string, since time.Time) (io.ReadCloser, error) {
	path := fmt.Sprintf("/v1.45/containers/%s/logs?stdout=true&stderr=true&timestamps=true&tail=all", id)
	if !since.IsZero() {
		path += fmt.Sprintf("&since=%d.%09d", since.Unix(), since.Nanosecond())
	}
	resp, err := d.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("container logs since %s: %w", id, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("container logs since %s: status %d", id, resp.StatusCode)
	}
	return resp.Body, nil
}

// ContainerLogsStream opens a streaming connection to the container log endpoint
// with follow=true. The caller is responsible for closing the returned ReadCloser.
// When ctx is cancelled the underlying HTTP connection is closed automatically,
// which causes reads on the stream to return an error, making the reader goroutine
// exit cleanly without an explicit close call.
//
// The since parameter (Unix seconds with nanosecond fraction) lets a caller
// resume after a stream failure without re-receiving lines that were already
// delivered. Pass time.Time{} for "from start of container".
func (d *Client) ContainerLogsStream(ctx context.Context, id string, since time.Time) (io.ReadCloser, error) {
	path := fmt.Sprintf("/v1.45/containers/%s/logs?stdout=true&stderr=true&follow=true&timestamps=true", id)
	if !since.IsZero() {
		path += fmt.Sprintf("&since=%d.%09d", since.Unix(), since.Nanosecond())
	}
	resp, err := d.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("container logs stream %s: %w", id, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("container logs stream %s: status %d", id, resp.StatusCode)
	}
	return resp.Body, nil
}

// WaitContainer blocks until a container exits. Returns the exit code.
func (d *Client) WaitContainer(ctx context.Context, id string) (int, error) {
	resp, err := d.doRequest(ctx, http.MethodPost, "/v1.45/containers/"+id+"/wait", nil)
	if err != nil {
		return -1, fmt.Errorf("wait container %s: %w", id, err)
	}
	defer resp.Body.Close()
	var result struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return -1, fmt.Errorf("wait container %s: decode: %w", id, err)
	}
	return result.StatusCode, nil
}

// ContainerMemoryUsage returns the current memory usage (in bytes) of a container.
// Uses the one-shot stats endpoint (stream=false) so it returns immediately.
func (d *Client) ContainerMemoryUsage(ctx context.Context, id string) (usageBytes int64, err error) {
	resp, err := d.doRequest(ctx, http.MethodGet, "/v1.45/containers/"+id+"/stats?stream=false", nil)
	if err != nil {
		return 0, fmt.Errorf("container stats %s: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("container stats %s: status %d", id, resp.StatusCode)
	}
	var stats struct {
		MemoryStats struct {
			Usage int64 `json:"usage"`
		} `json:"memory_stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return 0, fmt.Errorf("container stats %s: decode: %w", id, err)
	}
	return stats.MemoryStats.Usage, nil
}

// ─── Image operations ──────────────────────────────────────────────────────

// PullImage pulls an image. This blocks until the pull is complete.
func (d *Client) PullImage(ctx context.Context, image string) error {
	path := "/v1.45/images/create?fromImage=" + url.QueryEscape(image)
	resp, err := d.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("pull image %s: status %d: %s", image, resp.StatusCode, string(body))
	}

	// The pull response is a stream of JSON progress objects. We must consume
	// the entire body for the pull to complete. Check the last line for errors.
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("pull image %s: read response: %w", image, err)
	}

	// Check for error in the last JSON object.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 0 {
		var lastLine struct {
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(lines[len(lines)-1]), &lastLine) == nil && lastLine.Error != "" {
			return fmt.Errorf("pull image %s: %s", image, lastLine.Error)
		}
	}

	// Best-effort: reclaim disk from <none> layers left behind when a newer
	// version of the same tag is pulled. Failures are not fatal.
	if err := d.PruneDanglingImages(ctx); err != nil && d.logger != nil {
		d.logger.Debug("prune dangling images after pull", zap.String("image", image), zap.Error(err))
	}

	return nil
}

// PruneDanglingImages removes all dangling (untagged) images. Equivalent to
// `docker image prune -f`. Safe to call after any pull or image retag — it
// only removes images that have no tag and are not referenced by a running
// container, so it cannot break in-use resources.
func (d *Client) PruneDanglingImages(ctx context.Context) error {
	filterJSON, err := json.Marshal(map[string][]string{
		"dangling": {"true"},
	})
	if err != nil {
		return fmt.Errorf("prune images: marshal filters: %w", err)
	}
	path := "/v1.45/images/prune?filters=" + url.QueryEscape(string(filterJSON))
	resp, err := d.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("prune images: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("prune images: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ImageExists checks if an image exists locally.
func (d *Client) ImageExists(ctx context.Context, image string) (bool, error) {
	resp, err := d.doRequest(ctx, http.MethodGet, "/v1.45/images/"+url.PathEscape(image)+"/json", nil)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// ─── Network operations ────────────────────────────────────────────────────

type createNetworkRequest struct {
	Name           string            `json:"Name"`
	Driver         string            `json:"Driver"`
	CheckDuplicate bool              `json:"CheckDuplicate"`
	Internal       bool              `json:"Internal,omitempty"`
	Labels         map[string]string `json:"Labels,omitempty"`
	IPAM           *NetworkIPAM      `json:"IPAM,omitempty"`
}

// CreateNetwork creates a Docker network. Returns the network ID.
// Ignores "already exists" errors.
func (d *Client) CreateNetwork(ctx context.Context, name string) (string, error) {
	return d.CreateNetworkWithOptions(ctx, CreateNetworkOptions{Name: name})
}

// CreateNetworkOptions configures a Docker network.
type CreateNetworkOptions struct {
	Name     string
	Labels   map[string]string // nil = no labels
	Subnet   string            // CIDR, e.g. "10.0.0.0/16"; empty = Docker default
	Internal bool              // true = no outbound internet access
}

// CreateNetworkWithOptions creates a Docker network with full control over
// labels, CIDR, and internal mode. Returns the network ID.
// Ignores "already exists" errors.
func (d *Client) CreateNetworkWithOptions(ctx context.Context, opts CreateNetworkOptions) (string, error) {
	req := createNetworkRequest{
		Name:           opts.Name,
		Driver:         "bridge",
		CheckDuplicate: true,
		Internal:       opts.Internal,
		Labels:         opts.Labels,
	}
	if opts.Subnet != "" {
		req.IPAM = &NetworkIPAM{
			Config: []NetworkIPAMConfig{{Subnet: opts.Subnet}},
		}
	}
	var resp struct {
		ID      string `json:"Id"`
		Warning string `json:"Warning"`
	}
	err := d.doJSON(ctx, http.MethodPost, "/v1.45/networks/create", &req, &resp)
	if err != nil {
		// Check if it's a "name already in use" error — that's fine.
		if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "already in use") {
			// Look up the existing network.
			existing, lookupErr := d.InspectNetwork(ctx, opts.Name)
			if lookupErr != nil {
				return "", lookupErr
			}
			return existing.ID, nil
		}
		return "", fmt.Errorf("create network %s: %w", opts.Name, err)
	}
	return resp.ID, nil
}

// InspectNetwork returns network details.
func (d *Client) InspectNetwork(ctx context.Context, nameOrID string) (*NetworkInspect, error) {
	var info NetworkInspect
	if err := d.doJSON(ctx, http.MethodGet, "/v1.45/networks/"+url.PathEscape(nameOrID), nil, &info); err != nil {
		return nil, fmt.Errorf("inspect network %s: %w", nameOrID, err)
	}
	return &info, nil
}

// ConnectNetwork attaches a container to a network.
func (d *Client) ConnectNetwork(ctx context.Context, networkID, containerID string) error {
	body := struct {
		Container string `json:"Container"`
	}{Container: containerID}
	return d.doJSON(ctx, http.MethodPost, "/v1.45/networks/"+networkID+"/connect", &body, nil)
}

// DisconnectNetwork detaches a container from a network.
func (d *Client) DisconnectNetwork(ctx context.Context, networkID, containerID string) error {
	body := struct {
		Container string `json:"Container"`
		Force     bool   `json:"Force"`
	}{Container: containerID, Force: true}
	return d.doJSON(ctx, http.MethodPost, "/v1.45/networks/"+networkID+"/disconnect", &body, nil)
}

// RemoveNetwork removes a Docker network by name or ID.
func (d *Client) RemoveNetwork(ctx context.Context, nameOrID string) error {
	resp, err := d.doRequest(ctx, http.MethodDelete, "/v1.45/networks/"+url.PathEscape(nameOrID), nil)
	if err != nil {
		return fmt.Errorf("remove network %s: %w", nameOrID, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("remove network %s: status %d", nameOrID, resp.StatusCode)
	}
	return nil
}

// ListNetworks returns all Overcast-managed networks, optionally filtered by service.
func (d *Client) ListNetworks(ctx context.Context, service string) ([]NetworkSummary, error) {
	filterMap := map[string][]string{
		"label": {LabelManaged + "=true"},
	}
	if service != "" {
		filterMap["label"] = append(filterMap["label"], LabelService+"="+service)
	}
	filterJSON, err := json.Marshal(filterMap)
	if err != nil {
		return nil, fmt.Errorf("list networks: marshal filters: %w", err)
	}
	path := "/v1.45/networks?filters=" + url.QueryEscape(string(filterJSON))
	var networks []NetworkSummary
	if err := d.doJSON(ctx, http.MethodGet, path, nil, &networks); err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	return networks, nil
}

// CopyToContainer copies a tar archive into a container at the given path.
// This uses the Docker "Put Archive" API endpoint.
func (d *Client) CopyToContainer(ctx context.Context, id, destPath string, tarData io.Reader) error {
	path := fmt.Sprintf("/v1.45/containers/%s/archive?path=%s", id, url.QueryEscape(destPath))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, d.host+path, tarData)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("copy to container %s: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("copy to container %s: status %d: %s", id, resp.StatusCode, string(body))
	}
	return nil
}

// ─── Helpers ───────────────────────────────────────────────────────────────

// Available checks if the Docker daemon is reachable.
func (d *Client) Available(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return d.Ping(ctx) == nil
}
