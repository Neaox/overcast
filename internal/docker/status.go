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

	// Decisions is what this run *resolved* each managed network's isolation
	// to, and why. Networks is what the daemon has; this is what the
	// configuration asked for and what it got.
	//
	// They are separate because they have different lifetimes, and conflating
	// them is a bug this file has already had once. An entry leaves Networks
	// when the network does — ForgetNetwork, on a Docker `destroy` — so a rule
	// that reads a *configuration* fact out of Networks stops firing the moment
	// somebody runs `overcast network reset`, while the fact itself is
	// unchanged. A decision outlives the network it was applied to.
	//
	// Not in the health payload: while the network exists, `networks` carries
	// the same isolation and reason, and a second array saying it again would
	// be one more thing to keep consistent. This exists for the advisory rules,
	// which have to keep working in the window where `networks` does not.
	Decisions []NetworkDecision `json:"-"`

	// Instance is this Overcast's sweep-domain identity — the value stamped
	// into LabelInstance on the Docker resources it creates
	// (serviceutil.InstanceDomain). Empty when it could not be established.
	//
	// Reported because ownership is otherwise unknowable from outside the
	// process, and the tools that most need it are outside: `overcast network
	// reset` has to decide whether a network carrying somebody's instance label
	// is this daemon's before it rebuilds it. Without this it either guesses or
	// rebuilds a neighbour's live network in silence.
	Instance string `json:"instance,omitempty"`

	LastEvent   string `json:"lastEvent,omitempty"`
	LastEventAt string `json:"lastEventAt,omitempty"`
}

// NetworkDecision is one network's resolved isolation and the reason for it, as
// decided at probe time. See Status.Decisions.
type NetworkDecision struct {
	// Network is the Docker network name the decision was applied to.
	Network string

	// Internal is what the isolation was resolved to.
	Internal bool

	// Reason names what decided it, in the vocabulary docker.InternalDecision
	// uses: "OVERCAST_VPC_EGRESS=none",
	// "OVERCAST_CONTROL_PLANE_INTERNAL=false", or one of those followed by
	// ", overridden: …" where the host would not allow what the mode asked for.
	//
	// Empty for a network whose isolation is a constant of the model rather
	// than a decision, which is why callers must not read an empty reason as
	// "no override".
	Reason string
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
	if len(t.status.Decisions) > 0 {
		s.Decisions = make([]NetworkDecision, len(t.status.Decisions))
		copy(s.Decisions, t.status.Decisions)
	}
	return s
}

// NetworkReporter is what a service needs to report the Docker facts it owns
// but the Probe never sees: its sweep-domain identity, and the state of the
// networks it creates on its own schedule.
//
// It exists so that per-VPC networks — created on demand by EC2 long after
// Probe has run — reach /_overcast/health through the same field as the two
// planes. A verification that reports two of the three network classes is one
// an operator learns not to trust.
type NetworkReporter interface {
	RecordInstance(id string)
	RecordNetworks(networks []NetworkStatus)
	ForgetNetwork(name string)
}

// RecordInstance records this Overcast's sweep-domain identity, once it is
// resolvable. Called by the service that owns the identity rather than by
// Probe, which runs before any store is read.
func (t *Tracker) RecordInstance(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.Instance = id
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
		t.recordDecisionLocked(n)
	}
}

// recordDecisionLocked keeps the resolved isolation beside the observed state.
// Caller holds t.mu.
func (t *Tracker) recordDecisionLocked(n NetworkStatus) {
	d := NetworkDecision{Network: n.Name, Internal: n.Internal, Reason: n.Reason}
	for i, existing := range t.status.Decisions {
		if existing.Network == d.Network {
			t.status.Decisions[i] = d
			return
		}
	}
	t.status.Decisions = append(t.status.Decisions, d)
}

// ForgetNetwork drops one network from the report.
//
// A recorded status is a statement about a network that exists. Once it does
// not — its VPC was deleted, or `overcast network reset` removed it to rebuild
// it — keeping the last thing Overcast knew turns the report into a memory,
// and a memory is exactly the wrong shape for this field: an operator who runs
// the `overcast network reset` the advisory told them to run, watches it
// succeed, and then sees the same advisory still standing has been given no
// way to tell a fix that did not work from a report that did not move (#1583).
//
// Forgetting rather than re-verifying is the conservative half of that. An
// absent network reports nothing, and nothing is what the rest of this file
// already means by absence — see the Networks field, and advisoryInput.Networks:
// "no networks reported" is indistinguishable from "no Docker", so it fires
// nothing. The next probe re-establishes the truth.
//
// The decision is deliberately kept. What went away is the network, not the
// configuration that asked for it — and the advisory rules that read a
// configuration fact must not be switched off by a `docker network rm`.
func (t *Tracker) ForgetNetwork(name string) {
	if name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, existing := range t.status.Networks {
		if existing.Name == name {
			t.status.Networks = append(t.status.Networks[:i], t.status.Networks[i+1:]...)
			return
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
