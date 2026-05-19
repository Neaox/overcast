package autoscaling

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── Request types (json tags used by codec.Decode for form mapping) ───

type createASGReq struct {
	AutoScalingGroupName    string   `json:"AutoScalingGroupName"`
	AvailabilityZones       []string `json:"AvailabilityZones"`
	LaunchConfigurationName string   `json:"LaunchConfigurationName"`
	MinSize                 int      `json:"MinSize"`
	MaxSize                 int      `json:"MaxSize"`
	DesiredCapacity         int      `json:"DesiredCapacity"`
	DefaultCooldown         int      `json:"DefaultCooldown"`
}

type updateASGReq struct {
	AutoScalingGroupName    string `json:"AutoScalingGroupName"`
	MinSize                 int    `json:"MinSize"`
	MaxSize                 int    `json:"MaxSize"`
	DesiredCapacity         int    `json:"DesiredCapacity"`
	LaunchConfigurationName string `json:"LaunchConfigurationName"`
	DefaultCooldown         int    `json:"DefaultCooldown"`
}

type describeASGsReq struct {
	AutoScalingGroupNames []string `json:"AutoScalingGroupNames"`
}

type deleteASGReq struct {
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
}

type setDesiredCapacityReq struct {
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
	DesiredCapacity      int    `json:"DesiredCapacity"`
}

type terminateInstanceReq struct {
	AutoScalingGroupName          string `json:"AutoScalingGroupName"`
	InstanceId                    string `json:"InstanceId"`
	ShouldDecrementDesiredCapacity string `json:"ShouldDecrementDesiredCapacity"`
}

type createLaunchConfigReq struct {
	LaunchConfigurationName string   `json:"LaunchConfigurationName"`
	ImageId                 string   `json:"ImageId"`
	InstanceType            string   `json:"InstanceType"`
	KeyName                 string   `json:"KeyName"`
	SecurityGroups          []string `json:"SecurityGroups"`
	IamInstanceProfile      string   `json:"IamInstanceProfile"`
	UserData                string   `json:"UserData"`
}

type describeLaunchConfigsReq struct {
	LaunchConfigurationNames []string `json:"LaunchConfigurationNames"`
}

type deleteLaunchConfigReq struct {
	LaunchConfigurationName string `json:"LaunchConfigurationName"`
}

type putScalingPolicyReq struct {
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
	PolicyName           string `json:"PolicyName"`
	PolicyType           string `json:"PolicyType"`
	AdjustmentType       string `json:"AdjustmentType"`
	ScalingAdjustment    int    `json:"ScalingAdjustment"`
	Cooldown             int    `json:"Cooldown"`
}

type describePoliciesReq struct {
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
}

type deletePolicyReq struct {
	PolicyName           string `json:"PolicyName"`
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
}

type putLifecycleHookReq struct {
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
	LifecycleHookName    string `json:"LifecycleHookName"`
	LifecycleTransition  string `json:"LifecycleTransition"`
	DefaultResult        string `json:"DefaultResult"`
	HeartbeatTimeout     int    `json:"HeartbeatTimeout"`
}

type describeLifecycleHooksReq struct {
	AutoScalingGroupName string   `json:"AutoScalingGroupName"`
	LifecycleHookNames   []string `json:"LifecycleHookNames"`
}

type deleteLifecycleHookReq struct {
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
	LifecycleHookName    string `json:"LifecycleHookName"`
}

type asgTagMember struct {
	ResourceId        string `json:"ResourceId"`
	ResourceType      string `json:"ResourceType"`
	Key               string `json:"Key"`
	Value             string `json:"Value"`
	PropagateAtLaunch string `json:"PropagateAtLaunch"`
}

type createOrUpdateTagsReq struct {
	Tags []asgTagMember `json:"Tags"`
}

type deleteTagsReq struct {
	Tags []asgTagMember `json:"Tags"`
}

type asgFilterMember struct {
	Name   string   `json:"Name"`
	Values []string `json:"Values"`
}

type describeTagsReq struct {
	Filters []asgFilterMember `json:"Filters"`
}

// ── Response types (xml tags for QueryXML codec WriteResponse) ─────

type asgResponseMeta struct {
	RequestId string `xml:"RequestId"`
}

type asgEmptyResp struct {
	XMLName struct{}        `xml:""`
	Xmlns   string          `xml:"xmlns,attr"`
	Meta    asgResponseMeta `xml:"ResponseMetadata"`
}

type describeASGsResp struct {
	XMLName struct{}       `xml:"DescribeAutoScalingGroupsResponse"`
	Xmlns   string         `xml:"xmlns,attr"`
	Result  asgGroupsResult `xml:"DescribeAutoScalingGroupsResult"`
	Meta    asgResponseMeta `xml:"ResponseMetadata"`
}

type asgGroupsResult struct {
	AutoScalingGroups []asgXMLGroup `xml:"AutoScalingGroups>member"`
}

type asgXMLGroup struct {
	AutoScalingGroupName    string   `xml:"AutoScalingGroupName"`
	AutoScalingGroupARN     string   `xml:"AutoScalingGroupARN"`
	LaunchConfigurationName string   `xml:"LaunchConfigurationName,omitempty"`
	MinSize                 int      `xml:"MinSize"`
	MaxSize                 int      `xml:"MaxSize"`
	DesiredCapacity         int      `xml:"DesiredCapacity"`
	DefaultCooldown         int      `xml:"DefaultCooldown"`
	AvailabilityZones       []string `xml:"AvailabilityZones>member"`
	Status                  string   `xml:"Status"`
	CreatedTime             string   `xml:"CreatedTime"`
	Instances               struct{} `xml:"Instances"`
	LoadBalancerNames       struct{} `xml:"LoadBalancerNames"`
	TargetGroupARNs         struct{} `xml:"TargetGroupARNs"`
	TerminationPolicies     struct{} `xml:"TerminationPolicies"`
	Tags                    struct{} `xml:"Tags"`
	SuspendedProcesses      struct{} `xml:"SuspendedProcesses"`
	EnabledMetrics          struct{} `xml:"EnabledMetrics"`
}

type terminateInstanceResp struct {
	XMLName struct{}                 `xml:"TerminateInstanceInAutoScalingGroupResponse"`
	Xmlns   string                   `xml:"xmlns,attr"`
	Result  terminateInstanceResult  `xml:"TerminateInstanceInAutoScalingGroupResult"`
	Meta    asgResponseMeta          `xml:"ResponseMetadata"`
}

type terminateInstanceResult struct {
	Activity asgActivityXML `xml:"Activity"`
}

type asgActivityXML struct {
	ActivityId           string `xml:"ActivityId"`
	AutoScalingGroupName string `xml:"AutoScalingGroupName"`
	Description          string `xml:"Description"`
	StatusCode           string `xml:"StatusCode"`
	StartTime            string `xml:"StartTime"`
}

type describeLaunchConfigsResp struct {
	XMLName struct{}             `xml:"DescribeLaunchConfigurationsResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  launchConfigsResult  `xml:"DescribeLaunchConfigurationsResult"`
	Meta    asgResponseMeta      `xml:"ResponseMetadata"`
}

type launchConfigsResult struct {
	LaunchConfigurations []asgXMLLaunchConfig `xml:"LaunchConfigurations>member"`
}

type asgXMLLaunchConfig struct {
	LaunchConfigurationName string   `xml:"LaunchConfigurationName"`
	LaunchConfigurationARN  string   `xml:"LaunchConfigurationARN"`
	ImageId                 string   `xml:"ImageId"`
	InstanceType            string   `xml:"InstanceType"`
	KeyName                 string   `xml:"KeyName,omitempty"`
	SecurityGroups          []string `xml:"SecurityGroups>member,omitempty"`
	IamInstanceProfile      string   `xml:"IamInstanceProfile,omitempty"`
	CreatedTime             string   `xml:"CreatedTime"`
}

type putScalingPolicyResp struct {
	XMLName struct{}              `xml:"PutScalingPolicyResponse"`
	Xmlns   string                `xml:"xmlns,attr"`
	Result  putScalingPolicyResult `xml:"PutScalingPolicyResult"`
	Meta    asgResponseMeta       `xml:"ResponseMetadata"`
}

type putScalingPolicyResult struct {
	PolicyARN string `xml:"PolicyARN"`
}

type describePoliciesResp struct {
	XMLName struct{}             `xml:"DescribePoliciesResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  policiesResult       `xml:"DescribePoliciesResult"`
	Meta    asgResponseMeta      `xml:"ResponseMetadata"`
}

type policiesResult struct {
	ScalingPolicies []asgXMLPolicy `xml:"ScalingPolicies>member"`
}

type asgXMLPolicy struct {
	PolicyARN            string `xml:"PolicyARN"`
	PolicyName           string `xml:"PolicyName"`
	AutoScalingGroupName string `xml:"AutoScalingGroupName"`
	PolicyType           string `xml:"PolicyType,omitempty"`
	AdjustmentType       string `xml:"AdjustmentType,omitempty"`
	ScalingAdjustment    int    `xml:"ScalingAdjustment"`
	Cooldown             int    `xml:"Cooldown"`
}

type describeLifecycleHooksResp struct {
	XMLName struct{}               `xml:"DescribeLifecycleHooksResponse"`
	Xmlns   string                 `xml:"xmlns,attr"`
	Result  lifecycleHooksResult   `xml:"DescribeLifecycleHooksResult"`
	Meta    asgResponseMeta        `xml:"ResponseMetadata"`
}

type lifecycleHooksResult struct {
	LifecycleHooks []asgXMLHook `xml:"LifecycleHooks>member"`
}

type asgXMLHook struct {
	LifecycleHookName    string `xml:"LifecycleHookName"`
	AutoScalingGroupName string `xml:"AutoScalingGroupName"`
	LifecycleTransition  string `xml:"LifecycleTransition,omitempty"`
	DefaultResult        string `xml:"DefaultResult,omitempty"`
	HeartbeatTimeout     int    `xml:"HeartbeatTimeout"`
}

type describeTagsResp struct {
	XMLName struct{}          `xml:"DescribeTagsResponse"`
	Xmlns   string            `xml:"xmlns,attr"`
	Result  tagsResult        `xml:"DescribeTagsResult"`
	Meta    asgResponseMeta   `xml:"ResponseMetadata"`
}

type tagsResult struct {
	Tags []asgXMLTag `xml:"Tags>member"`
}

type asgXMLTag struct {
	ResourceId        string `xml:"ResourceId"`
	ResourceType      string `xml:"ResourceType,omitempty"`
	Key               string `xml:"Key"`
	Value             string `xml:"Value,omitempty"`
	PropagateAtLaunch bool   `xml:"PropagateAtLaunch"`
}

type describeInstancesResp struct {
	XMLName struct{}       `xml:"DescribeAutoScalingInstancesResponse"`
	Xmlns   string         `xml:"xmlns,attr"`
	Result  struct{}       `xml:"DescribeAutoScalingInstancesResult"`
	Meta    asgResponseMeta `xml:"ResponseMetadata"`
}

// ── Typed handler functions ────────────────────────────────────────

func asgErr(code, message string, httpStatus int) *protocol.AWSError {
	return &protocol.AWSError{Code: code, Message: message, HTTPStatus: httpStatus}
}

func asgMetaFromCtx(ctx context.Context) asgResponseMeta {
	return asgResponseMeta{RequestId: protocol.RequestIDFromContext(ctx)}
}

func (h *Handler) createASGTyped(ctx context.Context, req *createASGReq) (*asgEmptyResp, *protocol.AWSError) {
	if req.AutoScalingGroupName == "" {
		return nil, asgErr("ValidationError", "AutoScalingGroupName is required", http.StatusBadRequest)
	}

	desired := req.DesiredCapacity
	if desired == 0 {
		desired = req.MinSize
	}

	asg := AutoScalingGroup{
		AutoScalingGroupName:    req.AutoScalingGroupName,
		AutoScalingGroupARN:     h.asgARN(req.AutoScalingGroupName),
		LaunchConfigurationName: req.LaunchConfigurationName,
		MinSize:                 req.MinSize,
		MaxSize:                 req.MaxSize,
		DesiredCapacity:         desired,
		DefaultCooldown:         req.DefaultCooldown,
		AvailabilityZones:       req.AvailabilityZones,
		Status:                  "InService",
		CreatedTime:             h.clk.Now(),
	}

	raw, err := json.Marshal(&asg)
	if err != nil {
		return nil, asgErr("InternalError", "failed to persist group", http.StatusInternalServerError)
	}
	if err := h.store.Set(ctx, nsGroups, req.AutoScalingGroupName, string(raw)); err != nil {
		return nil, asgErr("InternalError", "failed to persist group", http.StatusInternalServerError)
	}

	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) updateASGTyped(ctx context.Context, req *updateASGReq) (*asgEmptyResp, *protocol.AWSError) {
	if req.AutoScalingGroupName == "" {
		return nil, asgErr("ValidationError", "AutoScalingGroupName is required", http.StatusBadRequest)
	}

	raw, found, err := h.store.Get(ctx, nsGroups, req.AutoScalingGroupName)
	if err != nil || !found {
		return nil, asgErr("ValidationError", fmt.Sprintf("Auto Scaling group '%s' not found", req.AutoScalingGroupName), http.StatusBadRequest)
	}
	var asg AutoScalingGroup
	if json.Unmarshal([]byte(raw), &asg) != nil {
		return nil, asgErr("ValidationError", fmt.Sprintf("Auto Scaling group '%s' not found", req.AutoScalingGroupName), http.StatusBadRequest)
	}

	if req.MinSize != 0 {
		asg.MinSize = req.MinSize
	}
	if req.MaxSize != 0 {
		asg.MaxSize = req.MaxSize
	}
	if req.DesiredCapacity != 0 {
		asg.DesiredCapacity = req.DesiredCapacity
	}
	if req.LaunchConfigurationName != "" {
		asg.LaunchConfigurationName = req.LaunchConfigurationName
	}
	if req.DefaultCooldown != 0 {
		asg.DefaultCooldown = req.DefaultCooldown
	}

	raw2, err := json.Marshal(&asg)
	if err != nil {
		return nil, asgErr("InternalError", "failed to persist group", http.StatusInternalServerError)
	}
	if err := h.store.Set(ctx, nsGroups, req.AutoScalingGroupName, string(raw2)); err != nil {
		return nil, asgErr("InternalError", "failed to persist group", http.StatusInternalServerError)
	}

	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) describeASGsTyped(ctx context.Context, req *describeASGsReq) (*describeASGsResp, *protocol.AWSError) {
	filterSet := make(map[string]bool, len(req.AutoScalingGroupNames))
	for _, n := range req.AutoScalingGroupNames {
		filterSet[n] = true
	}

	pairs, scanErr := h.store.Scan(ctx, nsGroups, "")
	if scanErr != nil {
		return nil, asgErr("InternalError", "failed to scan groups", http.StatusInternalServerError)
	}

	xmlGroups := make([]asgXMLGroup, 0, len(pairs))
	for _, kv := range pairs {
		if len(filterSet) > 0 && !filterSet[kv.Key] {
			continue
		}
		var g AutoScalingGroup
		if json.Unmarshal([]byte(kv.Value), &g) != nil {
			continue
		}
		xmlGroups = append(xmlGroups, asgXMLGroup{
			AutoScalingGroupName:    g.AutoScalingGroupName,
			AutoScalingGroupARN:     g.AutoScalingGroupARN,
			LaunchConfigurationName: g.LaunchConfigurationName,
			MinSize:                 g.MinSize,
			MaxSize:                 g.MaxSize,
			DesiredCapacity:         g.DesiredCapacity,
			DefaultCooldown:         g.DefaultCooldown,
			AvailabilityZones:       g.AvailabilityZones,
			Status:                  g.Status,
			CreatedTime:             g.CreatedTime.UTC().Format(time.RFC3339),
		})
	}

	return &describeASGsResp{
		Xmlns:  asXMLNS,
		Result: asgGroupsResult{AutoScalingGroups: xmlGroups},
		Meta:   asgMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) deleteASGTyped(ctx context.Context, req *deleteASGReq) (*asgEmptyResp, *protocol.AWSError) {
	if req.AutoScalingGroupName == "" {
		return nil, asgErr("ValidationError", "AutoScalingGroupName is required", http.StatusBadRequest)
	}
	_ = h.store.Delete(ctx, nsGroups, req.AutoScalingGroupName)
	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) setDesiredCapacityTyped(ctx context.Context, req *setDesiredCapacityReq) (*asgEmptyResp, *protocol.AWSError) {
	raw, found, err := h.store.Get(ctx, nsGroups, req.AutoScalingGroupName)
	if err != nil || !found {
		return nil, asgErr("ValidationError", fmt.Sprintf("Auto Scaling group '%s' not found", req.AutoScalingGroupName), http.StatusBadRequest)
	}
	var asg AutoScalingGroup
	if json.Unmarshal([]byte(raw), &asg) != nil {
		return nil, asgErr("ValidationError", fmt.Sprintf("Auto Scaling group '%s' not found", req.AutoScalingGroupName), http.StatusBadRequest)
	}
	asg.DesiredCapacity = req.DesiredCapacity

	raw2, err := json.Marshal(&asg)
	if err != nil {
		return nil, asgErr("InternalError", "failed to persist group", http.StatusInternalServerError)
	}
	if err := h.store.Set(ctx, nsGroups, req.AutoScalingGroupName, string(raw2)); err != nil {
		return nil, asgErr("InternalError", "failed to persist group", http.StatusInternalServerError)
	}

	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) terminateInstanceTyped(ctx context.Context, req *terminateInstanceReq) (*terminateInstanceResp, *protocol.AWSError) {
	return &terminateInstanceResp{
		Xmlns: asXMLNS,
		Result: terminateInstanceResult{
			Activity: asgActivityXML{
				ActivityId:           "00000000-0000-0000-0000-000000000000",
				AutoScalingGroupName: req.AutoScalingGroupName,
				Description:          "Terminating EC2 instance: " + req.InstanceId,
				StatusCode:           "InProgress",
				StartTime:            h.clk.Now().UTC().Format(time.RFC3339),
			},
		},
		Meta: asgMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) createLaunchConfigTyped(ctx context.Context, req *createLaunchConfigReq) (*asgEmptyResp, *protocol.AWSError) {
	if req.LaunchConfigurationName == "" {
		return nil, asgErr("ValidationError", "LaunchConfigurationName is required", http.StatusBadRequest)
	}

	lc := LaunchConfiguration{
		LaunchConfigurationName: req.LaunchConfigurationName,
		LaunchConfigurationARN:  h.lcARN(req.LaunchConfigurationName),
		ImageId:                 req.ImageId,
		InstanceType:            req.InstanceType,
		KeyName:                 req.KeyName,
		SecurityGroups:          req.SecurityGroups,
		IamInstanceProfile:      req.IamInstanceProfile,
		UserData:                req.UserData,
		CreatedTime:             h.clk.Now(),
	}

	raw, err := json.Marshal(&lc)
	if err != nil {
		return nil, asgErr("InternalError", "failed to persist config", http.StatusInternalServerError)
	}
	if err := h.store.Set(ctx, nsLaunchCfgs, req.LaunchConfigurationName, string(raw)); err != nil {
		return nil, asgErr("InternalError", "failed to persist config", http.StatusInternalServerError)
	}

	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) describeLaunchConfigsTyped(ctx context.Context, req *describeLaunchConfigsReq) (*describeLaunchConfigsResp, *protocol.AWSError) {
	filterSet := make(map[string]bool, len(req.LaunchConfigurationNames))
	for _, n := range req.LaunchConfigurationNames {
		filterSet[n] = true
	}

	lcPairs, scanErr := h.store.Scan(ctx, nsLaunchCfgs, "")
	if scanErr != nil {
		return nil, asgErr("InternalError", "failed to scan configs", http.StatusInternalServerError)
	}

	xmlLCs := make([]asgXMLLaunchConfig, 0, len(lcPairs))
	for _, kv := range lcPairs {
		if len(filterSet) > 0 && !filterSet[kv.Key] {
			continue
		}
		var lc LaunchConfiguration
		if json.Unmarshal([]byte(kv.Value), &lc) != nil {
			continue
		}
		xmlLCs = append(xmlLCs, asgXMLLaunchConfig{
			LaunchConfigurationName: lc.LaunchConfigurationName,
			LaunchConfigurationARN:  lc.LaunchConfigurationARN,
			ImageId:                 lc.ImageId,
			InstanceType:            lc.InstanceType,
			KeyName:                 lc.KeyName,
			SecurityGroups:          lc.SecurityGroups,
			IamInstanceProfile:      lc.IamInstanceProfile,
			CreatedTime:             lc.CreatedTime.UTC().Format(time.RFC3339),
		})
	}

	return &describeLaunchConfigsResp{
		Xmlns:  asXMLNS,
		Result: launchConfigsResult{LaunchConfigurations: xmlLCs},
		Meta:   asgMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) deleteLaunchConfigTyped(ctx context.Context, req *deleteLaunchConfigReq) (*asgEmptyResp, *protocol.AWSError) {
	if req.LaunchConfigurationName == "" {
		return nil, asgErr("ValidationError", "LaunchConfigurationName is required", http.StatusBadRequest)
	}
	_ = h.store.Delete(ctx, nsLaunchCfgs, req.LaunchConfigurationName)
	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) putScalingPolicyTyped(ctx context.Context, req *putScalingPolicyReq) (*putScalingPolicyResp, *protocol.AWSError) {
	if req.AutoScalingGroupName == "" || req.PolicyName == "" {
		return nil, asgErr("ValidationError", "AutoScalingGroupName and PolicyName are required", http.StatusBadRequest)
	}

	arn := h.policyARN(req.AutoScalingGroupName, req.PolicyName)
	policy := ScalingPolicy{
		PolicyARN:            arn,
		PolicyName:           req.PolicyName,
		AutoScalingGroupName: req.AutoScalingGroupName,
		PolicyType:           req.PolicyType,
		AdjustmentType:       req.AdjustmentType,
		ScalingAdjustment:    req.ScalingAdjustment,
		Cooldown:             req.Cooldown,
	}

	key := req.AutoScalingGroupName + "/" + req.PolicyName
	raw, err := json.Marshal(&policy)
	if err != nil {
		return nil, asgErr("InternalError", "failed to persist policy", http.StatusInternalServerError)
	}
	if err := h.store.Set(ctx, nsPolicies, key, string(raw)); err != nil {
		return nil, asgErr("InternalError", "failed to persist policy", http.StatusInternalServerError)
	}

	return &putScalingPolicyResp{
		Xmlns:  asXMLNS,
		Result: putScalingPolicyResult{PolicyARN: arn},
		Meta:   asgMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) describePoliciesTyped(ctx context.Context, req *describePoliciesReq) (*describePoliciesResp, *protocol.AWSError) {
	policyPairs, scanErr := h.store.Scan(ctx, nsPolicies, "")
	if scanErr != nil {
		return nil, asgErr("InternalError", "failed to scan policies", http.StatusInternalServerError)
	}

	xmlPolicies := make([]asgXMLPolicy, 0, len(policyPairs))
	for _, kv := range policyPairs {
		var p ScalingPolicy
		if json.Unmarshal([]byte(kv.Value), &p) != nil {
			continue
		}
		if req.AutoScalingGroupName != "" && p.AutoScalingGroupName != req.AutoScalingGroupName {
			continue
		}
		xmlPolicies = append(xmlPolicies, asgXMLPolicy{
			PolicyARN:            p.PolicyARN,
			PolicyName:           p.PolicyName,
			AutoScalingGroupName: p.AutoScalingGroupName,
			PolicyType:           p.PolicyType,
			AdjustmentType:       p.AdjustmentType,
			ScalingAdjustment:    p.ScalingAdjustment,
			Cooldown:             p.Cooldown,
		})
	}

	return &describePoliciesResp{
		Xmlns:  asXMLNS,
		Result: policiesResult{ScalingPolicies: xmlPolicies},
		Meta:   asgMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) deletePolicyTyped(ctx context.Context, req *deletePolicyReq) (*asgEmptyResp, *protocol.AWSError) {
	if req.AutoScalingGroupName != "" {
		_ = h.store.Delete(ctx, nsPolicies, req.AutoScalingGroupName+"/"+req.PolicyName)
	} else {
		pairs, _ := h.store.Scan(ctx, nsPolicies, "")
		for _, kv := range pairs {
			var p ScalingPolicy
			if json.Unmarshal([]byte(kv.Value), &p) != nil {
				continue
			}
			if p.PolicyName == req.PolicyName || p.PolicyARN == req.PolicyName {
				_ = h.store.Delete(ctx, nsPolicies, kv.Key)
				break
			}
		}
	}
	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) putLifecycleHookTyped(ctx context.Context, req *putLifecycleHookReq) (*asgEmptyResp, *protocol.AWSError) {
	if req.AutoScalingGroupName == "" || req.LifecycleHookName == "" {
		return nil, asgErr("ValidationError", "AutoScalingGroupName and LifecycleHookName are required", http.StatusBadRequest)
	}

	hook := LifecycleHook{
		LifecycleHookName:    req.LifecycleHookName,
		AutoScalingGroupName: req.AutoScalingGroupName,
		LifecycleTransition:  req.LifecycleTransition,
		DefaultResult:        req.DefaultResult,
		HeartbeatTimeout:     req.HeartbeatTimeout,
	}
	if hook.DefaultResult == "" {
		hook.DefaultResult = "ABANDON"
	}

	key := req.AutoScalingGroupName + "/" + req.LifecycleHookName
	raw, err := json.Marshal(&hook)
	if err != nil {
		return nil, asgErr("InternalError", "failed to persist hook", http.StatusInternalServerError)
	}
	if err := h.store.Set(ctx, nsHooks, key, string(raw)); err != nil {
		return nil, asgErr("InternalError", "failed to persist hook", http.StatusInternalServerError)
	}

	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) describeLifecycleHooksTyped(ctx context.Context, req *describeLifecycleHooksReq) (*describeLifecycleHooksResp, *protocol.AWSError) {
	filterSet := make(map[string]bool, len(req.LifecycleHookNames))
	for _, n := range req.LifecycleHookNames {
		filterSet[n] = true
	}

	var prefix string
	if req.AutoScalingGroupName != "" {
		prefix = req.AutoScalingGroupName + "/"
	}
	hookPairs, scanErr := h.store.Scan(ctx, nsHooks, prefix)
	if scanErr != nil {
		return nil, asgErr("InternalError", "failed to scan hooks", http.StatusInternalServerError)
	}

	xmlHooks := make([]asgXMLHook, 0, len(hookPairs))
	for _, kv := range hookPairs {
		var hk LifecycleHook
		if json.Unmarshal([]byte(kv.Value), &hk) != nil {
			continue
		}
		if len(filterSet) > 0 && !filterSet[hk.LifecycleHookName] {
			continue
		}
		xmlHooks = append(xmlHooks, asgXMLHook{
			LifecycleHookName:    hk.LifecycleHookName,
			AutoScalingGroupName: hk.AutoScalingGroupName,
			LifecycleTransition:  hk.LifecycleTransition,
			DefaultResult:        hk.DefaultResult,
			HeartbeatTimeout:     hk.HeartbeatTimeout,
		})
	}

	return &describeLifecycleHooksResp{
		Xmlns:  asXMLNS,
		Result: lifecycleHooksResult{LifecycleHooks: xmlHooks},
		Meta:   asgMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) deleteLifecycleHookTyped(ctx context.Context, req *deleteLifecycleHookReq) (*asgEmptyResp, *protocol.AWSError) {
	key := req.AutoScalingGroupName + "/" + req.LifecycleHookName
	_ = h.store.Delete(ctx, nsHooks, key)
	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) createOrUpdateTagsTyped(ctx context.Context, req *createOrUpdateTagsReq) (*asgEmptyResp, *protocol.AWSError) {
	for _, tm := range req.Tags {
		if tm.ResourceId == "" {
			continue
		}
		tag := GroupTag{
			ResourceId:        tm.ResourceId,
			ResourceType:      tm.ResourceType,
			Key:               tm.Key,
			Value:             tm.Value,
			PropagateAtLaunch: tm.PropagateAtLaunch == "true",
		}
		storeKey := tm.ResourceId + "/" + tm.Key
		raw, err := json.Marshal(&tag)
		if err != nil {
			return nil, asgErr("InternalError", "failed to persist tag", http.StatusInternalServerError)
		}
		if err := h.store.Set(ctx, nsGroupTags, storeKey, string(raw)); err != nil {
			return nil, asgErr("InternalError", "failed to persist tag", http.StatusInternalServerError)
		}
	}
	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) deleteTagsTyped(ctx context.Context, req *deleteTagsReq) (*asgEmptyResp, *protocol.AWSError) {
	for _, tm := range req.Tags {
		if tm.ResourceId == "" {
			continue
		}
		storeKey := tm.ResourceId + "/" + tm.Key
		_ = h.store.Delete(ctx, nsGroupTags, storeKey)
	}
	return &asgEmptyResp{Xmlns: asXMLNS, Meta: asgMetaFromCtx(ctx)}, nil
}

func (h *Handler) describeTagsTyped(ctx context.Context, req *describeTagsReq) (*describeTagsResp, *protocol.AWSError) {
	var resourceFilter string
	for _, f := range req.Filters {
		if f.Name == "auto-scaling-group" && len(f.Values) > 0 {
			resourceFilter = f.Values[0]
		}
	}

	tagPairs, scanErr := h.store.Scan(ctx, nsGroupTags, "")
	if scanErr != nil {
		return nil, asgErr("InternalError", "failed to scan tags", http.StatusInternalServerError)
	}

	xmlTags := make([]asgXMLTag, 0, len(tagPairs))
	for _, kv := range tagPairs {
		var t GroupTag
		if json.Unmarshal([]byte(kv.Value), &t) != nil {
			continue
		}
		if resourceFilter != "" && t.ResourceId != resourceFilter {
			continue
		}
		xmlTags = append(xmlTags, asgXMLTag{
			ResourceId:        t.ResourceId,
			ResourceType:      t.ResourceType,
			Key:               t.Key,
			Value:             t.Value,
			PropagateAtLaunch: t.PropagateAtLaunch,
		})
	}

	return &describeTagsResp{
		Xmlns:  asXMLNS,
		Result: tagsResult{Tags: xmlTags},
		Meta:   asgMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) describeInstancesTyped(ctx context.Context, _ *struct{}) (*describeInstancesResp, *protocol.AWSError) {
	return &describeInstancesResp{
		Xmlns: asXMLNS,
		Meta:  asgMetaFromCtx(ctx),
	}, nil
}
