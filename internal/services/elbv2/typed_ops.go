package elbv2

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateLoadBalancer":    op.NewTyped("CreateLoadBalancer", h.createLoadBalancerTyped),
		"DescribeLoadBalancers": op.NewTyped("DescribeLoadBalancers", h.describeLoadBalancersTyped),
		"DeleteLoadBalancer":    op.NewTyped("DeleteLoadBalancer", h.deleteLoadBalancerTyped),
		"CreateTargetGroup":     op.NewTyped("CreateTargetGroup", h.createTargetGroupTyped),
		"DescribeTargetGroups":  op.NewTyped("DescribeTargetGroups", h.describeTargetGroupsTyped),
		"DeleteTargetGroup":     op.NewTyped("DeleteTargetGroup", h.deleteTargetGroupTyped),
		"CreateListener":        op.NewTyped("CreateListener", h.createListenerTyped),
		"DescribeListeners":     op.NewTyped("DescribeListeners", h.describeListenersTyped),
		"DeleteListener":        op.NewTyped("DeleteListener", h.deleteListenerTyped),
		"RegisterTargets":       op.NewTyped("RegisterTargets", h.registerTargetsTyped),
		"DeregisterTargets":     op.NewTyped("DeregisterTargets", h.deregisterTargetsTyped),
		"DescribeTargetHealth":  op.NewTyped("DescribeTargetHealth", h.describeTargetHealthTyped),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}
