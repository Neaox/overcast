// Package containerendpoint keeps AWS resource URLs dialable from inside the
// containers Overcast starts — Lambda functions, ECS tasks — which are
// siblings of the Overcast container rather than children of it.
//
// Why this is needed at all: several AWS SDKs resolve a service endpoint from
// a resource URL instead of from client configuration. SQS is the sharp edge —
// @aws-sdk/middleware-sdk-sqs replaces the resolved endpoint with the QueueUrl's
// origin whenever the two differ and the client was not constructed with an
// explicit `endpoint`, and AWS_ENDPOINT_URL does not count because it resolves
// through the endpoint ruleset's Endpoint parameter, never as config.endpoint.
// .NET and Java v1 go further and use the queue URL as the request URI. So
// setting AWS_ENDPOINT_URL on the container is not sufficient: a queue URL
// carrying "localhost:4566" sends the container's SQS client to the container's
// own loopback.
//
// Two mechanisms cover the two ways a URL reaches a container:
//
//   - Minted at runtime by the container itself (CreateQueue, GetQueueUrl,
//     ListQueues) — handled server-side, see internal/middleware.
//   - Baked into the container environment by a host-side deploy (CDK/
//     CloudFormation writing queue.queueUrl into env) — handled here, by
//     Mapper.RewriteURLs for loopback origins and Mapper.ExtraHosts for
//     split-horizon hostnames.
package containerendpoint

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Neaox/overcast/internal/config"
)

// splitHorizonHostnames resolve to 127.0.0.1 in public DNS, so a URL built on
// one of them works unmodified from the host. Inside containers the same names
// are remapped to Overcast's address through /etc/hosts (Docker's --add-host),
// which takes precedence over DNS in both glibc and musl. That makes a single
// URL dialable from both sides of the container boundary — the trick
// LocalStack uses with localhost.localstack.cloud, which is included here so
// URLs carried over from a LocalStack setup keep working.
//
// Extend per-deployment with OVERCAST_SPLIT_HORIZON_HOSTS rather than editing
// this list.
var splitHorizonHostnames = config.WildcardDNSDomains

// dockerHostGateway is Docker's magic --add-host target resolving to the host's
// gateway address, used when Overcast runs on the host rather than in a container.
const dockerHostGateway = "host-gateway"

// loopbackHostnames are the origins that are guaranteed wrong inside a sibling
// container: they resolve to the container itself. URLs on any other host are
// left untouched, since they may legitimately resolve in the container's
// network (a compose service name, a split-horizon domain, a third-party API).
var loopbackHostnames = []string{"localhost", "127.0.0.1", "0.0.0.0", "::1", "[::1]"}

// Mapper adapts URLs and hostnames for one container network, given the
// endpoint containers on that network use to reach Overcast.
type Mapper struct {
	cfg      *config.Config
	endpoint string
	// publishedPort is the host port Overcast's API is reachable on when that
	// differs from cfg.Port. Zero when they are the same or it is unknown.
	publishedPort int
}

// New returns a Mapper for containers that reach Overcast at endpoint (an
// origin such as "http://172.18.0.1:4566" — see Resolve). A Mapper with an
// empty endpoint is inert rather than invalid: it leaves URLs alone and
// produces no /etc/hosts entries, so a caller that could not resolve an
// address still starts containers with the user's environment intact.
func New(cfg *config.Config, endpoint string) *Mapper {
	return &Mapper{cfg: cfg, endpoint: endpoint}
}

// WithPublishedPort records the host port Overcast's API is published on, so
// RewriteURLs also recognises URLs a host-side caller minted against it.
//
// Needed because the two ports differ whenever Overcast's container remaps its
// API port (`docker run -p 4580:4566`): Overcast listens on 4566, but a
// host-side `cdk deploy` reaches it on 4580 and bakes queue URLs carrying that
// port into function and task environment. Matching only cfg.Port leaves those
// untouched and the container dials its own loopback on a dead port.
//
// Pass 0 when the mapping is unknown — a native binary, or a container with no
// Docker socket to ask — and matching is unchanged. See PublishedPort.
func (m *Mapper) WithPublishedPort(port int) *Mapper {
	if m == nil {
		return nil
	}
	m.publishedPort = port
	return m
}

// rewritePorts returns the ports whose loopback origins belong to Overcast:
// the port it listens on, plus the published port when that differs.
func (m *Mapper) rewritePorts() []int {
	ports := []int{m.cfg.Port}
	if m.publishedPort > 0 && m.publishedPort != m.cfg.Port {
		ports = append(ports, m.publishedPort)
	}
	return ports
}

// Endpoint returns the origin containers use to reach Overcast, suitable for
// AWS_ENDPOINT_URL. Empty when no address could be resolved.
func (m *Mapper) Endpoint() string {
	if m == nil {
		return ""
	}
	return m.endpoint
}

// ExtraHosts returns Docker --add-host entries ("name:target") that point every
// hostname Overcast might have minted a URL under at Overcast itself. Names
// that cannot usefully be shadowed — "localhost" (the container needs its own
// loopback) and bare IPs (already routable or already wrong) — are skipped.
func (m *Mapper) ExtraHosts() []string {
	if m == nil {
		return nil
	}
	target := overcastHostAddress(m.endpoint)
	if target == "" {
		return nil
	}

	names := slices.Clone(splitHorizonHostnames)
	if m.cfg != nil {
		names = append(names, m.cfg.SplitHorizonHosts...)
		names = append(names, m.cfg.Hostname)
	}
	// The endpoint's own hostname, when it is a name rather than an IP. That
	// only happens on the host.docker.internal fallback, and the entry is what
	// makes the fallback work at all off Docker Desktop: Docker Desktop
	// synthesises the name, native Linux does not resolve it without this.
	if u, err := url.Parse(m.endpoint); err == nil {
		names = append(names, u.Hostname())
	}

	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || net.ParseIP(name) != nil || slices.Contains(loopbackHostnames, strings.ToLower(name)) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name+":"+target)
	}
	return out
}

// overcastHostAddress returns the /etc/hosts target for Overcast's endpoint: its
// IP when Overcast runs in a container on the container network, or Docker's
// host-gateway when it runs on the host (where the endpoint is a name the
// container's resolver would otherwise have to look up itself).
func overcastHostAddress(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	if net.ParseIP(host) != nil {
		return host
	}
	return dockerHostGateway
}

// RewriteURLs re-points Overcast URLs that a host-side caller minted —
// http://localhost:<port>/... and friends — at the endpoint the container can
// reach. Substring replacement rather than whole-value URL parsing, because
// deploy tools pass URLs inside JSON blobs and comma-separated lists as often
// as they pass them bare.
//
// Only loopback origins on a port that is Overcast's own — the port it listens
// on, or the host port it is published on (see WithPublishedPort) — are
// rewritten. Anything else is left as the user set it.
func (m *Mapper) RewriteURLs(value string) string {
	if m == nil || value == "" || m.cfg == nil || m.cfg.Port <= 0 || m.endpoint == "" {
		return value
	}
	if !strings.Contains(value, "//") {
		return value
	}
	for _, p := range m.rewritePorts() {
		port := strconv.Itoa(p)
		for _, scheme := range []string{"http", "https"} {
			for _, host := range loopbackHostnames {
				origin := fmt.Sprintf("%s://%s:%s", scheme, host, port)
				if strings.Contains(value, origin) {
					value = strings.ReplaceAll(value, origin, m.endpoint)
				}
			}
		}
	}
	return value
}
