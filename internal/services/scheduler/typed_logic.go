package scheduler

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

type createScheduleGroupRequest struct {
	Name string            `json:"Name" cbor:"Name"`
	Tags map[string]string `json:"Tags" cbor:"Tags"`
}

type createScheduleGroupResponse struct {
	ScheduleGroupArn string `json:"ScheduleGroupArn" cbor:"ScheduleGroupArn"`
}

func (s *Service) createScheduleGroupTyped(ctx context.Context, req *createScheduleGroupRequest) (*createScheduleGroupResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, found := s.loadGroup(ctx, region, req.Name); found {
		return nil, &protocol.AWSError{
			Code: "ConflictException", Message: fmt.Sprintf("Schedule group %s already exists.", req.Name),
			HTTPStatus: 409,
		}
	}
	now := s.clk.Now()
	g := &ScheduleGroup{
		Name: req.Name, Arn: s.groupARN(region, req.Name), State: "ACTIVE",
		CreationDate: now, LastModificationDate: now,
	}
	if err := s.saveGroup(ctx, region, g); err != nil {
		return nil, protocol.ErrInternalError
	}
	s.saveTagsJSON(ctx, g.Arn, req.Tags)
	return &createScheduleGroupResponse{ScheduleGroupArn: g.Arn}, nil
}

type getScheduleGroupRequest struct {
	Name string `json:"Name" cbor:"Name"`
}

type getScheduleGroupResponse ScheduleGroup

func (s *Service) getScheduleGroupTyped(ctx context.Context, req *getScheduleGroupRequest) (*getScheduleGroupResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	g, found := s.loadGroup(ctx, region, req.Name)
	if !found {
		return nil, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: fmt.Sprintf("Schedule group %s does not exist.", req.Name),
			HTTPStatus: 404,
		}
	}
	return (*getScheduleGroupResponse)(g), nil
}

type deleteScheduleGroupRequest struct {
	Name string `json:"Name" cbor:"Name"`
}

func (s *Service) deleteScheduleGroupTyped(ctx context.Context, req *deleteScheduleGroupRequest) (any, *protocol.AWSError) {
	if req.Name == defaultGroup {
		return nil, &protocol.AWSError{
			Code: "ValidationException", Message: "Cannot delete default schedule group.",
			HTTPStatus: 400,
		}
	}
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, found := s.loadGroup(ctx, region, req.Name); !found {
		return nil, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: fmt.Sprintf("Schedule group %s does not exist.", req.Name),
			HTTPStatus: 404,
		}
	}
	if err := s.deleteGroup(ctx, region, req.Name); err != nil {
		return nil, protocol.ErrInternalError
	}
	return struct{}{}, nil
}

type listScheduleGroupsRequest struct{}

type listScheduleGroupsResponse struct {
	ScheduleGroups []*ScheduleGroup `json:"ScheduleGroups" cbor:"ScheduleGroups"`
}

func (s *Service) listScheduleGroupsTyped(ctx context.Context, _ *listScheduleGroupsRequest) (*listScheduleGroupsResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	groups, err := s.listGroups(ctx, region)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &listScheduleGroupsResponse{ScheduleGroups: groups}, nil
}

type tagResourceRequest struct {
	ResourceArn string            `json:"ResourceArn" cbor:"ResourceArn"`
	Tags        map[string]string `json:"Tags" cbor:"Tags"`
}

func (s *Service) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (any, *protocol.AWSError) {
	s.mergeTags(ctx, req.ResourceArn, req.Tags)
	return struct{}{}, nil
}

type untagResourceRequest struct {
	ResourceArn string   `json:"ResourceArn" cbor:"ResourceArn"`
	TagKeys     []string `json:"TagKeys" cbor:"TagKeys"`
}

func (s *Service) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (any, *protocol.AWSError) {
	s.removeTags(ctx, req.ResourceArn, req.TagKeys)
	return struct{}{}, nil
}

type listTagsForResourceRequest struct {
	ResourceArn string `json:"ResourceArn" cbor:"ResourceArn"`
}

type listTagsForResourceResponse struct {
	Tags map[string]string `json:"Tags" cbor:"Tags"`
}

func (s *Service) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	tags := s.loadTags(ctx, req.ResourceArn)
	return &listTagsForResourceResponse{Tags: tags}, nil
}

type createScheduleRequest struct {
	GroupName                  string             `json:"GroupName" cbor:"GroupName"`
	Name                       string             `json:"Name" cbor:"Name"`
	ScheduleExpression         string             `json:"ScheduleExpression" cbor:"ScheduleExpression"`
	ScheduleExpressionTimezone string             `json:"ScheduleExpressionTimezone" cbor:"ScheduleExpressionTimezone"`
	Description                string             `json:"Description" cbor:"Description"`
	FlexibleTimeWindow         flexibleTimeWindow `json:"FlexibleTimeWindow" cbor:"FlexibleTimeWindow"`
	Target                     scheduleTarget     `json:"Target" cbor:"Target"`
	State                      string             `json:"State" cbor:"State"`
}

type createScheduleResponse struct {
	ScheduleArn string `json:"ScheduleArn" cbor:"ScheduleArn"`
}

func (s *Service) createScheduleTyped(ctx context.Context, req *createScheduleRequest) (*createScheduleResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	group := req.GroupName
	if group == "" {
		group = defaultGroup
	}
	if _, found := s.loadGroup(ctx, region, group); !found {
		if group == defaultGroup {
			now := s.clk.Now()
			_ = s.saveGroup(ctx, region, &ScheduleGroup{
				Name: defaultGroup, Arn: s.groupARN(region, defaultGroup),
				State: "ACTIVE", CreationDate: now, LastModificationDate: now,
			})
		} else {
			return nil, &protocol.AWSError{
				Code: "ResourceNotFoundException", Message: fmt.Sprintf("Schedule group %s does not exist.", group),
				HTTPStatus: 404,
			}
		}
	}
	if req.ScheduleExpression == "" {
		return nil, &protocol.AWSError{
			Code: "ValidationException", Message: "ScheduleExpression is required.",
			HTTPStatus: 400,
		}
	}
	state := req.State
	if state == "" {
		state = "ENABLED"
	}
	now := s.clk.Now()
	sc := &Schedule{
		Name: req.Name, GroupName: group, Arn: s.scheduleARN(region, group, req.Name),
		State: state, ScheduleExpression: req.ScheduleExpression,
		ScheduleExpressionTimezone: req.ScheduleExpressionTimezone,
		Description:                req.Description, FlexibleTimeWindow: req.FlexibleTimeWindow,
		Target: req.Target, CreationDate: now, LastModificationDate: now,
	}
	if err := s.saveSchedule(ctx, region, sc); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &createScheduleResponse{ScheduleArn: sc.Arn}, nil
}

type getScheduleRequest struct {
	GroupName string `json:"GroupName" cbor:"GroupName"`
	Name      string `json:"Name" cbor:"Name"`
}

type getScheduleResponse Schedule

func (s *Service) getScheduleTyped(ctx context.Context, req *getScheduleRequest) (*getScheduleResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	group := req.GroupName
	if group == "" {
		group = defaultGroup
	}
	sc, found := s.loadSchedule(ctx, region, group, req.Name)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Schedule %s in group %s does not exist.", req.Name, group),
			HTTPStatus: 404,
		}
	}
	return (*getScheduleResponse)(sc), nil
}

type updateScheduleRequest struct {
	GroupName                  string             `json:"GroupName" cbor:"GroupName"`
	Name                       string             `json:"Name" cbor:"Name"`
	ScheduleExpression         string             `json:"ScheduleExpression" cbor:"ScheduleExpression"`
	ScheduleExpressionTimezone string             `json:"ScheduleExpressionTimezone" cbor:"ScheduleExpressionTimezone"`
	Description                string             `json:"Description" cbor:"Description"`
	FlexibleTimeWindow         flexibleTimeWindow `json:"FlexibleTimeWindow" cbor:"FlexibleTimeWindow"`
	Target                     scheduleTarget     `json:"Target" cbor:"Target"`
	State                      string             `json:"State" cbor:"State"`
}

type updateScheduleResponse struct {
	ScheduleArn string `json:"ScheduleArn" cbor:"ScheduleArn"`
}

func (s *Service) updateScheduleTyped(ctx context.Context, req *updateScheduleRequest) (*updateScheduleResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	group := req.GroupName
	if group == "" {
		group = defaultGroup
	}
	sc, found := s.loadSchedule(ctx, region, group, req.Name)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Schedule %s in group %s does not exist.", req.Name, group),
			HTTPStatus: 404,
		}
	}
	if req.ScheduleExpression != "" {
		sc.ScheduleExpression = req.ScheduleExpression
		_ = s.store.Delete(ctx, nsLastFire, s.scheduleKey(region, group, req.Name))
	}
	if req.ScheduleExpressionTimezone != "" {
		sc.ScheduleExpressionTimezone = req.ScheduleExpressionTimezone
	}
	if req.Description != "" {
		sc.Description = req.Description
	}
	if req.FlexibleTimeWindow.Mode != "" {
		sc.FlexibleTimeWindow = req.FlexibleTimeWindow
	}
	if req.Target.Arn != "" {
		sc.Target = req.Target
	}
	if req.State != "" {
		sc.State = req.State
	}
	sc.LastModificationDate = s.clk.Now()
	if err := s.saveSchedule(ctx, region, sc); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &updateScheduleResponse{ScheduleArn: sc.Arn}, nil
}

type deleteScheduleRequest struct {
	GroupName string `json:"GroupName" cbor:"GroupName"`
	Name      string `json:"Name" cbor:"Name"`
}

func (s *Service) deleteScheduleTyped(ctx context.Context, req *deleteScheduleRequest) (any, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	group := req.GroupName
	if group == "" {
		group = defaultGroup
	}
	if _, found := s.loadSchedule(ctx, region, group, req.Name); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Schedule %s in group %s does not exist.", req.Name, group),
			HTTPStatus: 404,
		}
	}
	if err := s.deleteScheduleRecord(ctx, region, group, req.Name); err != nil {
		return nil, protocol.ErrInternalError
	}
	return struct{}{}, nil
}

type listSchedulesRequest struct {
	ScheduleGroup string `json:"ScheduleGroup" cbor:"ScheduleGroup"`
}

type listSchedulesResponse struct {
	Schedules []*Schedule `json:"Schedules" cbor:"Schedules"`
}

func (s *Service) listSchedulesTyped(ctx context.Context, req *listSchedulesRequest) (*listSchedulesResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	var schedules []*Schedule
	var err error
	if req.ScheduleGroup != "" {
		schedules, err = s.listSchedulesByGroup(ctx, region, req.ScheduleGroup)
	} else {
		schedules, err = s.listAllSchedules(ctx, region)
	}
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &listSchedulesResponse{Schedules: schedules}, nil
}
