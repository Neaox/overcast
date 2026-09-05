package backup

// handler_access_points.go — AWS Backup's six Backup Access Point operations
// (#1467), at the bindings the pinned model gives them:
//
//	PUT    /backup-access-point/create                            CreateBackupAccessPoint
//	GET    /backup-access-point/{AccessPointArn}                  DescribeBackupAccessPoint
//	DELETE /backup-access-point/delete/{AccessPointArn}           DeleteBackupAccessPoint
//	GET    /backup-access-point                                   ListBackupAccessPoints
//	POST   /backup-access-point/recovery-point/{RecoveryPointArn} ListBackupAccessPointsByRecoveryPoint
//	POST   /backup-access-point/resource/{ResourceArn}            ListBackupAccessPointsByResource
//
// The three roots are one prefix, "/backup-access-point" — a third subtree
// beside /backup-vaults and /backup/plans rather than a member of either.
//
// On AWS a backup access point exposes an S3 recovery point's contents through
// an S3 access point without a restore. Overcast runs no backup jobs, so no
// recovery point and no S3 access point exists behind any of this: the six
// operations store, filter and return the metadata a client supplied, which is
// the same metadata-only scope the rest of the service has (router.TierInert).
// Every member that AWS reads off the recovery point — BackupVaultName,
// BackupVaultArn, ResourceArn, ResourceType — and the S3AccessPointArn/
// S3AccessPointAlias pair AWS adds to AccessPointMetadata are therefore absent
// rather than invented; see accessPointMember below and accessPointRecord in
// store.go.
//
// Shapes verified against the API reference, 2026-09-05:
// https://docs.aws.amazon.com/aws-backup/latest/APIReference/API_CreateBackupAccessPoint.html
// https://docs.aws.amazon.com/aws-backup/latest/APIReference/API_DescribeBackupAccessPoint.html
// https://docs.aws.amazon.com/aws-backup/latest/APIReference/API_DeleteBackupAccessPoint.html
// https://docs.aws.amazon.com/aws-backup/latest/APIReference/API_ListBackupAccessPoints.html
// https://docs.aws.amazon.com/aws-backup/latest/APIReference/API_ListBackupAccessPointsByRecoveryPoint.html
// https://docs.aws.amazon.com/aws-backup/latest/APIReference/API_ListBackupAccessPointsByResource.html
//
// CloudFormation models no AWS::Backup::*AccessPoint* resource type — the
// AWS::Backup namespace is BackupPlan, BackupSelection, BackupVault,
// Framework, LegalHold, LogicallyAirGappedBackupVault, ReportPlan,
// RestoreTestingPlan, RestoreTestingSelection and TieringConfiguration — so
// there is no provisioner entry to add for these operations.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

const (
	// pathAccessPoints is the root all six operations bind under.
	pathAccessPoints = "/backup-access-point"

	// The two AccessPointStatus members a metadata-only emulation can be in.
	// AWS also models DELETING, DISASSOCIATED, DISASSOCIATING, EXPIRED and
	// FAILED; none is reachable here, because there is no S3 access point to
	// provision, expire or fail and the store write is synchronous.
	accessPointStatusCreating  = "CREATING"
	accessPointStatusAvailable = "AVAILABLE"

	// accessPointMaxResults is MaxResults' modeled range maximum for all three
	// List operations — 1–100, a hundredth of the 1–1000 the vault and plan
	// listings carry.
	accessPointMaxResults = 100
)

var (
	// accessPointNamePattern is Name's modeled alphabet and length,
	// [\da-z]{1}[\da-z-]{1,48}[\da-z]{1}. The pattern's two negative
	// lookbehinds are enforced by hasReservedAliasSuffix instead: Go's regexp
	// has no lookbehind.
	accessPointNamePattern = regexp.MustCompile(`^[\da-z][\da-z-]{1,48}[\da-z]$`)

	// accessPointArnPattern is AccessPointArn's modeled pattern, minus the
	// same two lookbehinds. The name is a "/"-separated resource component,
	// not the ":"-separated one vault and plan ARNs use, because the name is
	// shared with the S3 access point namespace.
	accessPointArnPattern = regexp.MustCompile(`^arn:aws[a-z-]*:backup:[a-z\d-]+:\d{12}:accesspoint/[\da-z][\da-z-]{1,48}[\da-z]$`)

	// recoveryPointArnPattern is RecoveryPointArn's modeled pattern,
	// (arn:aws[a-z-]*:[a-z-\d]+:[a-z-\d]+:).+ — service and region, then
	// anything. It is shared by CreateBackupAccessPoint's body member and
	// ListBackupAccessPointsByRecoveryPoint's path label.
	recoveryPointArnPattern = regexp.MustCompile(`^arn:aws[a-z-]*:[a-z\d-]+:[a-z\d-]+:.+`)

	// resourceArnPattern is ListBackupAccessPointsByResource's ResourceArn
	// label, (arn:aws[a-z-]*:[a-z-\d]+:).+ — one segment shorter, because the
	// resource may be a global one such as an S3 bucket.
	resourceArnPattern = regexp.MustCompile(`^arn:aws[a-z-]*:[a-z\d-]+:.+`)
)

// accessPointNameRule is Name's modeled length and alphabet. Unlike
// vaultNameRule, the name it validates is *not* what the route binds: every
// route here binds an ARN, so the name only ever arrives in a request body.
var accessPointNameRule = serviceutil.NameRule{
	MinLength:      3,
	MaxLength:      50,
	Pattern:        accessPointNamePattern,
	ErrorCode:      "InvalidParameterValueException",
	LengthMessage:  "Backup access point name must be 3 to 50 characters long.",
	PatternMessage: "Backup access point name must start and end with a lowercase letter or digit and contain only lowercase letters, digits and hyphens.",
	HTTPStatus:     http.StatusBadRequest,
}

// ─── Wire shapes ──────────────────────────────────────────────

// accessPointMember is AWS's ListAccessPointsMember, which
// DescribeBackupAccessPoint answers with too — its output shape carries the
// same members.
//
// Four of AWS's members are absent, and deliberately so. BackupVaultName,
// BackupVaultArn, ResourceType and StatusMessage are read off the recovery
// point the access point was created against, or off a failure that cannot
// happen here; Overcast creates no recovery points, so none of them has a
// truthful value and inventing one would be the expensive kind of divergence.
// ResourceArn is the exception that is present but never populated: it is the
// member ListBackupAccessPointsByResource filters on, so it earns a field for
// that comparison to be a real one rather than a hardcoded empty answer. It is
// `omitempty`, so it stays off the wire like the other three.
type accessPointMember struct {
	AccessPointArn      string            `json:"AccessPointArn"`
	AccessPointMetadata map[string]string `json:"AccessPointMetadata,omitempty"`
	CreationTime        float64           `json:"CreationTime"`
	Name                string            `json:"Name"`
	RecoveryPointArn    string            `json:"RecoveryPointArn"`
	ResourceArn         string            `json:"ResourceArn,omitempty"`
	Status              string            `json:"Status"`
}

// createBackupAccessPointRequest is CreateBackupAccessPointInput. Every member
// is a body member: the operation binds no path label and no query string.
type createBackupAccessPointRequest struct {
	AccessPointMetadata map[string]string `json:"AccessPointMetadata"`
	// AccessPointPolicy is accepted and dropped. The field is declared so the
	// struct names every modeled member, not because anything reads it — see
	// accessPointRecord in store.go for why storing it would persist data
	// nothing can ever read back.
	AccessPointPolicy string            `json:"AccessPointPolicy"`
	Name              string            `json:"Name"`
	RecoveryPointArn  string            `json:"RecoveryPointArn"`
	Tags              map[string]string `json:"Tags"`
}

// createBackupAccessPointResponse carries these two members and no others —
// notably not Name, which the caller already knows.
type createBackupAccessPointResponse struct {
	AccessPointArn string `json:"AccessPointArn"`
	Status         string `json:"Status"`
}

// listBackupAccessPointsResponse is the shape all three List operations share.
type listBackupAccessPointsResponse struct {
	BackupAccessPoints []accessPointMember `json:"BackupAccessPoints"`
	NextToken          string              `json:"NextToken,omitempty"`
}

// ─── Handlers ─────────────────────────────────────────────────

// createBackupAccessPoint handles PUT /backup-access-point/create.
func (s *Service) createBackupAccessPoint(w http.ResponseWriter, r *http.Request) {
	var req createBackupAccessPointRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	// Name and RecoveryPointArn are the two required members; an absent one is
	// a different fault from a present-but-malformed one, and AWS models a
	// different error for each.
	if req.Name == "" {
		protocol.WriteJSONError(w, r, missingParameterError("Name"))
		return
	}
	if req.RecoveryPointArn == "" {
		protocol.WriteJSONError(w, r, missingParameterError("RecoveryPointArn"))
		return
	}
	if aerr := accessPointName(req.Name); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !recoveryPointArnPattern.MatchString(req.RecoveryPointArn) {
		protocol.WriteJSONError(w, r, invalidParameterError(
			"Invalid recovery point ARN: "+req.RecoveryPointArn))
		return
	}
	// Request-shape validation before the duplicate-name check resolves
	// against the store, as createBackupVault does and for the same reason.
	if aerr := serviceutil.ValidateTags(backupTagCfg, req.Tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	_, found, err := s.store.getAccessPoint(r.Context(), region, req.Name)
	if err != nil {
		s.storeError(w, r, "CreateBackupAccessPoint", err)
		return
	}
	if found {
		// "It must be unique within your account and Region", so a repeat
		// create is a conflict rather than an overwrite.
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "AlreadyExistsException",
			Message:    fmt.Sprintf("Backup access point already exists: %s", req.Name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	record := &accessPointRecord{
		Name:             req.Name,
		AccessPointArn:   accessPointARN(region, s.cfg.AccountID, req.Name),
		RecoveryPointArn: req.RecoveryPointArn,
		CreationTime:     s.clk.Now().UTC(),
	}
	if len(req.AccessPointMetadata) > 0 {
		record.Metadata = make(map[string]string, len(req.AccessPointMetadata))
		for k, v := range req.AccessPointMetadata {
			record.Metadata[k] = v
		}
	}
	if len(req.Tags) > 0 {
		record.Tags = make(map[string]string, len(req.Tags))
		for k, v := range req.Tags {
			record.Tags[k] = v
		}
	}
	if err := s.store.putAccessPoint(r.Context(), region, record); err != nil {
		s.storeError(w, r, "CreateBackupAccessPoint", err)
		return
	}
	// 201, the only operation in this service that answers anything but 200 or
	// 204. CREATING is what AWS answers with — "a newly created backup access
	// point begins in the CREATING state" — even though the store write above
	// already finished, so a describe that follows sees AVAILABLE.
	protocol.WriteJSON(w, r, http.StatusCreated, createBackupAccessPointResponse{
		AccessPointArn: record.AccessPointArn,
		Status:         accessPointStatusCreating,
	})
}

// describeBackupAccessPoint handles GET /backup-access-point/{AccessPointArn}.
func (s *Service) describeBackupAccessPoint(w http.ResponseWriter, r *http.Request) {
	ref, aerr := parseAccessPointARN(arnParam(r, "AccessPointArn"))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	record, found, err := s.store.getAccessPoint(r.Context(), ref.region, ref.name)
	if err != nil {
		s.storeError(w, r, "DescribeBackupAccessPoint", err)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, notFoundError("Backup access point", ref.name))
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, wireAccessPoint(record))
}

// deleteBackupAccessPoint handles
// DELETE /backup-access-point/delete/{AccessPointArn}.
func (s *Service) deleteBackupAccessPoint(w http.ResponseWriter, r *http.Request) {
	ref, aerr := parseAccessPointARN(arnParam(r, "AccessPointArn"))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	_, found, err := s.store.getAccessPoint(r.Context(), ref.region, ref.name)
	if err != nil {
		s.storeError(w, r, "DeleteBackupAccessPoint", err)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, notFoundError("Backup access point", ref.name))
		return
	}
	if err := s.store.deleteAccessPoint(r.Context(), ref.region, ref.name); err != nil {
		s.storeError(w, r, "DeleteBackupAccessPoint", err)
		return
	}
	// The modeled output is Unit and the documented response is "HTTP/1.1 204"
	// with an empty body — unlike DeleteBackupVault, whose Unit output AWS
	// answers 200.
	protocol.WriteEmpty(w, r, http.StatusNoContent)
}

// listBackupAccessPoints handles GET /backup-access-point.
func (s *Service) listBackupAccessPoints(w http.ResponseWriter, r *http.Request) {
	s.writeAccessPointPage(w, r, "ListBackupAccessPoints", nil)
}

// listBackupAccessPointsByRecoveryPoint handles
// POST /backup-access-point/recovery-point/{RecoveryPointArn}.
func (s *Service) listBackupAccessPointsByRecoveryPoint(w http.ResponseWriter, r *http.Request) {
	arn := arnParam(r, "RecoveryPointArn")
	if !recoveryPointArnPattern.MatchString(arn) {
		protocol.WriteJSONError(w, r, invalidParameterError("Invalid recovery point ARN: "+arn))
		return
	}
	// Overcast runs no backup jobs, so no recovery point is ever created and
	// this answers empty for every ARN a client could have got from the
	// emulator. It is not stubbed to do so: an access point stores the
	// RecoveryPointArn its creator named, and that opaque reference is what
	// matches here. Answering empty is the truthful result of the filter, not
	// a shortcut around it.
	s.writeAccessPointPage(w, r, "ListBackupAccessPointsByRecoveryPoint",
		func(m accessPointMember) bool { return m.RecoveryPointArn == arn })
}

// listBackupAccessPointsByResource handles
// POST /backup-access-point/resource/{ResourceArn}.
func (s *Service) listBackupAccessPointsByResource(w http.ResponseWriter, r *http.Request) {
	arn := arnParam(r, "ResourceArn")
	if !resourceArnPattern.MatchString(arn) {
		protocol.WriteJSONError(w, r, invalidParameterError("Invalid resource ARN: "+arn))
		return
	}
	// An access point's ResourceArn is a property of the recovery point it was
	// created against, which Overcast never has, so no stored access point
	// carries one and the comparison below matches nothing. Same filter, same
	// reason, as ByRecoveryPoint above.
	s.writeAccessPointPage(w, r, "ListBackupAccessPointsByResource",
		func(m accessPointMember) bool { return m.ResourceArn == arn })
}

// ─── Helpers ──────────────────────────────────────────────────

// writeAccessPointPage is the body all three List operations share: read the
// region's access points, keep the ones the operation asks for, page the
// result. keep is nil for the unfiltered listing.
//
// MaxResults and NextToken carry a leading capital here, unlike
// ListBackupVaults' maxResults/nextToken — the model spells the two families
// differently, and reading the spelling off the sibling operation would leave
// a client's page size silently ignored.
func (s *Service) writeAccessPointPage(w http.ResponseWriter, r *http.Request, operation string, keep func(accessPointMember) bool) {
	query := r.URL.Query()
	limit, aerr := pageSize(query.Get("MaxResults"), accessPointMaxResults)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	records, err := s.store.listAccessPoints(r.Context(), region)
	if err != nil {
		s.storeError(w, r, operation, err)
		return
	}
	sortByName(records, func(a accessPointRecord) string { return a.Name })

	out := make([]accessPointMember, 0, len(records))
	for i := range records {
		member := wireAccessPoint(&records[i])
		if keep != nil && !keep(member) {
			continue
		}
		out = append(out, member)
	}

	page, err := serviceutil.Paginate(out, limit, query.Get("NextToken"),
		serviceutil.PaginateOptions{DefaultLimit: accessPointMaxResults, MaxLimit: accessPointMaxResults})
	if err != nil {
		protocol.WriteJSONError(w, r, invalidParameterError("The specified continuation token is not valid."))
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, listBackupAccessPointsResponse{
		BackupAccessPoints: page.Items,
		NextToken:          page.NextToken,
	})
}

// wireAccessPoint renders a persisted access point as the shape AWS puts on
// the wire.
func wireAccessPoint(a *accessPointRecord) accessPointMember {
	return accessPointMember{
		AccessPointArn:      a.AccessPointArn,
		AccessPointMetadata: a.Metadata,
		CreationTime:        epochSeconds(a.CreationTime),
		Name:                a.Name,
		RecoveryPointArn:    a.RecoveryPointArn,
		// The store write is synchronous, so an access point is usable by the
		// time any read can observe it — it is never still CREATING.
		Status: accessPointStatusAvailable,
	}
}

// accessPointRef is an AccessPointArn resolved to the region and name it
// addresses. The region comes from the ARN rather than from the request's
// signing region for the reason resolveTagTarget records in handler_tags.go:
// an ARN names its own region unambiguously, and reading anything else would
// let a caller in one region address a resource whose ARN claims another.
type accessPointRef struct {
	region string
	name   string
}

// parseAccessPointARN validates an {AccessPointArn} path label against its
// modeled pattern and splits out the region and name.
//
// A value that violates the pattern is InvalidParameterValueException, which
// is a different fault from naming an access point that does not exist —
// "arn:aws:backup:…:backup-vault:v" is not an access point ARN at all, however
// many access points happen to be stored.
func parseAccessPointARN(arn string) (accessPointRef, *protocol.AWSError) {
	if !accessPointArnPattern.MatchString(arn) {
		return accessPointRef{}, invalidParameterError("Invalid backup access point ARN: " + arn)
	}
	// arn : partition : backup : region : account : accesspoint/name — the
	// pattern above guarantees all six parts, so the indexes are safe.
	parts := strings.SplitN(arn, ":", 6)
	name := strings.TrimPrefix(parts[5], "accesspoint/")
	if aerr := accessPointName(name); aerr != nil {
		return accessPointRef{}, aerr
	}
	return accessPointRef{region: parts[3], name: name}, nil
}

// accessPointName validates a backup access point name against Name's modeled
// length, alphabet and the two reserved suffixes.
func accessPointName(name string) *protocol.AWSError {
	if aerr := serviceutil.ResourceName(name, accessPointNameRule); aerr != nil {
		return aerr
	}
	if hasReservedAliasSuffix(name) {
		return invalidParameterError(
			"Backup access point name must not end with a reserved Amazon S3 access point alias suffix: " + name)
	}
	return nil
}

// hasReservedAliasSuffix reports whether name ends in one of the two suffixes
// Name's pattern excludes with negative lookbehinds — (?<!-s3alias) and
// (?<!-ext-s3alias) — which S3 reserves for access point aliases. Go's regexp
// has no lookbehind, so this is checked apart from the pattern. Testing
// "-s3alias" alone covers both, because "-ext-s3alias" ends with it.
func hasReservedAliasSuffix(name string) bool {
	return strings.HasSuffix(name, "-s3alias")
}

// accessPointARN mints the ARN AWS gives a backup access point. Its resource
// component is "/"-separated, not ":"-separated like vaultARN's and planARN's,
// because the name is shared with the Amazon S3 access point namespace.
func accessPointARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:backup:%s:%s:accesspoint/%s", region, accountID, name)
}

// accessPointTagStore adapts one resolved access point's inline Tags field to
// serviceutil.TagStore — see vaultTagStore in handler_tags.go, which this
// mirrors. CreateBackupAccessPoint's Tags member is the only way tags get
// here that is specific to this resource; TagResource and UntagResource reach
// it through the shared dispatcher like any other Backup ARN.
type accessPointTagStore struct {
	s      *Service
	region string
	name   string
}

func (t accessPointTagStore) Load(ctx context.Context, _ string) (map[string]string, *protocol.AWSError) {
	a, found, err := t.s.store.getAccessPoint(ctx, t.region, t.name)
	if err != nil {
		return nil, t.s.wrapStoreErr(err)
	}
	if !found {
		return nil, notFoundError("Backup access point", t.name)
	}
	if a.Tags == nil {
		return map[string]string{}, nil
	}
	return a.Tags, nil
}

func (t accessPointTagStore) Save(ctx context.Context, _ string, tags map[string]string) *protocol.AWSError {
	a, found, err := t.s.store.getAccessPoint(ctx, t.region, t.name)
	if err != nil {
		return t.s.wrapStoreErr(err)
	}
	if !found {
		return notFoundError("Backup access point", t.name)
	}
	a.Tags = tags
	if err := t.s.store.putAccessPoint(ctx, t.region, a); err != nil {
		return t.s.wrapStoreErr(err)
	}
	return nil
}

// Delete satisfies TagStore; see vaultTagStore.Delete.
func (t accessPointTagStore) Delete(ctx context.Context, _ string) *protocol.AWSError { return nil }
