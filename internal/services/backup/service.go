// Package backup provides a metadata-only emulation of AWS Backup.
//
// Implemented operations: CreateBackupVault, DescribeBackupVault,
// ListBackupVaults, DeleteBackupVault, CreateBackupPlan, GetBackupPlan,
// ListBackupPlans, UpdateBackupPlan, DeleteBackupPlan.
//
// Routes are AWS's own restJson1 bindings from the pinned model
// (backup-2018-11-15) rather than an emulator invention: vaults hang off
// /backup-vaults and plans off /backup/plans — two subtrees, not one prefix.
// See RegisterRoutes. The service answers on no other wire protocol; all 109
// modeled backup operations carry `Protocols: ProtocolsRESTJSON` and an empty
// target prefix, so the `X-Amz-Target: AWSBackup.*` namespace Overcast used to
// dispatch on was a wire contract AWS does not have (#815).
//
// Nothing runs behind the control plane, which is what router.TierInert
// records for this service: no backup job ever executes, so a vault's
// NumberOfRecoveryPoints is always zero and a plan's rules never fire.
package backup

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName = "backup"

	// pathVaults and pathPlans are the two subtrees the modeled bindings use.
	// They are separate roots in AWS's own model, so a single "/backup" prefix
	// would serve neither.
	pathVaults = "/backup-vaults"
	pathPlans  = "/backup/plans"

	// vaultTypeStandard and vaultStateAvailable are the only members of AWS's
	// VaultType and VaultState enums a vault created here can be in: Overcast
	// implements neither logically air-gapped nor restore-access vaults, and a
	// vault is stored synchronously, so it is never CREATING or FAILED.
	vaultTypeStandard   = "BACKUP_VAULT"
	vaultStateAvailable = "AVAILABLE"

	// maxResultsCeiling is MaxResults' modeled range maximum (1–1000). It also
	// stands in for the page size of a request that omits MaxResults, which
	// AWS does not document.
	maxResultsCeiling = 1000
)

// vaultNameRule is BackupVaultName's modeled pattern, ^[a-zA-Z0-9\-\_]{2,50}$.
// The pattern is why the route can bind {BackupVaultName} as an ordinary chi
// label with no unescaping: it admits neither '/' nor ':', so a client never
// percent-encodes one — unlike the ARN labels elsewhere in this codebase.
var vaultNameRule = serviceutil.NameRule{
	Pattern:        regexp.MustCompile(`^[a-zA-Z0-9\-_]{2,50}$`),
	ErrorCode:      "InvalidParameterValueException",
	PatternMessage: "Backup vault name must be 2 to 50 characters long and contain only letters, numbers, hyphens and underscores.",
	HTTPStatus:     http.StatusBadRequest,
}

// ─── Wire shapes ──────────────────────────────────────────────
//
// Every timestamp below is a float64 of epoch seconds, not a string. The
// backup model carries no timestampFormat trait on any shape, so restJson1's
// default applies, and the generated SDK deserialiser is explicit about what
// that means: a string is rejected with "expected timestamp to be a JSON
// Number, got string instead". Overcast served RFC 3339 strings here, which no
// SDK could have parsed.

// backupVault is AWS's BackupVaultListMember, which DescribeBackupVault also
// answers with — its output shape carries the same members plus the vault-lock
// and MPA-approval ones, none of which Overcast emulates.
type backupVault struct {
	BackupVaultName        string  `json:"BackupVaultName"`
	BackupVaultArn         string  `json:"BackupVaultArn"`
	VaultType              string  `json:"VaultType"`
	VaultState             string  `json:"VaultState"`
	CreationDate           float64 `json:"CreationDate"`
	EncryptionKeyArn       string  `json:"EncryptionKeyArn,omitempty"`
	CreatorRequestId       string  `json:"CreatorRequestId,omitempty"`
	NumberOfRecoveryPoints int64   `json:"NumberOfRecoveryPoints"`
	Locked                 bool    `json:"Locked"`
}

// createBackupVaultRequest is CreateBackupVaultInput's body. BackupVaultName
// is not here: it is an httpLabel, carried in the path.
type createBackupVaultRequest struct {
	EncryptionKeyArn string `json:"EncryptionKeyArn"`
	CreatorRequestId string `json:"CreatorRequestId"`
	// BackupVaultTags is declared so the accepted-and-dropped member is
	// visible in the shape, and is deliberately not read — see
	// createBackupVault.
	BackupVaultTags map[string]string `json:"BackupVaultTags"`
}

type createBackupVaultResponse struct {
	BackupVaultName string  `json:"BackupVaultName"`
	BackupVaultArn  string  `json:"BackupVaultArn"`
	CreationDate    float64 `json:"CreationDate"`
}

type listBackupVaultsResponse struct {
	BackupVaultList []backupVault `json:"BackupVaultList"`
	NextToken       string        `json:"NextToken,omitempty"`
}

// backupPlanInput is AWS's BackupPlanInput. BackupPlanName and Rules are both
// required members. Rules are stored and echoed verbatim: a BackupRule carries
// fourteen members describing a schedule nothing here runs.
type backupPlanInput struct {
	BackupPlanName string           `json:"BackupPlanName"`
	Rules          []map[string]any `json:"Rules"`
}

type createBackupPlanRequest struct {
	BackupPlan       backupPlanInput `json:"BackupPlan"`
	CreatorRequestId string          `json:"CreatorRequestId"`
	// BackupPlanTags is declared and deliberately not read, as
	// BackupVaultTags is above.
	BackupPlanTags map[string]string `json:"BackupPlanTags"`
}

type updateBackupPlanRequest struct {
	BackupPlan backupPlanInput `json:"BackupPlan"`
}

// createBackupPlanResponse is the shape CreateBackupPlan and UpdateBackupPlan
// share. UpdateBackupPlan's modeled output carries CreationDate too, which
// Overcast used to omit.
type createBackupPlanResponse struct {
	BackupPlanId  string  `json:"BackupPlanId"`
	BackupPlanArn string  `json:"BackupPlanArn"`
	CreationDate  float64 `json:"CreationDate"`
	VersionId     string  `json:"VersionId"`
}

type getBackupPlanResponse struct {
	BackupPlan    backupPlanInput `json:"BackupPlan"`
	BackupPlanId  string          `json:"BackupPlanId"`
	BackupPlanArn string          `json:"BackupPlanArn"`
	VersionId     string          `json:"VersionId"`
	CreationDate  float64         `json:"CreationDate"`
}

// backupPlansListMember is AWS's BackupPlansListMember, the element
// ListBackupPlans returns.
type backupPlansListMember struct {
	BackupPlanArn  string  `json:"BackupPlanArn"`
	BackupPlanId   string  `json:"BackupPlanId"`
	BackupPlanName string  `json:"BackupPlanName"`
	CreationDate   float64 `json:"CreationDate"`
	VersionId      string  `json:"VersionId"`
}

type listBackupPlansResponse struct {
	BackupPlansList []backupPlansListMember `json:"BackupPlansList"`
	NextToken       string                  `json:"NextToken,omitempty"`
}

type deleteBackupPlanResponse struct {
	BackupPlanId  string  `json:"BackupPlanId"`
	BackupPlanArn string  `json:"BackupPlanArn"`
	DeletionDate  float64 `json:"DeletionDate"`
	VersionId     string  `json:"VersionId"`
}

// ─── Errors ───────────────────────────────────────────────────
//
// The status codes are the model's, not a guess: no backup error shape carries
// an httpError trait except ConflictException, so every client error defaults
// to 400 — ResourceNotFoundException included, where Overcast answered 404.
// The model declares no ValidationException anywhere in the service, so the
// two errors below are what a bad request gets instead.

func notFoundError(what, name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("%s not found: %s", what, name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func missingParameterError(param string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "MissingParameterValueException",
		Message:    fmt.Sprintf("Required parameter %s is missing.", param),
		HTTPStatus: http.StatusBadRequest,
	}
}

func invalidParameterError(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameterValueException",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// ─── Service ──────────────────────────────────────────────────

// Service implements router.Service for AWS Backup.
type Service struct {
	cfg   *config.Config
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	store *backupStore
}

// New returns a configured Backup Service. Pure field assignment — no store
// reads or I/O (startup-budget rule).
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:   cfg,
		log:   log,
		clk:   clk,
		store: newBackupStore(st, log),
	}
}

func (s *Service) Name() string { return serviceName }

// PathPrefixes satisfies router.PathPrefixService, so a router built with a
// service subset answers 501 on Backup's paths rather than dropping them into
// S3's wildcard.
func (s *Service) PathPrefixes() []string { return []string{pathVaults, pathPlans} }

// RegisterRoutes registers Backup's modeled REST bindings:
//
//	PUT    /backup-vaults/{BackupVaultName}  CreateBackupVault
//	GET    /backup-vaults/{BackupVaultName}  DescribeBackupVault
//	DELETE /backup-vaults/{BackupVaultName}  DeleteBackupVault
//	GET    /backup-vaults                    ListBackupVaults
//	PUT    /backup/plans                     CreateBackupPlan
//	GET    /backup/plans                     ListBackupPlans
//	GET    /backup/plans/{BackupPlanId}      GetBackupPlan
//	POST   /backup/plans/{BackupPlanId}      UpdateBackupPlan
//	DELETE /backup/plans/{BackupPlanId}      DeleteBackupPlan
//
// The paths are absolute rather than nested under chi sub-routers for
// /backup-vaults and /backup, and that is deliberate. A sub-router owns its
// whole subtree, so every path beneath it that Overcast does not implement
// would hit its own NotFound instead of the parent's generated fallback.
// Backup declares nine of the 109 operations AWS binds here, and the other
// hundred — /backup-vaults/{name}/recovery-points, /backup/plans/{id}/
// selections and the rest — must keep falling through to a protocol-correct
// 501. This is the fault docs/plans/manifest-enforcement.md records for
// AppConfig under AppRegistry's /applications.
func (s *Service) RegisterRoutes(r chi.Router) {
	// Vaults
	r.Get(pathVaults, s.listBackupVaults)
	r.Put(pathVaults+"/{BackupVaultName}", s.createBackupVault)
	r.Get(pathVaults+"/{BackupVaultName}", s.describeBackupVault)
	r.Delete(pathVaults+"/{BackupVaultName}", s.deleteBackupVault)

	// Plans
	r.Get(pathPlans, s.listBackupPlans)
	r.Put(pathPlans, s.createBackupPlan)
	r.Get(pathPlans+"/{BackupPlanId}", s.getBackupPlan)
	r.Post(pathPlans+"/{BackupPlanId}", s.updateBackupPlan)
	r.Delete(pathPlans+"/{BackupPlanId}", s.deleteBackupPlan)
}

// ─── Vault handlers ───────────────────────────────────────────

func (s *Service) createBackupVault(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "BackupVaultName")
	if aerr := serviceutil.ResourceName(name, vaultNameRule); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	var req createBackupVaultRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	_, found, err := s.store.getVault(r.Context(), region, name)
	if err != nil {
		s.storeError(w, r, "CreateBackupVault", err)
		return
	}
	if found {
		// Vault names are unique per account per region, so a repeat create is
		// a conflict rather than an overwrite.
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "AlreadyExistsException",
			Message:    fmt.Sprintf("Backup vault already exists: %s", name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	now := s.clk.Now().UTC()
	vault := &vaultRecord{
		BackupVaultName:  name,
		BackupVaultArn:   vaultARN(region, s.cfg.AccountID, name),
		CreationDate:     now,
		EncryptionKeyArn: req.EncryptionKeyArn,
		CreatorRequestID: req.CreatorRequestId,
	}
	if err := s.store.putVault(r.Context(), region, vault); err != nil {
		s.storeError(w, r, "CreateBackupVault", err)
		return
	}
	// req.BackupVaultTags is accepted and dropped: Backup has no tag
	// operations here yet, and storing tags nothing can read would be worse
	// than not storing them. Tracked on #815; the capability row says so.
	protocol.WriteJSON(w, r, http.StatusOK, createBackupVaultResponse{
		BackupVaultName: vault.BackupVaultName,
		BackupVaultArn:  vault.BackupVaultArn,
		CreationDate:    epochSeconds(vault.CreationDate),
	})
}

func (s *Service) describeBackupVault(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "BackupVaultName")
	// BackupVaultAccountId is an httpQuery member, not a body member —
	// DescribeBackupVault is a GET and has no request body. Overcast emulates
	// one account, so a request naming another finds nothing there.
	if account := r.URL.Query().Get("backupVaultAccountId"); account != "" && account != s.cfg.AccountID {
		protocol.WriteJSONError(w, r, notFoundError("Backup vault", name))
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	vault, found, err := s.store.getVault(r.Context(), region, name)
	if err != nil {
		s.storeError(w, r, "DescribeBackupVault", err)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, notFoundError("Backup vault", name))
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, wireVault(vault))
}

func (s *Service) listBackupVaults(w http.ResponseWriter, r *http.Request) {
	// ByVaultType, ByShared, MaxResults and NextToken are all httpQuery
	// members. This is a GET with no request body to carry any of them in.
	query := r.URL.Query()
	limit, aerr := pageSize(query.Get("maxResults"))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	vaults, err := s.store.listVaults(r.Context(), region)
	if err != nil {
		s.storeError(w, r, "ListBackupVaults", err)
		return
	}
	sortByName(vaults, func(v vaultRecord) string { return v.BackupVaultName })

	// Every vault Overcast creates is a standard, unshared vault, so a filter
	// for any other type — or for shared vaults — answers empty rather than
	// answering with vaults it was asked to exclude.
	vaultType := query.Get("vaultType")
	if query.Get("shared") == "true" || (vaultType != "" && vaultType != vaultTypeStandard) {
		vaults = nil
	}

	out := make([]backupVault, 0, len(vaults))
	for i := range vaults {
		out = append(out, wireVault(&vaults[i]))
	}

	page, err := serviceutil.Paginate(out, limit, query.Get("nextToken"),
		serviceutil.PaginateOptions{DefaultLimit: maxResultsCeiling, MaxLimit: maxResultsCeiling})
	if err != nil {
		// A token that will not decode is an error, not a silent restart at
		// page one — see serviceutil.ErrInvalidPageToken.
		protocol.WriteJSONError(w, r, invalidParameterError("The specified continuation token is not valid."))
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, listBackupVaultsResponse{
		BackupVaultList: page.Items,
		NextToken:       page.NextToken,
	})
}

func (s *Service) deleteBackupVault(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "BackupVaultName")
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	_, found, err := s.store.getVault(r.Context(), region, name)
	if err != nil {
		s.storeError(w, r, "DeleteBackupVault", err)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, notFoundError("Backup vault", name))
		return
	}
	if err := s.store.deleteVault(r.Context(), region, name); err != nil {
		s.storeError(w, r, "DeleteBackupVault", err)
		return
	}
	// DeleteBackupVault's modeled output is Unit: AWS answers 200 with no
	// body, and the SDK deserialiser discards whatever arrives. Overcast used
	// to invent a BackupVaultName/BackupVaultArn/DeletionDate document here.
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// ─── Plan handlers ────────────────────────────────────────────

func (s *Service) createBackupPlan(w http.ResponseWriter, r *http.Request) {
	var req createBackupPlanRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.BackupPlan.BackupPlanName == "" {
		protocol.WriteJSONError(w, r, missingParameterError("BackupPlan.BackupPlanName"))
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	// AWS mints a UUID here. The previous identifier was derived from the
	// clock, so two plans created in the same nanosecond — every pair created
	// under a mock clock — collided, and the second silently replaced the
	// first.
	planID := uuid.NewString()
	now := s.clk.Now().UTC()
	plan := &planRecord{
		BackupPlanID:   planID,
		BackupPlanArn:  planARN(region, s.cfg.AccountID, planID),
		BackupPlanName: req.BackupPlan.BackupPlanName,
		Rules:          req.BackupPlan.Rules,
		Version:        1,
		CreationDate:   now,
	}
	if err := s.store.putPlan(r.Context(), region, plan); err != nil {
		s.storeError(w, r, "CreateBackupPlan", err)
		return
	}
	// req.BackupPlanTags is dropped for the same reason as BackupVaultTags —
	// see createBackupVault.
	protocol.WriteJSON(w, r, http.StatusOK, planWriteResponse(plan))
}

func (s *Service) getBackupPlan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "BackupPlanId")
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	plan, found, err := s.store.getPlan(r.Context(), region, id)
	if err != nil {
		s.storeError(w, r, "GetBackupPlan", err)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, notFoundError("Backup plan", id))
		return
	}
	// VersionId is an httpQuery member. Overcast keeps only the current
	// version of a plan, so a request for any other one is answered
	// not-found rather than with the current version wearing the requested
	// version's name.
	if want := r.URL.Query().Get("versionId"); want != "" && want != versionID(plan.Version) {
		protocol.WriteJSONError(w, r, notFoundError("Backup plan version", want))
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, getBackupPlanResponse{
		BackupPlan: backupPlanInput{
			BackupPlanName: plan.BackupPlanName,
			Rules:          plan.Rules,
		},
		BackupPlanId:  plan.BackupPlanID,
		BackupPlanArn: plan.BackupPlanArn,
		VersionId:     versionID(plan.Version),
		CreationDate:  epochSeconds(plan.CreationDate),
	})
}

func (s *Service) updateBackupPlan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "BackupPlanId")
	var req updateBackupPlanRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.BackupPlan.BackupPlanName == "" {
		protocol.WriteJSONError(w, r, missingParameterError("BackupPlan.BackupPlanName"))
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	plan, found, err := s.store.getPlan(r.Context(), region, id)
	if err != nil {
		s.storeError(w, r, "UpdateBackupPlan", err)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, notFoundError("Backup plan", id))
		return
	}
	plan.BackupPlanName = req.BackupPlan.BackupPlanName
	plan.Rules = req.BackupPlan.Rules
	// A new version per update. The previous implementation set "v2" every
	// time, so no two updates after the first were distinguishable.
	plan.Version = currentVersion(plan.Version) + 1
	if err := s.store.putPlan(r.Context(), region, plan); err != nil {
		s.storeError(w, r, "UpdateBackupPlan", err)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, planWriteResponse(plan))
}

func (s *Service) deleteBackupPlan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "BackupPlanId")
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	plan, found, err := s.store.getPlan(r.Context(), region, id)
	if err != nil {
		s.storeError(w, r, "DeleteBackupPlan", err)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, notFoundError("Backup plan", id))
		return
	}
	if err := s.store.deletePlan(r.Context(), region, id); err != nil {
		s.storeError(w, r, "DeleteBackupPlan", err)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, deleteBackupPlanResponse{
		BackupPlanId:  plan.BackupPlanID,
		BackupPlanArn: plan.BackupPlanArn,
		DeletionDate:  epochSeconds(s.clk.Now().UTC()),
		VersionId:     versionID(plan.Version),
	})
}

func (s *Service) listBackupPlans(w http.ResponseWriter, r *http.Request) {
	// NextToken, MaxResults and IncludeDeleted are httpQuery members.
	// IncludeDeleted is read and has nothing to add: plans are removed
	// outright rather than tombstoned, so no plan is ever in the deleted
	// state it would include.
	query := r.URL.Query()
	limit, aerr := pageSize(query.Get("maxResults"))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	plans, err := s.store.listPlans(r.Context(), region)
	if err != nil {
		s.storeError(w, r, "ListBackupPlans", err)
		return
	}
	sortByName(plans, func(p planRecord) string { return p.BackupPlanName })

	out := make([]backupPlansListMember, 0, len(plans))
	for i := range plans {
		p := &plans[i]
		out = append(out, backupPlansListMember{
			BackupPlanArn:  p.BackupPlanArn,
			BackupPlanId:   p.BackupPlanID,
			BackupPlanName: p.BackupPlanName,
			CreationDate:   epochSeconds(p.CreationDate),
			VersionId:      versionID(p.Version),
		})
	}

	page, err := serviceutil.Paginate(out, limit, query.Get("nextToken"),
		serviceutil.PaginateOptions{DefaultLimit: maxResultsCeiling, MaxLimit: maxResultsCeiling})
	if err != nil {
		protocol.WriteJSONError(w, r, invalidParameterError("The specified continuation token is not valid."))
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, listBackupPlansResponse{
		BackupPlansList: page.Items,
		NextToken:       page.NextToken,
	})
}

// ─── Helpers ──────────────────────────────────────────────────

// wireVault renders a persisted vault as the shape AWS puts on the wire.
func wireVault(v *vaultRecord) backupVault {
	return backupVault{
		BackupVaultName:  v.BackupVaultName,
		BackupVaultArn:   v.BackupVaultArn,
		VaultType:        vaultTypeStandard,
		VaultState:       vaultStateAvailable,
		CreationDate:     epochSeconds(v.CreationDate),
		EncryptionKeyArn: v.EncryptionKeyArn,
		CreatorRequestId: v.CreatorRequestID,
		// No backup job ever runs, so a vault never acquires a recovery point,
		// and vault lock is not emulated.
		NumberOfRecoveryPoints: 0,
		Locked:                 false,
	}
}

// planWriteResponse is the body CreateBackupPlan and UpdateBackupPlan share.
func planWriteResponse(p *planRecord) createBackupPlanResponse {
	return createBackupPlanResponse{
		BackupPlanId:  p.BackupPlanID,
		BackupPlanArn: p.BackupPlanArn,
		CreationDate:  epochSeconds(p.CreationDate),
		VersionId:     versionID(p.Version),
	}
}

// pageSize reads the maxResults query parameter against MaxResults' modeled
// range of 1–1000. An empty value means the client omitted it.
func pageSize(raw string) (int, *protocol.AWSError) {
	if raw == "" {
		return 0, nil
	}
	value := serviceutil.ParseIntDefault(raw, -1)
	if value < 1 || value > maxResultsCeiling {
		return 0, invalidParameterError(fmt.Sprintf("MaxResults must be between 1 and %d.", maxResultsCeiling))
	}
	return value, nil
}

// currentVersion reads a plan's version counter. Records written before #815
// carry no counter and decode as 0; they were all written as version one.
func currentVersion(version int) int {
	if version < 1 {
		return 1
	}
	return version
}

// versionID renders the version counter as the opaque VersionId AWS returns.
func versionID(version int) string {
	return fmt.Sprintf("v%d", currentVersion(version))
}

// epochSeconds renders a timestamp the way restJson1 binds one with no
// timestampFormat trait: epoch seconds, with millisecond precision as AWS
// itself returns.
func epochSeconds(t time.Time) float64 {
	return float64(t.UnixMilli()) / 1000.0
}

// sortByName orders a listing by the name clients see, so a page boundary
// falls in the same place on every call.
func sortByName[T any](items []T, name func(T) string) {
	sort.Slice(items, func(i, j int) bool { return name(items[i]) < name(items[j]) })
}

func vaultARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:%s", region, accountID, name)
}

func planARN(region, accountID, planID string) string {
	return fmt.Sprintf("arn:aws:backup:%s:%s:backup-plan:%s", region, accountID, planID)
}

// storeError logs a state-store failure and answers with
// ServiceUnavailableException, the server error backup models on every one of
// these operations, rather than the emulator's generic InternalError.
func (s *Service) storeError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	s.log.Error("state store failure", zap.String("operation", operation), zap.Error(err))
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "ServiceUnavailableException",
		Message:    "The request failed due to a temporary failure of the server.",
		HTTPStatus: http.StatusInternalServerError,
	})
}
