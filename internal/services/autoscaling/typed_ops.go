package autoscaling

import (
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateAutoScalingGroup":              op.NewTyped[createASGReq, createASGResp]("CreateAutoScalingGroup", h.createASGTyped),
		"UpdateAutoScalingGroup":              op.NewTyped[updateASGReq, updateASGResp]("UpdateAutoScalingGroup", h.updateASGTyped),
		"DescribeAutoScalingGroups":           op.NewTyped[describeASGsReq, describeASGsResp]("DescribeAutoScalingGroups", h.describeASGsTyped),
		"DeleteAutoScalingGroup":              op.NewTyped[deleteASGReq, deleteASGResp]("DeleteAutoScalingGroup", h.deleteASGTyped),
		"SetDesiredCapacity":                  op.NewTyped[setDesiredCapacityReq, setDesiredCapacityResp]("SetDesiredCapacity", h.setDesiredCapacityTyped),
		"TerminateInstanceInAutoScalingGroup": op.NewTyped[terminateInstanceReq, terminateInstanceResp]("TerminateInstanceInAutoScalingGroup", h.terminateInstanceTyped),
		"CreateLaunchConfiguration":           op.NewTyped[createLaunchConfigReq, createLaunchConfigResp]("CreateLaunchConfiguration", h.createLaunchConfigTyped),
		"DescribeLaunchConfigurations":        op.NewTyped[describeLaunchConfigsReq, describeLaunchConfigsResp]("DescribeLaunchConfigurations", h.describeLaunchConfigsTyped),
		"DeleteLaunchConfiguration":           op.NewTyped[deleteLaunchConfigReq, deleteLaunchConfigResp]("DeleteLaunchConfiguration", h.deleteLaunchConfigTyped),
		"PutScalingPolicy":                    op.NewTyped[putScalingPolicyReq, putScalingPolicyResp]("PutScalingPolicy", h.putScalingPolicyTyped),
		"DescribePolicies":                    op.NewTyped[describePoliciesReq, describePoliciesResp]("DescribePolicies", h.describePoliciesTyped),
		"DeletePolicy":                        op.NewTyped[deletePolicyReq, deletePolicyResp]("DeletePolicy", h.deletePolicyTyped),
		"ExecutePolicy":                       op.NewTyped[executePolicyReq, executePolicyResp]("ExecutePolicy", h.executePolicyTyped),
		"PutLifecycleHook":                    op.NewTyped[putLifecycleHookReq, putLifecycleHookResp]("PutLifecycleHook", h.putLifecycleHookTyped),
		"DescribeLifecycleHooks":              op.NewTyped[describeLifecycleHooksReq, describeLifecycleHooksResp]("DescribeLifecycleHooks", h.describeLifecycleHooksTyped),
		"DeleteLifecycleHook":                 op.NewTyped[deleteLifecycleHookReq, deleteLifecycleHookResp]("DeleteLifecycleHook", h.deleteLifecycleHookTyped),
		"CompleteLifecycleAction":             op.NewTyped[completeLifecycleActionReq, completeLifecycleActionResp]("CompleteLifecycleAction", h.completeLifecycleActionTyped),
		"RecordLifecycleActionHeartbeat":      op.NewTyped[recordHeartbeatReq, recordHeartbeatResp]("RecordLifecycleActionHeartbeat", h.recordHeartbeatTyped),
		"CreateOrUpdateTags":                  op.NewTyped[createOrUpdateTagsReq, createOrUpdateTagsResp]("CreateOrUpdateTags", h.createOrUpdateTagsTyped),
		"DeleteTags":                          op.NewTyped[deleteTagsReq, deleteTagsResp]("DeleteTags", h.deleteTagsTyped),
		"DescribeTags":                        op.NewTyped[describeTagsReq, describeTagsResp]("DescribeTags", h.describeTagsTyped),
		"DescribeAutoScalingInstances":        op.NewTyped[describeInstancesReq, describeInstancesResp]("DescribeAutoScalingInstances", h.describeInstancesTyped),
		"DescribeScalingActivities":           op.NewTyped[describeActivitiesReq, describeActivitiesResp]("DescribeScalingActivities", h.describeActivitiesTyped),
		"SetInstanceHealth":                   op.NewTyped[setInstanceHealthReq, setInstanceHealthResp]("SetInstanceHealth", h.setInstanceHealthTyped),
		"SetInstanceProtection":               op.NewTyped[setInstanceProtectionReq, setInstanceProtectionResp]("SetInstanceProtection", h.setInstanceProtectionTyped),
	}
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}
