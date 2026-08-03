package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/config"
)

// EFS resource handlers translate AWS::EFS::* CloudFormation properties to the
// EFS API and dispatch through the emulator router (X-Amz-Target "EFS.<Op>"),
// so all validation, defaulting, and lifecycle behaviour stays in the service.

// ── AWS::EFS::FileSystem ───────────────────────────────────────────────────

type efsFileSystemHandler struct{}

func (h *efsFileSystemHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{
		// Real CloudFormation generates a unique creation token per resource
		// instance; the random suffix keeps replacements collision-free.
		"CreationToken": fmt.Sprintf("%s-%s-%s", rCtx.StackName, rCtx.LogicalID, strings.ReplaceAll(uuid.NewString(), "-", "")[:8]),
	}
	if v, _ := props["PerformanceMode"].(string); v != "" {
		body["PerformanceMode"] = v
	}
	if v, ok := props["Encrypted"]; ok {
		body["Encrypted"] = asBool(v)
	}
	if v, _ := props["KmsKeyId"].(string); v != "" {
		body["KmsKeyId"] = v
	}
	if v, _ := props["ThroughputMode"].(string); v != "" {
		body["ThroughputMode"] = v
	}
	if v, ok := cfnFloatProp(props, "ProvisionedThroughputInMibps"); ok {
		body["ProvisionedThroughputInMibps"] = v
	}
	if v, _ := props["AvailabilityZoneName"].(string); v != "" {
		body["AvailabilityZoneName"] = v
	}
	if status := efsBackupPolicyStatus(props); status != "" {
		body["Backup"] = status == "ENABLED"
	}
	if tags, ok := props["FileSystemTags"].([]any); ok && len(tags) > 0 {
		body["Tags"] = tags
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "EFS.CreateFileSystem", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateFileSystem: %w", err)
	}
	var resp struct {
		FileSystemId  string `json:"FileSystemId"`
		FileSystemArn string `json:"FileSystemArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateFileSystem: parse response: %w", err)
	}

	if err := h.applySubResources(ctx, router, rCtx.Region, resp.FileSystemId, props, nil); err != nil {
		return "", nil, err
	}

	arn := resp.FileSystemArn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", rCtx.Region, rCtx.AccountID, resp.FileSystemId)
	}
	return resp.FileSystemId, map[string]string{"Arn": arn, "FileSystemId": resp.FileSystemId}, nil
}

func (h *efsFileSystemHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	if _, err := internalJSON(ctx, router, rCtx.Region, "EFS.DeleteFileSystem", map[string]any{"FileSystemId": physicalID}); err != nil {
		return fmt.Errorf("DeleteFileSystem: %w", err)
	}
	return nil
}

func (h *efsFileSystemHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Replacement-triggering properties, matching real AWS "Replacement: Yes".
	for _, immutable := range []string{"AvailabilityZoneName", "Encrypted", "KmsKeyId", "PerformanceMode"} {
		if !efsPropEqual(props[immutable], oldProps[immutable]) {
			return "", nil, errReplacementRequired
		}
	}

	newMode, _ := props["ThroughputMode"].(string)
	oldMode, _ := oldProps["ThroughputMode"].(string)
	newTP, hasNewTP := cfnFloatProp(props, "ProvisionedThroughputInMibps")
	oldTP, hasOldTP := cfnFloatProp(oldProps, "ProvisionedThroughputInMibps")
	if newMode != oldMode || hasNewTP != hasOldTP || newTP != oldTP {
		body := map[string]any{"FileSystemId": physicalID}
		if newMode != "" {
			body["ThroughputMode"] = newMode
		}
		if hasNewTP {
			body["ProvisionedThroughputInMibps"] = newTP
		}
		if _, err := internalJSON(ctx, router, rCtx.Region, "EFS.UpdateFileSystem", body); err != nil {
			return "", nil, fmt.Errorf("UpdateFileSystem: %w", err)
		}
	}

	if err := h.applySubResources(ctx, router, rCtx.Region, physicalID, props, oldProps); err != nil {
		return "", nil, err
	}
	if err := efsSyncTags(ctx, router, rCtx.Region, physicalID, props["FileSystemTags"], oldProps["FileSystemTags"]); err != nil {
		return "", nil, err
	}

	arn := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", rCtx.Region, rCtx.AccountID, physicalID)
	return physicalID, map[string]string{"Arn": arn, "FileSystemId": physicalID}, nil
}

// applySubResources pushes the policy, lifecycle, backup, and protection
// properties that ride on AWS::EFS::FileSystem but map to separate EFS APIs.
// oldProps is nil on create; on update, unchanged groups are skipped.
func (h *efsFileSystemHandler) applySubResources(ctx context.Context, router http.Handler, region, fsID string, props, oldProps map[string]any) error {
	if policy, ok := props["FileSystemPolicy"]; ok && policy != nil {
		if oldProps == nil || !efsPropEqual(policy, oldProps["FileSystemPolicy"]) {
			policyJSON, err := json.Marshal(policy)
			if err != nil {
				return fmt.Errorf("FileSystemPolicy: %w", err)
			}
			if _, err := internalJSON(ctx, router, region, "EFS.PutFileSystemPolicy", map[string]any{
				"FileSystemId": fsID, "Policy": string(policyJSON),
			}); err != nil {
				return fmt.Errorf("PutFileSystemPolicy: %w", err)
			}
		}
	} else if oldProps != nil && oldProps["FileSystemPolicy"] != nil {
		if _, err := internalJSON(ctx, router, region, "EFS.DeleteFileSystemPolicy", map[string]any{"FileSystemId": fsID}); err != nil {
			return fmt.Errorf("DeleteFileSystemPolicy: %w", err)
		}
	}

	if policies, ok := props["LifecyclePolicies"].([]any); ok && len(policies) > 0 {
		if oldProps == nil || !efsPropEqual(props["LifecyclePolicies"], oldProps["LifecyclePolicies"]) {
			if _, err := internalJSON(ctx, router, region, "EFS.PutLifecycleConfiguration", map[string]any{
				"FileSystemId": fsID, "LifecyclePolicies": policies,
			}); err != nil {
				return fmt.Errorf("PutLifecycleConfiguration: %w", err)
			}
		}
	}

	if oldProps != nil {
		if status := efsBackupPolicyStatus(props); status != "" && status != efsBackupPolicyStatus(oldProps) {
			if _, err := internalJSON(ctx, router, region, "EFS.PutBackupPolicy", map[string]any{
				"FileSystemId": fsID, "BackupPolicy": map[string]any{"Status": status},
			}); err != nil {
				return fmt.Errorf("PutBackupPolicy: %w", err)
			}
		}
	}

	if protection, ok := props["FileSystemProtection"].(map[string]any); ok {
		if rop, _ := protection["ReplicationOverwriteProtection"].(string); rop != "" {
			if oldProps == nil || !efsPropEqual(props["FileSystemProtection"], oldProps["FileSystemProtection"]) {
				if _, err := internalJSON(ctx, router, region, "EFS.UpdateFileSystemProtection", map[string]any{
					"FileSystemId": fsID, "ReplicationOverwriteProtection": rop,
				}); err != nil {
					return fmt.Errorf("UpdateFileSystemProtection: %w", err)
				}
			}
		}
	}
	return nil
}

// ── AWS::EFS::MountTarget ──────────────────────────────────────────────────

type efsMountTargetHandler struct{}

func (h *efsMountTargetHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["FileSystemId"].(string); v != "" {
		body["FileSystemId"] = v
	}
	if v, _ := props["SubnetId"].(string); v != "" {
		body["SubnetId"] = v
	}
	if v, _ := props["IpAddress"].(string); v != "" {
		body["IpAddress"] = v
	}
	if v, ok := props["SecurityGroups"].([]any); ok {
		body["SecurityGroups"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "EFS.CreateMountTarget", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateMountTarget: %w", err)
	}
	var resp struct {
		MountTargetId string `json:"MountTargetId"`
		IpAddress     string `json:"IpAddress"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateMountTarget: parse response: %w", err)
	}
	return resp.MountTargetId, map[string]string{"Id": resp.MountTargetId, "IpAddress": resp.IpAddress}, nil
}

func (h *efsMountTargetHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	if _, err := internalJSON(ctx, router, rCtx.Region, "EFS.DeleteMountTarget", map[string]any{"MountTargetId": physicalID}); err != nil {
		return fmt.Errorf("DeleteMountTarget: %w", err)
	}
	return nil
}

func (h *efsMountTargetHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// FileSystemId, SubnetId, and IpAddress replace on real AWS.
	for _, immutable := range []string{"FileSystemId", "SubnetId", "IpAddress"} {
		if !efsPropEqual(props[immutable], oldProps[immutable]) {
			return "", nil, errReplacementRequired
		}
	}
	if !efsPropEqual(props["SecurityGroups"], oldProps["SecurityGroups"]) {
		body := map[string]any{"MountTargetId": physicalID}
		if v, ok := props["SecurityGroups"].([]any); ok {
			body["SecurityGroups"] = v
		}
		if _, err := internalJSON(ctx, router, rCtx.Region, "EFS.ModifyMountTargetSecurityGroups", body); err != nil {
			return "", nil, fmt.Errorf("ModifyMountTargetSecurityGroups: %w", err)
		}
	}
	return physicalID, nil, nil
}

// ── AWS::EFS::AccessPoint ──────────────────────────────────────────────────

type efsAccessPointHandler struct{}

func (h *efsAccessPointHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	clientToken, _ := props["ClientToken"].(string)
	if clientToken == "" {
		clientToken = fmt.Sprintf("%s-%s-%s", rCtx.StackName, rCtx.LogicalID, strings.ReplaceAll(uuid.NewString(), "-", "")[:8])
	}
	body := map[string]any{"ClientToken": clientToken}
	if v, _ := props["FileSystemId"].(string); v != "" {
		body["FileSystemId"] = v
	}
	if v, ok := props["PosixUser"].(map[string]any); ok {
		body["PosixUser"] = efsNumericPosix(v)
	}
	if v, ok := props["RootDirectory"].(map[string]any); ok {
		body["RootDirectory"] = efsNumericRootDirectory(v)
	}
	if tags, ok := props["AccessPointTags"].([]any); ok && len(tags) > 0 {
		body["Tags"] = tags
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "EFS.CreateAccessPoint", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateAccessPoint: %w", err)
	}
	var resp struct {
		AccessPointId  string `json:"AccessPointId"`
		AccessPointArn string `json:"AccessPointArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateAccessPoint: parse response: %w", err)
	}
	arn := resp.AccessPointArn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:access-point/%s", rCtx.Region, rCtx.AccountID, resp.AccessPointId)
	}
	return resp.AccessPointId, map[string]string{"AccessPointId": resp.AccessPointId, "Arn": arn}, nil
}

func (h *efsAccessPointHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	if _, err := internalJSON(ctx, router, rCtx.Region, "EFS.DeleteAccessPoint", map[string]any{"AccessPointId": physicalID}); err != nil {
		return fmt.Errorf("DeleteAccessPoint: %w", err)
	}
	return nil
}

func (h *efsAccessPointHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Every access-point property except tags replaces on real AWS.
	for _, immutable := range []string{"ClientToken", "FileSystemId", "PosixUser", "RootDirectory"} {
		if !efsPropEqual(props[immutable], oldProps[immutable]) {
			return "", nil, errReplacementRequired
		}
	}
	if err := efsSyncTags(ctx, router, rCtx.Region, physicalID, props["AccessPointTags"], oldProps["AccessPointTags"]); err != nil {
		return "", nil, err
	}
	return physicalID, nil, nil
}

// ── shared helpers ─────────────────────────────────────────────────────────

// efsBackupPolicyStatus extracts BackupPolicy.Status ("ENABLED"/"DISABLED").
func efsBackupPolicyStatus(props map[string]any) string {
	bp, ok := props["BackupPolicy"].(map[string]any)
	if !ok {
		return ""
	}
	status, _ := bp["Status"].(string)
	return status
}

// efsPropEqual compares two resolved CFN property values structurally.
func efsPropEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// efsSyncTags diffs old and new CFN tag lists ({Key,Value} maps) and applies
// the difference via EFS.TagResource / EFS.UntagResource.
func efsSyncTags(ctx context.Context, router http.Handler, region, resourceID string, newTags, oldTags any) error {
	toMap := func(v any) map[string]string {
		out := map[string]string{}
		list, _ := v.([]any)
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			key, _ := m["Key"].(string)
			value, _ := m["Value"].(string)
			if key != "" {
				out[key] = value
			}
		}
		return out
	}
	want, have := toMap(newTags), toMap(oldTags)

	var upserts []map[string]string
	for k, v := range want {
		if old, ok := have[k]; !ok || old != v {
			upserts = append(upserts, map[string]string{"Key": k, "Value": v})
		}
	}
	var removals []string
	for k := range have {
		if _, ok := want[k]; !ok {
			removals = append(removals, k)
		}
	}

	if len(upserts) > 0 {
		if _, err := internalJSON(ctx, router, region, "EFS.TagResource", map[string]any{
			"ResourceId": resourceID, "Tags": upserts,
		}); err != nil {
			return fmt.Errorf("TagResource: %w", err)
		}
	}
	if len(removals) > 0 {
		if _, err := internalJSON(ctx, router, region, "EFS.UntagResource", map[string]any{
			"ResourceId": resourceID, "TagKeys": removals,
		}); err != nil {
			return fmt.Errorf("UntagResource: %w", err)
		}
	}
	return nil
}

// efsNumericPosix normalises PosixUser numeric members that CFN may pass as
// strings ("1000") into numbers the EFS API accepts.
func efsNumericPosix(in map[string]any) map[string]any {
	out := copyStringAnyMap(in)
	for _, key := range []string{"Uid", "Gid"} {
		if v, ok := cfnFloatProp(out, key); ok {
			out[key] = v
		}
	}
	if gids, ok := out["SecondaryGids"].([]any); ok {
		converted := make([]any, 0, len(gids))
		for _, g := range gids {
			if f, ok := cfnFloatProp(map[string]any{"v": g}, "v"); ok {
				converted = append(converted, f)
			}
		}
		out["SecondaryGids"] = converted
	}
	return out
}

// efsNumericRootDirectory normalises RootDirectory.CreationInfo numeric
// members that CFN may pass as strings.
func efsNumericRootDirectory(in map[string]any) map[string]any {
	out := copyStringAnyMap(in)
	if ci, ok := out["CreationInfo"].(map[string]any); ok {
		converted := copyStringAnyMap(ci)
		for _, key := range []string{"OwnerUid", "OwnerGid"} {
			if v, ok := cfnFloatProp(converted, key); ok {
				converted[key] = v
			}
		}
		out["CreationInfo"] = converted
	}
	return out
}
