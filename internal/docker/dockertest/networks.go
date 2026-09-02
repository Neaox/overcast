// Package dockertest is the Docker housekeeping the test suites share: removing
// the networks a test minted for itself, and sweeping the ones a test process
// that died before its cleanups could run left behind.
//
// Only test code and scripts/docker-clean-test-networks.go import it. Nothing
// under cmd/overcast links it, so it ships nowhere.
//
// A leaked network is worse than untidy. A daemon subnets its networks out of a
// finite address pool — Docker Desktop's defaults stretch to roughly thirty —
// so a suite that mints a pair per test server and loses a few a day exhausts
// it. From then on every `docker network create` fails, the emulator reports
// that as "Docker not available", and every container test fails for a reason
// that has nothing to do with the code under test.
package dockertest

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/overcast-sh/overcast/internal/docker"
)

// Logf receives one line per action taken or declined. testing.T's Logf fits;
// the sweep script hands in a printer.
type Logf func(format string, args ...any)

// testNetworkName is the whole of what the suites mint: helpers.WithECSDocker's
// overcast_ecs_test_<nanotime>, the RDS and EFS data-plane tests'
// overcast_<suite>_test_<id>, and the _control twin of each.
//
// Anchored at both ends, with the _test_ segment between a suite name and a
// per-run id — a nanotime or a hex suffix, eight characters or more — so a
// shared instance's planes cannot match: overcast and overcast_control carry
// no such segment, and neither does whatever OVERCAST_NETWORK names on a
// developer's or another agent's instance. Nor do EFS's fixed-name
// overcast_efs_test planes: those are recreated by every run rather than
// minted per run, and "control" is not an id. A suite that mints a new shape
// extends this, and does not give a long-lived instance a name of this shape —
// the sweep would take its planes the moment they were idle.
//
// The `-vpc-<id>` tail is EC2's per-VPC network under a test-scoped
// OVERCAST_NETWORK (config.VPCNetwork). It is derived from the plane name
// rather than minted, so a suite that sets a per-run OVERCAST_NETWORK gets
// these for free and had no way to name them into the pattern; four of them
// survived a sweep before this. A long-lived instance is still safe: its
// networks are `overcast-vpc-…` with no `_test_` segment at all.
var testNetworkName = regexp.MustCompile(
	`^overcast(?:_[a-z0-9]+)+_test_[0-9a-f]{8,}(?:_control|-vpc-[a-z0-9-]+)?$`)

// IsTestNetwork reports whether name is one a test suite minted for itself,
// and so one the sweep may consider. It answers no for every shared plane.
func IsTestNetwork(name string) bool { return testNetworkName.MatchString(name) }

// RemoveOwned removes networks the caller created for one test, and is
// therefore entitled to empty first. For each name, in the order given:
//
//   - a network the daemon does not know is skipped silently — it was never
//     created, or an earlier cleanup already took it;
//   - every container still on it is evicted. An Overcast-managed one is
//     force-removed: the service that started it has already stopped, and
//     "stopped" is not "removed" while AutoRemove is still working. Anything
//     else — the dev container the suite itself runs in, when it does — is only
//     disconnected, never removed;
//   - the removal is retried while the daemon reports active endpoints, which
//     it does for a moment after a container is gone, until ctx expires.
//
// Nothing here fails the test: by the time it runs the assertions have been
// made. But everything off the quiet path is reported through logf, naming the
// sweep that finishes the job. The helper this replaced swallowed every error,
// which is how a machine came to hold twenty-odd empty networks with nothing in
// any log to say what had refused them.
func RemoveOwned(ctx context.Context, dc *docker.Client, names []string, logf Logf) {
	for _, name := range names {
		if name == "" {
			continue
		}
		removeOwned(ctx, dc, name, logf)
	}
}

func removeOwned(ctx context.Context, dc *docker.Client, name string, logf Logf) {
	info, err := dc.InspectNetwork(ctx, name)
	switch {
	case docker.IsNotFound(err):
		return
	case err != nil:
		// Try the removal anyway: an inspect that failed for a transient reason
		// says nothing about whether the network is empty.
		logf("cleanup: inspect network %s: %v", name, err)
	default:
		for id, c := range info.Containers {
			evict(ctx, dc, name, id, c.Name, logf)
		}
	}
	if err := removeWithRetry(ctx, dc, name); err != nil && !docker.IsNotFound(err) {
		logf("cleanup: network %s was not removed: %v "+
			"(`make docker-clean-test-networks` sweeps it once it is empty)", name, err)
	}
}

// evict takes one container off a network the caller is about to remove.
func evict(ctx context.Context, dc *docker.Client, network, id, containerName string, logf Logf) {
	info, err := dc.InspectContainer(ctx, id)
	if err != nil {
		if docker.IsNotFound(err) {
			return // gone between the two inspects, which is the point
		}
		logf("cleanup: inspect container %s on network %s: %v", containerName, network, err)
		return
	}
	if info.Managed() {
		if err := dc.RemoveContainer(ctx, id, true); err != nil && !docker.IsNotFound(err) {
			logf("cleanup: remove container %s still on network %s: %v", containerName, network, err)
			return
		}
		logf("cleanup: network %s still held container %s; removed it", network, containerName)
		return
	}
	if err := dc.DisconnectNetwork(ctx, network, id); err != nil {
		logf("cleanup: disconnect %s (not Overcast's) from network %s: %v", containerName, network, err)
		return
	}
	logf("cleanup: network %s still held %s, which is not Overcast's; disconnected it", network, containerName)
}

// removeWithRetry removes the network, retrying only the daemon's "has active
// endpoints" refusal: an endpoint outlives its container by a moment, because
// the daemon's removal is asynchronous. Any other error is final.
func removeWithRetry(ctx context.Context, dc *docker.Client, name string) error {
	for {
		err := dc.RemoveNetwork(ctx, name)
		if err == nil || !hasActiveEndpoints(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// hasActiveEndpoints reports whether err is the daemon refusing to remove a
// network because a container is still attached. The wording is Docker's own:
// "error while removing network: network <name> id <id> has active endpoints".
func hasActiveEndpoints(err error) bool {
	return err != nil && strings.Contains(err.Error(), "active endpoints")
}

// SweepOptions tunes Sweep.
type SweepOptions struct {
	// MinAge keeps a network created more recently than this, even when
	// nothing is on it. A test server's planes are empty between their creation
	// and the first container, and one `go test` package cannot tell a
	// neighbour's networks from a dead run's by name alone. An age longer than
	// any test package may run — the Makefile caps test-integration at 600 s —
	// is what keeps the sweep from pulling a live network out from under a
	// suite that is still using it. Zero disables the guard.
	MinAge time.Duration
	// DryRun reports what would be removed without removing it.
	DryRun bool
	// Now overrides the clock, for tests. Nil means time.Now.
	Now func() time.Time
}

// SweepResult names what Sweep removed and what it declined to.
type SweepResult struct {
	Removed  []string
	Retained []string
}

// Sweep removes every per-test network — IsTestNetwork decides which — that
// has no container on it and is older than opts.MinAge. Shared planes are never
// candidates, whatever their state. A candidate with anything attached is
// retained whatever its age, and so is one the daemon refuses. Every removal
// and every retention goes to logf with its reason, and both lists come back.
//
// The daemon's name filter is a substring match, so the listing is wider than
// the candidates; IsTestNetwork narrows it to exactly the shape the suites mint.
func Sweep(ctx context.Context, dc *docker.Client, opts SweepOptions, logf Logf) (SweepResult, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	nets, err := dc.ListNetworksNamed(ctx, "overcast_")
	if err != nil {
		return SweepResult{}, err
	}
	sort.Slice(nets, func(i, j int) bool { return nets[i].Name < nets[j].Name })

	var res SweepResult
	retain := func(name, format string, args ...any) {
		logf("sweep: retained "+name+": "+format, args...)
		res.Retained = append(res.Retained, name)
	}
	for _, n := range nets {
		if !IsTestNetwork(n.Name) {
			continue
		}
		info, err := dc.InspectNetwork(ctx, n.Name)
		if err != nil {
			if docker.IsNotFound(err) {
				continue // removed between list and inspect
			}
			retain(n.Name, "inspect: %v", err)
			continue
		}
		if len(info.Containers) > 0 {
			retain(n.Name, "%d container(s) attached: %s", len(info.Containers), containerNames(info))
			continue
		}
		age := now().Sub(info.Created).Round(time.Second)
		if opts.MinAge > 0 && !info.Created.IsZero() && age < opts.MinAge {
			retain(n.Name, "created %s ago, younger than %s — a test package may still own it "+
				"(pass a shorter -min-age to sweep it)", age, opts.MinAge)
			continue
		}
		if opts.DryRun {
			logf("sweep: would remove %s (empty, created %s ago)", n.Name, age)
			res.Removed = append(res.Removed, n.Name)
			continue
		}
		if err := dc.RemoveNetwork(ctx, n.Name); err != nil {
			if docker.IsNotFound(err) {
				continue
			}
			retain(n.Name, "%v", err)
			continue
		}
		logf("sweep: removed %s (empty, created %s ago)", n.Name, age)
		res.Removed = append(res.Removed, n.Name)
	}
	return res, nil
}

func containerNames(info *docker.NetworkInspect) string {
	names := make([]string, 0, len(info.Containers))
	for id, c := range info.Containers {
		if c.Name != "" {
			names = append(names, c.Name)
		} else {
			names = append(names, id)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
