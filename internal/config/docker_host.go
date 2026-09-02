package config

import (
	"os"
	"strings"
)

// docker_host.go resolves the Docker endpoint Overcast dials when
// LAMBDA_DOCKER_SOCKET is not set.
//
// DOCKER_HOST is not an Overcast setting and not a LocalStack one either: it
// is the Docker CLI's own convention, and every Docker-compatible daemon that
// does not live at /var/run/docker.sock tells its users to set it — Colima,
// Rancher Desktop, Podman, rootless Docker, a DinD sidecar in CI. LocalStack
// reads it, so a setup that reaches Docker through it keeps working there and
// used to go dark here: Overcast dialled the platform default, found nothing,
// and every Docker-backed service (Lambda invocations above all) degraded with
// only a startup log line to say why.
//
// LAMBDA_DOCKER_SOCKET still wins when both are set — it is the more specific
// of the two, and the one Overcast documents.

// dockerHostVar is the environment variable name, named once so the resolver
// and the provenance it reports cannot drift.
const dockerHostVar = "DOCKER_HOST"

// resolveDockerEndpoint returns the endpoint LAMBDA_DOCKER_SOCKET defaults to
// when unset, plus provenance for the startup log:
//
//	source      dockerHostVar when DOCKER_HOST supplied the endpoint, else ""
//	unsupported the raw DOCKER_HOST value when it names a transport Overcast
//	            cannot dial, so the caller can say so rather than silently
//	            falling back to a socket the operator has already told us is
//	            the wrong one
func resolveDockerEndpoint() (endpoint, source, unsupported string) {
	raw := strings.TrimSpace(os.Getenv(dockerHostVar))
	if raw == "" {
		return DefaultDockerSocket(), "", ""
	}
	normalised, ok := normaliseDockerHost(raw)
	if !ok {
		return DefaultDockerSocket(), "", raw
	}
	return normalised, dockerHostVar, ""
}

// normaliseDockerHost converts a DOCKER_HOST value into the endpoint form
// internal/docker's dialEndpoint understands: a bare path for a Unix socket,
// or a scheme-prefixed address for everything else.
//
// ok is false for a transport Overcast has no dialer for — ssh:// (which
// needs an SSH client to tunnel the socket) and https:// (which needs the
// client certificate material DOCKER_CERT_PATH points at). Returning false
// rather than guessing keeps the failure loud: the caller warns and falls
// back, instead of dialling a local socket that answers for a different
// daemon than the operator meant.
func normaliseDockerHost(raw string) (string, bool) {
	switch {
	case strings.HasPrefix(raw, "unix://"):
		// The Docker CLI's canonical spelling. dialEndpoint dials a bare
		// path, so the scheme comes off here rather than being taught to
		// every dialer.
		return strings.TrimPrefix(raw, "unix://"), true
	case strings.HasPrefix(raw, "tcp://"), strings.HasPrefix(raw, "npipe://"):
		// Both are dialEndpoint's own input forms already.
		return raw, true
	case strings.HasPrefix(raw, "http://"):
		// Docker treats http:// as a synonym for tcp://; dialEndpoint only
		// knows the latter.
		return "tcp://" + strings.TrimPrefix(raw, "http://"), true
	case strings.HasPrefix(raw, "/"):
		// An absolute path with no scheme. Docker accepts it and some tools
		// emit it, so it costs nothing to accept.
		return raw, true
	default:
		return "", false
	}
}
