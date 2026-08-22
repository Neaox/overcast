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
// without real sleeps. Expressions are evaluated in cron.go. Delivery goes
// through internal/eventtarget, so every target kind an EventBridge rule can
// reach a schedule can reach too (see delivery.go).
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

	// deliveryWorkers is how many firings the engine delivers at the same
	// time. The tick goroutine hands a due schedule to this pool and moves on,
	// so one target that is slow, wedged or working through its RetryPolicy
	// occupies a single worker instead of the whole engine.
	deliveryWorkers = 8

	// deliveryQueueDepth bounds the firings waiting for a worker. A schedule is
	// never in flight twice (see claimFiring), so the queue can only fill when
	// this many *distinct* schedules are due at once and every worker is busy.
	// A firing that finds it full is not dropped: the tick leaves the
	// schedule's last-fire time alone, so it is still due on the next tick.
	deliveryQueueDepth = 256
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
	KmsKeyArn                  string             `json:"KmsKeyArn,omitempty"`
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
	cfg   *config.Config
	store state.Store
	clk   clock.Clock
	log   *serviceutil.ServiceLogger

	// targets dispatches a firing to whatever the target ARN names. Built once
	// from the root router in InitRouter; nil until then.
	targets *eventtarget.Dispatcher

	// scheduleLocks serialises the read-modify-write of one schedule record.
	// Held by every operation that removes or replaces a stored schedule —
	// UpdateSchedule, DeleteSchedule and DeleteScheduleGroup's cascade — across
	// both the read and the write, which is the part that makes them one step.
	scheduleLocks serviceutil.RecordLocks

	// deliveries carries due firings from the tick goroutine to the delivery
	// worker pool, and inflight names the schedules a worker currently owns.
	deliveries   chan firing
	deliveryOnce sync.Once
	inflightMu   sync.Mutex
	inflight     map[string]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// firing is one due schedule on its way from the tick goroutine to a delivery
// worker. The Schedule is the tick's own decoded copy, so no two firings — and
// no firing and the store — share one.
type firing struct {
	key      string // store key, "region:group/name"
	region   string
	schedule *Schedule
	at       time.Time
}

// New returns a configured Scheduler Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		cfg:        cfg,
		store:      st,
		clk:        clk,
		log:        serviceutil.NewServiceLogger(logger, serviceName),
		deliveries: make(chan firing, deliveryQueueDepth),
		inflight:   make(map[string]struct{}),
		stopCh:     make(chan struct{}),
	}
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
		s.startDelivery()
		s.wg.Add(1)
		go s.runEngine()
	})
}

// startDelivery brings up the delivery worker pool. It is idempotent, and tick
// calls it too, so a caller that drives tick() directly rather than through the
// ticker loop still has workers to deliver on.
func (s *Service) startDelivery() {
	s.deliveryOnce.Do(func() {
		for i := 0; i < deliveryWorkers; i++ {
			s.wg.Add(1)
			go s.deliveryWorker()
		}
	})
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

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
	if err := s.deleteTags(ctx, s.groupARN(region, name)); err != nil {
		return err
	}
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
	if err := s.deleteTags(ctx, s.scheduleARN(region, group, name)); err != nil {
		return err
	}
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

// tick hands every due schedule, in every region, to the delivery pool.
//
// It does not deliver anything itself. Delivery replays the firing against the
// emulator's own API and retries it as the target's RetryPolicy asks, so it
// takes as long as the target does. The engine used to do that inline and in
// scan order, which meant one unreachable Lambda held up every other schedule
// in the emulator for as long as it took to exhaust its retries.
func (s *Service) tick() {
	s.startDelivery()

	ctx := context.Background()
	now := s.clk.Now()
	deferred := 0

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

		// Queue the firing. The store keys schedules per region
		// ("region:group/name" — see scheduleKey) and the targets (SQS queues)
		// resolve from region-keyed stores, so the schedule's own region
		// travels with the firing and is pinned on the delivery context —
		// otherwise schedules outside the default region deliver into the
		// default region's queues.
		region, _, hasRegion := strings.Cut(kv.Key, ":")
		if !hasRegion || region == "" {
			region = s.cfg.Region
		}
		if !s.claimFiring(kv.Key) {
			// This schedule's previous firing is still being delivered.
			// Skipping leaves its last-fire time untouched, so it stays due
			// and a later tick picks it up.
			continue
		}
		select {
		case s.deliveries <- firing{key: kv.Key, region: region, schedule: &sc, at: now}:
		default:
			s.releaseFiring(kv.Key)
			deferred++
		}
	}

	if deferred > 0 {
		s.log.Warn("scheduler: tick: delivery queue full, deferring firings to a later tick",
			zap.Int("deferred", deferred), zap.Int("queue_depth", deliveryQueueDepth))
	}
}

// claimFiring reserves a schedule for one in-flight firing, reporting false
// when it already has one.
//
// This is what keeps a schedule's own firings ordered while different schedules
// run concurrently: a schedule is never in flight twice, so a target that takes
// longer to answer than the schedule's period delays that schedule's next
// firing and nothing else. Overlapping a schedule with itself would reorder its
// deliveries, which neither AWS nor the previous serial engine ever did.
func (s *Service) claimFiring(key string) bool {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	if _, busy := s.inflight[key]; busy {
		return false
	}
	s.inflight[key] = struct{}{}
	return true
}

// releaseFiring hands a schedule back, so the next tick may fire it again.
func (s *Service) releaseFiring(key string) {
	s.inflightMu.Lock()
	delete(s.inflight, key)
	s.inflightMu.Unlock()
}

// deliveryWorker delivers queued firings until the service stops.
func (s *Service) deliveryWorker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case f := <-s.deliveries:
			s.deliverFiring(f)
		}
	}
}

// deliverFiring records the fire time and delivers one firing.
//
// The last-fire time is written here, before the delivery, rather than on the
// tick goroutine after it. The schedule is held in flight for the whole
// delivery, so nothing can re-fire it in the meantime, and recording the tick's
// own time keeps a schedule's cadence anchored to when it became due rather
// than to how long its target took to answer.
func (s *Service) deliverFiring(f firing) {
	defer s.releaseFiring(f.key)
	ctx := context.Background()
	s.setLastFire(ctx, f.key, f.at)
	s.fire(middleware.ContextWithRegion(ctx, f.region), f.schedule, f.at)
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
