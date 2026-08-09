// Package scheduler provides emulation of Amazon EventBridge Scheduler.
//
// Implemented operations:
//
//	Schedule groups: CreateScheduleGroup, GetScheduleGroup, ListScheduleGroups,
//	  DeleteScheduleGroup, TagResource, UntagResource, ListTagsForResource
//	Schedules: CreateSchedule, GetSchedule, UpdateSchedule, DeleteSchedule,
//	  ListSchedules
//
// Routes are AWS's own REST-JSON bindings from the pinned Smithy model
// (scheduler-2021-06-30): /schedules/{Name}, /schedules, /schedule-groups/{Name},
// /schedule-groups, and /tags/{ResourceArn} — see RegisterRoutes and TagsRouter.
//
// A lightweight cron engine fires rate/cron/at expressions against their
// declared targets using the injected clock — tests can fast-forward time
// without real sleeps. Delivery goes through internal/eventtarget, so every
// target kind an EventBridge rule can reach a schedule can reach too (see
// delivery.go).
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/eventtarget"
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
	Arn                   string             `json:"Arn"`
	RoleArn               string             `json:"RoleArn"`
	Input                 string             `json:"Input,omitempty"`
	DeadLetterConfig      *dlqConfig         `json:"DeadLetterConfig,omitempty"`
	RetryPolicy           *retryPolicy       `json:"RetryPolicy,omitempty"`
	SqsParameters         *sqsParameters     `json:"SqsParameters,omitempty"`
	KinesisParameters     *kinesisParams     `json:"KinesisParameters,omitempty"`
	EcsParameters         *ecsParams         `json:"EcsParameters,omitempty"`
	EventBridgeParameters *eventBridgeParams `json:"EventBridgeParameters,omitempty"`
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

// ecsParams is AWS's EcsParameters — the templated target for RunTask. Only
// the members that shape the emulated RunTask call are modelled; the rest of
// AWS's shape (tags, placement, capacity provider strategy) is accepted and
// ignored, as it is everywhere else in this emulator.
type ecsParams struct {
	TaskDefinitionArn    string            `json:"TaskDefinitionArn"`
	TaskCount            int               `json:"TaskCount,omitempty"`
	LaunchType           string            `json:"LaunchType,omitempty"`
	PlatformVersion      string            `json:"PlatformVersion,omitempty"`
	Group                string            `json:"Group,omitempty"`
	NetworkConfiguration *ecsNetworkConfig `json:"NetworkConfiguration,omitempty"`
}

type ecsNetworkConfig struct {
	AwsvpcConfiguration *ecsAwsvpcConfig `json:"awsvpcConfiguration,omitempty"`
}

type ecsAwsvpcConfig struct {
	Subnets        []string `json:"Subnets,omitempty"`
	SecurityGroups []string `json:"SecurityGroups,omitempty"`
	AssignPublicIp string   `json:"AssignPublicIp,omitempty"`
}

// eventBridgeParams is AWS's EventBridgeParameters — the routing fields a
// schedule's PutEvents entry carries when its target is an event bus. Both
// members are required by AWS, and a downstream rule filtering on source or
// detail-type could never match without them.
type eventBridgeParams struct {
	DetailType string `json:"DetailType"`
	Source     string `json:"Source"`
}

type flexibleTimeWindow struct {
	Mode                   string `json:"Mode"`
	MaximumWindowInMinutes int    `json:"MaximumWindowInMinutes,omitempty"`
}

// ─── Service ──────────────────────────────────────────────────────────────────

// Service implements router.Service for EventBridge Scheduler.
type Service struct {
	cfg     *config.Config
	store   state.Store
	clk     clock.Clock
	log     *serviceutil.ServiceLogger
	typedOp map[string]op.Operation

	// targets dispatches a firing to whatever the target ARN names. Built once
	// from the root router in InitRouter; nil until then.
	targets *eventtarget.Dispatcher

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
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

// InitRouter wires the root router so a schedule reaches its target through the
// same path an SDK call would take, and starts the cron engine.
//
// The engine starts here rather than in RegisterRoutes so it never observes a
// half-wired service: routes are registered before the composition root has a
// dispatcher to hand over, and a tick in that window would read the field the
// wiring is about to write.
func (s *Service) InitRouter(router http.Handler) {
	s.initDispatcher(router)
	s.startEngine()
}

// initDispatcher wires target delivery without starting the engine, so unit
// tests can drive fire() on their own schedule.
func (s *Service) initDispatcher(router http.Handler) {
	s.targets = eventtarget.NewDispatcher(router, s.cfg.Region)
}

// dispatcher returns the target dispatcher, or nil before InitRouter has run.
func (s *Service) dispatcher() *eventtarget.Dispatcher { return s.targets }

func (s *Service) startEngine() {
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.runEngine()
	})
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
//
// The paths are EventBridge Scheduler's own @http bindings, not an emulator
// invention: a schedule is addressed by name alone, and its group travels in
// the body on create/update and as ?groupName on read/delete. Every SDK and
// `aws scheduler …` call lands here.
func (s *Service) RegisterRoutes(r chi.Router) {
	// Schedule groups.
	r.Post("/schedule-groups/{name}", s.createScheduleGroup)
	r.Get("/schedule-groups/{name}", s.getScheduleGroup)
	r.Delete("/schedule-groups/{name}", s.deleteScheduleGroup)
	r.Get("/schedule-groups", s.listScheduleGroups)

	// Schedules.
	r.Post("/schedules/{name}", s.createSchedule)
	r.Get("/schedules/{name}", s.getSchedule)
	r.Put("/schedules/{name}", s.updateSchedule)
	r.Delete("/schedules/{name}", s.deleteSchedule)
	r.Get("/schedules", s.listSchedules)

	// NOTE: /tags routes are NOT registered here — see TagsRouter. The /tags
	// path space is shared with Pipes, EKS and API Gateway, so the main router
	// owns it and dispatches by the resource ARN's service prefix.
}

// TagsRouter returns a chi.Router for Scheduler's tagging routes, which live
// under the shared /tags/{ResourceArn} path space. The main router mounts it
// behind the ARN-dispatching owner it shares with Pipes, EKS and API Gateway.
// SDK clients URL-escape the ARN into a single path segment, which is what the
// {resourceArn} pattern matches.
func (s *Service) TagsRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/{resourceArn}", s.listTagsForResource)
	r.Post("/{resourceArn}", s.tagResource)
	r.Delete("/{resourceArn}", s.untagResource)
	return r
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

// loadGroup reads one schedule group.
//
// A store failure and a record that will not decode are deliberately different
// answers. The first is infrastructure and surfaces as InternalError; the
// second is one bad persisted payload, which is reported as the AWS not-found
// the caller can act on rather than a 500 (AGENTS.md § State).
func (s *Service) loadGroup(ctx context.Context, region, name string) (*ScheduleGroup, bool, *protocol.AWSError) {
	key := s.groupKey(region, name)
	raw, found, err := s.store.Get(ctx, nsGroups, key)
	if err != nil {
		return nil, false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, false, nil
	}
	var g ScheduleGroup
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		s.logSkippedRecord(nsGroups, key, err)
		return nil, false, nil
	}
	return &g, true, nil
}

func (s *Service) deleteGroup(ctx context.Context, region, name string) error {
	return s.store.Delete(ctx, nsGroups, s.groupKey(region, name))
}

// listGroups returns every group in a region, seeding "default" into the answer
// when it has not been created yet. Undecodable records are skipped and logged
// rather than failing the whole listing.
func (s *Service) listGroups(ctx context.Context, region string) ([]*ScheduleGroup, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsGroups, region+":")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*ScheduleGroup, 0, len(pairs)+1)
	hasDefault := false
	for _, kv := range pairs {
		var g ScheduleGroup
		if err := json.Unmarshal([]byte(kv.Value), &g); err != nil {
			s.logSkippedRecord(nsGroups, kv.Key, err)
			continue
		}
		out = append(out, &g)
		if g.Name == defaultGroup {
			hasDefault = true
		}
	}
	if !hasDefault {
		now := s.clk.Now()
		out = append(out, &ScheduleGroup{
			Name:                 defaultGroup,
			Arn:                  s.groupARN(region, defaultGroup),
			State:                "ACTIVE",
			CreationDate:         now,
			LastModificationDate: now,
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

// loadSchedule reads one schedule. It splits store failure from an undecodable
// record for the same reason loadGroup does.
func (s *Service) loadSchedule(ctx context.Context, region, group, name string) (*Schedule, bool, *protocol.AWSError) {
	key := s.scheduleKey(region, group, name)
	raw, found, err := s.store.Get(ctx, nsSchedules, key)
	if err != nil {
		return nil, false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, false, nil
	}
	var sc Schedule
	if err := json.Unmarshal([]byte(raw), &sc); err != nil {
		s.logSkippedRecord(nsSchedules, key, err)
		return nil, false, nil
	}
	return &sc, true, nil
}

func (s *Service) deleteScheduleRecord(ctx context.Context, region, group, name string) error {
	_ = s.store.Delete(ctx, nsLastFire, s.scheduleKey(region, group, name))
	return s.store.Delete(ctx, nsSchedules, s.scheduleKey(region, group, name))
}

func (s *Service) listSchedulesByGroup(ctx context.Context, region, group string) ([]*Schedule, *protocol.AWSError) {
	return s.scanSchedules(ctx, fmt.Sprintf("%s:%s/", region, group))
}

func (s *Service) listAllSchedules(ctx context.Context, region string) ([]*Schedule, *protocol.AWSError) {
	return s.scanSchedules(ctx, region+":")
}

// scanSchedules decodes every schedule under a store key prefix, skipping and
// logging records that will not decode so one bad payload cannot fail a listing.
func (s *Service) scanSchedules(ctx context.Context, prefix string) ([]*Schedule, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsSchedules, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*Schedule, 0, len(pairs))
	for _, kv := range pairs {
		var sc Schedule
		if err := json.Unmarshal([]byte(kv.Value), &sc); err != nil {
			s.logSkippedRecord(nsSchedules, kv.Key, err)
			continue
		}
		out = append(out, &sc)
	}
	return out, nil
}

// ─── REST Handlers ────────────────────────────────────────────────────────────
//
// Every handler below is an adapter and nothing more: it lifts AWS's @http
// bindings — the path label, the query string, the body — into the request
// struct, and hands it to the codec-agnostic implementation in typed_logic.go
// that the JSON/CBOR dispatch path also uses. No behaviour lives here.

// writeTyped runs a typed operation and renders its result as REST-JSON.
func writeTyped[In, Out any](w http.ResponseWriter, r *http.Request, req *In,
	fn func(context.Context, *In) (*Out, *protocol.AWSError),
) {
	out, aerr := fn(r.Context(), req)
	writeResult(w, r, out, aerr)
}

// writeTypedAny is writeTyped for the operations whose output shape is empty,
// which op.NewTypedAny models as `any`. Go cannot unify the two signatures.
func writeTypedAny[In any](w http.ResponseWriter, r *http.Request, req *In,
	fn func(context.Context, *In) (any, *protocol.AWSError),
) {
	out, aerr := fn(r.Context(), req)
	writeResult(w, r, out, aerr)
}

// writeResult renders a typed operation's outcome. Every Scheduler operation
// binds HTTP 200 on success.
func writeResult(w http.ResponseWriter, r *http.Request, out any, aerr *protocol.AWSError) {
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, out)
}

// ─── Schedule groups ──────────────────────────────────────────────────────────

func (s *Service) createScheduleGroup(w http.ResponseWriter, r *http.Request) {
	// Tags is the operation's only body member and it is optional, so an
	// absent or unreadable body means "no tags" rather than a client error.
	// Everything else the operation needs is on the path.
	var req createScheduleGroupRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.Name = chi.URLParam(r, "name")
	writeTyped(w, r, &req, s.createScheduleGroupTyped)
}

func (s *Service) getScheduleGroup(w http.ResponseWriter, r *http.Request) {
	req := getScheduleGroupRequest{Name: chi.URLParam(r, "name")}
	writeTyped(w, r, &req, s.getScheduleGroupTyped)
}

func (s *Service) deleteScheduleGroup(w http.ResponseWriter, r *http.Request) {
	req := deleteScheduleGroupRequest{Name: chi.URLParam(r, "name")}
	writeTypedAny(w, r, &req, s.deleteScheduleGroupTyped)
}

func (s *Service) listScheduleGroups(w http.ResponseWriter, r *http.Request) {
	req := listScheduleGroupsRequest{
		NamePrefix: r.URL.Query().Get("NamePrefix"),
		MaxResults: serviceutil.QueryInt(r, "MaxResults", 0),
		NextToken:  r.URL.Query().Get("NextToken"),
	}
	writeTyped(w, r, &req, s.listScheduleGroupsTyped)
}

// ─── Tags ─────────────────────────────────────────────────────────────────────

// resourceARN reads the {resourceArn} path label. AWS SDKs percent-encode the
// ARN into a single path segment, so it has to be unescaped before it matches
// the key the tag store was written under.
func resourceARN(r *http.Request) string {
	raw := chi.URLParam(r, "resourceArn")
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	req := listTagsForResourceRequest{ResourceArn: resourceARN(r)}
	writeTyped(w, r, &req, s.listTagsForResourceTyped)
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	var req tagResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	req.ResourceArn = resourceARN(r)
	writeTypedAny(w, r, &req, s.tagResourceTyped)
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	req := untagResourceRequest{
		ResourceArn: resourceARN(r),
		TagKeys:     r.URL.Query()["TagKeys"],
	}
	writeTypedAny(w, r, &req, s.untagResourceTyped)
}

// ─── Schedules ────────────────────────────────────────────────────────────────

func (s *Service) createSchedule(w http.ResponseWriter, r *http.Request) {
	var req createScheduleRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	req.Name = chi.URLParam(r, "name")
	writeTyped(w, r, &req, s.createScheduleTyped)
}

func (s *Service) getSchedule(w http.ResponseWriter, r *http.Request) {
	req := getScheduleRequest{
		Name:      chi.URLParam(r, "name"),
		GroupName: r.URL.Query().Get("groupName"),
	}
	writeTyped(w, r, &req, s.getScheduleTyped)
}

func (s *Service) updateSchedule(w http.ResponseWriter, r *http.Request) {
	var req updateScheduleRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	req.Name = chi.URLParam(r, "name")
	writeTyped(w, r, &req, s.updateScheduleTyped)
}

func (s *Service) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	req := deleteScheduleRequest{
		Name:      chi.URLParam(r, "name"),
		GroupName: r.URL.Query().Get("groupName"),
	}
	writeTypedAny(w, r, &req, s.deleteScheduleTyped)
}

func (s *Service) listSchedules(w http.ResponseWriter, r *http.Request) {
	req := listSchedulesRequest{
		ScheduleGroup: r.URL.Query().Get("ScheduleGroup"),
		NamePrefix:    r.URL.Query().Get("NamePrefix"),
		State:         r.URL.Query().Get("State"),
		MaxResults:    serviceutil.QueryInt(r, "MaxResults", 0),
		NextToken:     r.URL.Query().Get("NextToken"),
	}
	writeTyped(w, r, &req, s.listSchedulesTyped)
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
		if err := json.Unmarshal([]byte(kv.Value), &sc); err != nil {
			s.logSkippedRecord(nsSchedules, kv.Key, err)
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

		// Fire and update last-fire timestamp. The store keys schedules per
		// region ("region:group/name" — see scheduleKey) and fire's targets
		// (SQS queues) resolve from region-keyed stores, so pin the schedule's
		// own region on the delivery context — otherwise schedules outside the
		// default region deliver into the default region's queues.
		region, _, hasRegion := strings.Cut(kv.Key, ":")
		if !hasRegion || region == "" {
			region = s.cfg.Region
		}
		s.fire(middleware.ContextWithRegion(ctx, region), &sc, now)
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
	// AWS writes the unit singular for a value of 1 and plural otherwise, and
	// accepts either, so the trailing "s" is dropped before matching.
	unit := strings.TrimSuffix(strings.ToLower(parts[1]), "s")
	var period time.Duration
	switch unit {
	case "minute":
		period = time.Duration(n) * time.Minute
	case "hour":
		period = time.Duration(n) * time.Hour
	case "day":
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
