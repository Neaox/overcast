package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"

	"github.com/Neaox/overcast/internal/awsapi"
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

const ecrTargetPrefix = "AmazonEC2ContainerRegistry_V20150921."

type ecrRepositoryHandler struct{}

// ecrPolicyText renders a policy property as the JSON text the ECR API takes.
// Templates supply the policy either inline as an object or pre-rendered as a
// string.
func ecrPolicyText(v any) (string, bool) {
	switch p := v.(type) {
	case string:
		return p, p != ""
	case map[string]any:
		b, err := json.Marshal(p)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	return "", false
}

// ecrApplyRepositoryPolicies pushes the mutable policy properties
// (RepositoryPolicyText, LifecyclePolicy) onto an existing repository.
// oldProps is consulted only to remove a policy the previous template carried
// and the new one dropped.
func ecrApplyRepositoryPolicies(ctx context.Context, router http.Handler, rCtx *resolveContext, name string, props, oldProps map[string]any) error {
	if text, ok := ecrPolicyText(props["RepositoryPolicyText"]); ok {
		body := map[string]any{"repositoryName": name, "policyText": text}
		if _, err := internalJSON(ctx, router, rCtx.Region, ecrTargetPrefix+"SetRepositoryPolicy", body); err != nil {
			return fmt.Errorf("SetRepositoryPolicy: %w", err)
		}
	} else if _, had := oldProps["RepositoryPolicyText"]; had {
		_, _ = internalJSON(ctx, router, rCtx.Region, ecrTargetPrefix+"DeleteRepositoryPolicy", map[string]any{"repositoryName": name})
	}

	lifecycleText := ""
	if lp, ok := props["LifecyclePolicy"].(map[string]any); ok {
		lifecycleText, _ = lp["LifecyclePolicyText"].(string)
	}
	if lifecycleText != "" {
		body := map[string]any{"repositoryName": name, "lifecyclePolicyText": lifecycleText}
		if _, err := internalJSON(ctx, router, rCtx.Region, ecrTargetPrefix+"PutLifecyclePolicy", body); err != nil {
			return fmt.Errorf("PutLifecyclePolicy: %w", err)
		}
	} else if _, had := oldProps["LifecyclePolicy"]; had {
		_, _ = internalJSON(ctx, router, rCtx.Region, ecrTargetPrefix+"DeleteLifecyclePolicy", map[string]any{"repositoryName": name})
	}
	return nil
}

func (h *ecrRepositoryHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["RepositoryName"].(string)
	if name == "" {
		// Lowercase: ECR rejects uppercase repository names, and both halves
		// of a generated name are mixed case.
		name = rCtx.generatedNameLowerWithin(maxNameLenECR)
	}

	body := map[string]any{
		"repositoryName": name,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, ecrTargetPrefix+"CreateRepository", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateRepository: %w", err)
	}

	if err := ecrApplyRepositoryPolicies(ctx, router, rCtx, name, props, nil); err != nil {
		return "", nil, err
	}

	var resp struct {
		Repository struct {
			RepositoryArn string `json:"repositoryArn"`
			RepositoryUri string `json:"repositoryUri"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateRepository: parse response: %w", err)
	}

	arn := resp.Repository.RepositoryArn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", rCtx.Region, rCtx.AccountID, name)
	}

	// RepositoryUri comes from the CreateRepository response rather than being
	// rebuilt here. ECR mints it on the address its registry is actually
	// listening on (see Service.registryEndpoint); synthesising
	// "{account}.dkr.ecr.{region}.amazonaws.com/{name}" instead meant
	// Fn::GetAtt RepositoryUri handed stacks a registry no `docker push` in
	// this environment can reach, while `aws ecr describe-repositories` on the
	// same repository returned the working one.
	uri := resp.Repository.RepositoryUri
	if uri == "" {
		uri = fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s", rCtx.AccountID, rCtx.Region, name)
	}

	attrs := map[string]string{
		"Arn":            arn,
		"RepositoryUri":  uri,
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
	_, _ = internalJSON(ctx, router, rCtx.Region, ecrTargetPrefix+"DeleteRepository", body)
	return nil
}

func (h *ecrRepositoryHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Physical ID is the repository ARN, "arn:…:repository/{name}".
	oldName := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		oldName = physicalID[idx+1:]
	}

	// RepositoryName is the only AWS::ECR::Repository property real
	// CloudFormation replaces on. Answering "replace" for anything else breaks
	// re-running `cdk bootstrap` after a CDK upgrade: the toolkit stack pins
	// the repository name, so the replacement re-creates the same name and
	// fails with RepositoryAlreadyExistsException.
	if n, ok := props["RepositoryName"].(string); ok && n != "" && n != oldName {
		return "", nil, errReplacementRequired
	}

	if err := ecrApplyRepositoryPolicies(ctx, router, rCtx, oldName, props, oldProps); err != nil {
		return "", nil, err
	}

	// Same reason as in Create: RepositoryUri must be the one ECR minted, so
	// re-read it rather than synthesising the amazonaws.com form.
	uri := ""
	if rec, err := internalJSON(ctx, router, rCtx.Region, ecrTargetPrefix+"DescribeRepositories", map[string]any{"repositoryNames": []string{oldName}}); err == nil {
		var resp struct {
			Repositories []struct {
				RepositoryUri string `json:"repositoryUri"`
			} `json:"repositories"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &resp) == nil && len(resp.Repositories) == 1 {
			uri = resp.Repositories[0].RepositoryUri
		}
	}
	if uri == "" {
		uri = fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s", rCtx.AccountID, rCtx.Region, oldName)
	}

	attrs := map[string]string{
		"Arn":            physicalID,
		"RepositoryUri":  uri,
		"RepositoryName": oldName,
	}
	return physicalID, attrs, nil
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
	// Physical ID is the vault ARN, "arn:…:backup-vault:{name}".
	oldName := physicalID
	if idx := strings.LastIndex(oldName, ":"); idx >= 0 {
		oldName = oldName[idx+1:]
	}
	if n, ok := props["BackupVaultName"].(string); ok && n != "" && n != oldName {
		return "", nil, errReplacementRequired
	}
	// BackupVaultName is the only replacement property on real AWS, and
	// CreateBackupVault rejects duplicates, so replacing under an unchanged
	// name can never succeed. Nothing else this handler provisions is mutable
	// (EncryptionKeyArn is create-only), so keeping the vault is the update.
	return physicalID, nil, nil
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
	// Physical ID is "{serverID}/{userName}"; both replace on real AWS, and
	// CreateUser rejects duplicates, so replacing under an unchanged pair can
	// never succeed.
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return "", nil, errReplacementRequired
	}
	if sid, ok := props["ServerId"].(string); ok && sid != "" && sid != parts[0] {
		return "", nil, errReplacementRequired
	}
	if un, ok := props["UserName"].(string); ok && un != "" && un != parts[1] {
		return "", nil, errReplacementRequired
	}

	body := map[string]any{
		"ServerId": parts[0],
		"UserName": parts[1],
	}
	if v, _ := props["Role"].(string); v != "" {
		body["Role"] = v
	}
	if v, _ := props["HomeDirectory"].(string); v != "" {
		body["HomeDirectory"] = v
	}
	if v, _ := props["Policy"].(string); v != "" {
		body["Policy"] = v
	}
	if _, err := internalJSON(ctx, router, rCtx.Region, "TransferService.UpdateUser", body); err != nil {
		return "", nil, fmt.Errorf("UpdateUser: %w", err)
	}
	return physicalID, nil, nil
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

// cloudwatchAlarmProperties are every AWS::CloudWatch::Alarm property other
// than AlarmName, each of which maps one-for-one onto PutMetricAlarm.
//
// The list is exhaustive on purpose. Every one of them is optional on the
// resource, and a dropped optional property does not fail the stack — it
// produces an alarm that exists and is evaluated under a configuration the
// template did not ask for (a dropped TreatMissingData turns notBreaching back
// into missing; a dropped DatapointsToAlarm turns an "M out of N" alarm into
// "N out of N"). The ones Overcast refuses — Metrics, ThresholdMetricId,
// ExtendedStatistic — have to reach the service to be refused at all, or the
// stack dies claiming a missing MetricName the template never had to supply.
var cloudwatchAlarmProperties = []string{
	"ActionsEnabled", "AlarmActions", "AlarmDescription", "ComparisonOperator",
	"DatapointsToAlarm", "Dimensions", "EvaluateLowSampleCountPercentile",
	"EvaluationCriteria", "EvaluationInterval", "EvaluationPeriods",
	"EvaluationWindow", "ExtendedStatistic", "InsufficientDataActions",
	"MetricName", "Metrics", "Namespace", "OKActions", "Period", "Statistic",
	"Tags", "Threshold", "ThresholdMetricId", "TreatMissingData", "Unit",
}

// putMetricAlarm issues PutMetricAlarm for the given alarm name and template
// properties, and returns the physical ID and attributes. Shared by Create and
// Update: PutMetricAlarm is itself an upsert, so the two are the same call.
//
// A property the template omitted is left out of the request rather than sent
// empty, so the service applies AWS's own defaults instead of being told the
// caller asked for "".
func (h *cloudwatchAlarmHandler) putMetricAlarm(ctx context.Context, router http.Handler, rCtx *resolveContext, alarmName string, props map[string]any) (string, map[string]string, error) {
	body := map[string]any{"AlarmName": alarmName}
	for _, name := range cloudwatchAlarmProperties {
		if v, ok := props[name]; ok && v != nil {
			body[name] = v
		}
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

func (h *cloudwatchAlarmHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// AlarmName is optional on the resource — CloudFormation names the alarm
	// when the template leaves it out, and CDK leaves it out unless the caller
	// passes `alarmName`, which the constructs that build alarms for you
	// (metric.createAlarm, queue/lambda metric helpers) never do. PutMetricAlarm
	// itself requires the name, so forwarding the empty string turned every
	// such stack into AWS's own "Value null at 'alarmName'" ValidationError.
	alarmName, _ := props["AlarmName"].(string)
	if alarmName == "" {
		alarmName = rCtx.generatedName()
	}
	return h.putMetricAlarm(ctx, router, rCtx, alarmName, props)
}

func (h *cloudwatchAlarmHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{
		"AlarmNames": []string{physicalID},
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "GraniteServiceVersion20100801.DeleteAlarms", body)
	return nil
}

func (h *cloudwatchAlarmHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Physical ID is the alarm name; only AlarmName replaces on real AWS.
	if n, ok := props["AlarmName"].(string); ok && n != "" && n != physicalID {
		return "", nil, errReplacementRequired
	}
	id, attrs, err := h.putMetricAlarm(ctx, router, rCtx, physicalID, props)
	if err != nil {
		return "", nil, err
	}
	// After PutMetricAlarm, not before: tagging is addressed to the alarm's ARN,
	// and the alarm has to be there to carry the tags.
	//
	// A tag call that fails here leaves the alarm holding its new configuration
	// and some part of its old tags, which is why the failure is dirty rather
	// than terminal: rollback must report the resource as failed instead of
	// claiming the previous state was restored, and it must not answer a
	// half-applied update by building a second alarm.
	if err := h.syncTags(ctx, router, rCtx.Region, attrs["Arn"], props["Tags"], oldProps["Tags"]); err != nil {
		return "", nil, failDirtyUpdate(err)
	}
	return id, attrs, nil
}

// syncTags reconciles an alarm's tags with the template's.
//
// PutMetricAlarm applies Tags only when it creates an alarm and ignores them
// when it updates one, exactly as real CloudWatch does (see the Tagging section
// of docs/services/cloudwatch.md). Left to the upsert alone, a stack update that
// changed only an alarm's tags would reach UPDATE_COMPLETE having changed
// nothing. Real CloudFormation calls TagResource/UntagResource against the
// resource's ARN when its tags change, and so does this.
//
// The calls go over the Query protocol rather than through internalJSON,
// because CloudWatch's JSON dispatch covers only the alarm and metric
// operations — TagResource over a GraniteServiceVersion20100801 target is an
// UnknownOperationException. This is the same route applySNSTopicTags takes for
// the same reason.
func (h *cloudwatchAlarmHandler) syncTags(ctx context.Context, router http.Handler, region, alarmARN string, newTags, oldTags any) error {
	want, have := cloudwatchAlarmTagMap(newTags), cloudwatchAlarmTagMap(oldTags)

	// Sorted so the request a given diff produces is always the same one —
	// the member indices have to be contiguous from 1 either way, because the
	// service stops reading the list at the first gap.
	var added []string
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if old, ok := have[k]; !ok || old != want[k] {
			added = append(added, k)
		}
	}
	var removed []string
	for _, k := range slices.Sorted(maps.Keys(have)) {
		if _, ok := want[k]; !ok {
			removed = append(removed, k)
		}
	}

	if len(added) > 0 {
		params := map[string]string{
			"Action":      "TagResource",
			"ResourceARN": alarmARN,
			"Version":     awsapi.VersionCloudWatch,
		}
		for i, k := range added {
			params[fmt.Sprintf("Tags.member.%d.Key", i+1)] = k
			params[fmt.Sprintf("Tags.member.%d.Value", i+1)] = want[k]
		}
		if _, err := internalQuery(ctx, router, region, params); err != nil {
			return fmt.Errorf("TagResource: %w", err)
		}
	}
	if len(removed) > 0 {
		params := map[string]string{
			"Action":      "UntagResource",
			"ResourceARN": alarmARN,
			"Version":     awsapi.VersionCloudWatch,
		}
		for i, k := range removed {
			params[fmt.Sprintf("TagKeys.member.%d", i+1)] = k
		}
		if _, err := internalQuery(ctx, router, region, params); err != nil {
			return fmt.Errorf("UntagResource: %w", err)
		}
	}
	return nil
}

// cloudwatchAlarmTagMap flattens a template Tags list into key/value pairs.
//
// A tag with an empty key is dropped rather than sent: the Query-protocol tag
// lists are read until the first missing member, so one empty key would
// silently truncate every tag after it.
func cloudwatchAlarmTagMap(raw any) map[string]string {
	out := map[string]string{}
	list, _ := raw.([]any)
	for _, item := range list {
		tag, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key, _ := tag["Key"].(string)
		if key == "" {
			continue
		}
		value := ""
		if v := tag["Value"]; v != nil {
			value = cfnScalarString(v)
		}
		out[key] = value
	}
	return out
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
	// Name is optional on AWS::Scheduler::ScheduleGroup. The empty name was
	// accepted, so a second unnamed group in the same stack collided with the
	// first — "Schedule group  already exists".
	name, _ := props["Name"].(string)
	if name == "" {
		name = rCtx.generatedNameWithin(maxNameLenScheduler)
	}

	body := map[string]any{
		"Name": name,
	}
	// The template carries tags as [{Key,Value}]; the emulated scheduler API
	// takes a flat map.
	if tags, ok := props["Tags"].([]any); ok {
		m := map[string]string{}
		for _, t := range tags {
			if tm, ok := t.(map[string]any); ok {
				if k, _ := tm["Key"].(string); k != "" {
					v, _ := tm["Value"].(string)
					m[k] = v
				}
			}
		}
		if len(m) > 0 {
			body["Tags"] = m
		}
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
	// Physical ID is the group ARN, "arn:…:schedule-group/{name}".
	oldName := physicalID
	if idx := strings.LastIndex(oldName, "/"); idx >= 0 {
		oldName = oldName[idx+1:]
	}
	if n, ok := props["Name"].(string); ok && n != "" && n != oldName {
		return "", nil, errReplacementRequired
	}
	// Name is the only replacement property on real AWS (the rest is Tags),
	// and CreateScheduleGroup rejects duplicates, so replacing under an
	// unchanged name can never succeed. Keep the group.
	return physicalID, nil, nil
}

// ── AWS::OpenSearchService::Domain ──────────────────────────────────────────

// opensearchDomainPath is OpenSearch's modeled CreateDomain binding. Domains
// are addressed by name beneath it, and the physical ID this handler returns
// is the ARN, so Delete has to recover the name from it.
const opensearchDomainPath = "/2021-01-01/opensearch/domain"

type opensearchDomainHandler struct{}

func (h *opensearchDomainHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	domainName, _ := props["DomainName"].(string)
	engineVersion, _ := props["EngineVersion"].(string)

	body := map[string]any{
		"DomainName":    domainName,
		"EngineVersion": engineVersion,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDomain: %w", err)
	}

	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost,
		opensearchDomainPath, "application/json", data)
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

// Delete addresses the domain by name. The physical ID is the ARN
// (arn:aws:es:…:domain/<name>), and DeleteDomain binds the name as a path
// label, so the name is taken from the ARN's last segment — sending the whole
// ARN as the name, as this did before, never matched a domain.
func (h *opensearchDomainHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	name := physicalID
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	_, _ = internalRequest(ctx, router, rCtx.Region, http.MethodDelete,
		opensearchDomainPath+"/"+url.PathEscape(name), "", nil)
	return nil
}

func (h *opensearchDomainHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::AppConfig::Application ─────────────────────────────────────────────

// appconfigApplicationsPath is AppConfig's modeled CreateApplication binding.
// Every nested AppConfig resource hangs off an application beneath it.
const appconfigApplicationsPath = "/applications"

// internalAppConfigRequest dispatches an AppConfig REST request. It differs
// from a bare internalRequest only by the SigV4 scope header, which the main
// router needs to see to claim /applications for AppConfig rather than hand it
// to Service Catalog AppRegistry, whose tree it shares (see #854). Without it a
// stack that declares an AWS::AppConfig::Application would silently create an
// AppRegistry one.
func internalAppConfigRequest(ctx context.Context, router http.Handler, region, method, path string, body []byte) (*httptest.ResponseRecorder, error) {
	contentType := ""
	if body != nil {
		contentType = "application/json"
	}
	return restCall("appconfig", region, method, path, contentType, body, http.Header{
		"Authorization": []string{"AWS4-HMAC-SHA256 Credential=overcast/20250101/" + region + "/appconfig/aws4_request, SignedHeaders=host, Signature=overcast"},
	}).do(ctx, router)
}

// appconfigRESTJSON dispatches an AppConfig REST call and decodes its response.
func appconfigRESTJSON(ctx context.Context, router http.Handler, region, method, path, opName string, body map[string]any, out any) error {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%s: marshal request: %w", opName, err)
		}
	}
	rec, err := internalAppConfigRequest(ctx, router, region, method, path, data)
	if err != nil {
		return fmt.Errorf("%s: %w", opName, err)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			return fmt.Errorf("%s: parse response: %w", opName, err)
		}
	}
	return nil
}

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

	var resp struct {
		Id   string `json:"Id"`
		Name string `json:"Name"`
	}
	if err := appconfigRESTJSON(ctx, router, rCtx.Region, http.MethodPost,
		appconfigApplicationsPath, "CreateApplication", body, &resp); err != nil {
		return "", nil, err
	}

	attrs := map[string]string{
		"Id":   resp.Id,
		"Name": resp.Name,
	}
	return resp.Id, attrs, nil
}

func (h *appconfigApplicationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	_, _ = internalAppConfigRequest(ctx, router, rCtx.Region, http.MethodDelete,
		appconfigApplicationsPath+"/"+url.PathEscape(physicalID), nil)
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

	// ApplicationId is a path label on this binding, not a body member.
	body := map[string]any{"Name": name}
	if desc, _ := props["Description"].(string); desc != "" {
		body["Description"] = desc
	}

	var resp struct {
		Id string `json:"Id"`
	}
	if err := appconfigRESTJSON(ctx, router, rCtx.Region, http.MethodPost,
		appconfigApplicationsPath+"/"+url.PathEscape(appID)+"/environments",
		"CreateEnvironment", body, &resp); err != nil {
		return "", nil, err
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
	_, _ = internalAppConfigRequest(ctx, router, rCtx.Region, http.MethodDelete,
		appconfigApplicationsPath+"/"+url.PathEscape(parts[0])+"/environments/"+url.PathEscape(parts[1]), nil)
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

	// ApplicationId is a path label on this binding, not a body member.
	body := map[string]any{
		"Name":        name,
		"LocationUri": locationURI,
	}

	var resp struct {
		Id string `json:"Id"`
	}
	if err := appconfigRESTJSON(ctx, router, rCtx.Region, http.MethodPost,
		appconfigApplicationsPath+"/"+url.PathEscape(appID)+"/configurationprofiles",
		"CreateConfigurationProfile", body, &resp); err != nil {
		return "", nil, err
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
	_, _ = internalAppConfigRequest(ctx, router, rCtx.Region, http.MethodDelete,
		appconfigApplicationsPath+"/"+url.PathEscape(parts[0])+"/configurationprofiles/"+url.PathEscape(parts[1]), nil)
	return nil
}

func (h *appconfigConfigurationProfileHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}
