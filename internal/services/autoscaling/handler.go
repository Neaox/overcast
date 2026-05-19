package autoscaling

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds Auto Scaling handler dependencies.
type Handler struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, log: log, clk: clk}
	h.initOps()
	return h
}

func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"CreateAutoScalingGroup":              h.CreateAutoScalingGroup,
		"UpdateAutoScalingGroup":              h.UpdateAutoScalingGroup,
		"DescribeAutoScalingGroups":           h.DescribeAutoScalingGroups,
		"DeleteAutoScalingGroup":              h.DeleteAutoScalingGroup,
		"SetDesiredCapacity":                  h.SetDesiredCapacity,
		"TerminateInstanceInAutoScalingGroup": h.TerminateInstanceInAutoScalingGroup,
		"CreateLaunchConfiguration":           h.CreateLaunchConfiguration,
		"DescribeLaunchConfigurations":        h.DescribeLaunchConfigurations,
		"DeleteLaunchConfiguration":           h.DeleteLaunchConfiguration,
		"PutScalingPolicy":                    h.PutScalingPolicy,
		"DescribePolicies":                    h.DescribePolicies,
		"DeletePolicy":                        h.DeletePolicy,
		"PutLifecycleHook":                    h.PutLifecycleHook,
		"DescribeLifecycleHooks":              h.DescribeLifecycleHooks,
		"DeleteLifecycleHook":                 h.DeleteLifecycleHook,
		"CreateOrUpdateTags":                  h.CreateOrUpdateTags,
		"DeleteTags":                          h.DeleteTags,
		"DescribeTags":                        h.DescribeTags,
		"DescribeAutoScalingInstances":        h.DescribeAutoScalingInstances,
	}
	h.typedOp = h.typedOps()
}

func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		protocol.NotImplementedQueryXML(w, r)
		return
	}
	action := r.FormValue("Action")
	if fn, ok := h.ops[action]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedQueryXML(w, r)
}

// ─── Helpers ───────────────────────────────────────────────────────

func (h *Handler) accountID() string {
	if h.cfg != nil && h.cfg.AccountID != "" {
		return h.cfg.AccountID
	}
	return "000000000000"
}

func (h *Handler) region() string {
	if h.cfg != nil && h.cfg.Region != "" {
		return h.cfg.Region
	}
	return "us-east-1"
}

func (h *Handler) asgARN(name string) string {
	return fmt.Sprintf("arn:aws:autoscaling:%s:%s:autoScalingGroup:00000000-0000-0000-0000-000000000001:autoScalingGroupName/%s",
		h.region(), h.accountID(), name)
}

func (h *Handler) lcARN(name string) string {
	return fmt.Sprintf("arn:aws:autoscaling:%s:%s:launchConfiguration:00000000-0000-0000-0000-000000000002:launchConfigurationName/%s",
		h.region(), h.accountID(), name)
}

func (h *Handler) policyARN(asgName, policyName string) string {
	return fmt.Sprintf("arn:aws:autoscaling:%s:%s:scalingPolicy:00000000-0000-0000-0000-000000000003:autoScalingGroupName/%s:policyName/%s",
		h.region(), h.accountID(), asgName, policyName)
}

func (h *Handler) asWriteXML(w http.ResponseWriter, r *http.Request, v interface{}) {
	protocol.WriteXML(w, r, http.StatusOK, v)
}

func (h *Handler) asResponseMeta(r *http.Request) protocol.ResponseMetadata {
	return protocol.QueryResponseMetadata(r)
}

func (h *Handler) asPutJSON(r *http.Request, ns, key string, v interface{}) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return h.store.Set(r.Context(), ns, key, string(raw))
}

func (h *Handler) asGetASG(r *http.Request, name string) (*AutoScalingGroup, bool) {
	raw, found, err := h.store.Get(r.Context(), nsGroups, name)
	if err != nil || !found {
		return nil, false
	}
	var asg AutoScalingGroup
	if json.Unmarshal([]byte(raw), &asg) != nil {
		return nil, false
	}
	return &asg, true
}

// ─── Auto Scaling Groups ───────────────────────────────────────────

func (h *Handler) CreateAutoScalingGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("AutoScalingGroupName")
	if name == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ValidationError", Message: "AutoScalingGroupName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	azs := parseIndexedStrings(r, "AvailabilityZones")

	desired := parseInt(r.FormValue("DesiredCapacity"))
	min := parseInt(r.FormValue("MinSize"))
	if desired == 0 {
		desired = min
	}

	asg := AutoScalingGroup{
		AutoScalingGroupName:    name,
		AutoScalingGroupARN:     h.asgARN(name),
		LaunchConfigurationName: r.FormValue("LaunchConfigurationName"),
		MinSize:                 min,
		MaxSize:                 parseInt(r.FormValue("MaxSize")),
		DesiredCapacity:         desired,
		DefaultCooldown:         parseInt(r.FormValue("DefaultCooldown")),
		AvailabilityZones:       azs,
		Status:                  "InService",
		CreatedTime:             h.clk.Now(),
	}

	if err := h.asPutJSON(r, nsGroups, name, &asg); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type response struct {
		XMLName          xml.Name                  `xml:"CreateAutoScalingGroupResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

func (h *Handler) UpdateAutoScalingGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("AutoScalingGroupName")
	if name == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ValidationError", Message: "AutoScalingGroupName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	asg, found := h.asGetASG(r, name)
	if !found {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ValidationError", Message: fmt.Sprintf("Auto Scaling group '%s' not found", name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if v := r.FormValue("MinSize"); v != "" {
		asg.MinSize = parseInt(v)
	}
	if v := r.FormValue("MaxSize"); v != "" {
		asg.MaxSize = parseInt(v)
	}
	if v := r.FormValue("DesiredCapacity"); v != "" {
		asg.DesiredCapacity = parseInt(v)
	}
	if v := r.FormValue("LaunchConfigurationName"); v != "" {
		asg.LaunchConfigurationName = v
	}
	if v := r.FormValue("DefaultCooldown"); v != "" {
		asg.DefaultCooldown = parseInt(v)
	}

	if err := h.asPutJSON(r, nsGroups, name, asg); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type response struct {
		XMLName          xml.Name                  `xml:"UpdateAutoScalingGroupResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

func (h *Handler) DescribeAutoScalingGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filterNames := parseIndexedStrings(r, "AutoScalingGroupNames")
	filterSet := make(map[string]bool, len(filterNames))
	for _, n := range filterNames {
		filterSet[n] = true
	}

	pairs, scanErr := h.store.Scan(ctx, nsGroups, "")
	if scanErr != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type xmlGroup struct {
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

	xmlGroups := make([]xmlGroup, 0, len(pairs))
	for _, kv := range pairs {
		if len(filterSet) > 0 && !filterSet[kv.Key] {
			continue
		}
		var g AutoScalingGroup
		if json.Unmarshal([]byte(kv.Value), &g) != nil {
			continue
		}
		xmlGroups = append(xmlGroups, xmlGroup{
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

	type result struct {
		AutoScalingGroups []xmlGroup `xml:"AutoScalingGroups>member"`
	}
	type response struct {
		XMLName                         xml.Name                  `xml:"DescribeAutoScalingGroupsResponse"`
		XMLNS                           string                    `xml:"xmlns,attr"`
		DescribeAutoScalingGroupsResult result                    `xml:"DescribeAutoScalingGroupsResult"`
		ResponseMetadata                protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{
		XMLNS:                           asXMLNS,
		DescribeAutoScalingGroupsResult: result{AutoScalingGroups: xmlGroups},
		ResponseMetadata:                h.asResponseMeta(r),
	})
}

func (h *Handler) DeleteAutoScalingGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("AutoScalingGroupName")
	if name == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ValidationError", Message: "AutoScalingGroupName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	_ = h.store.Delete(r.Context(), nsGroups, name)

	type response struct {
		XMLName          xml.Name                  `xml:"DeleteAutoScalingGroupResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

func (h *Handler) SetDesiredCapacity(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("AutoScalingGroupName")
	asg, found := h.asGetASG(r, name)
	if !found {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ValidationError", Message: fmt.Sprintf("Auto Scaling group '%s' not found", name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	asg.DesiredCapacity = parseInt(r.FormValue("DesiredCapacity"))
	if err := h.asPutJSON(r, nsGroups, name, asg); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type response struct {
		XMLName          xml.Name                  `xml:"SetDesiredCapacityResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

func (h *Handler) TerminateInstanceInAutoScalingGroup(w http.ResponseWriter, r *http.Request) {
	type activity struct {
		ActivityId           string `xml:"ActivityId"`
		AutoScalingGroupName string `xml:"AutoScalingGroupName"`
		Description          string `xml:"Description"`
		StatusCode           string `xml:"StatusCode"`
		StartTime            string `xml:"StartTime"`
	}
	type result struct {
		Activity activity `xml:"Activity"`
	}
	type response struct {
		XMLName                                   xml.Name                  `xml:"TerminateInstanceInAutoScalingGroupResponse"`
		XMLNS                                     string                    `xml:"xmlns,attr"`
		TerminateInstanceInAutoScalingGroupResult result                    `xml:"TerminateInstanceInAutoScalingGroupResult"`
		ResponseMetadata                          protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{
		XMLNS: asXMLNS,
		TerminateInstanceInAutoScalingGroupResult: result{
			Activity: activity{
				ActivityId:           "00000000-0000-0000-0000-000000000000",
				AutoScalingGroupName: r.FormValue("AutoScalingGroupName"),
				Description:          "Terminating EC2 instance: " + r.FormValue("InstanceId"),
				StatusCode:           "InProgress",
				StartTime:            h.clk.Now().UTC().Format(time.RFC3339),
			},
		},
		ResponseMetadata: h.asResponseMeta(r),
	})
}

// ─── Launch Configurations ─────────────────────────────────────────

func (h *Handler) CreateLaunchConfiguration(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("LaunchConfigurationName")
	if name == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ValidationError", Message: "LaunchConfigurationName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	var sgs []string
	for i := 1; ; i++ {
		sg := r.FormValue(fmt.Sprintf("SecurityGroups.member.%d", i))
		if sg == "" {
			break
		}
		sgs = append(sgs, sg)
	}

	lc := LaunchConfiguration{
		LaunchConfigurationName: name,
		LaunchConfigurationARN:  h.lcARN(name),
		ImageId:                 r.FormValue("ImageId"),
		InstanceType:            r.FormValue("InstanceType"),
		KeyName:                 r.FormValue("KeyName"),
		SecurityGroups:          sgs,
		IamInstanceProfile:      r.FormValue("IamInstanceProfile"),
		UserData:                r.FormValue("UserData"),
		CreatedTime:             h.clk.Now(),
	}

	if err := h.asPutJSON(r, nsLaunchCfgs, name, &lc); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type response struct {
		XMLName          xml.Name                  `xml:"CreateLaunchConfigurationResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

func (h *Handler) DescribeLaunchConfigurations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filterNames := parseIndexedStrings(r, "LaunchConfigurationNames")
	filterSet := make(map[string]bool, len(filterNames))
	for _, n := range filterNames {
		filterSet[n] = true
	}

	lcPairs, scanErr := h.store.Scan(ctx, nsLaunchCfgs, "")
	if scanErr != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type xmlLC struct {
		LaunchConfigurationName string   `xml:"LaunchConfigurationName"`
		LaunchConfigurationARN  string   `xml:"LaunchConfigurationARN"`
		ImageId                 string   `xml:"ImageId"`
		InstanceType            string   `xml:"InstanceType"`
		KeyName                 string   `xml:"KeyName,omitempty"`
		SecurityGroups          []string `xml:"SecurityGroups>member,omitempty"`
		IamInstanceProfile      string   `xml:"IamInstanceProfile,omitempty"`
		CreatedTime             string   `xml:"CreatedTime"`
	}

	xmlLCs := make([]xmlLC, 0, len(lcPairs))
	for _, kv := range lcPairs {
		if len(filterSet) > 0 && !filterSet[kv.Key] {
			continue
		}
		var lc LaunchConfiguration
		if json.Unmarshal([]byte(kv.Value), &lc) != nil {
			continue
		}
		xmlLCs = append(xmlLCs, xmlLC{
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

	type result struct {
		LaunchConfigurations []xmlLC `xml:"LaunchConfigurations>member"`
	}
	type response struct {
		XMLName                            xml.Name                  `xml:"DescribeLaunchConfigurationsResponse"`
		XMLNS                              string                    `xml:"xmlns,attr"`
		DescribeLaunchConfigurationsResult result                    `xml:"DescribeLaunchConfigurationsResult"`
		ResponseMetadata                   protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{
		XMLNS:                              asXMLNS,
		DescribeLaunchConfigurationsResult: result{LaunchConfigurations: xmlLCs},
		ResponseMetadata:                   h.asResponseMeta(r),
	})
}

func (h *Handler) DeleteLaunchConfiguration(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("LaunchConfigurationName")
	if name == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ValidationError", Message: "LaunchConfigurationName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	_ = h.store.Delete(r.Context(), nsLaunchCfgs, name)

	type response struct {
		XMLName          xml.Name                  `xml:"DeleteLaunchConfigurationResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

// ─── Scaling Policies ──────────────────────────────────────────────

func (h *Handler) PutScalingPolicy(w http.ResponseWriter, r *http.Request) {
	asgName := r.FormValue("AutoScalingGroupName")
	policyName := r.FormValue("PolicyName")
	if asgName == "" || policyName == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ValidationError", Message: "AutoScalingGroupName and PolicyName are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	arn := h.policyARN(asgName, policyName)
	policy := ScalingPolicy{
		PolicyARN:            arn,
		PolicyName:           policyName,
		AutoScalingGroupName: asgName,
		PolicyType:           r.FormValue("PolicyType"),
		AdjustmentType:       r.FormValue("AdjustmentType"),
		ScalingAdjustment:    parseInt(r.FormValue("ScalingAdjustment")),
		Cooldown:             parseInt(r.FormValue("Cooldown")),
	}

	key := asgName + "/" + policyName
	if err := h.asPutJSON(r, nsPolicies, key, &policy); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type result struct {
		PolicyARN string `xml:"PolicyARN"`
	}
	type response struct {
		XMLName                xml.Name                  `xml:"PutScalingPolicyResponse"`
		XMLNS                  string                    `xml:"xmlns,attr"`
		PutScalingPolicyResult result                    `xml:"PutScalingPolicyResult"`
		ResponseMetadata       protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{
		XMLNS:                  asXMLNS,
		PutScalingPolicyResult: result{PolicyARN: arn},
		ResponseMetadata:       h.asResponseMeta(r),
	})
}

func (h *Handler) DescribePolicies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asgFilter := r.FormValue("AutoScalingGroupName")

	policyPairs, scanErr := h.store.Scan(ctx, nsPolicies, "")
	if scanErr != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type xmlPolicy struct {
		PolicyARN            string `xml:"PolicyARN"`
		PolicyName           string `xml:"PolicyName"`
		AutoScalingGroupName string `xml:"AutoScalingGroupName"`
		PolicyType           string `xml:"PolicyType,omitempty"`
		AdjustmentType       string `xml:"AdjustmentType,omitempty"`
		ScalingAdjustment    int    `xml:"ScalingAdjustment"`
		Cooldown             int    `xml:"Cooldown"`
	}

	xmlPolicies := make([]xmlPolicy, 0, len(policyPairs))
	for _, kv := range policyPairs {
		var p ScalingPolicy
		if json.Unmarshal([]byte(kv.Value), &p) != nil {
			continue
		}
		if asgFilter != "" && p.AutoScalingGroupName != asgFilter {
			continue
		}
		xmlPolicies = append(xmlPolicies, xmlPolicy{
			PolicyARN:            p.PolicyARN,
			PolicyName:           p.PolicyName,
			AutoScalingGroupName: p.AutoScalingGroupName,
			PolicyType:           p.PolicyType,
			AdjustmentType:       p.AdjustmentType,
			ScalingAdjustment:    p.ScalingAdjustment,
			Cooldown:             p.Cooldown,
		})
	}

	type result struct {
		ScalingPolicies []xmlPolicy `xml:"ScalingPolicies>member"`
	}
	type response struct {
		XMLName                xml.Name                  `xml:"DescribePoliciesResponse"`
		XMLNS                  string                    `xml:"xmlns,attr"`
		DescribePoliciesResult result                    `xml:"DescribePoliciesResult"`
		ResponseMetadata       protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{
		XMLNS:                  asXMLNS,
		DescribePoliciesResult: result{ScalingPolicies: xmlPolicies},
		ResponseMetadata:       h.asResponseMeta(r),
	})
}

func (h *Handler) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	policyName := r.FormValue("PolicyName")
	asgName := r.FormValue("AutoScalingGroupName")

	ctx := r.Context()
	if asgName != "" {
		_ = h.store.Delete(ctx, nsPolicies, asgName+"/"+policyName)
	} else {
		pairs, _ := h.store.Scan(ctx, nsPolicies, "")
		for _, kv := range pairs {
			var p ScalingPolicy
			if json.Unmarshal([]byte(kv.Value), &p) != nil {
				continue
			}
			if p.PolicyName == policyName || p.PolicyARN == policyName {
				_ = h.store.Delete(ctx, nsPolicies, kv.Key)
				break
			}
		}
	}

	type response struct {
		XMLName          xml.Name                  `xml:"DeletePolicyResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

// ─── Lifecycle Hooks ───────────────────────────────────────────────

func (h *Handler) PutLifecycleHook(w http.ResponseWriter, r *http.Request) {
	asgName := r.FormValue("AutoScalingGroupName")
	hookName := r.FormValue("LifecycleHookName")
	if asgName == "" || hookName == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ValidationError", Message: "AutoScalingGroupName and LifecycleHookName are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	hook := LifecycleHook{
		LifecycleHookName:    hookName,
		AutoScalingGroupName: asgName,
		LifecycleTransition:  r.FormValue("LifecycleTransition"),
		DefaultResult:        r.FormValue("DefaultResult"),
		HeartbeatTimeout:     parseInt(r.FormValue("HeartbeatTimeout")),
	}
	if hook.DefaultResult == "" {
		hook.DefaultResult = "ABANDON"
	}

	key := asgName + "/" + hookName
	if err := h.asPutJSON(r, nsHooks, key, &hook); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type response struct {
		XMLName          xml.Name                  `xml:"PutLifecycleHookResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

func (h *Handler) DescribeLifecycleHooks(w http.ResponseWriter, r *http.Request) {
	asgName := r.FormValue("AutoScalingGroupName")
	ctx := r.Context()

	filterNames := parseIndexedStrings(r, "LifecycleHookNames")
	filterSet := make(map[string]bool, len(filterNames))
	for _, n := range filterNames {
		filterSet[n] = true
	}

	var prefix string
	if asgName != "" {
		prefix = asgName + "/"
	}
	hookPairs, scanErr := h.store.Scan(ctx, nsHooks, prefix)
	if scanErr != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type xmlHook struct {
		LifecycleHookName    string `xml:"LifecycleHookName"`
		AutoScalingGroupName string `xml:"AutoScalingGroupName"`
		LifecycleTransition  string `xml:"LifecycleTransition,omitempty"`
		DefaultResult        string `xml:"DefaultResult,omitempty"`
		HeartbeatTimeout     int    `xml:"HeartbeatTimeout"`
	}

	xmlHooks := make([]xmlHook, 0, len(hookPairs))
	for _, kv := range hookPairs {
		var hk LifecycleHook
		if json.Unmarshal([]byte(kv.Value), &hk) != nil {
			continue
		}
		if len(filterSet) > 0 && !filterSet[hk.LifecycleHookName] {
			continue
		}
		xmlHooks = append(xmlHooks, xmlHook{
			LifecycleHookName:    hk.LifecycleHookName,
			AutoScalingGroupName: hk.AutoScalingGroupName,
			LifecycleTransition:  hk.LifecycleTransition,
			DefaultResult:        hk.DefaultResult,
			HeartbeatTimeout:     hk.HeartbeatTimeout,
		})
	}

	type result struct {
		LifecycleHooks []xmlHook `xml:"LifecycleHooks>member"`
	}
	type response struct {
		XMLName                      xml.Name                  `xml:"DescribeLifecycleHooksResponse"`
		XMLNS                        string                    `xml:"xmlns,attr"`
		DescribeLifecycleHooksResult result                    `xml:"DescribeLifecycleHooksResult"`
		ResponseMetadata             protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{
		XMLNS:                        asXMLNS,
		DescribeLifecycleHooksResult: result{LifecycleHooks: xmlHooks},
		ResponseMetadata:             h.asResponseMeta(r),
	})
}

func (h *Handler) DeleteLifecycleHook(w http.ResponseWriter, r *http.Request) {
	asgName := r.FormValue("AutoScalingGroupName")
	hookName := r.FormValue("LifecycleHookName")
	key := asgName + "/" + hookName

	ctx := r.Context()
	_ = h.store.Delete(ctx, nsHooks, key)

	type response struct {
		XMLName          xml.Name                  `xml:"DeleteLifecycleHookResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

// ─── Tags ──────────────────────────────────────────────────────────

func (h *Handler) CreateOrUpdateTags(w http.ResponseWriter, r *http.Request) {
	for i := 1; ; i++ {
		prefix := fmt.Sprintf("Tags.member.%d.", i)
		resourceId := r.FormValue(prefix + "ResourceId")
		if resourceId == "" {
			break
		}
		key := r.FormValue(prefix + "Key")
		tag := GroupTag{
			ResourceId:        resourceId,
			ResourceType:      r.FormValue(prefix + "ResourceType"),
			Key:               key,
			Value:             r.FormValue(prefix + "Value"),
			PropagateAtLaunch: parseBool(r.FormValue(prefix + "PropagateAtLaunch")),
		}
		storeKey := resourceId + "/" + key
		if err := h.asPutJSON(r, nsGroupTags, storeKey, &tag); err != nil {
			protocol.WriteXMLError(w, r, protocol.ErrInternalError)
			return
		}
	}

	type response struct {
		XMLName          xml.Name                  `xml:"CreateOrUpdateTagsResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

func (h *Handler) DeleteTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	for i := 1; ; i++ {
		prefix := fmt.Sprintf("Tags.member.%d.", i)
		resourceId := r.FormValue(prefix + "ResourceId")
		if resourceId == "" {
			break
		}
		key := r.FormValue(prefix + "Key")
		storeKey := resourceId + "/" + key
		_ = h.store.Delete(ctx, nsGroupTags, storeKey)
	}

	type response struct {
		XMLName          xml.Name                  `xml:"DeleteTagsResponse"`
		XMLNS            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}

func (h *Handler) DescribeTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var resourceFilter string
	for i := 1; ; i++ {
		prefix := fmt.Sprintf("Filters.member.%d.", i)
		name := r.FormValue(prefix + "Name")
		if name == "" {
			break
		}
		if name == "auto-scaling-group" {
			resourceFilter = r.FormValue(prefix + "Values.member.1")
		}
	}

	tagPairs, scanErr := h.store.Scan(ctx, nsGroupTags, "")
	if scanErr != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	type xmlTag struct {
		ResourceId        string `xml:"ResourceId"`
		ResourceType      string `xml:"ResourceType,omitempty"`
		Key               string `xml:"Key"`
		Value             string `xml:"Value,omitempty"`
		PropagateAtLaunch bool   `xml:"PropagateAtLaunch"`
	}

	xmlTags := make([]xmlTag, 0, len(tagPairs))
	for _, kv := range tagPairs {
		var t GroupTag
		if json.Unmarshal([]byte(kv.Value), &t) != nil {
			continue
		}
		if resourceFilter != "" && t.ResourceId != resourceFilter {
			continue
		}
		xmlTags = append(xmlTags, xmlTag{
			ResourceId:        t.ResourceId,
			ResourceType:      t.ResourceType,
			Key:               t.Key,
			Value:             t.Value,
			PropagateAtLaunch: t.PropagateAtLaunch,
		})
	}

	type result struct {
		Tags []xmlTag `xml:"Tags>member"`
	}
	type response struct {
		XMLName            xml.Name                  `xml:"DescribeTagsResponse"`
		XMLNS              string                    `xml:"xmlns,attr"`
		DescribeTagsResult result                    `xml:"DescribeTagsResult"`
		ResponseMetadata   protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{
		XMLNS:              asXMLNS,
		DescribeTagsResult: result{Tags: xmlTags},
		ResponseMetadata:   h.asResponseMeta(r),
	})
}

// ─── Instances ─────────────────────────────────────────────────────

func (h *Handler) DescribeAutoScalingInstances(w http.ResponseWriter, r *http.Request) {
	type result struct{}
	type response struct {
		XMLName                            xml.Name                  `xml:"DescribeAutoScalingInstancesResponse"`
		XMLNS                              string                    `xml:"xmlns,attr"`
		DescribeAutoScalingInstancesResult result                    `xml:"DescribeAutoScalingInstancesResult"`
		ResponseMetadata                   protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	h.asWriteXML(w, r, response{XMLNS: asXMLNS, ResponseMetadata: h.asResponseMeta(r)})
}
