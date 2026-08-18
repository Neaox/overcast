package scheduler

import (
	"context"
	"maps"

	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
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

// tagStore is the namespace schedule and schedule-group tags live in, keyed by
// the resource's ARN.
func (s *Service) tagStore() *serviceutil.NSStore {
	return &serviceutil.NSStore{Store: s.store, NS: nsTags}
}

func (s *Service) saveTagsJSON(ctx context.Context, arn string, tags map[string]string) {
	if len(tags) == 0 {
		return
	}
	_ = s.tagStore().Save(ctx, arn, tags)
}

func (s *Service) mergeTags(ctx context.Context, arn string, tags map[string]string) {
	existing := s.loadTags(ctx, arn)
	maps.Copy(existing, tags)
	_ = s.tagStore().Save(ctx, arn, existing)
}

func (s *Service) removeTags(ctx context.Context, arn string, keys []string) {
	existing := s.loadTags(ctx, arn)
	for _, k := range keys {
		delete(existing, k)
	}
	_ = s.tagStore().Save(ctx, arn, existing)
}

func (s *Service) loadTags(ctx context.Context, arn string) map[string]string {
	tags, _ := s.tagStore().Load(ctx, arn)
	return tags
}

// deleteTags removes the tags of a resource that is going away.
//
// Nothing ties a tag blob to the lifetime of the record it describes, so the
// delete paths that forgot this left ListTagsForResource answering for a
// schedule or group that no longer existed.
func (s *Service) deleteTags(ctx context.Context, arn string) error {
	if aerr := s.tagStore().Delete(ctx, arn); aerr != nil {
		return aerr
	}
	return nil
}
