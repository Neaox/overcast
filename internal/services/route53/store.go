package route53

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Store namespaces. Route 53 is a global service, so keys carry no region.
const (
	nsZones        = "route53:zones"
	nsRRSets       = "route53:rrsets"
	nsChanges      = "route53:changes"
	nsTags         = "route53:tags"
	nsHealthChecks = "route53:healthchecks"
)

// ─── Persisted types ──────────────────────────────────────────────────────────

// VPC identifies a VPC associated with a private hosted zone.
type VPC struct {
	VPCRegion string `json:"VPCRegion" xml:"VPCRegion"`
	VPCId     string `json:"VPCId" xml:"VPCId"`
}

// HostedZone represents a Route 53 hosted zone.
type HostedZone struct {
	Id              string    `json:"Id"` // AWS path form: /hostedzone/Z...
	Name            string    `json:"Name"`
	CallerReference string    `json:"CallerReference"`
	CreatedAt       time.Time `json:"CreatedAt"`
	Comment         string    `json:"Comment,omitempty"`
	PrivateZone     bool      `json:"PrivateZone"`
	NameServers     []string  `json:"NameServers,omitempty"`
	VPCs            []VPC     `json:"VPCs,omitempty"`
}

// bareID returns the zone ID without the /hostedzone/ prefix.
func (z *HostedZone) bareID() string { return strings.TrimPrefix(z.Id, "/hostedzone/") }

// nameServers returns the zone's delegation-set name servers, deriving a
// deterministic set for records persisted before name servers were stored.
func (z *HostedZone) nameServers() []string {
	if len(z.NameServers) > 0 {
		return z.NameServers
	}
	return deriveNameServers(z.bareID())
}

// GeoLocation mirrors the Route 53 GeoLocation shape on geolocation records.
type GeoLocation struct {
	ContinentCode   string `json:"ContinentCode,omitempty" xml:"ContinentCode,omitempty"`
	CountryCode     string `json:"CountryCode,omitempty" xml:"CountryCode,omitempty"`
	SubdivisionCode string `json:"SubdivisionCode,omitempty" xml:"SubdivisionCode,omitempty"`
}

// ResourceRecordSet represents a DNS record set in a hosted zone.
// Alias fields stay flattened for JSON compatibility with earlier releases.
type ResourceRecordSet struct {
	Name             string       `json:"Name"`
	Type             string       `json:"Type"`
	SetIdentifier    string       `json:"SetIdentifier,omitempty"`
	TTL              int64        `json:"TTL"`
	ResourceRecords  []string     `json:"ResourceRecords"`
	Weight           *int64       `json:"Weight,omitempty"`
	Region           string       `json:"Region,omitempty"`
	Failover         string       `json:"Failover,omitempty"`
	MultiValueAnswer *bool        `json:"MultiValueAnswer,omitempty"`
	GeoLocation      *GeoLocation `json:"GeoLocation,omitempty"`
	HealthCheckId    string       `json:"HealthCheckId,omitempty"`
	// AliasTarget fields
	AliasHostedZoneId         string `json:"AliasHostedZoneId,omitempty"`
	AliasDNSName              string `json:"AliasDNSName,omitempty"`
	AliasEvaluateTargetHealth bool   `json:"AliasEvaluateTargetHealth,omitempty"`
}

// isAlias reports whether the record set is an alias record.
func (rr *ResourceRecordSet) isAlias() bool { return rr.AliasDNSName != "" }

// rrsetKey returns the store key identifying a record set within a zone.
// The set identifier participates only when present, which keeps keys
// written by earlier releases (zone/name/type) addressable.
func rrsetKey(zoneID, name, rrType, setID string) string {
	key := zoneID + "/" + name + "/" + rrType
	if setID != "" {
		key += "/" + setID
	}
	return key
}

// AlarmIdentifier names the CloudWatch alarm backing a CLOUDWATCH_METRIC check.
type AlarmIdentifier struct {
	Region string `json:"Region" xml:"Region"`
	Name   string `json:"Name" xml:"Name"`
}

// HealthCheckConfig carries the health check configuration. The struct is
// shared between the wire format and persistence; field order matches the
// element order AWS emits.
type HealthCheckConfig struct {
	IPAddress                    string           `json:"IPAddress,omitempty" xml:"IPAddress,omitempty"`
	Port                         int              `json:"Port,omitempty" xml:"Port,omitempty"`
	Type                         string           `json:"Type" xml:"Type"`
	ResourcePath                 string           `json:"ResourcePath,omitempty" xml:"ResourcePath,omitempty"`
	FullyQualifiedDomainName     string           `json:"FullyQualifiedDomainName,omitempty" xml:"FullyQualifiedDomainName,omitempty"`
	SearchString                 string           `json:"SearchString,omitempty" xml:"SearchString,omitempty"`
	RequestInterval              int              `json:"RequestInterval,omitempty" xml:"RequestInterval,omitempty"`
	FailureThreshold             int              `json:"FailureThreshold,omitempty" xml:"FailureThreshold,omitempty"`
	MeasureLatency               *bool            `json:"MeasureLatency,omitempty" xml:"MeasureLatency,omitempty"`
	Inverted                     *bool            `json:"Inverted,omitempty" xml:"Inverted,omitempty"`
	Disabled                     *bool            `json:"Disabled,omitempty" xml:"Disabled,omitempty"`
	HealthThreshold              *int             `json:"HealthThreshold,omitempty" xml:"HealthThreshold,omitempty"`
	ChildHealthChecks            []string         `json:"ChildHealthChecks,omitempty" xml:"ChildHealthChecks>ChildHealthCheck,omitempty"`
	EnableSNI                    *bool            `json:"EnableSNI,omitempty" xml:"EnableSNI,omitempty"`
	Regions                      []string         `json:"Regions,omitempty" xml:"Regions>Region,omitempty"`
	AlarmIdentifier              *AlarmIdentifier `json:"AlarmIdentifier,omitempty" xml:"AlarmIdentifier,omitempty"`
	InsufficientDataHealthStatus string           `json:"InsufficientDataHealthStatus,omitempty" xml:"InsufficientDataHealthStatus,omitempty"`
	RoutingControlArn            string           `json:"RoutingControlArn,omitempty" xml:"RoutingControlArn,omitempty"`
}

// HealthCheck represents a Route 53 health check.
type HealthCheck struct {
	Id              string            `json:"Id"`
	CallerReference string            `json:"CallerReference"`
	CreatedAt       time.Time         `json:"CreatedAt"`
	Version         int64             `json:"Version"`
	Config          HealthCheckConfig `json:"Config"`
}

// Tag is a Route 53 resource tag.
type Tag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// Taggable resource types accepted by the tag operations.
const (
	tagResourceHostedZone  = "hostedzone"
	tagResourceHealthCheck = "healthcheck"
)

// tagKey returns the store key for a resource's tag map.
func tagKey(resourceType, bareID string) string { return resourceType + "/" + bareID }

// ─── Zone persistence ─────────────────────────────────────────────────────────

func (s *Service) putZone(ctx context.Context, z *HostedZone) error {
	raw, err := json.Marshal(z)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsZones, z.Id, string(raw))
}

// getZone loads one zone. A malformed persisted record is isolated as
// not-found (with a log) rather than surfacing as an internal error.
func (s *Service) getZone(ctx context.Context, id string) (*HostedZone, bool, error) {
	raw, found, err := s.store.Get(ctx, nsZones, id)
	if err != nil || !found {
		return nil, false, err
	}
	var z HostedZone
	if err := json.Unmarshal([]byte(raw), &z); err != nil {
		log := s.log.WithRecorder(ctx)
		log.Warn("skipping malformed hosted zone record", zap.String("zoneId", id), zap.Error(err))
		return nil, false, nil
	}
	return &z, true, nil
}

// listAllZones returns every hosted zone, skipping malformed records.
func (s *Service) listAllZones(ctx context.Context) ([]*HostedZone, error) {
	pairs, err := s.store.Scan(ctx, nsZones, "")
	if err != nil {
		return nil, err
	}
	out := make([]*HostedZone, 0, len(pairs))
	for _, kv := range pairs {
		var z HostedZone
		if err := json.Unmarshal([]byte(kv.Value), &z); err != nil {
			log := s.log.WithRecorder(ctx)
			log.Warn("skipping malformed hosted zone record", zap.String("key", kv.Key), zap.Error(err))
			continue
		}
		out = append(out, &z)
	}
	return out, nil
}

func (s *Service) deleteZone(ctx context.Context, id string) error {
	return s.store.Delete(ctx, nsZones, id)
}

// ─── Record set persistence ───────────────────────────────────────────────────

func (s *Service) putRRSet(ctx context.Context, zoneID string, rr *ResourceRecordSet) error {
	raw, err := json.Marshal(rr)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsRRSets, rrsetKey(zoneID, rr.Name, rr.Type, rr.SetIdentifier), string(raw))
}

func (s *Service) deleteRRSet(ctx context.Context, zoneID string, rr *ResourceRecordSet) error {
	return s.store.Delete(ctx, nsRRSets, rrsetKey(zoneID, rr.Name, rr.Type, rr.SetIdentifier))
}

// listRRSets returns a zone's record sets in DNS order (labels reversed,
// then type, then set identifier), skipping malformed records.
func (s *Service) listRRSets(ctx context.Context, zoneID string) ([]*ResourceRecordSet, error) {
	pairs, err := s.store.Scan(ctx, nsRRSets, zoneID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]*ResourceRecordSet, 0, len(pairs))
	for _, kv := range pairs {
		var rr ResourceRecordSet
		if err := json.Unmarshal([]byte(kv.Value), &rr); err != nil {
			log := s.log.WithRecorder(ctx)
			log.Warn("skipping malformed record set", zap.String("key", kv.Key), zap.Error(err))
			continue
		}
		out = append(out, &rr)
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := dnsSortKey(out[i].Name), dnsSortKey(out[j].Name)
		if ki != kj {
			return ki < kj
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].SetIdentifier < out[j].SetIdentifier
	})
	return out, nil
}

// deleteZoneRRSets removes every record set belonging to a zone.
func (s *Service) deleteZoneRRSets(ctx context.Context, zoneID string) error {
	pairs, err := s.store.Scan(ctx, nsRRSets, zoneID+"/")
	if err != nil {
		return err
	}
	for _, kv := range pairs {
		if err := s.store.Delete(ctx, nsRRSets, kv.Key); err != nil {
			return err
		}
	}
	return nil
}

// countRRSetsByZone counts record sets for every zone in one scan.
func (s *Service) countRRSetsByZone(ctx context.Context) (map[string]int, error) {
	pairs, err := s.store.Scan(ctx, nsRRSets, "")
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(pairs))
	for _, kv := range pairs {
		// Keys look like /hostedzone/Z123/name/type[/setid]; the zone ID is
		// the first three segments.
		parts := strings.SplitN(kv.Key, "/", 4)
		if len(parts) < 4 {
			continue
		}
		counts["/"+parts[1]+"/"+parts[2]]++
	}
	return counts, nil
}

func (s *Service) countRRSets(ctx context.Context, zoneID string) (int, error) {
	rrs, err := s.listRRSets(ctx, zoneID)
	return len(rrs), err
}

// ─── Change persistence ───────────────────────────────────────────────────────

func (s *Service) putChange(ctx context.Context, changeID string) error {
	return s.store.Set(ctx, nsChanges, changeID, "INSYNC")
}

func (s *Service) getChangeStatus(ctx context.Context, changeID string) (string, bool, error) {
	return s.store.Get(ctx, nsChanges, changeID)
}

// ─── Tag persistence ──────────────────────────────────────────────────────────

func (s *Service) getTags(ctx context.Context, resourceType, bareID string) (map[string]string, error) {
	raw, found, err := s.store.Get(ctx, nsTags, tagKey(resourceType, bareID))
	if err != nil || !found {
		return map[string]string{}, err
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		log := s.log.WithRecorder(ctx)
		log.Warn("skipping malformed tag record", zap.String("resource", tagKey(resourceType, bareID)), zap.Error(err))
		return map[string]string{}, nil
	}
	return tags, nil
}

func (s *Service) putTags(ctx context.Context, resourceType, bareID string, tags map[string]string) error {
	if len(tags) == 0 {
		return s.store.Delete(ctx, nsTags, tagKey(resourceType, bareID))
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsTags, tagKey(resourceType, bareID), string(raw))
}

func (s *Service) deleteTags(ctx context.Context, resourceType, bareID string) error {
	return s.store.Delete(ctx, nsTags, tagKey(resourceType, bareID))
}

// ─── Health check persistence ─────────────────────────────────────────────────

func (s *Service) putHealthCheck(ctx context.Context, hc *HealthCheck) error {
	raw, err := json.Marshal(hc)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsHealthChecks, hc.Id, string(raw))
}

func (s *Service) getHealthCheck(ctx context.Context, id string) (*HealthCheck, bool, error) {
	raw, found, err := s.store.Get(ctx, nsHealthChecks, id)
	if err != nil || !found {
		return nil, false, err
	}
	var hc HealthCheck
	if err := json.Unmarshal([]byte(raw), &hc); err != nil {
		log := s.log.WithRecorder(ctx)
		log.Warn("skipping malformed health check record", zap.String("id", id), zap.Error(err))
		return nil, false, nil
	}
	return &hc, true, nil
}

// listAllHealthChecks returns every health check sorted by ID, skipping
// malformed records.
func (s *Service) listAllHealthChecks(ctx context.Context) ([]*HealthCheck, error) {
	pairs, err := s.store.Scan(ctx, nsHealthChecks, "")
	if err != nil {
		return nil, err
	}
	out := make([]*HealthCheck, 0, len(pairs))
	for _, kv := range pairs {
		var hc HealthCheck
		if err := json.Unmarshal([]byte(kv.Value), &hc); err != nil {
			log := s.log.WithRecorder(ctx)
			log.Warn("skipping malformed health check record", zap.String("key", kv.Key), zap.Error(err))
			continue
		}
		out = append(out, &hc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out, nil
}

func (s *Service) deleteHealthCheck(ctx context.Context, id string) error {
	return s.store.Delete(ctx, nsHealthChecks, id)
}
