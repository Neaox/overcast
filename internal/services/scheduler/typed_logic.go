package scheduler

// The codec-agnostic implementation of every Scheduler operation.
//
// This is the *only* implementation. Both entry points reach it: the REST-JSON
// routes in service.go decode the path label, query string and body into these
// request structs and hand them straight over, and the JSON/CBOR dispatch in
// Dispatch does the same through op.NewTyped. Handlers stay thin so the two
// surfaces cannot drift apart — which they had, the REST copy honouring
// StartDate and EndDate that the typed copy silently dropped.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// AWS's documented page size for both List operations: MaxResults is bounded to
// 1–100, and an omitted value is served as a full page.
const (
	listDefaultLimit = 100
	listMaxLimit     = 100
)

// ─── Shared validation ────────────────────────────────────────────────────────

// validateName applies the Name/ScheduleGroupName constraint both shapes carry
// in the model: 1–64 characters of [0-9a-zA-Z-_.].
func validateName(value, field string) *protocol.AWSError {
	return serviceutil.ResourceName(value, serviceutil.NameRule{
		MinLength: 1,
		MaxLength: 64,
		Allowed:   serviceutil.AlphaNumericHyphenUnderscorePeriod,
		ErrorCode: "ValidationException",
		LengthMessage: fmt.Sprintf(
			"1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: Member must have length between 1 and 64.",
			value, field),
		AllowedMessage: fmt.Sprintf(
			"1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: Member must satisfy regular expression pattern: [0-9a-zA-Z-_.]+",
			value, field),
	})
}

// validateState rejects a State outside the modeled ScheduleState enum. An
// unknown value would otherwise be stored and quietly stop the schedule firing,
// because the engine only fires "ENABLED".
func validateState(state string) *protocol.AWSError {
	switch state {
	case "", "ENABLED", "DISABLED":
		return nil
	default:
		return validationError(fmt.Sprintf(
			"1 validation error detected: Value '%s' at 'state' failed to satisfy constraint: Member must satisfy enum value set: [ENABLED, DISABLED]",
			state))
	}
}

// validateFlexibleTimeWindow checks the member AWS marks required on both write
// operations. Mode is the only part the emulator can act on — see the service
// doc for why the window itself is not honoured.
func validateFlexibleTimeWindow(w flexibleTimeWindow) *protocol.AWSError {
	switch w.Mode {
	case "OFF", "FLEXIBLE":
		return nil
	case "":
		return validationError("FlexibleTimeWindow.Mode is required.")
	default:
		return validationError(fmt.Sprintf(
			"1 validation error detected: Value '%s' at 'flexibleTimeWindow.mode' failed to satisfy constraint: Member must satisfy enum value set: [OFF, FLEXIBLE]",
			w.Mode))
	}
}

// validateWrite runs every check CreateSchedule and UpdateSchedule share.
//
// The expression is parsed here rather than at fire time. An unparseable
// expression used to be accepted and then logged at ERROR on every tick, which
// leaves a schedule that looks correct in GetSchedule and never fires — the
// same failure mode validateTarget exists to prevent.
func validateWrite(name string, body scheduleWrite, expressionParses func() bool) *protocol.AWSError {
	if aerr := validateName(name, "name"); aerr != nil {
		return aerr
	}
	if body.GroupName != "" {
		if aerr := validateName(body.GroupName, "groupName"); aerr != nil {
			return aerr
		}
	}
	if strings.TrimSpace(body.ScheduleExpression) == "" {
		return validationError("ScheduleExpression is required.")
	}
	if !expressionParses() {
		return validationError(fmt.Sprintf(
			"Invalid ScheduleExpression: %q is not a rate(), cron() or at() expression Overcast can evaluate.",
			body.ScheduleExpression))
	}
	if aerr := validateFlexibleTimeWindow(body.FlexibleTimeWindow); aerr != nil {
		return aerr
	}
	return validateState(body.State)
}

// scheduleWrite is the subset of CreateSchedule/UpdateSchedule input that
// validation reads. Both request structs satisfy it by conversion.
type scheduleWrite struct {
	GroupName          string
	ScheduleExpression string
	FlexibleTimeWindow flexibleTimeWindow
	State              string
}

// ─── Schedule groups ──────────────────────────────────────────────────────────

type createScheduleGroupRequest struct {
	Name string            `json:"Name" cbor:"Name"`
	Tags map[string]string `json:"Tags" cbor:"Tags"`
}

type createScheduleGroupResponse struct {
	ScheduleGroupArn string `json:"ScheduleGroupArn" cbor:"ScheduleGroupArn"`
}

func (s *Service) createScheduleGroupTyped(ctx context.Context, req *createScheduleGroupRequest) (*createScheduleGroupResponse, *protocol.AWSError) {
	if aerr := validateName(req.Name, "name"); aerr != nil {
		return nil, aerr
	}
	region := s.regionOf(ctx)
	_, found, aerr := s.loadGroup(ctx, region, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if found {
		return nil, &protocol.AWSError{
			Code: "ConflictException", Message: fmt.Sprintf("Schedule group %s already exists.", req.Name),
			HTTPStatus: http.StatusConflict,
		}
	}
	now := s.clk.Now()
	g := &ScheduleGroup{
		Name: req.Name, Arn: s.groupARN(region, req.Name), State: "ACTIVE",
		CreationDate: now, LastModificationDate: now,
	}
	if err := s.saveGroup(ctx, region, g); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	s.saveTagsJSON(ctx, g.Arn, req.Tags)
	return &createScheduleGroupResponse{ScheduleGroupArn: g.Arn}, nil
}

type getScheduleGroupRequest struct {
	Name string `json:"Name" cbor:"Name"`
}

type getScheduleGroupResponse ScheduleGroup

func (s *Service) getScheduleGroupTyped(ctx context.Context, req *getScheduleGroupRequest) (*getScheduleGroupResponse, *protocol.AWSError) {
	g, found, aerr := s.loadGroup(ctx, s.regionOf(ctx), req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if !found {
		return nil, groupNotFound(req.Name)
	}
	return (*getScheduleGroupResponse)(g), nil
}

type deleteScheduleGroupRequest struct {
	Name string `json:"Name" cbor:"Name"`
}

// deleteScheduleGroupTyped deletes the group and every schedule inside it.
//
// AWS deletes a group's schedules with it. Leaving them behind orphaned them in
// the store *and* kept them firing on every engine tick, because the engine
// scans schedules directly and never consults their group.
func (s *Service) deleteScheduleGroupTyped(ctx context.Context, req *deleteScheduleGroupRequest) (any, *protocol.AWSError) {
	if req.Name == defaultGroup {
		return nil, validationError("Cannot delete default schedule group.")
	}
	region := s.regionOf(ctx)
	_, found, aerr := s.loadGroup(ctx, region, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if !found {
		return nil, groupNotFound(req.Name)
	}

	schedules, aerr := s.listSchedulesByGroup(ctx, region, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	for _, sc := range schedules {
		// Each cascaded delete takes the schedule's own record lock, for the
		// reason deleteScheduleTyped does: an UpdateSchedule already holding a
		// read of that record would otherwise write it back afterwards, leaving
		// a schedule in a group that no longer exists.
		unlock := s.scheduleLocks.Lock(s.scheduleKey(region, req.Name, sc.Name))
		err := s.deleteScheduleRecord(ctx, region, req.Name, sc.Name)
		unlock()
		if err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
	}
	if err := s.deleteGroup(ctx, region, req.Name); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type listScheduleGroupsRequest struct {
	NamePrefix string `json:"NamePrefix" cbor:"NamePrefix"`
	MaxResults int    `json:"MaxResults" cbor:"MaxResults"`
	NextToken  string `json:"NextToken" cbor:"NextToken"`
}

type listScheduleGroupsResponse struct {
	ScheduleGroups []*ScheduleGroup `json:"ScheduleGroups" cbor:"ScheduleGroups"`
	NextToken      string           `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
}

func (s *Service) listScheduleGroupsTyped(ctx context.Context, req *listScheduleGroupsRequest) (*listScheduleGroupsResponse, *protocol.AWSError) {
	groups, aerr := s.listGroups(ctx, s.regionOf(ctx))
	if aerr != nil {
		return nil, aerr
	}
	if req.NamePrefix != "" {
		filtered := groups[:0]
		for _, g := range groups {
			if strings.HasPrefix(g.Name, req.NamePrefix) {
				filtered = append(filtered, g)
			}
		}
		groups = filtered
	}
	page, aerr := paginate(groups, req.MaxResults, req.NextToken)
	if aerr != nil {
		return nil, aerr
	}
	return &listScheduleGroupsResponse{ScheduleGroups: page.Items, NextToken: page.NextToken}, nil
}

// ─── Tags ─────────────────────────────────────────────────────────────────────

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
	return &listTagsForResourceResponse{Tags: s.loadTags(ctx, req.ResourceArn)}, nil
}

// ─── Schedules ────────────────────────────────────────────────────────────────

type createScheduleRequest struct {
	GroupName                  string             `json:"GroupName" cbor:"GroupName"`
	Name                       string             `json:"Name" cbor:"Name"`
	ScheduleExpression         string             `json:"ScheduleExpression" cbor:"ScheduleExpression"`
	ScheduleExpressionTimezone string             `json:"ScheduleExpressionTimezone" cbor:"ScheduleExpressionTimezone"`
	Description                string             `json:"Description" cbor:"Description"`
	FlexibleTimeWindow         flexibleTimeWindow `json:"FlexibleTimeWindow" cbor:"FlexibleTimeWindow"`
	Target                     scheduleTarget     `json:"Target" cbor:"Target"`
	State                      string             `json:"State" cbor:"State"`
	StartDate                  *time.Time         `json:"StartDate" cbor:"StartDate"`
	EndDate                    *time.Time         `json:"EndDate" cbor:"EndDate"`
}

func (r *createScheduleRequest) write() scheduleWrite {
	return scheduleWrite{
		GroupName: r.GroupName, ScheduleExpression: r.ScheduleExpression,
		FlexibleTimeWindow: r.FlexibleTimeWindow, State: r.State,
	}
}

type createScheduleResponse struct {
	ScheduleArn string `json:"ScheduleArn" cbor:"ScheduleArn"`
}

func (s *Service) createScheduleTyped(ctx context.Context, req *createScheduleRequest) (*createScheduleResponse, *protocol.AWSError) {
	if aerr := s.validateSchedule(req.Name, req.write(), req.Target); aerr != nil {
		return nil, aerr
	}
	region := s.regionOf(ctx)
	group := groupOrDefault(req.GroupName)
	if aerr := s.requireGroup(ctx, region, group); aerr != nil {
		return nil, aerr
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
		Target: req.Target, StartDate: req.StartDate, EndDate: req.EndDate,
		CreationDate: now, LastModificationDate: now,
	}
	if err := s.saveSchedule(ctx, region, sc); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createScheduleResponse{ScheduleArn: sc.Arn}, nil
}

type getScheduleRequest struct {
	GroupName string `json:"GroupName" cbor:"GroupName"`
	Name      string `json:"Name" cbor:"Name"`
}

type getScheduleResponse Schedule

func (s *Service) getScheduleTyped(ctx context.Context, req *getScheduleRequest) (*getScheduleResponse, *protocol.AWSError) {
	group := groupOrDefault(req.GroupName)
	sc, found, aerr := s.loadSchedule(ctx, s.regionOf(ctx), group, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if !found {
		return nil, scheduleNotFound(req.Name, group)
	}
	return (*getScheduleResponse)(sc), nil
}

type updateScheduleRequest createScheduleRequest

type updateScheduleResponse struct {
	ScheduleArn string `json:"ScheduleArn" cbor:"ScheduleArn"`
}

// updateScheduleTyped replaces a stored schedule with the one in the request.
//
// AWS's UpdateSchedule is a full replacement: the request carries the whole
// schedule, and every optional member the caller omits ends up unset. The
// emulator used to merge instead, leaving an omitted member as it was, so a
// caller who read a schedule, changed one field and wrote it back got the AWS
// answer while a caller who sent only the required members silently kept
// settings they had dropped — the divergence only shows up in the second case,
// which is exactly the one a user writes by hand.
//
// What is replaced is the schedule's content, not the schedule: the name, group,
// ARN and creation date are its identity and survive, as they do on AWS. A
// caller who means to change one of those is creating a different schedule.
func (s *Service) updateScheduleTyped(ctx context.Context, req *updateScheduleRequest) (*updateScheduleResponse, *protocol.AWSError) {
	create := (*createScheduleRequest)(req)
	if aerr := s.validateSchedule(req.Name, create.write(), req.Target); aerr != nil {
		return nil, aerr
	}
	region := s.regionOf(ctx)
	group := groupOrDefault(req.GroupName)

	// Read and write are one step. The replacement is built from a record this
	// call has already read, so anything that changes that record in between —
	// another update, or a DeleteSchedule whose caller has already been told it
	// succeeded — would be written straight over.
	defer s.scheduleLocks.Lock(s.scheduleKey(region, group, req.Name))()

	existing, found, aerr := s.loadSchedule(ctx, region, group, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if !found {
		return nil, scheduleNotFound(req.Name, group)
	}

	state := req.State
	if state == "" {
		state = "ENABLED"
	}
	sc := &Schedule{
		Name: existing.Name, GroupName: existing.GroupName, Arn: existing.Arn,
		State: state, ScheduleExpression: req.ScheduleExpression,
		ScheduleExpressionTimezone: req.ScheduleExpressionTimezone,
		Description:                req.Description, FlexibleTimeWindow: req.FlexibleTimeWindow,
		Target: req.Target, StartDate: req.StartDate, EndDate: req.EndDate,
		CreationDate: existing.CreationDate, LastModificationDate: s.clk.Now(),
	}

	// The replacement carries a ScheduleExpression — AWS marks it required, and
	// validateWrite enforces that — so the cadence is always the new one's.
	// Forgetting the last-fire time is what lets the engine pick it up on the
	// next tick rather than from a firing that belonged to the old expression.
	if err := s.store.Delete(ctx, nsLastFire, s.scheduleKey(region, group, req.Name)); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.saveSchedule(ctx, region, sc); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateScheduleResponse{ScheduleArn: sc.Arn}, nil
}

type deleteScheduleRequest struct {
	GroupName string `json:"GroupName" cbor:"GroupName"`
	Name      string `json:"Name" cbor:"Name"`
}

// deleteScheduleTyped removes a schedule.
//
// It takes the same record lock UpdateSchedule does: the existence check and
// the delete are a read-modify-write like any other, and without the lock an
// update already in its own window writes the record back after this call has
// removed it.
func (s *Service) deleteScheduleTyped(ctx context.Context, req *deleteScheduleRequest) (any, *protocol.AWSError) {
	region := s.regionOf(ctx)
	group := groupOrDefault(req.GroupName)
	defer s.scheduleLocks.Lock(s.scheduleKey(region, group, req.Name))()

	_, found, aerr := s.loadSchedule(ctx, region, group, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if !found {
		return nil, scheduleNotFound(req.Name, group)
	}
	if err := s.deleteScheduleRecord(ctx, region, group, req.Name); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type listSchedulesRequest struct {
	ScheduleGroup string `json:"ScheduleGroup" cbor:"ScheduleGroup"`
	NamePrefix    string `json:"NamePrefix" cbor:"NamePrefix"`
	State         string `json:"State" cbor:"State"`
	MaxResults    int    `json:"MaxResults" cbor:"MaxResults"`
	NextToken     string `json:"NextToken" cbor:"NextToken"`
}

type listSchedulesResponse struct {
	Schedules []*Schedule `json:"Schedules" cbor:"Schedules"`
	NextToken string      `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
}

func (s *Service) listSchedulesTyped(ctx context.Context, req *listSchedulesRequest) (*listSchedulesResponse, *protocol.AWSError) {
	if aerr := validateState(req.State); aerr != nil {
		return nil, aerr
	}
	region := s.regionOf(ctx)

	var schedules []*Schedule
	var aerr *protocol.AWSError
	if req.ScheduleGroup != "" {
		schedules, aerr = s.listSchedulesByGroup(ctx, region, req.ScheduleGroup)
	} else {
		schedules, aerr = s.listAllSchedules(ctx, region)
	}
	if aerr != nil {
		return nil, aerr
	}

	if req.NamePrefix != "" || req.State != "" {
		filtered := schedules[:0]
		for _, sc := range schedules {
			if req.NamePrefix != "" && !strings.HasPrefix(sc.Name, req.NamePrefix) {
				continue
			}
			if req.State != "" && sc.State != req.State {
				continue
			}
			filtered = append(filtered, sc)
		}
		schedules = filtered
	}

	page, aerr := paginate(schedules, req.MaxResults, req.NextToken)
	if aerr != nil {
		return nil, aerr
	}
	return &listSchedulesResponse{Schedules: page.Items, NextToken: page.NextToken}, nil
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// validateSchedule runs the checks CreateSchedule and UpdateSchedule share,
// including parsing the expression against the injected clock.
func (s *Service) validateSchedule(name string, body scheduleWrite, target scheduleTarget) *protocol.AWSError {
	parses := func() bool {
		_, err := nextFireTime(body.ScheduleExpression, time.Time{}, s.clk.Now())
		return err == nil
	}
	if aerr := validateWrite(name, body, parses); aerr != nil {
		return aerr
	}
	return validateTarget(target)
}

// requireGroup checks that a schedule's group exists, seeding "default" on
// first use as AWS does.
func (s *Service) requireGroup(ctx context.Context, region, group string) *protocol.AWSError {
	_, found, aerr := s.loadGroup(ctx, region, group)
	if aerr != nil {
		return aerr
	}
	if found {
		return nil
	}
	if group != defaultGroup {
		return groupNotFound(group)
	}
	now := s.clk.Now()
	if err := s.saveGroup(ctx, region, &ScheduleGroup{
		Name: defaultGroup, Arn: s.groupARN(region, defaultGroup),
		State: "ACTIVE", CreationDate: now, LastModificationDate: now,
	}); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// regionOf resolves the region a context's request was signed for.
func (s *Service) regionOf(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.cfg.Region)
}

// paginate applies AWS's MaxResults/NextToken contract, mapping an unusable
// token to the ValidationException both List operations document rather than
// silently restarting from the first page.
func paginate[T any](items []T, maxResults int, nextToken string) (serviceutil.Page[T], *protocol.AWSError) {
	page, err := serviceutil.Paginate(items, maxResults, nextToken, serviceutil.PaginateOptions{
		DefaultLimit: listDefaultLimit,
		MaxLimit:     listMaxLimit,
	})
	if err != nil {
		if errors.Is(err, serviceutil.ErrInvalidPageToken) {
			return page, validationError("The specified NextToken is not valid.")
		}
		return page, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return page, nil
}

// groupOrDefault applies AWS's rule that an omitted GroupName means "default".
func groupOrDefault(name string) string {
	if strings.TrimSpace(name) == "" {
		return defaultGroup
	}
	return name
}

func groupNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("Schedule group %s does not exist.", name),
		HTTPStatus: http.StatusNotFound,
	}
}

func scheduleNotFound(name, group string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("Schedule %s in group %s does not exist.", name, group),
		HTTPStatus: http.StatusNotFound,
	}
}

// logSkippedRecord records a persisted record that could not be decoded. A
// single bad record must not fail a list operation, but it must not vanish
// without trace either.
func (s *Service) logSkippedRecord(ns, key string, err error) {
	s.log.Error("scheduler: skipping undecodable record",
		zap.String("namespace", ns), zap.String("key", key), zap.Error(err))
}
