package dataplane

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
)

// Guard answers the DNS server's question — should this name be answered with
// Overcast's address, or refused because it names a container the caller cannot
// reach — by reading Docker rather than by matching the name against a pattern.
//
// # Why not match the name
//
// The obvious implementation recognises the shapes Overcast mints
// (`{id}.{region}.rds.{base}`, `{id}.{region}.cfg.{base}`, …) and refuses any
// that it cannot place. It is wrong, and it breaks S3.
//
// Those labels are ordinary words in a bucket name, which is why
// docs/networking/host-routing.md declines to register `rds`, `cache`, `kafka`
// and `es` as
// host-routed labels: a bucket named `my.rds` is addressed virtual-hosted as
// `my.rds.localhost`, matches the shape exactly, and is not a data-plane
// endpoint at all. Refusing it would break the bucket.
//
// So a refusal requires positive identification: some container is advertising
// this exact alias, and the caller is on none of the networks carrying it. Both
// halves are facts read from the daemon. Nothing advertises `my.rds.localhost`,
// so a bucket can never be refused.
//
// # Why this is affordable on the query path
//
// A container resolves through Docker's embedded resolver, which answers from
// network aliases and forwards only what it cannot answer. A data-plane name
// that reaches Overcast's resolver has therefore *already* missed — the
// connection it belongs to is broken either way. The daemon calls here are paid
// by failures, never by working lookups.
// surveyor is the slice of *docker.Client the guard reads. An interface rather
// than the concrete client so the decision logic — which is the whole of the
// risk here — is testable without a daemon.
type surveyor interface {
	ListContainers(ctx context.Context, service string) ([]docker.ContainerSummary, error)
	InspectContainer(ctx context.Context, id string) (*docker.ContainerInspect, error)
}

type Guard struct {
	docker surveyor
	log    *zap.Logger

	mu    sync.Mutex
	cache map[string]cachedVerdict
	clock func() time.Time
}

// verdictTTL bounds how long a refusal is reused. A client that cannot resolve
// a name retries hard, and without this one misconfiguration would become a
// stream of daemon calls. It is short because the answer changes the moment
// somebody attaches the container correctly, and a stale refusal would outlive
// the fix.
const verdictTTL = 3 * time.Second

type cachedVerdict struct {
	refuse bool
	at     time.Time
}

// NewGuard returns a Guard reading from dc. A nil client makes it inert, which
// is what a metadata-only deployment wants: with no daemon there are no
// containers, so nothing can be positively identified and nothing is refused.
//
// The nil check is on the concrete type deliberately. A nil *docker.Client
// stored in the surveyor interface would be a non-nil interface holding a nil
// pointer, and every call on it would panic rather than degrade.
func NewGuard(dc *docker.Client, log *zap.Logger) *Guard {
	g := &Guard{
		log:   log,
		cache: make(map[string]cachedVerdict),
		clock: time.Now,
	}
	if dc != nil {
		g.docker = dc
	}
	return g
}

// Refuse implements dns.Guard.
func (g *Guard) Refuse(ctx context.Context, name string, peer netip.Addr) bool {
	if g == nil || g.docker == nil || name == "" || !peer.IsValid() {
		return false
	}

	key := strings.ToLower(name) + "\x00" + peer.String()
	if verdict, ok := g.cached(key); ok {
		return verdict
	}
	refuse := g.evaluate(ctx, name, peer)
	g.remember(key, refuse)
	return refuse
}

func (g *Guard) cached(key string) (bool, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.cache[key]
	if !ok || g.clock().Sub(v.at) > verdictTTL {
		return false, false
	}
	return v.refuse, true
}

func (g *Guard) remember(key string, refuse bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Bounded by clearing rather than by eviction order: the set is small (one
	// entry per broken name/caller pair, all of them short-lived) and a plain
	// map with a size cap keeps this off the critical path.
	if len(g.cache) >= maxGuardCache {
		clear(g.cache)
	}
	g.cache[key] = cachedVerdict{refuse: refuse, at: g.clock()}
}

const maxGuardCache = 512

// evaluate does the daemon work: find who is advertising the name, and whether
// the caller shares a network with any of them.
//
// Both sides must be positively identified. Failing to find the *caller* is not
// evidence that it cannot reach the target — ListContainers only returns
// Overcast-managed containers, so a user's own compose service joined to the
// shared plane is invisible here — and refusing on an absence would break
// exactly the setup the shared plane exists to support.
func (g *Guard) evaluate(ctx context.Context, name string, peer netip.Addr) bool {
	containers, err := g.docker.ListContainers(ctx, "")
	if err != nil {
		return false // cannot identify anything; answer as before
	}

	advertisedOn, callerOn, target, caller := g.survey(ctx, containers, name, peer)

	// An ordinary name this zone owns — a bucket, an API Gateway host — or a
	// resource whose container is gone. Indistinguishable from here.
	if len(advertisedOn) == 0 {
		return false
	}
	// The caller is not a container Overcast started, so its attachments are
	// unknown rather than known-disjoint.
	if len(callerOn) == 0 {
		return false
	}
	for network := range callerOn {
		if advertisedOn[network] {
			return false // they share a plane; the miss is not about placement
		}
	}

	g.log.Warn("refusing a data-plane name the caller cannot reach — "+
		"the container is on a network this caller is not attached to, so answering "+
		"with Overcast's address would connect it to the emulator on the engine's port and hang",
		zap.String("name", name),
		zap.String("target", target),
		zap.String("caller", caller),
		zap.Strings("target_networks", keys(advertisedOn)),
		zap.Strings("caller_networks", keys(callerOn)))
	return true
}

// survey inspects each container once and answers both questions from the same
// pass: which networks advertise name, and which the caller is attached to.
// Inspecting per question instead would double the daemon calls for no gain.
func (g *Guard) survey(
	ctx context.Context, containers []docker.ContainerSummary, name string, peer netip.Addr,
) (advertisedOn, callerOn map[string]bool, target, caller string) {
	advertisedOn = make(map[string]bool)
	want := peer.String()

	for i := range containers {
		info, err := g.docker.InspectContainer(ctx, containers[i].ID)
		if err != nil || info == nil {
			continue
		}
		holdsPeer := false
		on := make(map[string]bool, len(info.NetworkSettings.Networks))
		for network, endpoint := range info.NetworkSettings.Networks {
			on[network] = true
			if endpoint.IPAddress == want {
				holdsPeer = true
			}
			for _, alias := range endpoint.Aliases {
				if strings.EqualFold(alias, name) {
					advertisedOn[network] = true
					if target == "" {
						target = containerLabel(&containers[i])
					}
				}
			}
		}
		if holdsPeer && callerOn == nil {
			callerOn, caller = on, containerLabel(&containers[i])
		}
	}
	return advertisedOn, callerOn, target, caller
}

// containerLabel names a container the way somebody reading the log would look
// for it: the resource it belongs to when Overcast started it, else its name.
func containerLabel(c *docker.ContainerSummary) string {
	if rid := c.ResourceID(); rid != "" {
		if svc := c.Service(); svc != "" {
			return svc + " " + rid
		}
		return rid
	}
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
