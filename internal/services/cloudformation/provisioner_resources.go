package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/config"
)

// ── AWS::IAM::Policy (inline policy) ───────────────────────────────────────

type iamPolicyHandler struct{}

func (h *iamPolicyHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	policyName, _ := props["PolicyName"].(string)
	if policyName == "" {
		policyName = fmt.Sprintf("%s-policy", rCtx.StackName)
	}

	policyDoc, _ := props["PolicyDocument"].(map[string]any)
	policyJSON, _ := json.Marshal(policyDoc)

	// Attach to each role listed in Roles property.
	if roles, ok := props["Roles"].([]any); ok {
		for _, r := range roles {
			roleName, _ := r.(string)
			if roleName == "" {
				continue
			}
			params := map[string]string{
				"Action":         "PutRolePolicy",
				"Version":        "2010-05-08",
				"RoleName":       roleName,
				"PolicyName":     policyName,
				"PolicyDocument": string(policyJSON),
			}
			if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
				return "", nil, fmt.Errorf("PutRolePolicy on %s: %w", roleName, err)
			}
		}
	}

	physicalID := rCtx.StackName + "-" + policyName
	return physicalID, nil, nil
}

func (h *iamPolicyHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// Inline policies are cleaned up when the role is deleted.
	return nil
}

func (h *iamPolicyHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	policyName, _ := props["PolicyName"].(string)
	if policyName == "" {
		policyName = fmt.Sprintf("%s-policy", rCtx.StackName)
	}

	oldPolicyName := physicalID
	if prefix := rCtx.StackName + "-"; strings.HasPrefix(physicalID, prefix) {
		oldPolicyName = physicalID[len(prefix):]
	}
	if oldPolicyName != policyName {
		return "", nil, errReplacementRequired
	}

	policyDoc, _ := props["PolicyDocument"].(map[string]any)
	policyJSON, _ := json.Marshal(policyDoc)

	if roles, ok := props["Roles"].([]any); ok {
		for _, r := range roles {
			roleName, _ := r.(string)
			if roleName == "" {
				continue
			}
			params := map[string]string{
				"Action":         "PutRolePolicy",
				"Version":        "2010-05-08",
				"RoleName":       roleName,
				"PolicyName":     policyName,
				"PolicyDocument": string(policyJSON),
			}
			if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
				return "", nil, fmt.Errorf("PutRolePolicy on %s: %w", roleName, err)
			}
		}
	}

	if groups, ok := props["Groups"].([]any); ok {
		for _, g := range groups {
			groupName, _ := g.(string)
			if groupName == "" {
				continue
			}
			params := map[string]string{
				"Action":         "PutGroupPolicy",
				"Version":        "2010-05-08",
				"GroupName":      groupName,
				"PolicyName":     policyName,
				"PolicyDocument": string(policyJSON),
			}
			if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
				return "", nil, fmt.Errorf("PutGroupPolicy on %s: %w", groupName, err)
			}
		}
	}

	if users, ok := props["Users"].([]any); ok {
		for _, u := range users {
			userName, _ := u.(string)
			if userName == "" {
				continue
			}
			params := map[string]string{
				"Action":         "PutUserPolicy",
				"Version":        "2010-05-08",
				"UserName":       userName,
				"PolicyName":     policyName,
				"PolicyDocument": string(policyJSON),
			}
			if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
				return "", nil, fmt.Errorf("PutUserPolicy on %s: %w", userName, err)
			}
		}
	}

	return physicalID, nil, nil
}

// ── AWS::IAM::ManagedPolicy ────────────────────────────────────────────────

type iamManagedPolicyHandler struct{}

func (h *iamManagedPolicyHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	policyName, _ := props["ManagedPolicyName"].(string)
	if policyName == "" {
		policyName = fmt.Sprintf("%s-managed-policy", rCtx.StackName)
	}
	policyDoc, _ := props["PolicyDocument"].(map[string]any)
	policyJSON, _ := json.Marshal(policyDoc)

	path := "/"
	if v, _ := props["Path"].(string); v != "" {
		path = v
	}

	params := map[string]string{
		"Action":         "CreatePolicy",
		"Version":        "2010-05-08",
		"PolicyName":     policyName,
		"PolicyDocument": string(policyJSON),
		"Path":           path,
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreatePolicy: %w", err)
	}

	// IAM CreatePolicy returns XML with <Arn> inside <Policy>.
	// Parse the ARN from the response body.
	arn := extractXMLTag(rec.Body.String(), "Arn")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:iam::%s:policy%s%s", rCtx.AccountID, path, policyName)
	}

	attrs := map[string]string{
		"Arn": arn,
	}
	return arn, attrs, nil
}

func (h *iamManagedPolicyHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":    "DeletePolicy",
		"Version":   "2010-05-08",
		"PolicyArn": physicalID,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

func (h *iamManagedPolicyHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	policyName, _ := props["ManagedPolicyName"].(string)
	if policyName == "" {
		policyName = fmt.Sprintf("%s-managed-policy", rCtx.StackName)
	}

	// Extract policy name from ARN to detect rename.
	oldName := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		oldName = physicalID[idx+1:]
	}
	if oldName != policyName {
		return "", nil, errReplacementRequired
	}

	policyDoc, _ := props["PolicyDocument"].(map[string]any)
	policyJSON, _ := json.Marshal(policyDoc)

	params := map[string]string{
		"Action":         "CreatePolicyVersion",
		"Version":        "2010-05-08",
		"PolicyArn":      physicalID,
		"PolicyDocument": string(policyJSON),
		"SetAsDefault":   "true",
	}
	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("CreatePolicyVersion: %w", err)
	}

	return physicalID, nil, nil
}

// ── AWS::IAM::InstanceProfile ──────────────────────────────────────────────

type iamInstanceProfileHandler struct{}

func (h *iamInstanceProfileHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	profileName, _ := props["InstanceProfileName"].(string)
	if profileName == "" {
		profileName = fmt.Sprintf("%s-instance-profile", rCtx.StackName)
	}
	path := "/"
	if v, _ := props["Path"].(string); v != "" {
		path = v
	}

	params := map[string]string{
		"Action":              "CreateInstanceProfile",
		"Version":             "2010-05-08",
		"InstanceProfileName": profileName,
		"Path":                path,
	}
	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateInstanceProfile: %w", err)
	}

	arn := extractXMLTag(rec.Body.String(), "Arn")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:iam::%s:instance-profile%s%s", rCtx.AccountID, path, profileName)
	}

	// Add roles.
	if roles, ok := props["Roles"].([]any); ok {
		for _, r := range roles {
			roleName, _ := r.(string)
			if roleName == "" {
				continue
			}
			p := map[string]string{
				"Action":              "AddRoleToInstanceProfile",
				"Version":             "2010-05-08",
				"RoleName":            roleName,
				"InstanceProfileName": profileName,
			}
			if _, err := internalQuery(ctx, router, rCtx.Region, p); err != nil {
				return "", nil, fmt.Errorf("AddRoleToInstanceProfile: %w", err)
			}
		}
	}

	attrs := map[string]string{
		"Arn": arn,
	}
	return arn, attrs, nil
}

func (h *iamInstanceProfileHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	name := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		name = physicalID[idx+1:]
	}
	params := map[string]string{
		"Action":              "DeleteInstanceProfile",
		"Version":             "2010-05-08",
		"InstanceProfileName": name,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

func (h *iamInstanceProfileHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		name = physicalID[idx+1:]
	}

	if oldProps != nil {
		if newProfileName, _ := props["InstanceProfileName"].(string); newProfileName != "" {
			if oldProfileName, _ := oldProps["InstanceProfileName"].(string); oldProfileName != "" && newProfileName != oldProfileName {
				return "", nil, errReplacementRequired
			}
		}
		if newPath, _ := props["Path"].(string); newPath != "" {
			if oldPath, _ := oldProps["Path"].(string); oldPath != "" && newPath != oldPath {
				return "", nil, errReplacementRequired
			}
		}
	}

	newRoles, _ := props["Roles"].([]any)
	var oldRoleList []string
	if raw, ok := oldProps["Roles"].([]any); ok {
		for _, r := range raw {
			if s, _ := r.(string); s != "" {
				oldRoleList = append(oldRoleList, s)
			}
		}
	}

	oldSet := make(map[string]bool, len(oldRoleList))
	for _, r := range oldRoleList {
		oldSet[r] = true
	}
	newSet := make(map[string]bool)
	for _, r := range newRoles {
		if s, _ := r.(string); s != "" {
			newSet[s] = true
		}
	}

	for _, r := range oldRoleList {
		if !newSet[r] {
			p := map[string]string{
				"Action":              "RemoveRoleFromInstanceProfile",
				"Version":             "2010-05-08",
				"RoleName":            r,
				"InstanceProfileName": name,
			}
			if _, err := internalQuery(ctx, router, rCtx.Region, p); err != nil {
				return "", nil, fmt.Errorf("RemoveRoleFromInstanceProfile: %w", err)
			}
		}
	}

	for _, r := range newRoles {
		if s, _ := r.(string); s != "" && !oldSet[s] {
			p := map[string]string{
				"Action":              "AddRoleToInstanceProfile",
				"Version":             "2010-05-08",
				"RoleName":            s,
				"InstanceProfileName": name,
			}
			if _, err := internalQuery(ctx, router, rCtx.Region, p); err != nil {
				return "", nil, fmt.Errorf("AddRoleToInstanceProfile: %w", err)
			}
		}
	}

	attrs := map[string]string{"Arn": physicalID}
	return physicalID, attrs, nil
}

// ── AWS::IAM::ServiceLinkedRole ────────────────────────────────────────────

type iamServiceLinkedRoleHandler struct{}

func (h *iamServiceLinkedRoleHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	serviceName, _ := props["AWSServiceName"].(string)
	if serviceName == "" {
		return "", nil, fmt.Errorf("ServiceLinkedRole: AWSServiceName is required")
	}

	// Derive role name from service: e.g. elasticloadbalancing.amazonaws.com → AWSServiceRoleForElasticLoadBalancing
	roleName := "AWSServiceRoleFor" + serviceName
	if idx := strings.Index(roleName, "."); idx >= 0 {
		roleName = roleName[:idx]
	}

	assumePolicy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"%s"},"Action":"sts:AssumeRole"}]}`, serviceName)

	params := map[string]string{
		"Action":                   "CreateRole",
		"Version":                  "2010-05-08",
		"RoleName":                 roleName,
		"Path":                     "/aws-service-role/" + serviceName + "/",
		"AssumeRolePolicyDocument": assumePolicy,
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateServiceLinkedRole: %w", err)
	}

	arn := extractXMLTag(rec.Body.String(), "Arn")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:iam::%s:role/aws-service-role/%s/%s", rCtx.AccountID, serviceName, roleName)
	}

	attrs := map[string]string{
		"Arn":      arn,
		"RoleName": roleName,
	}
	return arn, attrs, nil
}

func (h *iamServiceLinkedRoleHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	roleName := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		roleName = physicalID[idx+1:]
	}
	params := map[string]string{
		"Action":   "DeleteRole",
		"Version":  "2010-05-08",
		"RoleName": roleName,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::Events::EventBus ──────────────────────────────────────────────────

type eventsEventBusHandler struct{}

func (h *eventsEventBusHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = rCtx.StackName + "-bus"
	}

	body := map[string]any{"Name": name}
	rec, err := internalJSON(ctx, router, rCtx.Region, "AWSEvents.CreateEventBus", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateEventBus: %w", err)
	}

	var resp struct {
		EventBusArn string `json:"EventBusArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateEventBus: parse response: %w", err)
	}

	arn := resp.EventBusArn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:events:%s:%s:event-bus/%s", rCtx.Region, rCtx.AccountID, name)
	}

	attrs := map[string]string{
		"Arn":  arn,
		"Name": name,
	}
	return arn, attrs, nil
}

func (h *eventsEventBusHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// Extract name from ARN.
	name := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		name = physicalID[idx+1:]
	}
	body := map[string]any{"Name": name}
	_, err := internalJSON(ctx, router, rCtx.Region, "AWSEvents.DeleteEventBus", body)
	return err
}

// ── AWS::Events::Rule ──────────────────────────────────────────────────────

type eventsRuleHandler struct{}

func (h *eventsRuleHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := make(map[string]any)
	if v, _ := props["Name"].(string); v != "" {
		body["Name"] = v
	}
	if v, _ := props["EventBusName"].(string); v != "" {
		body["EventBusName"] = v
	}
	if v, _ := props["State"].(string); v != "" {
		body["State"] = v
	} else {
		body["State"] = "ENABLED"
	}
	if v, _ := props["Description"].(string); v != "" {
		body["Description"] = v
	}
	if v, _ := props["EventPattern"].(map[string]any); v != nil {
		j, _ := json.Marshal(v)
		body["EventPattern"] = string(j)
	} else if v, _ := props["EventPattern"].(string); v != "" {
		body["EventPattern"] = v
	}
	if v, _ := props["ScheduleExpression"].(string); v != "" {
		body["ScheduleExpression"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AWSEvents.PutRule", body)
	if err != nil {
		return "", nil, fmt.Errorf("PutRule: %w", err)
	}

	var resp struct {
		RuleArn string `json:"RuleArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("PutRule: parse response: %w", err)
	}

	attrs := map[string]string{
		"Arn": resp.RuleArn,
	}
	return resp.RuleArn, attrs, nil
}

func (h *eventsRuleHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// Extract name from ARN: arn:aws:events:region:acct:rule/[bus/]name
	name := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		name = physicalID[idx+1:]
	}
	body := map[string]any{"Name": name}
	_, err := internalJSON(ctx, router, rCtx.Region, "AWSEvents.DeleteRule", body)
	return err
}

func (h *eventsRuleHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if n, ok := props["Name"].(string); ok && n != "" {
		tail := physicalID
		if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
			tail = physicalID[idx+1:]
		}
		if tail != n {
			return "", nil, errReplacementRequired
		}
	}
	if newBus, _ := props["EventBusName"].(string); oldProps != nil {
		if oldBus, _ := oldProps["EventBusName"].(string); oldBus != "" && newBus != oldBus {
			return "", nil, errReplacementRequired
		}
	}

	body := make(map[string]any)
	if v, _ := props["Name"].(string); v != "" {
		body["Name"] = v
	}
	if v, _ := props["EventBusName"].(string); v != "" {
		body["EventBusName"] = v
	}
	if v, _ := props["State"].(string); v != "" {
		body["State"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["Description"] = v
	}
	if v, ok := props["RoleArn"]; ok {
		body["RoleArn"] = v
	}
	if v, ok := props["EventPattern"].(map[string]any); ok && v != nil {
		j, _ := json.Marshal(v)
		body["EventPattern"] = string(j)
	} else if v, _ := props["EventPattern"].(string); v != "" {
		body["EventPattern"] = v
	}
	if v, _ := props["ScheduleExpression"].(string); v != "" {
		body["ScheduleExpression"] = v
	}

	if _, err := internalJSON(ctx, router, rCtx.Region, "AWSEvents.PutRule", body); err != nil {
		return "", nil, fmt.Errorf("PutRule: %w", err)
	}

	ruleName := physicalID
	if idx := strings.LastIndex(physicalID, "/"); idx >= 0 {
		ruleName = physicalID[idx+1:]
	}

	// Diff targets: remove old, add new.
	newTargets, _ := props["Targets"].([]any)
	var oldTargetList []any
	if oldProps != nil {
		oldTargetList, _ = oldProps["Targets"].([]any)
	}

	oldIDs := make(map[string]bool)
	for _, t := range oldTargetList {
		if m, ok := t.(map[string]any); ok {
			if id, _ := m["Id"].(string); id != "" {
				oldIDs[id] = true
			}
		}
	}
	newIDs := make(map[string]bool)
	for _, t := range newTargets {
		if m, ok := t.(map[string]any); ok {
			if id, _ := m["Id"].(string); id != "" {
				newIDs[id] = true
			}
		}
	}

	var toRemoveIDs []string
	for id := range oldIDs {
		if !newIDs[id] {
			toRemoveIDs = append(toRemoveIDs, id)
		}
	}
	if len(toRemoveIDs) > 0 {
		rmBody := map[string]any{"Rule": ruleName, "Ids": toRemoveIDs}
		if _, err := internalJSON(ctx, router, rCtx.Region, "AWSEvents.RemoveTargets", rmBody); err != nil {
			return "", nil, fmt.Errorf("RemoveTargets: %w", err)
		}
	}

	var toAdd []any
	for _, t := range newTargets {
		if m, ok := t.(map[string]any); ok {
			if id, _ := m["Id"].(string); id != "" && !oldIDs[id] {
				toAdd = append(toAdd, t)
			}
		}
	}
	if len(toAdd) > 0 {
		addBody := map[string]any{"Rule": ruleName, "Targets": toAdd}
		if _, err := internalJSON(ctx, router, rCtx.Region, "AWSEvents.PutTargets", addBody); err != nil {
			return "", nil, fmt.Errorf("PutTargets: %w", err)
		}
	}

	return physicalID, map[string]string{"Arn": physicalID}, nil
}

// ── AWS::KMS::Key ──────────────────────────────────────────────────────────

type kmsKeyHandler struct{}

func (h *kmsKeyHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["Description"].(string); v != "" {
		body["Description"] = v
	}
	if v, _ := props["KeySpec"].(string); v != "" {
		body["KeySpec"] = v
	}
	if v, _ := props["KeyUsage"].(string); v != "" {
		body["KeyUsage"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "TrentService.CreateKey", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateKey: %w", err)
	}

	var resp struct {
		KeyMetadata struct {
			KeyID string `json:"KeyId"`
			Arn   string `json:"Arn"`
		} `json:"KeyMetadata"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateKey: parse response: %w", err)
	}

	attrs := map[string]string{
		"KeyId": resp.KeyMetadata.KeyID,
		"Arn":   resp.KeyMetadata.Arn,
	}
	return resp.KeyMetadata.KeyID, attrs, nil
}

func (h *kmsKeyHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{
		"KeyId":               physicalID,
		"PendingWindowInDays": 7,
	}
	_, err := internalJSON(ctx, router, rCtx.Region, "TrentService.ScheduleKeyDeletion", body)
	return err
}

func (h *kmsKeyHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if oldProps != nil {
		if newSpec, _ := props["KeySpec"].(string); newSpec != "" {
			if oldSpec, _ := oldProps["KeySpec"].(string); oldSpec != "" && newSpec != oldSpec {
				return "", nil, errReplacementRequired
			}
		}
		if newUsage, _ := props["KeyUsage"].(string); newUsage != "" {
			if oldUsage, _ := oldProps["KeyUsage"].(string); oldUsage != "" && newUsage != oldUsage {
				return "", nil, errReplacementRequired
			}
		}
	}

	if keyPolicy, ok := props["KeyPolicy"]; ok {
		body := map[string]any{
			"KeyId":      physicalID,
			"PolicyName": "default",
			"Policy":     keyPolicy,
		}
		if _, err := internalJSON(ctx, router, rCtx.Region, "TrentService.PutKeyPolicy", body); err != nil {
			return "", nil, fmt.Errorf("PutKeyPolicy: %w", err)
		}
	}

	if newEnabled, ok := props["Enabled"]; ok {
		newEnabledBool := asBool(newEnabled)
		oldEnabledBool := true
		if oldProps != nil {
			if oldEnabled, ok := oldProps["Enabled"]; ok {
				oldEnabledBool = asBool(oldEnabled)
			}
		}
		if newEnabledBool != oldEnabledBool {
			kb := map[string]any{"KeyId": physicalID}
			if newEnabledBool {
				if _, err := internalJSON(ctx, router, rCtx.Region, "TrentService.EnableKey", kb); err != nil {
					return "", nil, fmt.Errorf("EnableKey: %w", err)
				}
			} else {
				if _, err := internalJSON(ctx, router, rCtx.Region, "TrentService.DisableKey", kb); err != nil {
					return "", nil, fmt.Errorf("DisableKey: %w", err)
				}
			}
		}
	}

	return physicalID, nil, nil
}

// ── AWS::KMS::Alias ────────────────────────────────────────────────────────

type kmsAliasHandler struct{}

func (h *kmsAliasHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	aliasName, _ := props["AliasName"].(string)
	targetKeyID, _ := props["TargetKeyId"].(string)

	body := map[string]any{
		"AliasName":   aliasName,
		"TargetKeyId": targetKeyID,
	}

	if _, err := internalJSON(ctx, router, rCtx.Region, "TrentService.CreateAlias", body); err != nil {
		return "", nil, fmt.Errorf("CreateAlias: %w", err)
	}

	attrs := map[string]string{
		"AliasName":   aliasName,
		"TargetKeyId": targetKeyID,
	}
	return aliasName, attrs, nil
}

func (h *kmsAliasHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"AliasName": physicalID}
	_, err := internalJSON(ctx, router, rCtx.Region, "TrentService.DeleteAlias", body)
	return err
}

// ── AWS::Lambda::EventSourceMapping ────────────────────────────────────────

type lambdaEventSourceMappingHandler struct{}

func (h *lambdaEventSourceMappingHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["EventSourceArn"].(string); v != "" {
		body["EventSourceArn"] = v
	}
	if v, _ := props["FunctionName"].(string); v != "" {
		body["FunctionName"] = v
	}
	if v, ok := props["BatchSize"]; ok {
		body["BatchSize"] = v
	}
	if v, ok := props["Enabled"]; ok {
		body["Enabled"] = v
	}
	if v, _ := props["StartingPosition"].(string); v != "" {
		body["StartingPosition"] = v
	}
	for _, key := range []string{
		"MaximumBatchingWindowInSeconds",
		"FilterCriteria",
		"MaximumRecordAgeInSeconds",
		"MaximumRetryAttempts",
		"TumblingWindowInSeconds",
		"BisectBatchOnFunctionError",
		"DestinationConfig",
		"ScalingConfig",
	} {
		if v, ok := props[key]; ok {
			body[key] = v
		}
	}

	data, _ := json.Marshal(body)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/2015-03-31/event-source-mappings/", "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateEventSourceMapping: %w", err)
	}

	var resp struct {
		UUID string `json:"UUID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateEventSourceMapping: parse response: %w", err)
	}

	esArn, _ := props["EventSourceArn"].(string)
	fnName, _ := props["FunctionName"].(string)
	attrs := map[string]string{
		"Id": resp.UUID,
	}
	if esArn != "" {
		attrs["EventSourceArn"] = esArn
	}
	if fnName != "" {
		attrs["FunctionArn"] = fnName
	}
	return resp.UUID, attrs, nil
}

func (h *lambdaEventSourceMappingHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	path := fmt.Sprintf("/2015-03-31/event-source-mappings/%s", physicalID)
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *lambdaEventSourceMappingHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if newESA, _ := props["EventSourceArn"].(string); newESA != "" {
		return "", nil, errReplacementRequired
	}
	if newFN, _ := props["FunctionName"].(string); newFN != "" {
		return "", nil, errReplacementRequired
	}

	body := map[string]any{"UUID": physicalID}
	haveMutable := false
	for _, key := range []string{
		"BatchSize",
		"Enabled",
		"MaximumBatchingWindowInSeconds",
		"FilterCriteria",
		"MaximumRecordAgeInSeconds",
		"MaximumRetryAttempts",
		"BisectBatchOnFunctionError",
		"DestinationConfig",
		"ScalingConfig",
		"FunctionResponseTypes",
		"ParallelizationFactor",
		"TumblingWindowInSeconds",
	} {
		if v, ok := props[key]; ok {
			body[key] = v
			haveMutable = true
		}
	}
	if haveMutable {
		data, _ := json.Marshal(body)
		path := fmt.Sprintf("/2015-03-31/event-source-mappings/%s", physicalID)
		if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, path, "application/json", data); err != nil {
			return "", nil, fmt.Errorf("UpdateEventSourceMapping: %w", err)
		}
	}

	return physicalID, map[string]string{"Id": physicalID}, nil
}

// ── AWS::Lambda::LayerVersion ──────────────────────────────────────────────

type lambdaLayerVersionHandler struct{}

func (h *lambdaLayerVersionHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	layerName, _ := props["LayerName"].(string)
	if layerName == "" {
		layerName = rCtx.StackName + "-layer"
	}

	body := map[string]any{}
	if v, _ := props["Description"].(string); v != "" {
		body["Description"] = v
	}
	if v, ok := props["Content"]; ok {
		body["Content"] = v
	}
	if v, ok := props["CompatibleRuntimes"]; ok {
		body["CompatibleRuntimes"] = v
	}

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/2018-10-31/layers/%s/versions", layerName)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("PublishLayerVersion: %w", err)
	}

	var resp struct {
		LayerVersionArn string `json:"LayerVersionArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("PublishLayerVersion: parse response: %w", err)
	}

	arn := resp.LayerVersionArn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:lambda:%s:%s:layer:%s:1", rCtx.Region, rCtx.AccountID, layerName)
	}

	attrs := map[string]string{
		"LayerVersionArn": arn,
	}
	return arn, attrs, nil
}

func (h *lambdaLayerVersionHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// Layer versions are immutable — no delete in most emulators.
	return nil
}

// ── AWS::StepFunctions::StateMachine ───────────────────────────────────────

type sfnStateMachineHandler struct{}

func (h *sfnStateMachineHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["StateMachineName"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["DefinitionString"].(string); v != "" {
		body["definition"] = v
	} else if v, ok := props["Definition"].(map[string]any); ok {
		j, _ := json.Marshal(v)
		body["definition"] = string(j)
	}
	if v, _ := props["RoleArn"].(string); v != "" {
		body["roleArn"] = v
	}
	if v, _ := props["StateMachineType"].(string); v != "" {
		body["type"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AWSStepFunctions.CreateStateMachine", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateStateMachine: %w", err)
	}

	var resp struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateStateMachine: parse response: %w", err)
	}

	arn := resp.StateMachineArn
	name := ""
	if idx := strings.LastIndex(arn, ":"); idx >= 0 {
		name = arn[idx+1:]
	}

	attrs := map[string]string{
		"Arn":  arn,
		"Name": name,
	}
	return arn, attrs, nil
}

func (h *sfnStateMachineHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"stateMachineArn": physicalID}
	_, err := internalJSON(ctx, router, rCtx.Region, "AWSStepFunctions.DeleteStateMachine", body)
	return err
}

func (h *sfnStateMachineHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if n, ok := props["StateMachineName"].(string); ok && n != "" {
		tail := physicalID
		if i := strings.LastIndex(physicalID, ":"); i >= 0 {
			tail = physicalID[i+1:]
		}
		if tail != n {
			return "", nil, errReplacementRequired
		}
	}
	if t, ok := props["StateMachineType"].(string); ok && t != "" {
		_ = t
	}

	body := map[string]any{"stateMachineArn": physicalID}
	haveMutable := false
	if v, ok := props["DefinitionString"].(string); ok && v != "" {
		body["definition"] = v
		haveMutable = true
	} else if v, ok := props["Definition"].(map[string]any); ok {
		j, _ := json.Marshal(v)
		body["definition"] = string(j)
		haveMutable = true
	}
	if v, _ := props["RoleArn"].(string); v != "" {
		body["roleArn"] = v
		haveMutable = true
	}
	if v, ok := props["LoggingConfiguration"]; ok {
		body["loggingConfiguration"] = v
		haveMutable = true
	}
	if v, ok := props["TracingConfiguration"]; ok {
		body["tracingConfiguration"] = v
		haveMutable = true
	}
	if haveMutable {
		if _, err := internalJSON(ctx, router, rCtx.Region, "AWSStepFunctions.UpdateStateMachine", body); err != nil {
			return "", nil, fmt.Errorf("UpdateStateMachine: %w", err)
		}
	}
	return physicalID, map[string]string{"Arn": physicalID}, nil
}

// ── AWS::S3::BucketPolicy ──────────────────────────────────────────────────

type s3BucketPolicyHandler struct{}

func (h *s3BucketPolicyHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	bucket, _ := props["Bucket"].(string)
	if bucket == "" {
		return "", nil, fmt.Errorf("BucketPolicy: Bucket is required")
	}

	policyDoc, _ := props["PolicyDocument"].(map[string]any)
	policyJSON, _ := json.Marshal(policyDoc)

	path := "/" + bucket + "?policy"
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, path, "application/json", policyJSON)
	if err != nil {
		return "", nil, fmt.Errorf("PutBucketPolicy: %w", err)
	}

	return bucket, nil, nil
}

func (h *s3BucketPolicyHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	path := "/" + physicalID + "?policy"
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *s3BucketPolicyHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	bucket, _ := props["Bucket"].(string)
	if bucket != "" && bucket != physicalID {
		return "", nil, errReplacementRequired
	}

	policyDoc, _ := props["PolicyDocument"].(map[string]any)
	policyJSON, _ := json.Marshal(policyDoc)
	path := "/" + physicalID + "?policy"
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, path, "application/json", policyJSON); err != nil {
		return "", nil, fmt.Errorf("PutBucketPolicy: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::Logs::LogStream ───────────────────────────────────────────────────

type logsLogStreamHandler struct{}

func (h *logsLogStreamHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	logGroupName, _ := props["LogGroupName"].(string)
	logStreamName, _ := props["LogStreamName"].(string)
	if logStreamName == "" {
		logStreamName = fmt.Sprintf("%s-stream", rCtx.StackName)
	}

	body := map[string]any{
		"logGroupName":  logGroupName,
		"logStreamName": logStreamName,
	}

	_, err := internalJSON(ctx, router, rCtx.Region, "Logs_20140328.CreateLogStream", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateLogStream: %w", err)
	}

	attrs := map[string]string{
		"LogStreamName": logStreamName,
		"LogGroupName":  logGroupName,
	}
	return logStreamName, attrs, nil
}

func (h *logsLogStreamHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// Log streams are cleaned up with log group deletion.
	return nil
}

// ── Helper: extract XML tag value ──────────────────────────────────────────

// extractXMLTag does a simple extraction of a tag value from raw XML.
// This avoids declaring full XML structs for each IAM response.
func extractXMLTag(body, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	i := strings.Index(body, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(body[i:], close)
	if j < 0 {
		return ""
	}
	return body[i : i+j]
}
