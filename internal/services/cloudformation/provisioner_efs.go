package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
)

// EFS resource handlers translate AWS::EFS::* CloudFormation properties to the
// EFS API and dispatch through the emulator router over EFS's own REST-JSON
// bindings (/2015-02-01/...), the same paths any EFS SDK call reaches, so all
// validation, defaulting, and lifecycle behaviour stays in the service.
//
// This used to go through the X-Amz-Target dispatcher ("EFS.<Op>" on POST /),
// which nothing but this provisioner and Overcast's own tests ever spoke: EFS
// is restJson1, and the pinned models give it no target prefix at all. #1226
// retired that invented door, so the internal caller moved to the same REST
// surface a real SDK uses instead of losing its own way in.

// efsAPIPrefix is EFS's versioned REST path prefix — see efs.apiPrefix.
const efsAPIPrefix = "/2015-02-01"

// ── EFS stabilization ──────────────────────────────────────────────────────
//
// Every EFS create is asynchronous and answers with the resource in "creating".
// A mount target is the one that matters most: with the NFS data plane on
// (OVERCAST_EFS_NFS) an export container has to come up behind it, and one that
// cannot settles in "error". Neither state stopped the stack, so it reported
// CREATE_COMPLETE around a mount target nothing could mount and the task that
// mounted it started against it and failed.
//
// All three resources report the same field and AWS documents the same values
// for each: LifeCycleState is "creating | available | updating | deleting |
// deleted | error".

// efsStabilizeTimeout bounds the wait for an EFS resource to come up. A file
// system and an access point are near-instant; a mount target has to start an
// NFS-Ganesha container behind an image pull, which is what this budget is
// sized for. A mount target that cannot be exported reaches "error" on its own,
// so the budget only bites on one that never answers either way.
const efsStabilizeTimeout = 10 * time.Minute

// efsLifecycleStatuses is the vocabulary all three EFS resources are read
// against.
//
// "deleting" and "deleted" are terminal rather than progress: a resource being
// torn down while a stack waits for it is never going to be available.
var efsLifecycleStatuses = statusVocabulary{
	ready:  []string{"available"},
	failed: []string{"error", "deleting", "deleted"},
}

// describedEFSResources is the projection an EFS status poll reads. All three
// describes decode through it: a response carries exactly one of the arrays,
// and encoding/json leaves the others nil.
type describedEFSResources struct {
	FileSystems []struct {
		LifeCycleState string `json:"LifeCycleState"`
	} `json:"FileSystems"`
	MountTargets []struct {
		LifeCycleState string `json:"LifeCycleState"`
	} `json:"MountTargets"`
	AccessPoints []struct {
		LifeCycleState string `json:"LifeCycleState"`
	} `json:"AccessPoints"`
}

// efsWait builds the wait for one EFS resource. stateOf pulls the lifecycle
// state out of whichever array was asked for and reports false when the
// resource is not in the answer at all — deleted from under the stack, which
// ends the wait rather than being polled over.
//
// EFS records no reason of its own: LifeCycleState is the whole of what the API
// says about a mount target whose export failed, so the state is what a failure
// here has to report.
//
// listPath is the REST collection endpoint (e.g. efsAPIPrefix+"/file-systems")
// and idParam is the query parameter EFS's Describe* operations filter it by
// (FileSystemId, MountTargetId, AccessPointId) — every one of the three reads
// its subject from a query string, never a path label.
func efsWait(router http.Handler, region, subject, operation, listPath, idParam, id string,
	stateOf func(describedEFSResources) (string, bool),
) stabilizeWait {
	return stabilizeWait{
		subject:  subject,
		goal:     `reach lifecycle state "available"`,
		timeout:  efsStabilizeTimeout,
		statuses: efsLifecycleStatuses,
		describe: func(ctx context.Context) (string, string, error) {
			path := listPath + "?" + url.Values{idParam: {id}}.Encode()
			rec, err := internalRequest(ctx, router, region, http.MethodGet, path, "", nil)
			if err != nil {
				return "", "", fmt.Errorf("%s: %s: %w", operation, subject, err)
			}
			var decoded describedEFSResources
			if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
				return "", "", fmt.Errorf("%s: parse response: %w", operation, err)
			}
			state, found := stateOf(decoded)
			if !found {
				return "", "", fmt.Errorf("%s no longer exists", subject)
			}
			return state, "", nil
		},
	}
}

// efsJSON POSTs/PUTs a JSON body to one EFS REST path.
func efsJSON(ctx context.Context, router http.Handler, region, method, path string, body map[string]any) (*httptest.ResponseRecorder, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return internalRequest(ctx, router, region, method, path, "application/json", data)
}

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

	rec, err := efsJSON(ctx, router, rCtx.Region, http.MethodPost, efsAPIPrefix+"/file-systems", body)
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

// Stabilize holds the resource open until the file system is available, which
// is what every mount target and access point in the same stack is waiting on.
// See resourceStabilizer.
func (h *efsFileSystemHandler) Stabilize(ctx context.Context, router http.Handler, _ *config.Config, clk clock.Clock, physicalID string, rCtx *resolveContext) error {
	return awaitResourceReady(ctx, clk, efsWait(router, rCtx.Region,
		fmt.Sprintf("file system %s", physicalID), "DescribeFileSystems",
		efsAPIPrefix+"/file-systems", "FileSystemId", physicalID,
		func(d describedEFSResources) (string, bool) {
			if len(d.FileSystems) == 0 {
				return "", false
			}
			return d.FileSystems[0].LifeCycleState, true
		}))
}

func (h *efsFileSystemHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete,
		efsAPIPrefix+"/file-systems/"+url.PathEscape(physicalID), "", nil)
	return teardownError("DeleteFileSystem", rec, err)
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
		body := map[string]any{}
		if newMode != "" {
			body["ThroughputMode"] = newMode
		}
		if hasNewTP {
			body["ProvisionedThroughputInMibps"] = newTP
		}
		if _, err := efsJSON(ctx, router, rCtx.Region, http.MethodPut,
			efsAPIPrefix+"/file-systems/"+url.PathEscape(physicalID), body); err != nil {
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
	fsPath := efsAPIPrefix + "/file-systems/" + url.PathEscape(fsID)

	if policy, ok := props["FileSystemPolicy"]; ok && policy != nil {
		if oldProps == nil || !efsPropEqual(policy, oldProps["FileSystemPolicy"]) {
			policyJSON, err := json.Marshal(policy)
			if err != nil {
				return fmt.Errorf("FileSystemPolicy: %w", err)
			}
			if _, err := efsJSON(ctx, router, region, http.MethodPut, fsPath+"/policy", map[string]any{
				"Policy": string(policyJSON),
			}); err != nil {
				return fmt.Errorf("PutFileSystemPolicy: %w", err)
			}
		}
	} else if oldProps != nil && oldProps["FileSystemPolicy"] != nil {
		if _, err := internalRequest(ctx, router, region, http.MethodDelete, fsPath+"/policy", "", nil); err != nil {
			return fmt.Errorf("DeleteFileSystemPolicy: %w", err)
		}
	}

	if policies, ok := props["LifecyclePolicies"].([]any); ok && len(policies) > 0 {
		if oldProps == nil || !efsPropEqual(props["LifecyclePolicies"], oldProps["LifecyclePolicies"]) {
			if _, err := efsJSON(ctx, router, region, http.MethodPut, fsPath+"/lifecycle-configuration", map[string]any{
				"LifecyclePolicies": policies,
			}); err != nil {
				return fmt.Errorf("PutLifecycleConfiguration: %w", err)
			}
		}
	}

	if oldProps != nil {
		if status := efsBackupPolicyStatus(props); status != "" && status != efsBackupPolicyStatus(oldProps) {
			if _, err := efsJSON(ctx, router, region, http.MethodPut, fsPath+"/backup-policy", map[string]any{
				"BackupPolicy": map[string]any{"Status": status},
			}); err != nil {
				return fmt.Errorf("PutBackupPolicy: %w", err)
			}
		}
	}

	if protection, ok := props["FileSystemProtection"].(map[string]any); ok {
		if rop, _ := protection["ReplicationOverwriteProtection"].(string); rop != "" {
			if oldProps == nil || !efsPropEqual(props["FileSystemProtection"], oldProps["FileSystemProtection"]) {
				if _, err := efsJSON(ctx, router, region, http.MethodPut, fsPath+"/protection", map[string]any{
					"ReplicationOverwriteProtection": rop,
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

	rec, err := efsJSON(ctx, router, rCtx.Region, http.MethodPost, efsAPIPrefix+"/mount-targets", body)
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

// Stabilize holds the resource open until the mount target is available. This
// is the one that costs the most to get wrong: the NFS export behind a mount
// target is what a task actually mounts, and a stack that completed while the
// export was still starting handed a container a target it could not mount. A
// target whose export cannot be brought up settles in "error", which ends the
// wait rather than running it out. See resourceStabilizer.
func (h *efsMountTargetHandler) Stabilize(ctx context.Context, router http.Handler, _ *config.Config, clk clock.Clock, physicalID string, rCtx *resolveContext) error {
	return awaitResourceReady(ctx, clk, efsWait(router, rCtx.Region,
		fmt.Sprintf("mount target %s", physicalID), "DescribeMountTargets",
		efsAPIPrefix+"/mount-targets", "MountTargetId", physicalID,
		func(d describedEFSResources) (string, bool) {
			if len(d.MountTargets) == 0 {
				return "", false
			}
			return d.MountTargets[0].LifeCycleState, true
		}))
}

func (h *efsMountTargetHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete,
		efsAPIPrefix+"/mount-targets/"+url.PathEscape(physicalID), "", nil)
	return teardownError("DeleteMountTarget", rec, err)
}

func (h *efsMountTargetHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// FileSystemId, SubnetId, and IpAddress replace on real AWS.
	for _, immutable := range []string{"FileSystemId", "SubnetId", "IpAddress"} {
		if !efsPropEqual(props[immutable], oldProps[immutable]) {
			return "", nil, errReplacementRequired
		}
	}
	if !efsPropEqual(props["SecurityGroups"], oldProps["SecurityGroups"]) {
		body := map[string]any{}
		if v, ok := props["SecurityGroups"].([]any); ok {
			body["SecurityGroups"] = v
		}
		if _, err := efsJSON(ctx, router, rCtx.Region, http.MethodPut,
			efsAPIPrefix+"/mount-targets/"+url.PathEscape(physicalID)+"/security-groups", body); err != nil {
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

	rec, err := efsJSON(ctx, router, rCtx.Region, http.MethodPost, efsAPIPrefix+"/access-points", body)
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

// Stabilize holds the resource open until the access point is available: a task
// that mounts through one cannot do so before then. See resourceStabilizer.
func (h *efsAccessPointHandler) Stabilize(ctx context.Context, router http.Handler, _ *config.Config, clk clock.Clock, physicalID string, rCtx *resolveContext) error {
	return awaitResourceReady(ctx, clk, efsWait(router, rCtx.Region,
		fmt.Sprintf("access point %s", physicalID), "DescribeAccessPoints",
		efsAPIPrefix+"/access-points", "AccessPointId", physicalID,
		func(d describedEFSResources) (string, bool) {
			if len(d.AccessPoints) == 0 {
				return "", false
			}
			return d.AccessPoints[0].LifeCycleState, true
		}))
}

func (h *efsAccessPointHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete,
		efsAPIPrefix+"/access-points/"+url.PathEscape(physicalID), "", nil)
	return teardownError("DeleteAccessPoint", rec, err)
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
// the difference via POST/DELETE .../resource-tags/{resourceID} — TagResource
// and UntagResource's own REST bindings. UntagResource carries its keys as a
// repeated ?tagKeys= query parameter, not a body, so it is built differently
// from every other EFS write here.
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

	resourceTagsPath := efsAPIPrefix + "/resource-tags/" + url.PathEscape(resourceID)
	if len(upserts) > 0 {
		if _, err := efsJSON(ctx, router, region, http.MethodPost, resourceTagsPath, map[string]any{
			"Tags": upserts,
		}); err != nil {
			return fmt.Errorf("TagResource: %w", err)
		}
	}
	if len(removals) > 0 {
		query := url.Values{}
		for _, k := range removals {
			query.Add("tagKeys", k)
		}
		if _, err := internalRequest(ctx, router, region, http.MethodDelete,
			resourceTagsPath+"?"+query.Encode(), "", nil); err != nil {
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
