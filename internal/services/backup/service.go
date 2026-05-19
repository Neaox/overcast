package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName   = "backup"
	targetPrefix  = "AWSBackup."
	nsVaults      = "backup:vaults"
	nsPlans       = "backup:plans"
	defaultRegion = "us-east-1"
)

type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	typedOp map[string]op.Operation
}

func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		cfg:   cfg,
		store: st,
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		clk:   clk,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) TargetPrefix() string { return targetPrefix }

func (s *Service) RegisterRoutes(_ chi.Router) {}

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Backup does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if c.Name() != codec.NameRPCv2CBOR {
			s.dispatchLegacy(w, r, opName)
			return
		}
		if typed, ok := s.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}

	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
	s.dispatchLegacy(w, r, op)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "CreateBackupVault":
		s.createBackupVault(w, r)
	case "DeleteBackupVault":
		s.deleteBackupVault(w, r)
	case "DescribeBackupVault":
		s.describeBackupVault(w, r)
	case "ListBackupVaults":
		s.listBackupVaults(w, r)
	case "CreateBackupPlan":
		s.createBackupPlan(w, r)
	case "DeleteBackupPlan":
		s.deleteBackupPlan(w, r)
	case "GetBackupPlan":
		s.getBackupPlan(w, r)
	case "ListBackupPlans":
		s.listBackupPlans(w, r)
	case "UpdateBackupPlan":
		s.updateBackupPlan(w, r)
	default:
		protocol.NotImplementedJSON(w, r)
	}
}

type backupVault struct {
	BackupVaultName        string `json:"BackupVaultName"`
	BackupVaultArn         string `json:"BackupVaultArn"`
	CreationDate           string `json:"CreationDate"`
	EncryptionKeyArn       string `json:"EncryptionKeyArn,omitempty"`
	NumberOfRecoveryPoints int    `json:"NumberOfRecoveryPoints"`
}

type backupPlan struct {
	BackupPlanId   string           `json:"BackupPlanId"`
	BackupPlanArn  string           `json:"BackupPlanArn"`
	BackupPlanName string           `json:"BackupPlanName"`
	Rules          []map[string]any `json:"Rules,omitempty"`
	VersionId      string           `json:"VersionId"`
	CreatedAt      string           `json:"CreationDate"`
}

func (s *Service) createBackupVault(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BackupVaultName  string `json:"BackupVaultName"`
		EncryptionKeyArn string `json:"EncryptionKeyArn"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.BackupVaultName) == "" {
		validationError(w, r, "BackupVaultName is required")
		return
	}
	if _, exists, aerr := s.getVault(r.Context(), in.BackupVaultName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	} else if exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "AlreadyExistsException", Message: "Backup vault already exists", HTTPStatus: http.StatusBadRequest})
		return
	}
	v := backupVault{
		BackupVaultName:        in.BackupVaultName,
		BackupVaultArn:         s.vaultARN(in.BackupVaultName),
		CreationDate:           s.clk.Now().UTC().Format(time.RFC3339),
		EncryptionKeyArn:       in.EncryptionKeyArn,
		NumberOfRecoveryPoints: 0,
	}
	if aerr := s.putVault(r.Context(), &v); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"BackupVaultName": v.BackupVaultName,
		"BackupVaultArn":  v.BackupVaultArn,
		"CreationDate":    v.CreationDate,
	})
}

func (s *Service) deleteBackupVault(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BackupVaultName string `json:"BackupVaultName"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.BackupVaultName) == "" {
		validationError(w, r, "BackupVaultName is required")
		return
	}
	v, exists, aerr := s.getVault(r.Context(), in.BackupVaultName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup vault not found", HTTPStatus: http.StatusNotFound})
		return
	}
	if err := s.store.Delete(r.Context(), nsVaults, in.BackupVaultName); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"BackupVaultName": v.BackupVaultName,
		"BackupVaultArn":  v.BackupVaultArn,
		"DeletionDate":    s.clk.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Service) describeBackupVault(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BackupVaultName string `json:"BackupVaultName"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.BackupVaultName) == "" {
		validationError(w, r, "BackupVaultName is required")
		return
	}
	v, exists, aerr := s.getVault(r.Context(), in.BackupVaultName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup vault not found", HTTPStatus: http.StatusNotFound})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, v)
}

func (s *Service) listBackupVaults(w http.ResponseWriter, r *http.Request) {
	kvs, err := s.store.Scan(r.Context(), nsVaults, "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	out := make([]backupVault, 0, len(kvs))
	for _, kv := range kvs {
		var v backupVault
		if json.Unmarshal([]byte(kv.Value), &v) == nil {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BackupVaultName < out[j].BackupVaultName })
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"BackupVaultList": out})
}

func (s *Service) createBackupPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BackupPlan struct {
			BackupPlanName string           `json:"BackupPlanName"`
			Rules          []map[string]any `json:"Rules"`
		} `json:"BackupPlan"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.BackupPlan.BackupPlanName) == "" {
		validationError(w, r, "BackupPlan.BackupPlanName is required")
		return
	}
	planID := fmt.Sprintf("plan-%d", s.clk.Now().UnixNano())
	p := backupPlan{
		BackupPlanId:   planID,
		BackupPlanArn:  s.planARN(planID),
		BackupPlanName: in.BackupPlan.BackupPlanName,
		Rules:          in.BackupPlan.Rules,
		VersionId:      "v1",
		CreatedAt:      s.clk.Now().UTC().Format(time.RFC3339),
	}
	if aerr := s.putPlan(r.Context(), &p); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"BackupPlanId":  p.BackupPlanId,
		"BackupPlanArn": p.BackupPlanArn,
		"CreationDate":  p.CreatedAt,
		"VersionId":     p.VersionId,
	})
}

func (s *Service) getBackupPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BackupPlanId string `json:"BackupPlanId"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	p, exists, aerr := s.getPlan(r.Context(), in.BackupPlanId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup plan not found", HTTPStatus: http.StatusNotFound})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"BackupPlan": map[string]any{
			"BackupPlanName": p.BackupPlanName,
			"Rules":          p.Rules,
		},
		"BackupPlanId":  p.BackupPlanId,
		"BackupPlanArn": p.BackupPlanArn,
		"VersionId":     p.VersionId,
		"CreationDate":  p.CreatedAt,
	})
}

func (s *Service) updateBackupPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BackupPlanId string `json:"BackupPlanId"`
		BackupPlan   struct {
			BackupPlanName string           `json:"BackupPlanName"`
			Rules          []map[string]any `json:"Rules"`
		} `json:"BackupPlan"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	p, exists, aerr := s.getPlan(r.Context(), in.BackupPlanId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup plan not found", HTTPStatus: http.StatusNotFound})
		return
	}
	if strings.TrimSpace(in.BackupPlan.BackupPlanName) != "" {
		p.BackupPlanName = in.BackupPlan.BackupPlanName
	}
	p.Rules = in.BackupPlan.Rules
	p.VersionId = "v2"
	if aerr := s.putPlan(r.Context(), p); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"BackupPlanId": p.BackupPlanId, "BackupPlanArn": p.BackupPlanArn, "VersionId": p.VersionId})
}

func (s *Service) deleteBackupPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BackupPlanId string `json:"BackupPlanId"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	p, exists, aerr := s.getPlan(r.Context(), in.BackupPlanId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup plan not found", HTTPStatus: http.StatusNotFound})
		return
	}
	if err := s.store.Delete(r.Context(), nsPlans, in.BackupPlanId); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"BackupPlanId": p.BackupPlanId, "BackupPlanArn": p.BackupPlanArn, "DeletionDate": s.clk.Now().UTC().Format(time.RFC3339)})
}

func (s *Service) listBackupPlans(w http.ResponseWriter, r *http.Request) {
	kvs, err := s.store.Scan(r.Context(), nsPlans, "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	out := make([]map[string]any, 0, len(kvs))
	for _, kv := range kvs {
		var p backupPlan
		if json.Unmarshal([]byte(kv.Value), &p) == nil {
			out = append(out, map[string]any{
				"BackupPlanId":   p.BackupPlanId,
				"BackupPlanArn":  p.BackupPlanArn,
				"BackupPlanName": p.BackupPlanName,
				"VersionId":      p.VersionId,
				"CreationDate":   p.CreatedAt,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["BackupPlanName"].(string) < out[j]["BackupPlanName"].(string) })
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"BackupPlansList": out})
}

func validationError(w http.ResponseWriter, r *http.Request, msg string) {
	protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ValidationException", Message: msg, HTTPStatus: http.StatusBadRequest})
}

func (s *Service) getVault(ctx context.Context, name string) (*backupVault, bool, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsVaults, name)
	if err != nil {
		return nil, false, protocol.ErrInternalError
	}
	if !ok {
		return nil, false, nil
	}
	var v backupVault
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, false, protocol.ErrInternalError
	}
	return &v, true, nil
}

func (s *Service) putVault(ctx context.Context, v *backupVault) *protocol.AWSError {
	b, err := json.Marshal(v)
	if err != nil {
		return protocol.ErrInternalError
	}
	if err := s.store.Set(ctx, nsVaults, v.BackupVaultName, string(b)); err != nil {
		return protocol.ErrInternalError
	}
	return nil
}

func (s *Service) getPlan(ctx context.Context, id string) (*backupPlan, bool, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsPlans, id)
	if err != nil {
		return nil, false, protocol.ErrInternalError
	}
	if !ok {
		return nil, false, nil
	}
	var p backupPlan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, false, protocol.ErrInternalError
	}
	return &p, true, nil
}

func (s *Service) putPlan(ctx context.Context, p *backupPlan) *protocol.AWSError {
	b, err := json.Marshal(p)
	if err != nil {
		return protocol.ErrInternalError
	}
	if err := s.store.Set(ctx, nsPlans, p.BackupPlanId, string(b)); err != nil {
		return protocol.ErrInternalError
	}
	return nil
}

func (s *Service) region() string {
	if s.cfg != nil && s.cfg.Region != "" {
		return s.cfg.Region
	}
	return defaultRegion
}

func (s *Service) accountID() string {
	if s.cfg != nil && s.cfg.AccountID != "" {
		return s.cfg.AccountID
	}
	return "000000000000"
}

func (s *Service) vaultARN(name string) string {
	return fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:%s", s.region(), s.accountID(), name)
}

func (s *Service) planARN(id string) string {
	return fmt.Sprintf("arn:aws:backup:%s:%s:backup-plan:%s", s.region(), s.accountID(), id)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil {
		return true
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(out); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "SerializationException", Message: "Invalid JSON request body", HTTPStatus: http.StatusBadRequest})
		return false
	}
	return true
}
