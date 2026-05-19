package eks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
)

func k3sImageForVersion(version string) string {
	// e.g. "1.31" → "rancher/k3s:v1.31.3-k3s1"
	return "rancher/k3s:v" + version + ".3-k3s1"
}

// startLiveCluster creates and starts a k3s control-plane container for the
// given cluster. The caller must have already persisted the cluster record with
// CREATING status. On success the live runtime registry is updated with the
// container ID. On failure a log warning is emitted and the registry entry
// keeps an empty container ID so the cluster stays in CREATING indefinitely
// until a retry or cleanup path handles it.
func (s *Service) startLiveCluster(ctx context.Context, region string, cluster *Cluster) {
	if s.docker == nil {
		s.log.Warn("startLiveCluster called without docker client", zap.String("cluster", cluster.Name))
		return
	}

	image := k3sImageForVersion(cluster.Version)
	containerName := "overcast-eks-" + cluster.Name
	network := s.cfg.EKSNetwork

	createReq := &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image:        image,
			Cmd:          []string{"server", "--disable=traefik", "--disable=metrics-server"},
			Labels:       docker.ManagedLabels(serviceName, cluster.Name),
			ExposedPorts: map[string]struct{}{"6443/tcp": {}},
		},
		HostConfig: &docker.HostConfig{AutoRemove: true,
			NetworkMode:  network,
			Privileged:   true,
			Tmpfs:        map[string]string{"/run": "", "/var/run": ""},
			PortBindings: map[string][]docker.PortBinding{"6443/tcp": {{HostIP: "0.0.0.0"}}},
		},
		NetworkingConfig: &docker.NetworkingConfig{
			EndpointsConfig: map[string]*docker.EndpointSettings{
				network: {},
			},
		},
	}

	containerID, err := s.docker.CreateContainer(ctx, containerName, createReq)
	if err != nil {
		if docker.IsConflict(err) {
			existing, inspectErr := s.docker.GetContainerByName(ctx, containerName)
			if inspectErr != nil || existing == nil {
				s.log.Warn("k3s container name conflict, inspect failed",
					zap.String("cluster", cluster.Name), zap.Error(err))
				return
			}
			if !existing.HasOvercastLabels(serviceName, cluster.Name) {
				s.log.Warn("conflicting container not managed by overcast-eks",
					zap.String("cluster", cluster.Name), zap.String("container", containerName))
				return
			}
			containerID = existing.ID
			if !existing.State.Running {
				if err := s.docker.StartContainer(ctx, containerID); err != nil {
					s.log.Warn("failed to start existing k3s container after name conflict",
						zap.String("cluster", cluster.Name), zap.Error(err))
					return
				}
			}
		} else {
			s.log.Warn("failed to create k3s container",
				zap.String("cluster", cluster.Name), zap.Error(err))
			return
		}
	} else {
		if err := s.docker.StartContainer(ctx, containerID); err != nil {
			_ = s.docker.RemoveContainerForce(containerID)
			s.log.Warn("failed to start k3s container",
				zap.String("cluster", cluster.Name), zap.Error(err))
			return
		}
	}

	s.setLiveClusterRuntime(region, cluster.Name, &liveClusterRuntime{containerID: containerID})
	s.pollK3sReady(ctx, region, cluster, containerID)
}

// pollK3sReady polls the k3s API server host-mapped port until it responds,
// then marks the cluster ACTIVE with the real endpoint URL. It is called
// synchronously at the end of startLiveCluster, which itself runs as a
// background goroutine, so the blocking poll does not delay API responses.
func (s *Service) pollK3sReady(ctx context.Context, region string, cluster *Cluster, containerID string) {
	const pollInterval = 2 * time.Second
	const pollTimeout = 5 * time.Minute

	inspect, err := s.docker.InspectContainer(ctx, containerID)
	if err != nil {
		s.log.Warn("pollK3sReady: failed to inspect container",
			zap.String("cluster", cluster.Name), zap.Error(err))
		return
	}

	hostPort := ""
	if bindings, ok := inspect.NetworkSettings.Ports["6443/tcp"]; ok && len(bindings) > 0 {
		hostPort = bindings[0].HostPort
	}
	if hostPort == "" {
		s.log.Warn("pollK3sReady: no host port mapping for 6443/tcp",
			zap.String("cluster", cluster.Name))
		return
	}

	pollEndpoint := "https://127.0.0.1:" + hostPort
	clusterEndpoint := "https://" + s.cfg.ExternalHostname() + ":" + hostPort
	readyzURL := pollEndpoint + "/readyz"

	//nolint:gosec // intentional: local dev only, k3s uses self-signed cert
	tlsClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	deadline := s.clk.Now().Add(pollTimeout)
	for s.clk.Now().Before(deadline) {
		resp, err := tlsClient.Get(readyzURL) //nolint:noctx // best-effort background poll
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
				caData, caErr := s.extractK3sCAData(ctx, containerID)
				if caErr != nil {
					s.log.Warn("pollK3sReady: failed to read k3s certificate authority data",
						zap.String("cluster", cluster.Name), zap.Error(caErr))
				}
				// API server is reachable — mark cluster ACTIVE.
				s.setClusterActiveWithEndpoint(ctx, region, cluster.Name, clusterEndpoint, caData)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-s.clk.After(pollInterval):
		}
	}
	s.log.Warn("pollK3sReady: timed out waiting for k3s API",
		zap.String("cluster", cluster.Name))
}

// setClusterActiveWithEndpoint reads the current cluster record and updates it
// to ACTIVE with the given endpoint. Re-reading before writing avoids clobbering
// concurrent mutations (e.g. a concurrent DeleteCluster).
func (s *Service) setClusterActiveWithEndpoint(ctx context.Context, region, name, endpoint, caData string) {
	existing, found, err := s.getCluster(ctx, region, name)
	if err != nil || !found {
		s.log.Warn("setClusterActiveWithEndpoint: cluster not found",
			zap.String("cluster", name))
		return
	}
	existing.Status = "ACTIVE"
	existing.Endpoint = endpoint
	if strings.TrimSpace(caData) != "" {
		existing.CertificateAuthority = map[string]any{"data": caData}
	}
	if err := s.putCluster(ctx, region, existing); err != nil {
		s.log.Warn("setClusterActiveWithEndpoint: failed to persist",
			zap.String("cluster", name), zap.Error(err))
	}
}

func (s *Service) extractK3sCAData(ctx context.Context, containerID string) (string, error) {
	if s.docker == nil {
		return "", nil
	}

	const k3sConfigPath = "/etc/rancher/k3s/k3s.yaml"
	raw, err := s.docker.CopyFileFromContainer(ctx, containerID, k3sConfigPath)
	if err != nil {
		return "", err
	}

	const caPrefix = "certificate-authority-data:"
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, caPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, caPrefix)), nil
		}
	}

	return "", nil
}

func liveClusterRuntimeKey(region, name string) string {
	return region + "/" + name
}

func (s *Service) setLiveClusterRuntime(region, name string, runtime *liveClusterRuntime) {
	if runtime == nil {
		return
	}
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	s.liveRuntimes[liveClusterRuntimeKey(region, name)] = runtime
}

func (s *Service) getLiveClusterRuntime(region, name string) (*liveClusterRuntime, bool) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	runtime, found := s.liveRuntimes[liveClusterRuntimeKey(region, name)]
	if !found || runtime == nil {
		return nil, false
	}
	copy := *runtime
	return &copy, true
}

func (s *Service) deleteLiveClusterRuntime(region, name string) (*liveClusterRuntime, bool) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	key := liveClusterRuntimeKey(region, name)
	runtime, found := s.liveRuntimes[key]
	if found {
		delete(s.liveRuntimes, key)
	}
	if !found || runtime == nil {
		return nil, false
	}
	copy := *runtime
	return &copy, true
}

func (s *Service) reconcileLiveClusterRuntime(ctx context.Context, region, name string) (*liveClusterRuntime, bool, error) {
	if s.docker == nil {
		return nil, false, nil
	}

	containerName := "overcast-eks-" + name
	inspect, err := s.docker.GetContainerByName(ctx, containerName)
	if err != nil {
		return nil, false, err
	}
	if inspect == nil || !inspect.HasOvercastLabels(serviceName, name) {
		return nil, false, nil
	}

	runtime := &liveClusterRuntime{containerID: inspect.ID}
	s.setLiveClusterRuntime(region, name, runtime)
	return runtime, true, nil
}

func (s *Service) reconcilePersistedLiveClusterRuntimes(ctx context.Context) {
	if s.docker == nil {
		return
	}

	pairs, err := s.store.Scan(ctx, nsClusters, "")
	if err != nil {
		s.log.Warn("failed to scan persisted EKS clusters for runtime reconciliation", zap.Error(err))
		return
	}

	for _, kv := range pairs {
		var cluster Cluster
		if err := json.Unmarshal([]byte(kv.Value), &cluster); err != nil {
			continue
		}
		if s.isMockModeClusterRecord(&cluster) {
			continue
		}

		region, name, ok := eksClusterFromResourceARN(cluster.Arn)
		if !ok {
			continue
		}
		// Refresh runtime identity by managed name even when a cached ID exists;
		// stale non-empty IDs can otherwise leak recreated managed containers.
		if _, _, err := s.reconcileLiveClusterRuntime(ctx, region, name); err != nil {
			s.log.Warn("failed to reconcile persisted EKS live runtime during stop",
				zap.String("cluster", name), zap.Error(err))
		}
	}
}

func (s *Service) reconcileReadyLiveCluster(ctx context.Context, region string, cluster *Cluster) *Cluster {
	if cluster == nil || s.docker == nil || s.isMockModeClusterRecord(cluster) {
		return cluster
	}

	caData, _ := cluster.CertificateAuthority["data"].(string)
	if cluster.Status == "ACTIVE" && strings.TrimSpace(cluster.Endpoint) != "" && strings.TrimSpace(caData) != "" {
		return cluster
	}

	var inspect *docker.ContainerInspect
	runtime, found := s.getLiveClusterRuntime(region, cluster.Name)
	if !found || strings.TrimSpace(runtime.containerID) == "" {
		containerName := "overcast-eks-" + cluster.Name
		var err error
		inspect, err = s.docker.GetContainerByName(ctx, containerName)
		if err != nil {
			s.log.Warn("failed to reconcile EKS live runtime during cluster describe",
				zap.String("cluster", cluster.Name), zap.Error(err))
			return cluster
		}
		if inspect != nil && inspect.HasOvercastLabels(serviceName, cluster.Name) {
			runtime = &liveClusterRuntime{containerID: inspect.ID}
			s.setLiveClusterRuntime(region, cluster.Name, runtime)
			found = true
		}
	}
	if !found || strings.TrimSpace(runtime.containerID) == "" {
		return cluster
	}

	if inspect == nil {
		var err error
		inspect, err = s.docker.InspectContainer(ctx, runtime.containerID)
		if err != nil {
			if !docker.IsNotFound(err) {
				s.log.Warn("failed to inspect reconciled EKS live runtime during cluster describe",
					zap.String("cluster", cluster.Name), zap.Error(err))
				return cluster
			}
			// Stale container ID — fall back to name-based lookup.
			containerName := "overcast-eks-" + cluster.Name
			nameInspect, nameErr := s.docker.GetContainerByName(ctx, containerName)
			if nameErr != nil {
				s.log.Warn("failed to look up EKS live runtime by name after stale ID during cluster describe",
					zap.String("cluster", cluster.Name), zap.Error(nameErr))
				return cluster
			}
			if nameInspect == nil || !nameInspect.HasOvercastLabels(serviceName, cluster.Name) {
				return cluster
			}
			runtime = &liveClusterRuntime{containerID: nameInspect.ID}
			s.setLiveClusterRuntime(region, cluster.Name, runtime)
			inspect, err = s.docker.InspectContainer(ctx, runtime.containerID)
			if err != nil {
				s.log.Warn("failed to inspect refreshed EKS live runtime after stale ID recovery",
					zap.String("cluster", cluster.Name), zap.Error(err))
				return cluster
			}
		}
	}

	hostPort := ""
	if bindings, ok := inspect.NetworkSettings.Ports["6443/tcp"]; ok && len(bindings) > 0 {
		hostPort = bindings[0].HostPort
	}
	if hostPort == "" {
		return cluster
	}

	readyzURL := "https://127.0.0.1:" + hostPort + "/readyz"
	//nolint:gosec // intentional: local dev only, k3s uses self-signed cert
	tlsClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := tlsClient.Get(readyzURL) //nolint:noctx // best-effort readiness reconciliation
	if err != nil {
		return cluster
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
		return cluster
	}

	clusterEndpoint := "https://" + s.cfg.ExternalHostname() + ":" + hostPort
	reconciledCAData, caErr := s.extractK3sCAData(ctx, runtime.containerID)
	if caErr != nil {
		s.log.Warn("failed to read k3s certificate authority data during cluster describe reconciliation",
			zap.String("cluster", cluster.Name), zap.Error(caErr))
	}
	s.setClusterActiveWithEndpoint(ctx, region, cluster.Name, clusterEndpoint, reconciledCAData)

	updated, found, err := s.getCluster(ctx, region, cluster.Name)
	if err != nil || !found {
		return cluster
	}
	return updated
}

func (s *Service) drainLiveClusterRuntimes() []*liveClusterRuntime {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	out := make([]*liveClusterRuntime, 0, len(s.liveRuntimes))
	for key, runtime := range s.liveRuntimes {
		delete(s.liveRuntimes, key)
		if runtime == nil {
			continue
		}
		copy := *runtime
		out = append(out, &copy)
	}
	return out
}

func (s *Service) cleanupLiveClusterRuntime(ctx context.Context, runtime *liveClusterRuntime) error {
	if runtime == nil || strings.TrimSpace(runtime.containerID) == "" || s.docker == nil {
		return nil
	}
	if err := s.docker.StopContainer(ctx, runtime.containerID, 10); err != nil {
		return err
	}
	if err := s.docker.RemoveContainerForce(runtime.containerID); err != nil {
		return err
	}
	return nil
}
