package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
)

type createBackupVaultRequest struct {
	BackupVaultName  string `json:"BackupVaultName" cbor:"BackupVaultName"`
	EncryptionKeyArn string `json:"EncryptionKeyArn" cbor:"EncryptionKeyArn"`
}

type createBackupVaultResponse struct {
	BackupVaultName string `json:"BackupVaultName" cbor:"BackupVaultName"`
	BackupVaultArn  string `json:"BackupVaultArn" cbor:"BackupVaultArn"`
	CreationDate    string `json:"CreationDate" cbor:"CreationDate"`
}

type deleteBackupVaultRequest struct {
	BackupVaultName string `json:"BackupVaultName" cbor:"BackupVaultName"`
}

type deleteBackupVaultResponse struct {
	BackupVaultName string `json:"BackupVaultName" cbor:"BackupVaultName"`
	BackupVaultArn  string `json:"BackupVaultArn" cbor:"BackupVaultArn"`
	DeletionDate    string `json:"DeletionDate" cbor:"DeletionDate"`
}

type describeBackupVaultRequest struct {
	BackupVaultName string `json:"BackupVaultName" cbor:"BackupVaultName"`
}

type listBackupVaultsRequest struct{}

type listBackupVaultsResponse struct {
	BackupVaultList []backupVault `json:"BackupVaultList" cbor:"BackupVaultList"`
}

type createBackupPlanRequest struct {
	BackupPlan struct {
		BackupPlanName string           `json:"BackupPlanName" cbor:"BackupPlanName"`
		Rules          []map[string]any `json:"Rules" cbor:"Rules"`
	} `json:"BackupPlan" cbor:"BackupPlan"`
}

type createBackupPlanResponse struct {
	BackupPlanId  string `json:"BackupPlanId" cbor:"BackupPlanId"`
	BackupPlanArn string `json:"BackupPlanArn" cbor:"BackupPlanArn"`
	CreationDate  string `json:"CreationDate" cbor:"CreationDate"`
	VersionId     string `json:"VersionId" cbor:"VersionId"`
}

type getBackupPlanRequest struct {
	BackupPlanId string `json:"BackupPlanId" cbor:"BackupPlanId"`
}

type getBackupPlanResponse struct {
	BackupPlan    map[string]any `json:"BackupPlan" cbor:"BackupPlan"`
	BackupPlanId  string         `json:"BackupPlanId" cbor:"BackupPlanId"`
	BackupPlanArn string         `json:"BackupPlanArn" cbor:"BackupPlanArn"`
	VersionId     string         `json:"VersionId" cbor:"VersionId"`
	CreationDate  string         `json:"CreationDate" cbor:"CreationDate"`
}

type updateBackupPlanRequest struct {
	BackupPlanId string `json:"BackupPlanId" cbor:"BackupPlanId"`
	BackupPlan   struct {
		BackupPlanName string           `json:"BackupPlanName" cbor:"BackupPlanName"`
		Rules          []map[string]any `json:"Rules" cbor:"Rules"`
	} `json:"BackupPlan" cbor:"BackupPlan"`
}

type updateBackupPlanResponse struct {
	BackupPlanId  string `json:"BackupPlanId" cbor:"BackupPlanId"`
	BackupPlanArn string `json:"BackupPlanArn" cbor:"BackupPlanArn"`
	VersionId     string `json:"VersionId" cbor:"VersionId"`
}

type deleteBackupPlanRequest struct {
	BackupPlanId string `json:"BackupPlanId" cbor:"BackupPlanId"`
}

type deleteBackupPlanResponse struct {
	BackupPlanId  string `json:"BackupPlanId" cbor:"BackupPlanId"`
	BackupPlanArn string `json:"BackupPlanArn" cbor:"BackupPlanArn"`
	DeletionDate  string `json:"DeletionDate" cbor:"DeletionDate"`
}

type listBackupPlansRequest struct{}

type listBackupPlansResponse struct {
	BackupPlansList []map[string]any `json:"BackupPlansList" cbor:"BackupPlansList"`
}

func (s *Service) createBackupVaultTyped(ctx context.Context, req *createBackupVaultRequest) (*createBackupVaultResponse, *protocol.AWSError) {
	if strings.TrimSpace(req.BackupVaultName) == "" {
		return nil, validationErr("BackupVaultName is required")
	}
	if _, exists, aerr := s.getVault(ctx, req.BackupVaultName); aerr != nil {
		return nil, aerr
	} else if exists {
		return nil, &protocol.AWSError{Code: "AlreadyExistsException", Message: "Backup vault already exists", HTTPStatus: http.StatusBadRequest}
	}
	v := backupVault{
		BackupVaultName:        req.BackupVaultName,
		BackupVaultArn:         s.vaultARN(req.BackupVaultName),
		CreationDate:           s.clk.Now().UTC().Format(time.RFC3339),
		EncryptionKeyArn:       req.EncryptionKeyArn,
		NumberOfRecoveryPoints: 0,
	}
	if aerr := s.putVault(ctx, &v); aerr != nil {
		return nil, aerr
	}
	return &createBackupVaultResponse{
		BackupVaultName: v.BackupVaultName,
		BackupVaultArn:  v.BackupVaultArn,
		CreationDate:    v.CreationDate,
	}, nil
}

func (s *Service) deleteBackupVaultTyped(ctx context.Context, req *deleteBackupVaultRequest) (*deleteBackupVaultResponse, *protocol.AWSError) {
	if strings.TrimSpace(req.BackupVaultName) == "" {
		return nil, validationErr("BackupVaultName is required")
	}
	v, exists, aerr := s.getVault(ctx, req.BackupVaultName)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup vault not found", HTTPStatus: http.StatusNotFound}
	}
	if err := s.store.Delete(ctx, nsVaults, req.BackupVaultName); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &deleteBackupVaultResponse{
		BackupVaultName: v.BackupVaultName,
		BackupVaultArn:  v.BackupVaultArn,
		DeletionDate:    s.clk.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *Service) describeBackupVaultTyped(ctx context.Context, req *describeBackupVaultRequest) (*backupVault, *protocol.AWSError) {
	if strings.TrimSpace(req.BackupVaultName) == "" {
		return nil, validationErr("BackupVaultName is required")
	}
	v, exists, aerr := s.getVault(ctx, req.BackupVaultName)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup vault not found", HTTPStatus: http.StatusNotFound}
	}
	return v, nil
}

func (s *Service) listBackupVaultsTyped(ctx context.Context, _ *listBackupVaultsRequest) (*listBackupVaultsResponse, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsVaults, "")
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	out := make([]backupVault, 0, len(kvs))
	for _, kv := range kvs {
		var v backupVault
		if json.Unmarshal([]byte(kv.Value), &v) == nil {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BackupVaultName < out[j].BackupVaultName })
	return &listBackupVaultsResponse{BackupVaultList: out}, nil
}

func (s *Service) createBackupPlanTyped(ctx context.Context, req *createBackupPlanRequest) (*createBackupPlanResponse, *protocol.AWSError) {
	if strings.TrimSpace(req.BackupPlan.BackupPlanName) == "" {
		return nil, validationErr("BackupPlan.BackupPlanName is required")
	}
	planID := fmt.Sprintf("plan-%d", s.clk.Now().UnixNano())
	p := backupPlan{
		BackupPlanId:   planID,
		BackupPlanArn:  s.planARN(planID),
		BackupPlanName: req.BackupPlan.BackupPlanName,
		Rules:          req.BackupPlan.Rules,
		VersionId:      "v1",
		CreatedAt:      s.clk.Now().UTC().Format(time.RFC3339),
	}
	if aerr := s.putPlan(ctx, &p); aerr != nil {
		return nil, aerr
	}
	return &createBackupPlanResponse{
		BackupPlanId:  p.BackupPlanId,
		BackupPlanArn: p.BackupPlanArn,
		CreationDate:  p.CreatedAt,
		VersionId:     p.VersionId,
	}, nil
}

func (s *Service) getBackupPlanTyped(ctx context.Context, req *getBackupPlanRequest) (*getBackupPlanResponse, *protocol.AWSError) {
	p, exists, aerr := s.getPlan(ctx, req.BackupPlanId)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup plan not found", HTTPStatus: http.StatusNotFound}
	}
	return &getBackupPlanResponse{
		BackupPlan: map[string]any{
			"BackupPlanName": p.BackupPlanName,
			"Rules":          p.Rules,
		},
		BackupPlanId:  p.BackupPlanId,
		BackupPlanArn: p.BackupPlanArn,
		VersionId:     p.VersionId,
		CreationDate:  p.CreatedAt,
	}, nil
}

func (s *Service) updateBackupPlanTyped(ctx context.Context, req *updateBackupPlanRequest) (*updateBackupPlanResponse, *protocol.AWSError) {
	p, exists, aerr := s.getPlan(ctx, req.BackupPlanId)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup plan not found", HTTPStatus: http.StatusNotFound}
	}
	if strings.TrimSpace(req.BackupPlan.BackupPlanName) != "" {
		p.BackupPlanName = req.BackupPlan.BackupPlanName
	}
	p.Rules = req.BackupPlan.Rules
	p.VersionId = "v2"
	if aerr := s.putPlan(ctx, p); aerr != nil {
		return nil, aerr
	}
	return &updateBackupPlanResponse{
		BackupPlanId:  p.BackupPlanId,
		BackupPlanArn: p.BackupPlanArn,
		VersionId:     p.VersionId,
	}, nil
}

func (s *Service) deleteBackupPlanTyped(ctx context.Context, req *deleteBackupPlanRequest) (*deleteBackupPlanResponse, *protocol.AWSError) {
	p, exists, aerr := s.getPlan(ctx, req.BackupPlanId)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Backup plan not found", HTTPStatus: http.StatusNotFound}
	}
	if err := s.store.Delete(ctx, nsPlans, req.BackupPlanId); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &deleteBackupPlanResponse{
		BackupPlanId:  p.BackupPlanId,
		BackupPlanArn: p.BackupPlanArn,
		DeletionDate:  s.clk.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *Service) listBackupPlansTyped(ctx context.Context, _ *listBackupPlansRequest) (*listBackupPlansResponse, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsPlans, "")
	if err != nil {
		return nil, protocol.ErrInternalError
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
	return &listBackupPlansResponse{BackupPlansList: out}, nil
}

func validationErr(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "ValidationException", Message: msg, HTTPStatus: http.StatusBadRequest}
}
