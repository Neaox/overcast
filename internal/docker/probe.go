package docker

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// legacyNetworks are the per-service networks Overcast created before the
// two-plane model (docs/plans/container-network-topology.md). An upgraded
// installation still has them, and they are pure clutter once nothing joins
// them — but a container adopted from an earlier run may still be attached, so
// removal is attempted and never forced: Docker refuses to remove a network
// with active endpoints, which is exactly the outcome wanted there.
var legacyNetworks = []string{
	"overcast_lambda",
	"overcast_ecs",
	"overcast_rds",
	"overcast_elasticache",
	"overcast_msk",
	"overcast_eks",
	"overcast_efs",
}

// ProbeResult is returned by Probe on success.
type ProbeResult struct {
	Client *Client

	// Networks is what became of each NetworkSpec: the isolation each plane
	// ended up with, why, and whether a pre-existing network disagreed. Carried
	// out of Probe rather than only logged so /_overcast/health can report it —
	// "which network am I on and is it internal" is a runtime topology fact,
	// and the whole point of #1564 is that it should not take a `docker network
	// inspect` and a guess to find out.
	Networks []NetworkStatus
}

// NetworkStatus is one plane's isolation as it actually stands, as reported by
// /_overcast/health under `docker.networks`.
type NetworkStatus struct {
	Name string `json:"name"`

	// Internal is what the network *is*, not what this run asked for. Those
	// differ only in the drift case below, and reporting the ask there would
	// repeat #1564's original lie in a new place: an engineer reading
	// `internal: false` while their function gets ENETUNREACH is exactly the
	// confusion this field exists to end.
	Internal bool `json:"internal"`

	// Reason names what this run decided and why — "auto: Overcast is
	// containerised", "OVERCAST_CONTROL_PLANE_INTERNAL=false". Empty for a
	// plane whose isolation is a constant of the model rather than a decision.
	Reason string `json:"reason,omitempty"`

	// Drift is set when the network's actual isolation is not what this run
	// decided, and says why it could not be applied. Docker never retroactively
	// applies `--internal`, so a plane created by an older version keeps the
	// isolation it was born with; Overcast recreates it when nothing is
	// attached, which leaves this empty. Empty is the normal case.
	Drift string `json:"drift,omitempty"`
}

// InternalDecision is a resolved isolation choice and the reason for it.
//
// The reason is the entire point. The isolation of the control plane changes
// whether a function in a gateway-less VPC can reach the internet, and before
// #1564 nothing — not `docker network inspect`, not the logs — said which way
// it had gone or why.
type InternalDecision struct {
	// Internal is the answer applied to the network.
	Internal bool

	// Reason is a short phrase naming what decided it, safe to print: the
	// setting that pinned it ("OVERCAST_CONTROL_PLANE_INTERNAL=true"), or what
	// the default resolved to and why.
	Reason string

	// Warnings are consequences of this decision the operator has to be told
	// without having to ask — logged at WARN by Probe, once, at startup.
	//
	// Isolating the control plane is the kind of choice whose cost lands a long
	// way from its cause: a function that cannot reach an external API fails
	// minutes later, inside somebody's application code, as ENETUNREACH. The
	// moment of decision is the only place that knows it is coming.
	Warnings []string
}

// NetworkSpec is one network the supervisor ensures at startup.
type NetworkSpec struct {
	// Name is the Docker network name.
	Name string

	// Internal cuts the network off from the wider network — no egress, no
	// route to anything Docker did not put on it.
	//
	// It matters for the control plane specifically. A VPC with no internet
	// gateway is created `--internal` to model a private subnet, but a
	// container on it is also on the control plane, so if *that* has egress the
	// VPC's isolation is defeated and the flag is decoration.
	//
	// Ignored when InternalMode is set.
	Internal bool

	// InternalMode decides Internal dynamically, using the Docker client Probe
	// has just dialled and verified — the seam a caller needs when the right
	// answer depends on facts about the daemon (a native Linux kernel vs. Docker
	// Desktop's VM) that are not knowable until a client exists to ask, and
	// which may not be knowable at all from a static config value computed
	// before Probe runs. Takes precedence over Internal when set.
	//
	// It returns a reason as well as an answer, because a caller that has to
	// choose dynamically is exactly the caller whose answer nobody can predict
	// from the outside — see InternalDecision.
	//
	// Called once per spec, after the client is confirmed available and before
	// the network is created — the one point in this function where the
	// client exists but no ordering has yet committed to an answer.
	InternalMode func(ctx context.Context, dc *Client) InternalDecision
}

// Probe creates a Docker client, verifies connectivity with retries, and
// ensures the given networks exist. This is the common bootstrap pattern
// shared by every service that runs containers.
//
// A network that already exists with a different Internal setting is
// recreated when nothing is attached to it, and otherwise left alone with a
// loud warning naming the exact fix — see reconcileInternalDrift.
//
// Returns nil with a logged warning (not an error) when Docker is unreachable
// — callers degrade gracefully (metadata ops work, container ops return errors).
func Probe(socketPath string, networks []NetworkSpec, logger *zap.Logger) (*ProbeResult, error) {
	dc := NewClient(socketPath, logger)

	// Retry briefly — when running inside a devcontainer the socket is
	// bind-mounted from the host and may not be responsive immediately.
	available := false
	for attempt := 1; attempt <= 5; attempt++ {
		if dc.Available(2 * time.Second) {
			available = true
			break
		}
		if attempt < 5 {
			logger.Debug("Docker not yet available, retrying",
				zap.Int("attempt", attempt), zap.String("socket", socketPath))
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}

	if !available {
		return nil, fmt.Errorf("docker not available at %s after 5 attempts", socketPath)
	}

	logger.Info("Docker available", zap.String("socket", socketPath))

	// Create the planes (idempotent). Doing it here, before any client is
	// handed to a service, is what lets services create containers straight
	// onto a network without each re-ensuring it first.
	statuses := make([]NetworkStatus, 0, len(networks))
	for _, spec := range networks {
		if spec.Name == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		internal := spec.Internal
		reason := ""
		if spec.InternalMode != nil {
			decision := spec.InternalMode(ctx, dc)
			internal, reason = decision.Internal, decision.Reason
			// Said once, at info, on every startup. The report that asked for
			// this (#1564) had two engineers on one pinned version get
			// different answers with nothing anywhere saying which they got.
			logger.Info("control plane network isolation",
				zap.String("network", spec.Name),
				zap.Bool("internal", internal),
				zap.String("reason", reason))
			for _, warning := range decision.Warnings {
				logger.Warn(warning, zap.String("network", spec.Name))
			}
		}
		_, err := dc.CreateNetworkWithOptions(ctx, CreateNetworkOptions{
			Name:     spec.Name,
			Internal: internal,
		})
		effective, drift := internal, ""
		if err == nil {
			effective, drift = reconcileInternalDrift(ctx, dc, spec.Name, internal, logger)
		}
		cancel()
		if err != nil {
			return nil, fmt.Errorf("create network %s: %w", spec.Name, err)
		}
		statuses = append(statuses, NetworkStatus{
			Name:     spec.Name,
			Internal: effective,
			Reason:   reason,
			Drift:    drift,
		})
	}

	return &ProbeResult{Client: dc, Networks: statuses}, nil
}

// reconcileInternalDrift handles a pre-existing network whose Internal flag
// does not match what this run asked for, which CreateNetworkWithOptions
// silently tolerates because it treats "already exists" as success.
//
// Docker never retroactively applies `--internal` to a network that already
// exists, so a plane created by an older Overcast keeps the isolation it was
// born with — forever, and invisibly. That is the whole of #1564: two machines
// on one pinned version behaved differently because one of them had a network
// from before alpha.37 and had simply never recreated it.
//
// So the drift is closed rather than merely reported, but only when closing it
// is free: a network with nothing attached is removed and recreated, because
// there is by definition no connection to sever. A network that still has
// containers on it is left exactly as it was, and warned about at Warn with
// the two commands that fix it — recreating it there would drop every attached
// container off the plane mid-run, which is a worse startup than an isolation
// property one command away from correct.
//
// wantInternal is the resolved value — spec.Internal after InternalMode has
// had its say — so a plane whose isolation depends on the daemon probe is
// compared against what was actually decided.
//
// Returns the isolation the network actually ends up with, and a short
// description of the drift when it survived (for NetworkStatus.Drift). A
// repaired or absent drift returns wantInternal and "".
func reconcileInternalDrift(ctx context.Context, dc *Client, name string, wantInternal bool, logger *zap.Logger) (bool, string) {
	info, err := dc.InspectNetwork(ctx, name)
	if err != nil || info == nil {
		// Nothing to compare against. Report the ask rather than inventing a
		// drift out of a failed inspect.
		return wantInternal, ""
	}
	if info.Internal == wantInternal {
		return wantInternal, ""
	}

	if len(info.Containers) == 0 {
		if rmErr := dc.RemoveNetwork(ctx, name); rmErr == nil {
			if _, createErr := dc.CreateNetworkWithOptions(ctx, CreateNetworkOptions{
				Name:     name,
				Internal: wantInternal,
			}); createErr == nil {
				logger.Info("recreated Docker network to apply its isolation setting — "+
					"it predated this configuration and had nothing attached",
					zap.String("network", name),
					zap.Bool("internal_was", info.Internal),
					zap.Bool("internal_now", wantInternal))
				return wantInternal, ""
			}
		}
		// Fall through to the warning: something else raced us to the network,
		// and saying so is better than pretending the isolation is what we
		// asked for.
	}

	logger.Warn("Docker network predates this configuration and its isolation differs — "+
		"containers are attached to it, so it was left as it is. Docker cannot change this in "+
		"place: stop what is attached, run `docker network rm "+name+"`, and restart Overcast, "+
		"which recreates it. Set OVERCAST_CONTROL_PLANE_INTERNAL to pin the answer you want",
		zap.String("network", name),
		zap.Bool("internal_now", info.Internal),
		zap.Bool("internal_wanted", wantInternal),
		zap.Int("attached_containers", len(info.Containers)))
	return info.Internal, fmt.Sprintf(
		"network predates this configuration: internal=%t, wanted internal=%t; %d container(s) attached",
		info.Internal, wantInternal, len(info.Containers))
}

// RemoveLegacyNetworks drops the pre-two-plane per-service networks. Networks
// that still have containers attached are left in place — Docker refuses the
// removal and the refusal is logged at debug, since an adopted container is a
// good reason to keep one and not a fault.
//
// keep names the planes currently in use, which must survive even when one of
// them was configured to a legacy name: OVERCAST_NETWORK=overcast_eks is a
// plausible migration setting, and without this the plane created moments
// earlier would be removed and every later attach would fail.
//
// Each name is inspected before the attempt, because RemoveNetwork treats a
// missing network as success: without the check, a fresh installation that
// never had these would report removing all seven on every startup.
func RemoveLegacyNetworks(ctx context.Context, dc *Client, logger *zap.Logger, keep ...string) {
	if dc == nil {
		return
	}
	inUse := make(map[string]struct{}, len(keep))
	for _, name := range keep {
		inUse[name] = struct{}{}
	}
	for _, name := range legacyNetworks {
		if _, ok := inUse[name]; ok {
			continue
		}
		if _, err := dc.InspectNetwork(ctx, name); err != nil {
			continue // absent, which is the normal case
		}
		if err := dc.RemoveNetwork(ctx, name); err != nil {
			logger.Debug("legacy network retained — still in use",
				zap.String("network", name), zap.Error(err))
			continue
		}
		logger.Info("removed legacy per-service Docker network",
			zap.String("network", name))
	}
}
