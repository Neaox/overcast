package route53

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/google/uuid"
)

// normalizeDNSName lowercases a domain name and ensures a trailing dot,
// matching how Route 53 canonicalises names on the wire.
func normalizeDNSName(name string) string {
	name = strings.ToLower(name)
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	return name
}

// validDNSName reports whether a (already normalised) domain name is
// acceptable to Route 53: dot-separated labels of 1-63 characters drawn from
// letters, digits, hyphen, underscore, and the wildcard/escape characters
// Route 53 permits, with a total length of at most 255 characters.
func validDNSName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(name, "."), ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for _, c := range label {
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			case c == '-' || c == '_' || c == '*' || c == '\\':
			default:
				return false
			}
		}
	}
	return true
}

// dnsSortKey returns the key Route 53 orders names by: the domain's labels
// reversed ("www.example.com." → "com.example.www."), so that a zone's apex
// sorts before its children and siblings group by parent domain.
func dnsSortKey(name string) string {
	labels := strings.Split(strings.TrimSuffix(name, "."), ".")
	for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
		labels[i], labels[j] = labels[j], labels[i]
	}
	return strings.Join(labels, ".") + "."
}

// inZone reports whether a record name belongs to the given zone name.
// Both arguments must be normalised.
func inZone(recordName, zoneName string) bool {
	return recordName == zoneName || strings.HasSuffix(recordName, "."+zoneName)
}

// newZoneID returns a fresh AWS-style hosted zone ID (bare form, no
// /hostedzone/ prefix), e.g. Z0AB12CD34EF56GH78IJ9.
func newZoneID() string { return "Z" + randomIDSuffix(20) }

// newChangeID returns a fresh AWS-style change ID (bare form).
func newChangeID() string { return "C" + randomIDSuffix(20) }

// randomIDSuffix returns n characters of uppercase hex randomness.
func randomIDSuffix(n int) string {
	return strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:n]
}

// awsdnsTLDs mirrors the four TLDs AWS spreads a delegation set across.
var awsdnsTLDs = [4]string{"com", "net", "org", "co.uk"}

// deriveNameServers returns the four delegation-set name servers for a zone,
// derived deterministically from the zone ID so the set is stable across
// requests and process restarts without needing a migration for zones
// persisted before name servers were stored.
func deriveNameServers(bareZoneID string) []string {
	h := fnv.New64a()
	h.Write([]byte(bareZoneID)) //nolint:errcheck // hash.Hash Write never fails
	seed := h.Sum64()
	out := make([]string, len(awsdnsTLDs))
	for i, tld := range awsdnsTLDs {
		// Spread each server across its own numeric block, as AWS does.
		host := seed%512 + uint64(i)*512
		pool := seed%64 + uint64(i)*64
		out[i] = fmt.Sprintf("ns-%d.awsdns-%02d.%s", host, pool, tld)
		seed = seed>>7 | seed<<57
	}
	return out
}
