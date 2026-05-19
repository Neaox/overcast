package autoscaling

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateAutoScalingGroup":              op.NewTyped[createASGReq, asgEmptyResp]("CreateAutoScalingGroup", h.createASGTyped),
		"UpdateAutoScalingGroup":              op.NewTyped[updateASGReq, asgEmptyResp]("UpdateAutoScalingGroup", h.updateASGTyped),
		"DescribeAutoScalingGroups":           op.NewTyped[describeASGsReq, describeASGsResp]("DescribeAutoScalingGroups", h.describeASGsTyped),
		"DeleteAutoScalingGroup":              op.NewTyped[deleteASGReq, asgEmptyResp]("DeleteAutoScalingGroup", h.deleteASGTyped),
		"SetDesiredCapacity":                  op.NewTyped[setDesiredCapacityReq, asgEmptyResp]("SetDesiredCapacity", h.setDesiredCapacityTyped),
		"TerminateInstanceInAutoScalingGroup": op.NewTyped[terminateInstanceReq, terminateInstanceResp]("TerminateInstanceInAutoScalingGroup", h.terminateInstanceTyped),
		"CreateLaunchConfiguration":           op.NewTyped[createLaunchConfigReq, asgEmptyResp]("CreateLaunchConfiguration", h.createLaunchConfigTyped),
		"DescribeLaunchConfigurations":        op.NewTyped[describeLaunchConfigsReq, describeLaunchConfigsResp]("DescribeLaunchConfigurations", h.describeLaunchConfigsTyped),
		"DeleteLaunchConfiguration":           op.NewTyped[deleteLaunchConfigReq, asgEmptyResp]("DeleteLaunchConfiguration", h.deleteLaunchConfigTyped),
		"PutScalingPolicy":                    op.NewTyped[putScalingPolicyReq, putScalingPolicyResp]("PutScalingPolicy", h.putScalingPolicyTyped),
		"DescribePolicies":                    op.NewTyped[describePoliciesReq, describePoliciesResp]("DescribePolicies", h.describePoliciesTyped),
		"DeletePolicy":                        op.NewTyped[deletePolicyReq, asgEmptyResp]("DeletePolicy", h.deletePolicyTyped),
		"PutLifecycleHook":                    op.NewTyped[putLifecycleHookReq, asgEmptyResp]("PutLifecycleHook", h.putLifecycleHookTyped),
		"DescribeLifecycleHooks":              op.NewTyped[describeLifecycleHooksReq, describeLifecycleHooksResp]("DescribeLifecycleHooks", h.describeLifecycleHooksTyped),
		"DeleteLifecycleHook":                 op.NewTyped[deleteLifecycleHookReq, asgEmptyResp]("DeleteLifecycleHook", h.deleteLifecycleHookTyped),
		"CreateOrUpdateTags":                  op.NewTyped[createOrUpdateTagsReq, asgEmptyResp]("CreateOrUpdateTags", h.createOrUpdateTagsTyped),
		"DeleteTags":                          op.NewTyped[deleteTagsReq, asgEmptyResp]("DeleteTags", h.deleteTagsTyped),
		"DescribeTags":                        op.NewTyped[describeTagsReq, describeTagsResp]("DescribeTags", h.describeTagsTyped),
		"DescribeAutoScalingInstances":        op.NewTyped[struct{}, describeInstancesResp]("DescribeAutoScalingInstances", h.describeInstancesTyped),
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
