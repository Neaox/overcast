package efs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// efsTagCfg tunes the shared tag validator to EFS's error shape (#1052).
// EFS reports every other rejected-input case this package models
// (errBadRequest, typed_logic.go) as BadRequest, and declares no dedicated
// tag-count exception, so the 50-tag limit is reported the same way an
// invalid key or value is.
var efsTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "BadRequest",
	InvalidCode:     "BadRequest",
	ExceededMessage: "Tags must be no more than 50.",
}

func efsTagsToMap(tags []Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[t.Key] = t.Value
	}
	return m
}

// Store namespaces. All keys are region-scoped via serviceutil.RegionKey so
// resources in different regions never collide.
const (
	nsFileSystems  = "efs:filesystems"
	nsMountTargets = "efs:mounttargets"
	nsAccessPoints = "efs:accesspoints"
	nsTags         = "efs:tags"
	nsPolicies     = "efs:policies"
	nsPreferences  = "efs:preferences"
	// nsInstance holds this instance's sweep-domain identity. See
	// Service.sweepDomain.
	nsInstance = "efs:instance"
)

// ─── Persisted records ────────────────────────────────────────────────────────
//
// Records are the persisted shape; the wire-facing descriptions in
// typed_logic.go are built from them on read so derived fields
// (NumberOfMountTargets, Name from tags) never go stale in the store.

// fileSystemRecord is the persisted state of one EFS file system.
type fileSystemRecord struct {
	FileSystemId                 string            `json:"FileSystemId"`
	CreationToken                string            `json:"CreationToken"`
	CreationTime                 float64           `json:"CreationTime"`
	LifeCycleState               string            `json:"LifeCycleState"`
	PerformanceMode              string            `json:"PerformanceMode"`
	Encrypted                    bool              `json:"Encrypted"`
	KmsKeyId                     string            `json:"KmsKeyId,omitempty"`
	ThroughputMode               string            `json:"ThroughputMode"`
	ProvisionedThroughputInMibps *float64          `json:"ProvisionedThroughputInMibps,omitempty"`
	AvailabilityZoneName         string            `json:"AvailabilityZoneName,omitempty"`
	AvailabilityZoneId           string            `json:"AvailabilityZoneId,omitempty"`
	BackupPolicyStatus           string            `json:"BackupPolicyStatus"`
	LifecyclePolicies            []LifecyclePolicy `json:"LifecyclePolicies,omitempty"`
	// ReplicationOverwriteProtection backs UpdateFileSystemProtection.
	ReplicationOverwriteProtection string `json:"ReplicationOverwriteProtection"`
}

// mountTargetRecord is the persisted state of one mount target.
type mountTargetRecord struct {
	MountTargetId        string   `json:"MountTargetId"`
	FileSystemId         string   `json:"FileSystemId"`
	SubnetId             string   `json:"SubnetId"`
	LifeCycleState       string   `json:"LifeCycleState"`
	IpAddress            string   `json:"IpAddress,omitempty"`
	Ipv6Address          string   `json:"Ipv6Address,omitempty"`
	NetworkInterfaceId   string   `json:"NetworkInterfaceId,omitempty"`
	AvailabilityZoneName string   `json:"AvailabilityZoneName,omitempty"`
	AvailabilityZoneId   string   `json:"AvailabilityZoneId,omitempty"`
	SecurityGroups       []string `json:"SecurityGroups,omitempty"`
	// NFSContainerId and NFSHostPort belong to the opt-in NFS data plane
	// (OVERCAST_EFS_NFS): the NFS-Ganesha container exporting this mount
	// target's file system, and the host port its 2049 is published on. Both
	// are empty in mock mode, in live mode without exports, and while the
	// container is still starting. See live_nfs.go.
	NFSContainerId string `json:"NFSContainerId,omitempty"`
	NFSHostPort    int    `json:"NFSHostPort,omitempty"`
}

// accessPointRecord is the persisted state of one access point.
type accessPointRecord struct {
	AccessPointId  string         `json:"AccessPointId"`
	ClientToken    string         `json:"ClientToken,omitempty"`
	FileSystemId   string         `json:"FileSystemId"`
	LifeCycleState string         `json:"LifeCycleState"`
	PosixUser      *PosixUser     `json:"PosixUser,omitempty"`
	RootDirectory  *RootDirectory `json:"RootDirectory,omitempty"`
}

// ─── Generic record helpers ───────────────────────────────────────────────────

func putRecord(ctx context.Context, s *Service, ns, region, id string, rec any) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, ns, serviceutil.RegionKey(region, id), string(raw))
}

// getRecord loads one record. found is false both when the key is absent and
// when the persisted payload cannot be decoded: a single corrupt record must
// surface as a modeled not-found, never break the service (see AGENTS.md
// "Malformed persisted state must be isolated").
func getRecord[T any](ctx context.Context, s *Service, ns, region, id string) (*T, bool, error) {
	raw, found, err := s.store.Get(ctx, ns, serviceutil.RegionKey(region, id))
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	var rec T
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		s.log.Warn("efs: skipping malformed record " + ns + "/" + id)
		return nil, false, nil
	}
	return &rec, true, nil
}

// listRecords returns every decodable record in a namespace for a region,
// sorted by store key (i.e. by resource ID). Malformed records are skipped.
func listRecords[T any](ctx context.Context, s *Service, ns, region string) ([]*T, error) {
	pairs, err := s.store.Scan(ctx, ns, serviceutil.RegionKey(region, ""))
	if err != nil {
		return nil, err
	}
	out := make([]*T, 0, len(pairs))
	keys := make([]string, 0, len(pairs))
	byKey := make(map[string]*T, len(pairs))
	for _, kv := range pairs {
		var rec T
		if json.Unmarshal([]byte(kv.Value), &rec) != nil {
			s.log.Warn("efs: skipping malformed record " + ns + "/" + kv.Key)
			continue
		}
		keys = append(keys, kv.Key)
		byKey[kv.Key] = &rec
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, byKey[k])
	}
	return out, nil
}

func deleteRecord(ctx context.Context, s *Service, ns, region, id string) error {
	return s.store.Delete(ctx, ns, serviceutil.RegionKey(region, id))
}

// ─── Tags ─────────────────────────────────────────────────────────────────────
//
// Tags are stored per resource ID (fs-… or fsap-…) as an ordered []Tag list,
// matching the wire shape so DescribeTags/ListTagsForResource echo insertion
// order after a merge.

func (s *Service) loadTags(ctx context.Context, region, resourceID string) []Tag {
	raw, found, err := s.store.Get(ctx, nsTags, serviceutil.RegionKey(region, resourceID))
	if err != nil || !found {
		return nil
	}
	var tags []Tag
	if json.Unmarshal([]byte(raw), &tags) != nil {
		return nil
	}
	return tags
}

func (s *Service) saveTags(ctx context.Context, region, resourceID string, tags []Tag) error {
	if len(tags) == 0 {
		return s.store.Delete(ctx, nsTags, serviceutil.RegionKey(region, resourceID))
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsTags, serviceutil.RegionKey(region, resourceID), string(raw))
}

// mergeTags upserts the given tags into the resource's tag list, preserving
// the position of existing keys. The merged set — not just the incoming
// delta — is validated against the shared limits before it is saved, so the
// 50-tag limit holds across repeated calls (#1052).
func (s *Service) mergeTags(ctx context.Context, region, resourceID string, tags []Tag) *protocol.AWSError {
	existing := s.loadTags(ctx, region, resourceID)
	for _, t := range tags {
		replaced := false
		for i := range existing {
			if existing[i].Key == t.Key {
				existing[i].Value = t.Value
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, t)
		}
	}
	if aerr := serviceutil.ValidateTags(efsTagCfg, efsTagsToMap(existing)); aerr != nil {
		return aerr
	}
	if err := s.saveTags(ctx, region, resourceID, existing); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *Service) removeTags(ctx context.Context, region, resourceID string, keys []string) error {
	existing := s.loadTags(ctx, region, resourceID)
	if len(existing) == 0 {
		return nil
	}
	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}
	kept := existing[:0]
	for _, t := range existing {
		if _, gone := drop[t.Key]; !gone {
			kept = append(kept, t)
		}
	}
	return s.saveTags(ctx, region, resourceID, kept)
}

// nameFromTags returns the value of the "Name" tag, which EFS surfaces as the
// Name field on file system and access point descriptions.
func nameFromTags(tags []Tag) string {
	for _, t := range tags {
		if t.Key == "Name" {
			return t.Value
		}
	}
	return ""
}

// ─── IDs, ARNs, and synthesized network fields ────────────────────────────────

// newEFSID mints a resource ID with the given prefix and a 17-hex-char suffix,
// matching AWS long resource IDs (fs-0123456789abcdef0).
func newEFSID(prefix string) string {
	hex := strings.ReplaceAll(uuid.NewString(), "-", "")
	return prefix + hex[:17]
}

func (s *Service) accountID() string {
	if s.cfg != nil && strings.TrimSpace(s.cfg.AccountID) != "" {
		return s.cfg.AccountID
	}
	return "000000000000"
}

func (s *Service) fileSystemARN(region, id string) string {
	return fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", region, s.accountID(), id)
}

func (s *Service) accessPointARN(region, id string) string {
	return fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:access-point/%s", region, s.accountID(), id)
}

// resourceIDFromARN accepts either a bare resource ID or a full EFS ARN and
// returns the bare ID (TagResource et al. accept both forms).
func resourceIDFromARN(idOrARN string) string {
	if !strings.HasPrefix(idOrARN, "arn:") {
		return idOrARN
	}
	if i := strings.LastIndexByte(idOrARN, '/'); i >= 0 {
		return idOrARN[i+1:]
	}
	return idOrARN
}

// azForSubnet deterministically derives an availability-zone letter from a
// subnet ID so that distinct subnets land in distinct zones (mount targets are
// limited to one per zone). Overcast does not model a real subnet↔AZ mapping.
func azForSubnet(region, subnetID string) (name, id string) {
	var h uint32
	for i := 0; i < len(subnetID); i++ {
		h = h*31 + uint32(subnetID[i])
	}
	idx := h % 3
	return fmt.Sprintf("%s%c", region, 'a'+idx), fmt.Sprintf("%s-az%d", compactRegion(region), idx+1)
}

// azIDForName derives a zone ID ("use1-az1") from a zone name ("us-east-1a").
func azIDForName(region, azName string) string {
	idx := 0
	if len(azName) > 0 {
		last := azName[len(azName)-1]
		if last >= 'a' && last <= 'z' {
			idx = int(last - 'a')
		}
	}
	return fmt.Sprintf("%s-az%d", compactRegion(region), idx+1)
}

// compactRegion converts "us-east-1" to the zone-ID prefix style "use1".
func compactRegion(region string) string {
	parts := strings.Split(region, "-")
	if len(parts) < 3 {
		return region
	}
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1 : len(parts)-1] {
		if p != "" {
			b.WriteByte(p[0])
		}
	}
	b.WriteString(parts[len(parts)-1])
	return b.String()
}

// syntheticIPForMountTarget derives a stable private IPv4 address for a mount
// target from its ID. Overcast does not allocate real subnet addresses.
func syntheticIPForMountTarget(mountTargetID string) string {
	var h uint32
	for i := 0; i < len(mountTargetID); i++ {
		h = h*31 + uint32(mountTargetID[i])
	}
	return fmt.Sprintf("10.0.%d.%d", (h>>8)&0xff, (h&0xff)|1)
}

// ─── File-system helpers ──────────────────────────────────────────────────────

func (s *Service) getFileSystem(ctx context.Context, region, id string) (*fileSystemRecord, bool, error) {
	return getRecord[fileSystemRecord](ctx, s, nsFileSystems, region, id)
}

func (s *Service) putFileSystem(ctx context.Context, region string, rec *fileSystemRecord) error {
	return putRecord(ctx, s, nsFileSystems, region, rec.FileSystemId, rec)
}

func (s *Service) listFileSystems(ctx context.Context, region string) ([]*fileSystemRecord, error) {
	return listRecords[fileSystemRecord](ctx, s, nsFileSystems, region)
}

// findFileSystemByCreationToken returns the file system created with the given
// token, if any. CreateFileSystem is idempotent on the creation token.
func (s *Service) findFileSystemByCreationToken(ctx context.Context, region, token string) (*fileSystemRecord, error) {
	all, err := s.listFileSystems(ctx, region)
	if err != nil {
		return nil, err
	}
	for _, fs := range all {
		if fs.CreationToken == token {
			return fs, nil
		}
	}
	return nil, nil
}

// ─── Mount-target helpers ─────────────────────────────────────────────────────

func (s *Service) getMountTarget(ctx context.Context, region, id string) (*mountTargetRecord, bool, error) {
	return getRecord[mountTargetRecord](ctx, s, nsMountTargets, region, id)
}

func (s *Service) putMountTarget(ctx context.Context, region string, rec *mountTargetRecord) error {
	return putRecord(ctx, s, nsMountTargets, region, rec.MountTargetId, rec)
}

func (s *Service) listMountTargets(ctx context.Context, region string) ([]*mountTargetRecord, error) {
	return listRecords[mountTargetRecord](ctx, s, nsMountTargets, region)
}

func (s *Service) listMountTargetsForFileSystem(ctx context.Context, region, fsID string) ([]*mountTargetRecord, error) {
	all, err := s.listMountTargets(ctx, region)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, mt := range all {
		if mt.FileSystemId == fsID {
			out = append(out, mt)
		}
	}
	return out, nil
}

// countMountTargetsByFileSystem returns the number of mount targets per file
// system in one scan, for list paths that would otherwise scan per item.
func (s *Service) countMountTargetsByFileSystem(ctx context.Context, region string) (map[string]int, error) {
	all, err := s.listMountTargets(ctx, region)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(all))
	for _, mt := range all {
		counts[mt.FileSystemId]++
	}
	return counts, nil
}

// ─── Access-point helpers ─────────────────────────────────────────────────────

func (s *Service) getAccessPoint(ctx context.Context, region, id string) (*accessPointRecord, bool, error) {
	return getRecord[accessPointRecord](ctx, s, nsAccessPoints, region, id)
}

func (s *Service) putAccessPoint(ctx context.Context, region string, rec *accessPointRecord) error {
	return putRecord(ctx, s, nsAccessPoints, region, rec.AccessPointId, rec)
}

func (s *Service) listAccessPoints(ctx context.Context, region string) ([]*accessPointRecord, error) {
	return listRecords[accessPointRecord](ctx, s, nsAccessPoints, region)
}

func (s *Service) listAccessPointsForFileSystem(ctx context.Context, region, fsID string) ([]*accessPointRecord, error) {
	all, err := s.listAccessPoints(ctx, region)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, ap := range all {
		if ap.FileSystemId == fsID {
			out = append(out, ap)
		}
	}
	return out, nil
}

// ─── File-system policy helpers ───────────────────────────────────────────────

func (s *Service) loadFileSystemPolicy(ctx context.Context, region, fsID string) (string, bool) {
	raw, found, err := s.store.Get(ctx, nsPolicies, serviceutil.RegionKey(region, fsID))
	if err != nil || !found {
		return "", false
	}
	return raw, true
}

func (s *Service) saveFileSystemPolicy(ctx context.Context, region, fsID, policy string) error {
	return s.store.Set(ctx, nsPolicies, serviceutil.RegionKey(region, fsID), policy)
}

func (s *Service) deleteFileSystemPolicyRecord(ctx context.Context, region, fsID string) error {
	return s.store.Delete(ctx, nsPolicies, serviceutil.RegionKey(region, fsID))
}
