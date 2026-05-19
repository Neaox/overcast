package scheduler

import (
	"context"
	"encoding/json"

	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateScheduleGroup": op.NewTyped[createScheduleGroupRequest, createScheduleGroupResponse](
			"CreateScheduleGroup", s.createScheduleGroupTyped,
		),
		"GetScheduleGroup": op.NewTyped[getScheduleGroupRequest, getScheduleGroupResponse](
			"GetScheduleGroup", s.getScheduleGroupTyped,
		),
		"DeleteScheduleGroup": op.NewTypedAny[deleteScheduleGroupRequest](
			"DeleteScheduleGroup", s.deleteScheduleGroupTyped,
		),
		"ListScheduleGroups": op.NewTyped[listScheduleGroupsRequest, listScheduleGroupsResponse](
			"ListScheduleGroups", s.listScheduleGroupsTyped,
		),
		"TagResource": op.NewTypedAny[tagResourceRequest](
			"TagResource", s.tagResourceTyped,
		),
		"UntagResource": op.NewTypedAny[untagResourceRequest](
			"UntagResource", s.untagResourceTyped,
		),
		"ListTagsForResource": op.NewTyped[listTagsForResourceRequest, listTagsForResourceResponse](
			"ListTagsForResource", s.listTagsForResourceTyped,
		),
		"CreateSchedule": op.NewTyped[createScheduleRequest, createScheduleResponse](
			"CreateSchedule", s.createScheduleTyped,
		),
		"GetSchedule": op.NewTyped[getScheduleRequest, getScheduleResponse](
			"GetSchedule", s.getScheduleTyped,
		),
		"UpdateSchedule": op.NewTyped[updateScheduleRequest, updateScheduleResponse](
			"UpdateSchedule", s.updateScheduleTyped,
		),
		"DeleteSchedule": op.NewTypedAny[deleteScheduleRequest](
			"DeleteSchedule", s.deleteScheduleTyped,
		),
		"ListSchedules": op.NewTyped[listSchedulesRequest, listSchedulesResponse](
			"ListSchedules", s.listSchedulesTyped,
		),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}

func (s *Service) saveTagsJSON(ctx context.Context, arn string, tags map[string]string) {
	if len(tags) == 0 {
		return
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return
	}
	_ = s.store.Set(ctx, nsTags, arn, string(raw))
}

func (s *Service) mergeTags(ctx context.Context, arn string, tags map[string]string) {
	existing := s.loadTags(ctx, arn)
	for k, v := range tags {
		existing[k] = v
	}
	s.saveTagsJSON(ctx, arn, existing)
}

func (s *Service) removeTags(ctx context.Context, arn string, keys []string) {
	existing := s.loadTags(ctx, arn)
	for _, k := range keys {
		delete(existing, k)
	}
	s.saveTagsJSON(ctx, arn, existing)
}

func (s *Service) loadTags(ctx context.Context, arn string) map[string]string {
	raw, found, _ := s.store.Get(ctx, nsTags, arn)
	tags := map[string]string{}
	if found {
		_ = json.Unmarshal([]byte(raw), &tags)
	}
	return tags
}
