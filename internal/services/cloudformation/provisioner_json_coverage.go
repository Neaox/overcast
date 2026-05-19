package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/config"
)

// ── AWS::CertificateManager::Certificate ─────────────────────────────────────

type acmCertificateHandler struct{}

func (h *acmCertificateHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["DomainName"].(string); v != "" {
		body["DomainName"] = v
	}
	if v, ok := props["SubjectAlternativeNames"]; ok {
		body["SubjectAlternativeNames"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "CertificateManager.RequestCertificate", body)
	if err != nil {
		return "", nil, fmt.Errorf("RequestCertificate: %w", err)
	}

	var resp struct {
		CertificateArn string `json:"CertificateArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("RequestCertificate: parse response: %w", err)
	}

	arn := resp.CertificateArn
	if arn == "" {
		domain, _ := props["DomainName"].(string)
		arn = fmt.Sprintf("arn:aws:acm:%s:%s:certificate/%s", rCtx.Region, rCtx.AccountID, domain)
	}

	attrs := map[string]string{
		"Arn": arn,
	}
	return arn, attrs, nil
}

func (h *acmCertificateHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"CertificateArn": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "CertificateManager.DeleteCertificate", body)
	return nil
}

func (h *acmCertificateHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::ECR::Repository ────────────────────────────────────────────────────

type ecrRepositoryHandler struct{}

func (h *ecrRepositoryHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["RepositoryName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-ecr", rCtx.StackName)
	}

	body := map[string]any{
		"repositoryName": name,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerRegistry_V20150921.CreateRepository", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateRepository: %w", err)
	}

	var resp struct {
		Repository struct {
			RepositoryArn string `json:"repositoryArn"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateRepository: parse response: %w", err)
	}

	arn := resp.Repository.RepositoryArn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", rCtx.Region, rCtx.AccountID, name)
	}

	attrs := map[string]string{
		"Arn":            arn,
		"RepositoryUri":  fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s", rCtx.AccountID, rCtx.Region, name),
		"RepositoryName": name,
	}
	return arn, attrs, nil
}

func (h *ecrRepositoryHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// Extract repository name from ARN if possible, otherwise use physicalID as name.
	name := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		name = physicalID[idx+1:]
	}
	body := map[string]any{
		"repositoryName": name,
		"force":          true,
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerRegistry_V20150921.DeleteRepository", body)
	return nil
}

func (h *ecrRepositoryHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::CloudTrail::Trail ──────────────────────────────────────────────────

type cloudtrailTrailHandler struct{}

func (h *cloudtrailTrailHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-trail", rCtx.StackName)
	}
	s3Bucket, _ := props["S3BucketName"].(string)
	if s3Bucket == "" {
		s3Bucket = fmt.Sprintf("%s-bucket", rCtx.StackName)
	}

	includeGlobal := true
	if v, ok := props["IncludeGlobalServiceEvents"].(bool); ok {
		includeGlobal = v
	}
	isMultiRegion := false
	if v, ok := props["IsMultiRegionTrail"].(bool); ok {
		isMultiRegion = v
	}

	body := map[string]any{
		"Name":                       name,
		"S3BucketName":               s3Bucket,
		"IncludeGlobalServiceEvents": includeGlobal,
		"IsMultiRegionTrail":         isMultiRegion,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101.CreateTrail", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateTrail: %w", err)
	}

	var resp struct {
		TrailARN string `json:"TrailARN"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateTrail: parse response: %w", err)
	}

	arn := resp.TrailARN
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:cloudtrail:%s:%s:trail/%s", rCtx.Region, rCtx.AccountID, name)
	}

	attrs := map[string]string{
		"Arn":  arn,
		"Name": name,
	}
	return arn, attrs, nil
}

func (h *cloudtrailTrailHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	name := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		name = physicalID[idx+1:]
	}
	body := map[string]any{"Name": name}
	_, _ = internalJSON(ctx, router, rCtx.Region, "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101.DeleteTrail", body)
	return nil
}

func (h *cloudtrailTrailHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if n, ok := props["Name"].(string); ok && n != "" {
		tail := physicalID
		if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
			tail = physicalID[idx+1:]
		}
		if tail != n {
			return "", nil, errReplacementRequired
		}
	}

	name, _ := props["Name"].(string)
	if name == "" {
		name = physicalID
		if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
			name = physicalID[idx+1:]
		}
	}
	s3Bucket, _ := props["S3BucketName"].(string)

	body := map[string]any{
		"Name":         name,
		"S3BucketName": s3Bucket,
	}
	if v, ok := props["IncludeGlobalServiceEvents"]; ok {
		body["IncludeGlobalServiceEvents"] = v
	}
	if v, ok := props["IsMultiRegionTrail"]; ok {
		body["IsMultiRegionTrail"] = v
	}

	if _, err := internalJSON(ctx, router, rCtx.Region, "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101.UpdateTrail", body); err != nil {
		return "", nil, fmt.Errorf("UpdateTrail: %w", err)
	}
	return physicalID, map[string]string{"Arn": physicalID}, nil
}

// ── AWS::Backup::BackupVault ────────────────────────────────────────────────

type backupBackupVaultHandler struct{}

func (h *backupBackupVaultHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["BackupVaultName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-vault", rCtx.StackName)
	}

	body := map[string]any{
		"BackupVaultName": name,
	}
	if v, ok := props["EncryptionKeyArn"].(string); ok && v != "" {
		body["EncryptionKeyArn"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AWSBackup.CreateBackupVault", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateBackupVault: %w", err)
	}

	var resp struct {
		BackupVaultArn  string `json:"BackupVaultArn"`
		BackupVaultName string `json:"BackupVaultName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateBackupVault: parse response: %w", err)
	}

	arn := resp.BackupVaultArn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:%s", rCtx.Region, rCtx.AccountID, name)
	}

	attrs := map[string]string{
		"BackupVaultArn":  arn,
		"BackupVaultName": name,
	}
	return arn, attrs, nil
}

func (h *backupBackupVaultHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	name := physicalID
	if idx := strings.LastIndex(physicalID, ":"); idx >= 0 {
		name = physicalID[idx+1:]
	}
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	body := map[string]any{"BackupVaultName": name}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AWSBackup.DeleteBackupVault", body)
	return nil
}

func (h *backupBackupVaultHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Backup::BackupPlan ─────────────────────────────────────────────────

type backupBackupPlanHandler struct{}

func (h *backupBackupPlanHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	backupPlan, _ := props["BackupPlan"].(map[string]any)
	body := map[string]any{
		"BackupPlan": backupPlan,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AWSBackup.CreateBackupPlan", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateBackupPlan: %w", err)
	}

	var resp struct {
		BackupPlanArn string `json:"BackupPlanArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateBackupPlan: parse response: %w", err)
	}

	arn := resp.BackupPlanArn
	if arn == "" {
		name := fmt.Sprintf("%s-plan", rCtx.StackName)
		if bp, ok := props["BackupPlan"].(map[string]any); ok {
			if n, _ := bp["BackupPlanName"].(string); n != "" {
				name = n
			}
		}
		arn = fmt.Sprintf("arn:aws:backup:%s:%s:backup-plan:%s", rCtx.Region, rCtx.AccountID, name)
	}

	attrs := map[string]string{
		"BackupPlanArn": arn,
	}
	return arn, attrs, nil
}

func (h *backupBackupPlanHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"BackupPlanId": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AWSBackup.DeleteBackupPlan", body)
	return nil
}

func (h *backupBackupPlanHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Transfer::Server ───────────────────────────────────────────────────

type transferServerHandler struct{}

func (h *transferServerHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	endpointType, _ := props["EndpointType"].(string)
	if endpointType == "" {
		endpointType = "PUBLIC"
	}
	identityProviderType, _ := props["IdentityProviderType"].(string)
	if identityProviderType == "" {
		identityProviderType = "SERVICE_MANAGED"
	}

	body := map[string]any{
		"EndpointType":         endpointType,
		"IdentityProviderType": identityProviderType,
	}
	if v, ok := props["Protocols"]; ok {
		body["Protocols"] = v
	}
	if v, ok := props["Tags"]; ok {
		body["Tags"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "TransferService.CreateServer", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateServer: %w", err)
	}

	var resp struct {
		ServerId string `json:"ServerId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateServer: parse response: %w", err)
	}

	attrs := map[string]string{
		"ServerId": resp.ServerId,
		"Arn":      fmt.Sprintf("arn:aws:transfer:%s:%s:server/%s", rCtx.Region, rCtx.AccountID, resp.ServerId),
	}
	return resp.ServerId, attrs, nil
}

func (h *transferServerHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"ServerId": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "TransferService.DeleteServer", body)
	return nil
}

func (h *transferServerHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Transfer::User ─────────────────────────────────────────────────────

type transferUserHandler struct{}

func (h *transferUserHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	serverID, _ := props["ServerId"].(string)
	userName, _ := props["UserName"].(string)
	role, _ := props["Role"].(string)

	body := map[string]any{
		"ServerId": serverID,
		"UserName": userName,
		"Role":     role,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "TransferService.CreateUser", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateUser: %w", err)
	}

	var resp struct {
		ServerId string `json:"ServerId"`
		UserName string `json:"UserName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateUser: parse response: %w", err)
	}

	sid := resp.ServerId
	if sid == "" {
		sid = serverID
	}
	uname := resp.UserName
	if uname == "" {
		uname = userName
	}

	physicalID := sid + "/" + uname
	attrs := map[string]string{
		"ServerId": sid,
		"UserName": uname,
	}
	return physicalID, attrs, nil
}

func (h *transferUserHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	body := map[string]any{
		"ServerId": parts[0],
		"UserName": parts[1],
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "TransferService.DeleteUser", body)
	return nil
}

func (h *transferUserHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Shield::Protection ─────────────────────────────────────────────────

type shieldProtectionHandler struct{}

func (h *shieldProtectionHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-shield", rCtx.StackName)
	}
	resourceArn, _ := props["ResourceArn"].(string)

	body := map[string]any{
		"Name":        name,
		"ResourceArn": resourceArn,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AWSShield_20160616.CreateProtection", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateProtection: %w", err)
	}

	var resp struct {
		ProtectionId string `json:"ProtectionId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateProtection: parse response: %w", err)
	}

	attrs := map[string]string{
		"ProtectionId": resp.ProtectionId,
	}
	return resp.ProtectionId, attrs, nil
}

func (h *shieldProtectionHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"ProtectionId": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AWSShield_20160616.DeleteProtection", body)
	return nil
}

func (h *shieldProtectionHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::KinesisFirehose::DeliveryStream ────────────────────────────────────

type firehoseDeliveryStreamHandler struct{}

func (h *firehoseDeliveryStreamHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["DeliveryStreamName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-stream", rCtx.StackName)
	}
	streamType, _ := props["DeliveryStreamType"].(string)
	if streamType == "" {
		streamType = "DirectPut"
	}

	body := map[string]any{
		"DeliveryStreamName": name,
		"DeliveryStreamType": streamType,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "Firehose_20150804.CreateDeliveryStream", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDeliveryStream: %w", err)
	}

	var resp struct {
		DeliveryStreamARN string `json:"DeliveryStreamARN"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateDeliveryStream: parse response: %w", err)
	}

	arn := resp.DeliveryStreamARN
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/%s", rCtx.Region, rCtx.AccountID, name)
	}

	attrs := map[string]string{
		"Arn": arn,
	}
	return arn, attrs, nil
}

func (h *firehoseDeliveryStreamHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"DeliveryStreamName": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "Firehose_20150804.DeleteDeliveryStream", body)
	return nil
}

func (h *firehoseDeliveryStreamHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Athena::WorkGroup ──────────────────────────────────────────────────

type athenaWorkGroupHandler struct{}

func (h *athenaWorkGroupHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-wg", rCtx.StackName)
	}

	body := map[string]any{
		"Name": name,
	}
	if v, ok := props["Description"].(string); ok && v != "" {
		body["Description"] = v
	}
	if v, ok := props["Configuration"]; ok {
		body["Configuration"] = v
	}

	_, err := internalJSON(ctx, router, rCtx.Region, "AmazonAthena.CreateWorkGroup", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateWorkGroup: %w", err)
	}

	attrs := map[string]string{
		"Name": name,
	}
	return name, attrs, nil
}

func (h *athenaWorkGroupHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"WorkGroup": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AmazonAthena.DeleteWorkGroup", body)
	return nil
}

func (h *athenaWorkGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Glue::Database ─────────────────────────────────────────────────────

type glueDatabaseHandler struct{}

func (h *glueDatabaseHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	databaseInput, _ := props["DatabaseInput"].(map[string]any)

	body := map[string]any{
		"DatabaseInput": databaseInput,
	}
	catalogID, _ := props["CatalogId"].(string)
	body["CatalogId"] = catalogID

	_, err := internalJSON(ctx, router, rCtx.Region, "AWSGlue.CreateDatabase", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDatabase: %w", err)
	}

	dbName := ""
	if databaseInput != nil {
		dbName, _ = databaseInput["Name"].(string)
	}
	if dbName == "" {
		dbName = fmt.Sprintf("%s-db", rCtx.StackName)
	}

	attrs := map[string]string{
		"Name": dbName,
	}
	return dbName, attrs, nil
}

func (h *glueDatabaseHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"Name": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AWSGlue.DeleteDatabase", body)
	return nil
}

func (h *glueDatabaseHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Glue::Table ────────────────────────────────────────────────────────

type glueTableHandler struct{}

func (h *glueTableHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	databaseName, _ := props["DatabaseName"].(string)
	tableInput, _ := props["TableInput"].(map[string]any)
	catalogID, _ := props["CatalogId"].(string)

	body := map[string]any{
		"DatabaseName": databaseName,
		"TableInput":   tableInput,
	}
	if catalogID != "" {
		body["CatalogId"] = catalogID
	}

	_, err := internalJSON(ctx, router, rCtx.Region, "AWSGlue.CreateTable", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateTable: %w", err)
	}

	tableName := ""
	if tableInput != nil {
		tableName, _ = tableInput["Name"].(string)
	}
	physicalID := databaseName + "/" + tableName
	attrs := map[string]string{
		"TableName": tableName,
	}
	return physicalID, attrs, nil
}

func (h *glueTableHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	body := map[string]any{
		"DatabaseName": parts[0],
		"Name":         parts[1],
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AWSGlue.DeleteTable", body)
	return nil
}

func (h *glueTableHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::CloudWatch::Alarm ──────────────────────────────────────────────────

type cloudwatchAlarmHandler struct{}

func (h *cloudwatchAlarmHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	alarmName, _ := props["AlarmName"].(string)
	metricName, _ := props["MetricName"].(string)
	namespace, _ := props["Namespace"].(string)
	statistic, _ := props["Statistic"].(string)
	comparisonOperator, _ := props["ComparisonOperator"].(string)

	body := map[string]any{
		"AlarmName":          alarmName,
		"MetricName":         metricName,
		"Namespace":          namespace,
		"Statistic":          statistic,
		"ComparisonOperator": comparisonOperator,
	}
	if v, ok := props["Period"]; ok {
		body["Period"] = v
	}
	if v, ok := props["EvaluationPeriods"]; ok {
		body["EvaluationPeriods"] = v
	}
	if v, ok := props["Threshold"]; ok {
		body["Threshold"] = v
	}
	if v, ok := props["AlarmDescription"]; ok {
		body["AlarmDescription"] = v
	}
	if v, ok := props["ActionsEnabled"]; ok {
		body["ActionsEnabled"] = v
	}
	if v, ok := props["OKActions"]; ok {
		body["OKActions"] = v
	}
	if v, ok := props["AlarmActions"]; ok {
		body["AlarmActions"] = v
	}
	if v, ok := props["InsufficientDataActions"]; ok {
		body["InsufficientDataActions"] = v
	}
	if v, ok := props["Unit"]; ok {
		body["Unit"] = v
	}
	if v, ok := props["Dimensions"]; ok {
		body["Dimensions"] = v
	}

	_, err := internalJSON(ctx, router, rCtx.Region, "GraniteServiceVersion20100801.PutMetricAlarm", body)
	if err != nil {
		return "", nil, fmt.Errorf("PutMetricAlarm: %w", err)
	}

	arn := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:%s", rCtx.Region, rCtx.AccountID, alarmName)
	attrs := map[string]string{
		"Arn": arn,
	}
	return alarmName, attrs, nil
}

func (h *cloudwatchAlarmHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{
		"AlarmNames": []string{physicalID},
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "GraniteServiceVersion20100801.DeleteAlarms", body)
	return nil
}

func (h *cloudwatchAlarmHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Scheduler::Schedule ────────────────────────────────────────────────

type schedulerScheduleHandler struct{}

func (h *schedulerScheduleHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	groupName, _ := props["GroupName"].(string)
	if groupName == "" {
		groupName = "default"
	}
	scheduleExpression, _ := props["ScheduleExpression"].(string)
	state, _ := props["State"].(string)
	if state == "" {
		state = "ENABLED"
	}

	body := map[string]any{
		"Name":               name,
		"GroupName":          groupName,
		"ScheduleExpression": scheduleExpression,
		"State":              state,
	}
	if v, ok := props["FlexibleTimeWindow"]; ok {
		body["FlexibleTimeWindow"] = v
	}
	if v, ok := props["Target"]; ok {
		body["Target"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "Scheduler.CreateSchedule", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateSchedule: %w", err)
	}

	var resp struct {
		ScheduleArn string `json:"ScheduleArn"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	physicalID := groupName + "/" + name
	attrs := map[string]string{
		"Arn":       resp.ScheduleArn,
		"GroupName": groupName,
		"Name":      name,
	}
	return physicalID, attrs, nil
}

func (h *schedulerScheduleHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	body := map[string]any{
		"Name":      parts[1],
		"GroupName": parts[0],
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "Scheduler.DeleteSchedule", body)
	return nil
}

func (h *schedulerScheduleHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Scheduler::ScheduleGroup ───────────────────────────────────────────

type schedulerScheduleGroupHandler struct{}

func (h *schedulerScheduleGroupHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)

	body := map[string]any{
		"Name": name,
	}
	if v, ok := props["Tags"]; ok {
		body["Tags"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "Scheduler.CreateScheduleGroup", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateScheduleGroup: %w", err)
	}

	var resp struct {
		ScheduleGroupArn string `json:"ScheduleGroupArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateScheduleGroup: parse response: %w", err)
	}

	arn := resp.ScheduleGroupArn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule-group/%s", rCtx.Region, rCtx.AccountID, name)
	}

	attrs := map[string]string{
		"Arn":  arn,
		"Name": name,
	}
	return arn, attrs, nil
}

func (h *schedulerScheduleGroupHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"Name": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "Scheduler.DeleteScheduleGroup", body)
	return nil
}

func (h *schedulerScheduleGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::OpenSearchService::Domain ──────────────────────────────────────────

type opensearchDomainHandler struct{}

func (h *opensearchDomainHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	domainName, _ := props["DomainName"].(string)
	engineVersion, _ := props["EngineVersion"].(string)

	body := map[string]any{
		"DomainName":    domainName,
		"EngineVersion": engineVersion,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "OpenSearch.CreateDomain", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDomain: %w", err)
	}

	var resp struct {
		DomainStatus struct {
			ARN string `json:"ARN"`
		} `json:"DomainStatus"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateDomain: parse response: %w", err)
	}

	arn := resp.DomainStatus.ARN
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:es:%s:%s:domain/%s", rCtx.Region, rCtx.AccountID, domainName)
	}

	attrs := map[string]string{
		"Arn":        arn,
		"DomainName": domainName,
	}
	return arn, attrs, nil
}

func (h *opensearchDomainHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"DomainName": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "OpenSearch.DeleteDomain", body)
	return nil
}

func (h *opensearchDomainHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::AppConfig::Application ─────────────────────────────────────────────

type appconfigApplicationHandler struct{}

func (h *appconfigApplicationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	desc, _ := props["Description"].(string)

	body := map[string]any{
		"Name": name,
	}
	if desc != "" {
		body["Description"] = desc
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AppConfig.CreateApplication", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateApplication: %w", err)
	}

	var resp struct {
		Id   string `json:"Id"`
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateApplication: parse response: %w", err)
	}

	attrs := map[string]string{
		"Id":   resp.Id,
		"Name": resp.Name,
	}
	return resp.Id, attrs, nil
}

func (h *appconfigApplicationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"ApplicationId": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AppConfig.DeleteApplication", body)
	return nil
}

func (h *appconfigApplicationHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::AppConfig::Environment ─────────────────────────────────────────────

type appconfigEnvironmentHandler struct{}

func (h *appconfigEnvironmentHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	appID, _ := props["ApplicationId"].(string)
	name, _ := props["Name"].(string)

	body := map[string]any{
		"ApplicationId": appID,
		"Name":          name,
	}
	if desc, _ := props["Description"].(string); desc != "" {
		body["Description"] = desc
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AppConfig.CreateEnvironment", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateEnvironment: %w", err)
	}

	var resp struct {
		Id string `json:"Id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateEnvironment: parse response: %w", err)
	}

	physicalID := appID + "/" + resp.Id
	attrs := map[string]string{
		"Id":            resp.Id,
		"ApplicationId": appID,
	}
	return physicalID, attrs, nil
}

func (h *appconfigEnvironmentHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	body := map[string]any{
		"ApplicationId": parts[0],
		"EnvironmentId": parts[1],
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AppConfig.DeleteEnvironment", body)
	return nil
}

func (h *appconfigEnvironmentHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::AppConfig::ConfigurationProfile ────────────────────────────────────

type appconfigConfigurationProfileHandler struct{}

func (h *appconfigConfigurationProfileHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	appID, _ := props["ApplicationId"].(string)
	name, _ := props["Name"].(string)
	locationURI, _ := props["LocationUri"].(string)

	body := map[string]any{
		"ApplicationId": appID,
		"Name":          name,
		"LocationUri":   locationURI,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AppConfig.CreateConfigurationProfile", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateConfigurationProfile: %w", err)
	}

	var resp struct {
		Id string `json:"Id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateConfigurationProfile: parse response: %w", err)
	}

	physicalID := appID + "/" + resp.Id
	attrs := map[string]string{
		"Id":            resp.Id,
		"ApplicationId": appID,
	}
	return physicalID, attrs, nil
}

func (h *appconfigConfigurationProfileHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	body := map[string]any{
		"ApplicationId":          parts[0],
		"ConfigurationProfileId": parts[1],
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AppConfig.DeleteConfigurationProfile", body)
	return nil
}

func (h *appconfigConfigurationProfileHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}
