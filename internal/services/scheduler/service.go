// Package scheduler provides emulation of Amazon EventBridge Scheduler.
//
// Implemented operations:
//
//	Schedule groups: CreateScheduleGroup, GetScheduleGroup, ListScheduleGroups,
//	  DeleteScheduleGroup, TagResource, UntagResource, ListTagsForResource
//	Schedules: CreateSchedule, GetSchedule, UpdateSchedule, DeleteSchedule,
//	  ListSchedules
//
// Routes are served under the /_scheduler/ path prefix (REST-JSON).
//
// A lightweight cron engine fires rate/cron/at expressions against their
// declared targets (Lambda async, SQS enqueue, SNS publish) using the
// injected clock — tests can fast-forward time without real sleeps.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName  = "scheduler"
	nsGroups     = "scheduler:groups"
	nsSchedules  = "scheduler:schedules"
	nsTags       = "scheduler:tags"
	nsLastFire   = "scheduler:last_fire"
	defaultGroup = "default"

	// engineTick is how often the cron engine polls for due schedules.
	// With a mock clock, time.Sleep is instantaneous so 1 s is fine.
	engineTick = 1 * time.Second
)

// ─── Types ────────────────────────────────────────────────────────────────────

// ScheduleGroup models an EventBridge Scheduler schedule group.
type ScheduleGroup struct {
	Name                 string    `json:"Name"`
	Arn                  string    `json:"Arn"`
	State                string    `json:"State"` // ACTIVE, DELETING
	CreationDate         time.Time `json:"CreationDate"`
	LastModificationDate time.Time `json:"LastModificationDate"`
}

// Schedule models an EventBridge Scheduler schedule.
type Schedule struct {
	Name                       string             `json:"Name"`
	GroupName                  string             `json:"GroupName"`
	Arn                        string             `json:"Arn"`
	State                      string             `json:"State"` // ENABLED, DISABLED
	ScheduleExpression         string             `json:"ScheduleExpression"`
	ScheduleExpressionTimezone string             `json:"ScheduleExpressionTimezone,omitempty"`
	Description                string             `json:"Description,omitempty"`
	FlexibleTimeWindow         flexibleTimeWindow `json:"FlexibleTimeWindow"`
	Target                     scheduleTarget     `json:"Target"`
	StartDate                  *time.Time         `json:"StartDate,omitempty"`
	EndDate                    *time.Time         `json:"EndDate,omitempty"`
	CreationDate               time.Time          `json:"CreationDate"`
	LastModificationDate       time.Time          `json:"LastModificationDate"`
}

// scheduleTarget models the Target field of a Schedule.
type scheduleTarget struct {
	Arn               string         `json:"Arn"`
	RoleArn           string         `json:"RoleArn"`
	Input             string         `json:"Input,omitempty"`
	DeadLetterConfig  *dlqConfig     `json:"DeadLetterConfig,omitempty"`
	RetryPolicy       *retryPolicy   `json:"RetryPolicy,omitempty"`
	SqsParameters     *sqsParameters `json:"SqsParameters,omitempty"`
	KinesisParameters *kinesisParams `json:"KinesisParameters,omitempty"`
	EcsParameters     *ecsParams     `json:"EcsParameters,omitempty"`
}

type dlqConfig struct {
	Arn string `json:"Arn"`
}

type retryPolicy struct {
	MaximumEventAgeInSeconds int `json:"MaximumEventAgeInSeconds,omitempty"`
	MaximumRetryAttempts     int `json:"MaximumRetryAttempts,omitempty"`
}

type sqsParameters struct {
	MessageGroupId string `json:"MessageGroupId,omitempty"`
}

type kinesisParams struct {
	PartitionKey string `json:"PartitionKey,omitempty"`
}

type ecsParams struct {
	TaskDefinitionArn string `json:"TaskDefinitionArn"`
}

type flexibleTimeWindow struct {
	Mode                   string `json:"Mode"`
	MaximumWindowInMinutes int    `json:"MaximumWindowInMinutes,omitempty"`
}

// ─── Target dispatch interfaces ───────────────────────────────────────────────

// TargetInvoker holds the optional cross-service invocation handles.
// Each field is optional; if nil, that target type is logged and skipped.
type TargetInvoker struct {
	Lambda events.FunctionInvoker
	SQS    events.MessageEnqueuer
}

// ─── Service ──────────────────────────────────────────────────────────────────

// Service implements router.Service for EventBridge Scheduler.
type Service struct {
	cfg     *config.Config
	store   state.Store
	clk     clock.Clock
	log     *serviceutil.ServiceLogger
	targets TargetInvoker
	typedOp map[string]op.Operation

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// New returns a configured Scheduler Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		cfg:    cfg,
		store:  st,
		clk:    clk,
		log:    serviceutil.NewServiceLogger(logger, serviceName),
		stopCh: make(chan struct{}),
	}
	s.typedOp = s.typedOps()
	return s
}

// InitTargets wires the cross-service invocation handles.
func (s *Service) InitTargets(ti TargetInvoker) {
	s.targets = ti
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

func (s *Service) TargetPrefix() string { return "Scheduler." }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if codec.Supports(s.SupportedProtocols(), c) {
			if typed, ok := s.typedOp[opName]; ok {
				typed.Invoke(w, r, c)
				return
			}
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// RegisterRoutes satisfies router.Service.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Route("/_scheduler", func(r chi.Router) {
		// Schedule groups
		r.Post("/schedule-groups/{name}", s.createScheduleGroup)
		r.Get("/schedule-groups/{name}", s.getScheduleGroup)
		r.Delete("/schedule-groups/{name}", s.deleteScheduleGroup)
		r.Get("/schedule-groups", s.listScheduleGroups)
		// Tag operations (by group ARN — routed on tag resource)
		r.Get("/tags/{arn}", s.listTagsForResource)
		r.Post("/tags/{arn}", s.tagResource)
		r.Delete("/tags/{arn}", s.untagResource)
		// Schedules — group-qualified paths
		r.Post("/schedules/{group}/{name}", s.createSchedule)
		r.Get("/schedules/{group}/{name}", s.getSchedule)
		r.Put("/schedules/{group}/{name}", s.updateSchedule)
		r.Delete("/schedules/{group}/{name}", s.deleteSchedule)
		// Schedules — default-group paths (no group prefix)
		r.Post("/schedules/{name}", s.createScheduleDefaultGroup)
		r.Get("/schedules/{name}", s.getScheduleDefaultGroup)
		r.Put("/schedules/{name}", s.updateScheduleDefaultGroup)
		r.Delete("/schedules/{name}", s.deleteScheduleDefaultGroup)
		// List all schedules (optional ?ScheduleGroup= filter)
		r.Get("/schedules", s.listSchedules)
	})

	// Start the cron engine.
	s.wg.Add(1)
	go s.runEngine()
}

// Stop shuts down the cron engine, waiting for the goroutine to exit.
func (s *Service) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// ─── Store helpers ────────────────────────────────────────────────────────────

func (s *Service) region(r *http.Request) string {
	return middleware.RegionFromContext(r.Context(), s.cfg.Region)
}

func (s *Service) accountID() string {
	if s.cfg != nil && strings.TrimSpace(s.cfg.AccountID) != "" {
		return s.cfg.AccountID
	}
	return "000000000000"
}

func (s *Service) groupARN(region, name string) string {
	return fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule-group/%s", region, s.accountID(), name)
}

func (s *Service) scheduleARN(region, group, name string) string {
	return fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule/%s/%s", region, s.accountID(), group, name)
}

func (s *Service) groupKey(region, name string) string {
	return fmt.Sprintf("%s:%s", region, name)
}

func (s *Service) scheduleKey(region, group, name string) string {
	return fmt.Sprintf("%s:%s/%s", region, group, name)
}

func (s *Service) saveGroup(ctx context.Context, region string, g *ScheduleGroup) error {
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsGroups, s.groupKey(region, g.Name), string(raw))
}

func (s *Service) loadGroup(ctx context.Context, region, name string) (*ScheduleGroup, bool) {
	raw, found, err := s.store.Get(ctx, nsGroups, s.groupKey(region, name))
	if err != nil || !found {
		return nil, false
	}
	var g ScheduleGroup
	if json.Unmarshal([]byte(raw), &g) != nil {
		return nil, false
	}
	return &g, true
}

func (s *Service) deleteGroup(ctx context.Context, region, name string) error {
	return s.store.Delete(ctx, nsGroups, s.groupKey(region, name))
}

func (s *Service) listGroups(ctx context.Context, region string) ([]*ScheduleGroup, error) {
	prefix := region + ":"
	pairs, err := s.store.Scan(ctx, nsGroups, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*ScheduleGroup, 0, len(pairs)+1)
	hasDefault := false
	for _, kv := range pairs {
		var g ScheduleGroup
		if json.Unmarshal([]byte(kv.Value), &g) == nil {
			out = append(out, &g)
			if g.Name == defaultGroup {
				hasDefault = true
			}
		}
	}
	if !hasDefault {
		out = append(out, &ScheduleGroup{
			Name:  defaultGroup,
			Arn:   s.groupARN(region, defaultGroup),
			State: "ACTIVE",
		})
	}
	return out, nil
}

func (s *Service) saveSchedule(ctx context.Context, region string, sc *Schedule) error {
	raw, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsSchedules, s.scheduleKey(region, sc.GroupName, sc.Name), string(raw))
}

func (s *Service) loadSchedule(ctx context.Context, region, group, name string) (*Schedule, bool) {
	raw, found, err := s.store.Get(ctx, nsSchedules, s.scheduleKey(region, group, name))
	if err != nil || !found {
		return nil, false
	}
	var sc Schedule
	if json.Unmarshal([]byte(raw), &sc) != nil {
		return nil, false
	}
	return &sc, true
}

func (s *Service) deleteScheduleRecord(ctx context.Context, region, group, name string) error {
	_ = s.store.Delete(ctx, nsLastFire, s.scheduleKey(region, group, name))
	return s.store.Delete(ctx, nsSchedules, s.scheduleKey(region, group, name))
}

func (s *Service) listSchedulesByGroup(ctx context.Context, region, group string) ([]*Schedule, error) {
	prefix := fmt.Sprintf("%s:%s/", region, group)
	pairs, err := s.store.Scan(ctx, nsSchedules, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Schedule, 0, len(pairs))
	for _, kv := range pairs {
		var sc Schedule
		if json.Unmarshal([]byte(kv.Value), &sc) == nil {
			out = append(out, &sc)
		}
	}
	return out, nil
}

func (s *Service) listAllSchedules(ctx context.Context, region string) ([]*Schedule, error) {
	prefix := region + ":"
	pairs, err := s.store.Scan(ctx, nsSchedules, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Schedule, 0, len(pairs))
	for _, kv := range pairs {
		var sc Schedule
		if json.Unmarshal([]byte(kv.Value), &sc) == nil {
			out = append(out, &sc)
		}
	}
	return out, nil
}

// ─── Schedule Group Handlers ──────────────────────────────────────────────────

func (s *Service) createScheduleGroup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, found := s.loadGroup(ctx, region, name); found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ConflictException", Message: fmt.Sprintf("Schedule group %s already exists.", name),
			HTTPStatus: http.StatusConflict,
		})
		return
	}

	now := s.clk.Now()
	g := &ScheduleGroup{
		Name:                 name,
		Arn:                  s.groupARN(region, name),
		State:                "ACTIVE",
		CreationDate:         now,
		LastModificationDate: now,
	}
	if err := s.saveGroup(ctx, region, g); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	// Store tags separately if provided.
	var req struct {
		Tags map[string]string `json:"Tags"`
	}
	if json.NewDecoder(r.Body).Decode(&req) == nil && len(req.Tags) > 0 {
		if raw, err := json.Marshal(req.Tags); err == nil {
			_ = s.store.Set(ctx, nsTags, g.Arn, string(raw))
		}
	}

	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"ScheduleGroupArn": g.Arn,
	})
}

func (s *Service) getScheduleGroup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	region := s.region(r)

	g, found := s.loadGroup(r.Context(), region, name)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: fmt.Sprintf("Schedule group %s does not exist.", name),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, g)
}

func (s *Service) deleteScheduleGroup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if name == defaultGroup {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ValidationException", Message: "Cannot delete default schedule group.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if _, found := s.loadGroup(ctx, region, name); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: fmt.Sprintf("Schedule group %s does not exist.", name),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if err := s.deleteGroup(ctx, region, name); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) listScheduleGroups(w http.ResponseWriter, r *http.Request) {
	region := s.region(r)
	groups, err := s.listGroups(r.Context(), region)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ScheduleGroups": groups,
	})
}

// ─── Tag Handlers ─────────────────────────────────────────────────────────────

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "arn")
	ctx := r.Context()
	raw, found, _ := s.store.Get(ctx, nsTags, arn)
	tags := map[string]string{}
	if found {
		_ = json.Unmarshal([]byte(raw), &tags)
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Tags": tags})
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "arn")
	ctx := r.Context()
	var req struct {
		Tags map[string]string `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	existing := map[string]string{}
	if raw, found, _ := s.store.Get(ctx, nsTags, arn); found {
		_ = json.Unmarshal([]byte(raw), &existing)
	}
	for k, v := range req.Tags {
		existing[k] = v
	}
	if raw, err := json.Marshal(existing); err == nil {
		_ = s.store.Set(ctx, nsTags, arn, string(raw))
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "arn")
	ctx := r.Context()
	tagKeys := r.URL.Query()["TagKeys"]
	existing := map[string]string{}
	if raw, found, _ := s.store.Get(ctx, nsTags, arn); found {
		_ = json.Unmarshal([]byte(raw), &existing)
	}
	for _, k := range tagKeys {
		delete(existing, k)
	}
	if raw, err := json.Marshal(existing); err == nil {
		_ = s.store.Set(ctx, nsTags, arn, string(raw))
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// ─── Schedule Handlers ────────────────────────────────────────────────────────

// createScheduleDefaultGroup handles POST /schedules/{name} (default group).
func (s *Service) createScheduleDefaultGroup(w http.ResponseWriter, r *http.Request) {
	// chi will match /{name} before /{group}/{name}, so we treat the single
	// segment as the name and use the default group.
	r = routeScheduleWithGroup(r, defaultGroup, chi.URLParam(r, "name"))
	s.createSchedule(w, r)
}

func (s *Service) getScheduleDefaultGroup(w http.ResponseWriter, r *http.Request) {
	r = routeScheduleWithGroup(r, defaultGroup, chi.URLParam(r, "name"))
	s.getSchedule(w, r)
}

func (s *Service) updateScheduleDefaultGroup(w http.ResponseWriter, r *http.Request) {
	r = routeScheduleWithGroup(r, defaultGroup, chi.URLParam(r, "name"))
	s.updateSchedule(w, r)
}

func (s *Service) deleteScheduleDefaultGroup(w http.ResponseWriter, r *http.Request) {
	r = routeScheduleWithGroup(r, defaultGroup, chi.URLParam(r, "name"))
	s.deleteSchedule(w, r)
}

// routeScheduleWithGroup returns a new request with chi URL params overridden to
// group and name. We store them in the request context so handlers can read them.
func routeScheduleWithGroup(r *http.Request, group, name string) *http.Request {
	rctx := chi.RouteContext(r.Context())
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "group", "name")
	rctx.URLParams.Values = append(rctx.URLParams.Values, group, name)
	return r
}

func (s *Service) createSchedule(w http.ResponseWriter, r *http.Request) {
	group := chi.URLParam(r, "group")
	name := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	// Ensure the group exists (auto-create "default").
	if _, found := s.loadGroup(ctx, region, group); !found {
		if group == defaultGroup {
			now := s.clk.Now()
			defGroup := ScheduleGroup{
				Name:  defaultGroup,
				Arn:   s.groupARN(region, defaultGroup),
				State: "ACTIVE", CreationDate: now, LastModificationDate: now,
			}
			_ = s.saveGroup(ctx, region, &defGroup)
		} else {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code: "ResourceNotFoundException", Message: fmt.Sprintf("Schedule group %s does not exist.", group),
				HTTPStatus: http.StatusNotFound,
			})
			return
		}
	}

	var req struct {
		ScheduleExpression         string             `json:"ScheduleExpression"`
		ScheduleExpressionTimezone string             `json:"ScheduleExpressionTimezone"`
		Description                string             `json:"Description"`
		FlexibleTimeWindow         flexibleTimeWindow `json:"FlexibleTimeWindow"`
		Target                     scheduleTarget     `json:"Target"`
		State                      string             `json:"State"`
		StartDate                  *time.Time         `json:"StartDate"`
		EndDate                    *time.Time         `json:"EndDate"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.ScheduleExpression == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ValidationException", Message: "ScheduleExpression is required.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	state := req.State
	if state == "" {
		state = "ENABLED"
	}

	now := s.clk.Now()
	sc := &Schedule{
		Name:                       name,
		GroupName:                  group,
		Arn:                        s.scheduleARN(region, group, name),
		State:                      state,
		ScheduleExpression:         req.ScheduleExpression,
		ScheduleExpressionTimezone: req.ScheduleExpressionTimezone,
		Description:                req.Description,
		FlexibleTimeWindow:         req.FlexibleTimeWindow,
		Target:                     req.Target,
		StartDate:                  req.StartDate,
		EndDate:                    req.EndDate,
		CreationDate:               now,
		LastModificationDate:       now,
	}

	if err := s.saveSchedule(ctx, region, sc); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"ScheduleArn": sc.Arn,
	})
}

func (s *Service) getSchedule(w http.ResponseWriter, r *http.Request) {
	group := chi.URLParam(r, "group")
	name := chi.URLParam(r, "name")
	region := s.region(r)

	sc, found := s.loadSchedule(r.Context(), region, group, name)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Schedule %s in group %s does not exist.", name, group),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, sc)
}

func (s *Service) updateSchedule(w http.ResponseWriter, r *http.Request) {
	group := chi.URLParam(r, "group")
	name := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	sc, found := s.loadSchedule(ctx, region, group, name)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Schedule %s in group %s does not exist.", name, group),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		ScheduleExpression         string             `json:"ScheduleExpression"`
		ScheduleExpressionTimezone string             `json:"ScheduleExpressionTimezone"`
		Description                string             `json:"Description"`
		FlexibleTimeWindow         flexibleTimeWindow `json:"FlexibleTimeWindow"`
		Target                     scheduleTarget     `json:"Target"`
		State                      string             `json:"State"`
		StartDate                  *time.Time         `json:"StartDate"`
		EndDate                    *time.Time         `json:"EndDate"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.ScheduleExpression != "" {
		sc.ScheduleExpression = req.ScheduleExpression
		// Reset last-fire so the engine picks up the new schedule immediately.
		_ = s.store.Delete(ctx, nsLastFire, s.scheduleKey(region, group, name))
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
	sc.StartDate = req.StartDate
	sc.EndDate = req.EndDate
	sc.LastModificationDate = s.clk.Now()

	if err := s.saveSchedule(ctx, region, sc); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ScheduleArn": sc.Arn,
	})
}

func (s *Service) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	group := chi.URLParam(r, "group")
	name := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, found := s.loadSchedule(ctx, region, group, name); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Schedule %s in group %s does not exist.", name, group),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if err := s.deleteScheduleRecord(ctx, region, group, name); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) listSchedules(w http.ResponseWriter, r *http.Request) {
	region := s.region(r)
	groupFilter := r.URL.Query().Get("ScheduleGroup")
	ctx := r.Context()

	var schedules []*Schedule
	var err error
	if groupFilter != "" {
		schedules, err = s.listSchedulesByGroup(ctx, region, groupFilter)
	} else {
		schedules, err = s.listAllSchedules(ctx, region)
	}
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"Schedules": schedules,
	})
}

// ─── Cron Engine ──────────────────────────────────────────────────────────────

// runEngine is the background schedule firing loop. It runs until stopCh is
// closed, ticking at engineTick intervals via the injectable clock.
func (s *Service) runEngine() {
	defer s.wg.Done()
	ticker := s.clk.Ticker(engineTick)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// tick fires all due schedules in all regions.
func (s *Service) tick() {
	ctx := context.Background()
	now := s.clk.Now()

	// List all schedules across all regions (prefix = "").
	pairs, err := s.store.Scan(ctx, nsSchedules, "")
	if err != nil {
		s.log.Error("scheduler: tick: list schedules", zap.Error(err))
		return
	}

	for _, kv := range pairs {
		var sc Schedule
		if json.Unmarshal([]byte(kv.Value), &sc) != nil {
			continue
		}
		if sc.State != "ENABLED" {
			continue
		}
		if sc.EndDate != nil && now.After(*sc.EndDate) {
			continue
		}
		if sc.StartDate != nil && now.Before(*sc.StartDate) {
			continue
		}

		// Parse expression to decide if it's time to fire.
		lastFire := s.getLastFire(ctx, kv.Key)
		nextFire, err := nextFireTime(sc.ScheduleExpression, lastFire, now)
		if err != nil {
			s.log.Error("scheduler: tick: parse expression",
				zap.String("name", sc.Name),
				zap.String("expr", sc.ScheduleExpression),
				zap.Error(err),
			)
			continue
		}

		if nextFire.IsZero() {
			// One-shot "at" expression that's already fired.
			continue
		}

		if now.Before(nextFire) {
			continue
		}

		// Fire and update last-fire timestamp.
		s.fire(ctx, &sc, now)
		s.setLastFire(ctx, kv.Key, now)
	}
}

// getLastFire returns the last time a schedule was fired (zero if never).
func (s *Service) getLastFire(ctx context.Context, key string) time.Time {
	raw, found, _ := s.store.Get(ctx, nsLastFire, key)
	if !found {
		return time.Time{}
	}
	var t time.Time
	if json.Unmarshal([]byte(raw), &t) != nil {
		return time.Time{}
	}
	return t
}

// setLastFire records the fire time for a schedule.
func (s *Service) setLastFire(ctx context.Context, key string, t time.Time) {
	raw, err := json.Marshal(t)
	if err != nil {
		return
	}
	_ = s.store.Set(ctx, nsLastFire, key, string(raw))
}

// fire dispatches a schedule to its configured target.
func (s *Service) fire(ctx context.Context, sc *Schedule, now time.Time) {
	target := sc.Target
	targetArn := target.Arn
	input := target.Input
	if input == "" {
		// Default event payload.
		input = fmt.Sprintf(`{"source":"aws.scheduler","time":%q,"id":%q}`, now.UTC().Format(time.RFC3339), uuid.NewString())
	}

	arn := strings.ToLower(targetArn)
	switch {
	case strings.Contains(arn, ":lambda:") || strings.Contains(arn, ":function:"):
		if s.targets.Lambda != nil {
			if err := s.targets.Lambda.InvokeAsync(ctx, targetArn, []byte(input)); err != nil {
				s.log.Error("scheduler: fire: lambda invoke", zap.String("arn", targetArn), zap.Error(err))
			}
		} else {
			s.log.Warn("scheduler: fire: lambda invoker not configured, skipping",
				zap.String("schedule", sc.Name), zap.String("arn", targetArn))
		}
	case strings.Contains(arn, ":sqs:") || strings.Contains(arn, ":queue/") || strings.HasSuffix(arn, ":sqs"):
		if s.targets.SQS != nil {
			queueName := arnToQueueName(targetArn)
			if err := s.targets.SQS.EnqueueRaw(ctx, queueName, input); err != nil {
				s.log.Error("scheduler: fire: sqs enqueue", zap.String("queue", queueName), zap.Error(err))
			}
		} else {
			s.log.Warn("scheduler: fire: sqs enqueuer not configured, skipping",
				zap.String("schedule", sc.Name), zap.String("arn", targetArn))
		}
	default:
		s.log.Warn("scheduler: fire: unsupported target type — event logged only",
			zap.String("schedule", sc.Name),
			zap.String("target_arn", targetArn),
		)
	}
}

// arnToQueueName extracts the queue name from an SQS ARN or URL.
// arn:aws:sqs:us-east-1:000000000000:my-queue → my-queue
func arnToQueueName(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) >= 6 {
		return parts[5]
	}
	// Fallback: last segment of slash-separated URL.
	segs := strings.Split(arn, "/")
	return segs[len(segs)-1]
}

// ─── Schedule Expression Parser ───────────────────────────────────────────────

// nextFireTime computes the next time that expr should fire, given the last fire
// time and the current time. Returns zero time when no future firing applies
// (e.g. a one-shot "at" expression that has already fired).
func nextFireTime(expr string, lastFire, now time.Time) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	switch {
	case strings.HasPrefix(expr, "rate("):
		return nextRateFire(expr, lastFire, now)
	case strings.HasPrefix(expr, "at("):
		return nextAtFire(expr)
	case strings.HasPrefix(expr, "cron("):
		return nextCronFire(expr, lastFire, now)
	default:
		return time.Time{}, fmt.Errorf("unknown expression type: %q", expr)
	}
}

// nextRateFire parses a rate expression and returns the next fire time.
func nextRateFire(expr string, lastFire, now time.Time) (time.Time, error) {
	// rate(N unit)
	inner := strings.TrimSuffix(strings.TrimPrefix(expr, "rate("), ")")
	inner = strings.TrimSpace(inner)
	parts := strings.Fields(inner)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("invalid rate expression: %q", expr)
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return time.Time{}, fmt.Errorf("invalid rate value: %q", parts[0])
	}
	unit := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(parts[1], "s"), ""))
	var period time.Duration
	switch unit {
	case "minute", "minutes":
		period = time.Duration(n) * time.Minute
	case "hour", "hours":
		period = time.Duration(n) * time.Hour
	case "day", "days":
		period = time.Duration(n) * 24 * time.Hour
	default:
		return time.Time{}, fmt.Errorf("unknown rate unit: %q", parts[1])
	}

	if lastFire.IsZero() {
		// Never fired: fire immediately on first tick after creation.
		return now, nil
	}
	return lastFire.Add(period), nil
}

// nextAtFire parses an at expression and returns the fire time (or zero if past).
func nextAtFire(expr string) (time.Time, error) {
	// at(yyyy-mm-ddThh:mm:ss)
	inner := strings.TrimSuffix(strings.TrimPrefix(expr, "at("), ")")
	inner = strings.TrimSpace(inner)
	t, err := time.Parse("2006-01-02T15:04:05", inner)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid at expression: %q: %w", expr, err)
	}
	return t, nil
}

// nextCronFire parses a 6-field AWS cron expression and computes the next fire
// time after lastFire (or now if never fired). Supports numeric values, *, ?,
// comma-separated lists, ranges, and step values. Does NOT support L, W, #.
func nextCronFire(expr string, lastFire, now time.Time) (time.Time, error) {
	// cron(min hour dom month dow year)
	inner := strings.TrimSuffix(strings.TrimPrefix(expr, "cron("), ")")
	inner = strings.TrimSpace(inner)
	fields := strings.Fields(inner)
	if len(fields) != 6 {
		return time.Time{}, fmt.Errorf("aws cron must have 6 fields, got %d: %q", len(fields), expr)
	}
	// field indices: 0=min 1=hour 2=dom 3=month 4=dow 5=year
	from := now
	if !lastFire.IsZero() {
		from = lastFire.Add(time.Minute) // search from 1 minute after last fire
	}
	from = from.Truncate(time.Minute)

	// Search up to 5 years ahead.
	limit := now.Add(5 * 365 * 24 * time.Hour)
	for t := from; t.Before(limit); t = t.Add(time.Minute) {
		if matchCronField(fields[5], t.Year(), 1970, 2099) &&
			matchCronField(fields[3], int(t.Month()), 1, 12) &&
			matchCronDayField(fields[2], fields[4], t) {
			if matchCronField(fields[1], t.Hour(), 0, 23) &&
				matchCronField(fields[0], t.Minute(), 0, 59) {
				return t, nil
			}
			// Skip ahead to next matching hour:minute to avoid iterating every minute.
			nextH, err := nextMatchingValue(fields[1], t.Hour(), 0, 23)
			if err == nil && nextH > t.Hour() {
				t = time.Date(t.Year(), t.Month(), t.Day(), nextH, 0, 0, 0, t.Location()).Add(-time.Minute)
			}
		}
	}
	return time.Time{}, fmt.Errorf("cron expression %q has no next fire within 5 years", expr)
}

// matchCronField returns true if value matches the cron field spec.
func matchCronField(spec string, value, min, max int) bool {
	if spec == "*" || spec == "?" {
		return true
	}
	for _, part := range strings.Split(spec, ",") {
		if matchCronPart(part, value, min, max) {
			return true
		}
	}
	return false
}

// matchCronPart handles a single comma-separated cron part.
func matchCronPart(part string, value, min, max int) bool {
	if strings.Contains(part, "/") {
		// Step: */5 or start/step
		segs := strings.SplitN(part, "/", 2)
		step, err := strconv.Atoi(segs[1])
		if err != nil || step <= 0 {
			return false
		}
		start := min
		if segs[0] != "*" && segs[0] != "?" {
			if s, err := strconv.Atoi(segs[0]); err == nil {
				start = s
			}
		}
		for v := start; v <= max; v += step {
			if v == value {
				return true
			}
		}
		return false
	}
	if strings.Contains(part, "-") {
		// Range: 1-5
		segs := strings.SplitN(part, "-", 2)
		lo, err1 := strconv.Atoi(segs[0])
		hi, err2 := strconv.Atoi(segs[1])
		if err1 != nil || err2 != nil {
			return false
		}
		return value >= lo && value <= hi
	}
	v, err := strconv.Atoi(part)
	return err == nil && v == value
}

// matchCronDayField handles the AWS-specific dom/dow interaction (?).
// When dom is ? → match only dow. When dow is ? → match only dom.
// When both are * → match all.
func matchCronDayField(dom, dow string, t time.Time) bool {
	domAny := dom == "*" || dom == "?"
	dowAny := dow == "*" || dow == "?"
	if domAny && dowAny {
		return true
	}
	if dom == "?" {
		// Match on dow only (0=Sun in AWS)
		awsDow := int(t.Weekday()) // Go: 0=Sun, same as AWS
		return matchCronField(dow, awsDow, 0, 6)
	}
	if dow == "?" {
		// Match on dom only
		return matchCronField(dom, t.Day(), 1, 31)
	}
	// Both set — match either (OR semantics in some cron dialects)
	return matchCronField(dom, t.Day(), 1, 31) || matchCronField(dow, int(t.Weekday()), 0, 6)
}

// nextMatchingValue returns the smallest value >= current that matches spec.
func nextMatchingValue(spec string, current, min, max int) (int, error) {
	if spec == "*" || spec == "?" {
		return current, nil
	}
	best := math.MaxInt32
	for v := current; v <= max; v++ {
		if matchCronField(spec, v, min, max) {
			if v < best {
				best = v
			}
			break
		}
	}
	if best == math.MaxInt32 {
		return 0, fmt.Errorf("no match")
	}
	return best, nil
}
