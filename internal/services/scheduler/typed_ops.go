package scheduler

import (
	"context"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/op"
	"github.com/overcast-sh/overcast/internal/serviceutil"
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

// tagStore is the namespace schedule and schedule-group tags live in, keyed by
// the resource's ARN.
func (s *Service) tagStore() *serviceutil.NSStore {
	return &serviceutil.NSStore{Store: s.store, NS: nsTags}
}

// schedulerTagCfg tunes the shared tag validation to Scheduler's error shape.
//
// ValidationException for both, because that is the code Scheduler returns for
// every other rejected input — a name that breaks its pattern, an undecodable
// pagination token — and a caller matching on it should not need a second one
// for tags.
var schedulerTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "ValidationException",
	InvalidCode:     "ValidationException",
	ExceededMessage: "Exceeded maximum number of tags allowed.",
}

// mergeTags adds tags to whatever the resource already carries, refusing the
// whole call if the result is something AWS would not store.
//
// Validation is on the merged set rather than on the incoming tags alone,
// which is what makes the count limit mean anything: fifty tags added one call
// at a time is still fifty-one tags. serviceutil.ApplyStoreTags does that
// load-merge-validate-save in the order every other service uses it in, so a
// refused call writes nothing.
func (s *Service) mergeTags(ctx context.Context, arn string, tags map[string]string) *protocol.AWSError {
	_, aerr := serviceutil.ApplyStoreTags(ctx, s.tagStore(), arn, tags, schedulerTagCfg)
	return aerr
}

// validateTags checks tags without storing them, for the create paths that must
// know the request is good before they commit a resource.
func validateTags(tags map[string]string) *protocol.AWSError {
	if len(tags) == 0 {
		return nil
	}
	return serviceutil.ValidateTags(schedulerTagCfg, tags)
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
