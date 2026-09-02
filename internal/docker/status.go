package docker

import (
	"net/http"
	"sync"
	"time"
)

type BackingType string

const (
	BackingContainer    BackingType = "container"
	BackingMetadataOnly BackingType = "metadata-only"
)

type ContainerHealth string

const (
	ContainerHealthHealthy  ContainerHealth = "healthy"
	ContainerHealthDegraded ContainerHealth = "degraded"
	ContainerHealthMissing  ContainerHealth = "missing"
	ContainerHealthUnknown  ContainerHealth = "unknown"
)

// ServiceHealth records the Docker connectivity state for a single service.
type ServiceHealth struct {
	Service   string    `json:"service"`
	Socket    string    `json:"socket"`
	Connected bool      `json:"connected"`
	Error     string    `json:"error,omitempty"`
	LastSeen  time.Time `json:"lastSeen,omitempty"`
}

// Status is a snapshot of the Docker daemon state across all services.
type Status struct {
	Available bool            `json:"available"`
	Services  []ServiceHealth `json:"services"`

	// Networks is the planes Overcast ensured at startup and the isolation
	// each ended up with. It is reported because "is the control plane
	// internal, and why" is a runtime topology fact that decides whether
	// compute in a gateway-less VPC reaches the internet — and until #1564 the
	// only way to find out was `docker network inspect` plus a guess at the
	// reason.
	Networks []NetworkStatus `json:"networks,omitempty"`

	LastEvent   string `json:"lastEvent,omitempty"`
	LastEventAt string `json:"lastEventAt,omitempty"`
}

// Tracker holds the canonical Docker connectivity state. The Supervisor writes
// to it during Probe, and the health endpoint reads from it.
type Tracker struct {
	mu     sync.RWMutex
	status Status
}

// NewTracker returns a Tracker that records per-service Docker health.
func NewTracker() *Tracker {
	return &Tracker{
		status: Status{Available: false},
	}
}

// Snapshot returns a copy of the current state.
func (t *Tracker) Snapshot() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s := t.status
	// Deep copy the services slice.
	s.Services = make([]ServiceHealth, len(t.status.Services))
	copy(s.Services, t.status.Services)
	if len(t.status.Networks) > 0 {
		s.Networks = make([]NetworkStatus, len(t.status.Networks))
		copy(s.Networks, t.status.Networks)
	}
	return s
}

// NetworkStatusSink is the one method a service needs to report the state of a
// network it manages on its own schedule.
//
// It exists so that per-VPC networks — created on demand by EC2 long after
// Probe has run — reach /_overcast/health through the same field as the two
// planes. A verification that reports two of the three network classes is a
// verification an operator learns not to trust.
type NetworkStatusSink interface {
	RecordNetworks(networks []NetworkStatus)
}

// RecordNetworks records the isolation each plane ended up with, as resolved
// by Probe.
//
// Later calls merge by network name rather than replacing the slice: services
// may be spread over more than one socket, so this is called once per daemon
// probed, and the planes are the same set on each. Order is first-seen, which
// is PlaneSpecs' order — data plane, then control plane.
func (t *Tracker) RecordNetworks(networks []NetworkStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, n := range networks {
		replaced := false
		for i, existing := range t.status.Networks {
			if existing.Name == n.Name {
				t.status.Networks[i] = n
				replaced = true
				break
			}
		}
		if !replaced {
			t.status.Networks = append(t.status.Networks, n)
		}
	}
}

// RecordProbeResult records the result of a probe attempt for a set of
// service configs. Successful services are marked connected; every
// registered service that was not in results is marked disconnected.
func (t *Tracker) RecordProbeResult(configs []ServiceConfig, results []ServiceResult) {
	t.mu.Lock()
	defer t.mu.Unlock()

	resultByName := make(map[string]ServiceResult, len(results))
	for _, r := range results {
		resultByName[r.Name] = r
	}

	now := time.Now().UTC()
	services := make([]ServiceHealth, 0, len(configs))
	anyConnected := false

	for _, cfg := range configs {
		_, ok := resultByName[cfg.Name]
		sh := ServiceHealth{
			Service:   cfg.Name,
			Socket:    cfg.Socket,
			Connected: ok,
			LastSeen:  now,
		}
		if !ok {
			sh.Error = "docker not available — service is metadata-only"
		}
		services = append(services, sh)
		if ok {
			anyConnected = true
		}
	}

	t.status.Available = anyConnected
	t.status.Services = services
}

// RecordDockerEvent records a Docker daemon event for observability.
func (t *Tracker) RecordDockerEvent(eventType string, eventTime time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.LastEvent = eventType
	t.status.LastEventAt = eventTime.UTC().Format(time.RFC3339)
}

// SetBackingHeaders writes x-overcast-backing, x-overcast-backing-reason,
// and x-overcast-container-health response headers. dockerReady should reflect
// whether the service handler has a live Docker client wired. health is the
// assessed container health state.
func SetBackingHeaders(w http.ResponseWriter, dockerReady bool, health ContainerHealth) {
	backing := BackingMetadataOnly
	reason := "docker-unavailable"
	if dockerReady {
		backing = BackingContainer
		reason = "docker-wired"
	}
	w.Header().Set("x-overcast-backing", string(backing))
	w.Header().Set("x-overcast-backing-reason", reason)
	if health != "" {
		w.Header().Set("x-overcast-container-health", string(health))
	}
}
