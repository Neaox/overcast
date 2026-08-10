package docker

// registry.go — asking the daemon about a registry, and reading its answers.
//
// Push, pull and login are all performed by the Docker daemon, never by the
// client that requested them, so the only vantage from which a registry's
// address can be judged is the daemon's own. DistributionInspect is that
// judgement made cheap: it has the daemon contact the registry without
// transferring an image, so "the daemon can reach this registry" becomes a
// startup fact instead of a task-launch failure.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DistributionInspect makes the daemon contact the registry serving ref and
// describe the image — GET /distribution/{ref}/json. A nil error means the
// registry answered with a manifest. An error carries the daemon's message,
// which distinguishes a registry that answered and refused (unknown manifest,
// authorization required) from one that could not be reached at all; classify
// with RegistryUnreachable.
func (d *Client) DistributionInspect(ctx context.Context, ref string) error {
	resp, err := d.doRequest(ctx, http.MethodGet, "/v1.45/distribution/"+url.PathEscape(ref)+"/json", nil)
	if err != nil {
		return fmt.Errorf("distribution inspect %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("distribution inspect %s: status %d: %s", ref, resp.StatusCode, string(body))
}

// RegistryUnreachable reports whether err means a registry could not be
// contacted — a transport failure or a rate-limit rejection — rather than a
// registry that answered and refused (unknown manifest, unauthorized, denied).
//
// The distinction decides opposite verdicts in the places it is used: a test
// skips on an unreachable registry and fails on a refusal; the ECR startup
// probe warns on unreachable and stays quiet on a refusal, because a refusal
// proves the network path this emulator is responsible for.
//
// Deliberately conservative: an error matching neither list is treated as a
// refusal, because the cost of wrongly reporting "unreachable" is a masked
// real failure while the cost of the reverse is one visible complaint.
func RegistryUnreachable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())

	// A refusal from a registry that did answer is never a reachability
	// problem, and some of these carry wording that would otherwise match the
	// transport list below.
	for _, answered := range []string{
		"manifest unknown",
		"manifest for",
		"not found",
		"unauthorized",
		"authentication required",
		"authorization failed",
		"denied",
		"no such image",
		"invalid reference",
	} {
		if strings.Contains(msg, answered) {
			return false
		}
	}

	for _, transport := range []string{
		"context deadline exceeded",
		"client.timeout exceeded",
		"i/o timeout",
		"tls handshake timeout",
		"no such host",
		"connection refused",
		"connection reset",
		"network is unreachable",
		"temporary failure in name resolution",
		"toomanyrequests",
		"too many requests",
		"rate limit",
		"503 service unavailable",
		"502 bad gateway",
	} {
		if strings.Contains(msg, transport) {
			return true
		}
	}
	return false
}
