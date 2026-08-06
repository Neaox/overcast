// Package pipes emulates the AWS EventBridge Pipes service.
//
// A pipe connects a source to a target, with an optional enrichment step in
// between. What Overcast runs:
//
//	sources     DynamoDB Streams (via the internal event bus), Kinesis streams
//	            and SQS queues (polled on the injected clock)
//	enrichment  a Lambda function, invoked synchronously; its return value
//	            replaces the batch, and an empty response drops it
//	targets     Lambda, SQS, SNS, Step Functions, Kinesis, Firehose and
//	            EventBridge event buses, through internal/eventtarget — the
//	            dispatch seam built for EventBridge rule targets (#467)
//
// Anything else is refused at CreatePipe/UpdatePipe time with a
// ValidationException naming the field, never stored and silently ignored
// (docs/plans/full-emulation-priority.md §2.1). See wiring.go.
//
// REST API (follows the real AWS EventBridge Pipes path prefix):
//
//	POST   /v1/pipes/{name}   — CreatePipe
//	GET    /v1/pipes/{name}   — DescribePipe
//	PUT    /v1/pipes/{name}   — UpdatePipe
//	DELETE /v1/pipes/{name}   — DeletePipe
//	GET    /v1/pipes          — ListPipes
//
// State machine:
//
//	CreatePipe            → CREATING → (timer) → RUNNING
//	UpdatePipe (config)   → UPDATING → (timer) → previous state
//	UpdatePipe (state)    → STARTING/STOPPING → (timer) → RUNNING/STOPPED
//	DeletePipe            → DELETING → (timer) → (removed from store)
package pipes

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/eventtarget"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// stateTransitionDelay is how long after CreatePipe / UpdatePipe / DeletePipe
// before the state machine advances. Short enough to be invisible in real
// usage; observable in tests via srv.Clock.Add(stateTransitionDelay).
const stateTransitionDelay = 50 * time.Millisecond

const serviceName = "pipes"

// Service implements router.Service for EventBridge Pipes.
type Service struct {
	cfg     *config.Config
	handler *Handler
}

// New returns a configured Pipes Service.
//
// It allocates only — no store reads, no network, nothing that would put work
// on the router's construction path (docs/dev/performance.md § Startup budget).
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	return &Service{
		cfg:     cfg,
		handler: newHandler(cfg, store, serviceutil.NewServiceLogger(logger, serviceName), clk),
	}
}

// Name returns the service identifier used in config and logging.
func (s *Service) Name() string { return serviceName }

// TargetPrefix returns "" — pipes uses REST routing, not X-Amz-Target dispatch.
func (s *Service) TargetPrefix() string { return "" }

// RegisterRoutes mounts the Pipes REST API under /v1/pipes, plus the web
// console's read-only wiring and delivery feeds.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Route("/v1/pipes", func(r chi.Router) {
		r.Get("/", s.handler.ListPipes)
		r.Post("/{name}", s.handler.CreatePipe)
		r.Get("/{name}", s.handler.DescribePipe)
		r.Delete("/{name}", s.handler.DeletePipe)
		r.Put("/{name}", s.handler.UpdatePipe)
		r.Route("/tags", func(r chi.Router) {
			r.Post("/*", s.handler.TagResource)
			r.Delete("/*", s.handler.UntagResource)
			r.Get("/*", s.handler.ListTagsForResource)
		})
	})
	r.Route("/tags", func(r chi.Router) {
		r.Post("/*", s.handler.TagResource)
		r.Delete("/*", s.handler.UntagResource)
		r.Get("/*", s.handler.ListTagsForResource)
	})
	s.handler.registerAdminRoutes(r)
}

// Sources wires the services a pipe reads from. Every member is optional: a
// nil one disables that source type rather than failing a pipe that does not
// use it.
type Sources struct {
	// Messages receives and acknowledges SQS queue sources.
	Messages events.MessageReceiver
	// Streams reads Kinesis stream sources.
	Streams events.StreamRecordReceiver
	// Enricher invokes a Lambda enrichment synchronously.
	Enricher events.FunctionSyncInvoker
}

// InitDelivery wires the event bus and the source services, and starts the
// source-polling loop.
//
// Must be called after the services a pipe can read from are constructed. Call
// it exactly once from the router: each call adds a new bus subscriber.
func (s *Service) InitDelivery(bus *events.Bus, sources Sources) {
	s.handler.bus = bus
	s.handler.messages = sources.Messages
	s.handler.streams = sources.Streams
	s.handler.enricher = sources.Enricher
	bus.Subscribe(events.DynamoDBStreamInsert, s.handler.deliverStreamEvent)
	bus.Subscribe(events.DynamoDBStreamModify, s.handler.deliverStreamEvent)
	bus.Subscribe(events.DynamoDBStreamRemove, s.handler.deliverStreamEvent)
	s.handler.startPolling()
}

// InitRouter wires the root router so a pipe can reach its target through the
// same path an SDK call would take.
func (s *Service) InitRouter(router http.Handler) {
	s.handler.targets = eventtarget.NewDispatcher(router, s.cfg.Region)
}

// Stop drains the source-polling loop. It satisfies router.Stopper.
func (s *Service) Stop(ctx context.Context) { s.handler.stopPolling(ctx) }

// ---- Handler ----------------------------------------------------------------

// Handler holds Pipes handler dependencies.
type Handler struct {
	cfg   *config.Config
	store *pipesStore
	log   *serviceutil.ServiceLogger
	clk   clock.Clock

	bus *events.Bus // injected by InitDelivery; nil until then

	// Source-side dependencies, injected by InitDelivery.
	messages events.MessageReceiver
	streams  events.StreamRecordReceiver
	enricher events.FunctionSyncInvoker

	// targets dispatches a batch to whatever the target ARN names. Built from
	// the root router in InitRouter; nil until then.
	targets *eventtarget.Dispatcher

	// deliveries is the console's recent execution-outcome feed.
	deliveries *deliveryLog
	// cursors holds each Kinesis source's per-shard read position.
	cursors *cursors

	pollOnce sync.Once
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

func newHandler(cfg *config.Config, st state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	return &Handler{
		cfg:        cfg,
		store:      newPipesStore(st, cfg.Region),
		log:        log,
		clk:        clk,
		deliveries: newDeliveryLog(),
		cursors:    newCursors(),
		stopCh:     make(chan struct{}),
	}
}

// dispatcher returns the target dispatcher, or nil before InitRouter has run.
func (h *Handler) dispatcher() *eventtarget.Dispatcher { return h.targets }

// ---- Request types -----------------------------------------------------------

// createPipeRequest is AWS's CreatePipe body. Name comes from the path.
type createPipeRequest struct {
	Description          string                `json:"Description"`
	DesiredState         PipeState             `json:"DesiredState"`
	RoleArn              string                `json:"RoleArn"`
	Source               string                `json:"Source"`
	SourceParameters     *SourceParameters     `json:"SourceParameters"`
	Enrichment           string                `json:"Enrichment"`
	EnrichmentParameters *EnrichmentParameters `json:"EnrichmentParameters"`
	Target               string                `json:"Target"`
	TargetParameters     *TargetParameters     `json:"TargetParameters"`
	Tags                 map[string]string     `json:"Tags"`
}

// updatePipeRequest is AWS's UpdatePipe body. Source is absent by design: AWS
// does not allow the source of a pipe to change, only its target.
type updatePipeRequest struct {
	Description          *string               `json:"Description,omitempty"`
	DesiredState         PipeState             `json:"DesiredState,omitempty"`
	RoleArn              string                `json:"RoleArn,omitempty"`
	SourceParameters     *SourceParameters     `json:"SourceParameters,omitempty"`
	Enrichment           *string               `json:"Enrichment,omitempty"`
	EnrichmentParameters *EnrichmentParameters `json:"EnrichmentParameters,omitempty"`
	Target               string                `json:"Target,omitempty"`
	TargetParameters     *TargetParameters     `json:"TargetParameters,omitempty"`
}

// ---- Handlers ---------------------------------------------------------------

// CreatePipe implements POST /v1/pipes/{name}.
// The pipe starts in CREATING state and transitions to RUNNING asynchronously.
func (h *Handler) CreatePipe(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var req createPipeRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.Source, "Source") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Target, "Target") {
		return
	}

	ctx := r.Context()

	// Reject duplicates.
	if _, aerr := h.store.getPipe(ctx, name); aerr == nil {
		writeError(w, r, errPipeAlreadyExists(name))
		return
	}

	now := h.clk.Now()
	desired := PipeStateRunning
	if req.DesiredState == PipeStateStopped {
		desired = PipeStateStopped
	}
	p := &Pipe{
		Name:                 name,
		Arn:                  h.pipeARN(ctx, name),
		Description:          req.Description,
		RoleArn:              req.RoleArn,
		SourceArn:            req.Source,
		SourceParameters:     req.SourceParameters,
		Enrichment:           req.Enrichment,
		EnrichmentParameters: req.EnrichmentParameters,
		TargetArn:            req.Target,
		TargetParameters:     req.TargetParameters,
		Tags:                 req.Tags,
		CurrentState:         PipeStateCreating,
		DesiredState:         desired,
		CreationTime:         float64(now.Unix()),
		LastModifiedTime:     float64(now.Unix()),
	}

	// Everything this emulator cannot run is refused here rather than stored.
	if aerr := validatePipe(p); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	if aerr := h.store.putPipe(ctx, p); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	log := h.log.WithRecorder(r.Context())
	log.Info("pipe created",
		zap.String("pipe", name),
		zap.String("source", req.Source),
		zap.String("enrichment", req.Enrichment),
		zap.String("target", req.Target))

	// Advance state CREATING → the desired state asynchronously.
	h.scheduleTransition(h.store.region(ctx), name, desired, PipeStateCreating)

	protocol.WriteJSON(w, r, http.StatusOK, p.mutationResponse())
}

// DescribePipe implements GET /v1/pipes/{name}.
func (h *Handler) DescribePipe(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	p, aerr := h.store.getPipe(r.Context(), name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, p)
}

// DeletePipe implements DELETE /v1/pipes/{name}.
// The pipe transitions to DELETING and is removed from the store asynchronously.
func (h *Handler) DeletePipe(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	p, aerr := h.store.getPipe(ctx, name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}

	p.CurrentState = PipeStateDeleting
	p.DesiredState = PipeStateStopped
	p.LastModifiedTime = float64(h.clk.Now().Unix())
	if aerr := h.store.putPipe(ctx, p); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	log := h.log.WithRecorder(r.Context())
	log.Info("pipe deleting", zap.String("pipe", name))

	// Remove from store asynchronously after the transition delay.
	h.scheduleDeletion(h.store.region(ctx), name)

	protocol.WriteJSON(w, r, http.StatusOK, p.mutationResponse())
}

// UpdatePipe implements PUT /v1/pipes/{name}.
//
// AWS allows the desired state, the description, the role, the target and every
// parameter block to change; the source is immutable and is not part of the
// request. A configuration change goes through UPDATING, and a desired-state
// change through STARTING or STOPPING.
func (h *Handler) UpdatePipe(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	var req updatePipeRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	// RoleArn is a required member of UpdatePipe, as it is of CreatePipe.
	if !serviceutil.RequireString(w, r, req.RoleArn, "RoleArn") {
		return
	}

	p, aerr := h.store.getPipe(ctx, name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}

	// Cannot update a pipe that is being deleted.
	if p.CurrentState == PipeStateDeleting {
		writeError(w, r, &protocol.AWSError{
			Code:       "ConflictException",
			Message:    "Pipe is being deleted and cannot be updated.",
			HTTPStatus: http.StatusConflict,
		})
		return
	}

	now := h.clk.Now()
	changed := false
	reconfigured := false

	if req.Description != nil {
		p.Description = *req.Description
		changed = true
	}
	if req.RoleArn != "" {
		p.RoleArn = req.RoleArn
		changed, reconfigured = true, true
	}
	if req.Enrichment != nil {
		p.Enrichment = *req.Enrichment
		changed, reconfigured = true, true
	}
	if req.EnrichmentParameters != nil {
		p.EnrichmentParameters = req.EnrichmentParameters
		changed, reconfigured = true, true
	}
	if req.SourceParameters != nil {
		p.SourceParameters = req.SourceParameters
		changed, reconfigured = true, true
	}
	if req.Target != "" && req.Target != p.TargetArn {
		p.TargetArn = req.Target
		changed, reconfigured = true, true
	}
	if req.TargetParameters != nil {
		p.TargetParameters = req.TargetParameters
		changed, reconfigured = true, true
	}

	// Validate the pipe as it would be after the update, so a change that makes
	// it undeliverable is refused rather than stored (§2.1).
	if reconfigured {
		if aerr := validatePipe(p); aerr != nil {
			writeError(w, r, aerr)
			return
		}
	}

	nextState := p.CurrentState
	if req.DesiredState != "" && req.DesiredState != p.DesiredState {
		switch req.DesiredState { //nolint:exhaustive // only RUNNING/STOPPED are valid desired states
		case PipeStateRunning, PipeStateStopped:
		default:
			writeError(w, r, validationError("DesiredState", "DesiredState must be RUNNING or STOPPED."))
			return
		}
		p.DesiredState = req.DesiredState
		changed = true
		// Transient state chosen by where the pipe is heading.
		if req.DesiredState == PipeStateRunning {
			p.CurrentState = PipeStateStarting
		} else {
			p.CurrentState = PipeStateStopping
		}
		nextState = p.DesiredState
	} else if reconfigured {
		// A configuration-only change goes through UPDATING and lands back
		// where it was, matching AWS's PipeState machine.
		p.CurrentState = PipeStateUpdating
	}

	if !changed {
		protocol.WriteJSON(w, r, http.StatusOK, p.mutationResponse())
		return
	}

	p.LastModifiedTime = float64(now.Unix())
	if aerr := h.store.putPipe(ctx, p); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	log := h.log.WithRecorder(r.Context())
	log.Info("pipe updated", zap.String("pipe", name),
		zap.String("desiredState", string(p.DesiredState)))

	if p.CurrentState == PipeStateStarting || p.CurrentState == PipeStateStopping || p.CurrentState == PipeStateUpdating {
		h.scheduleTransition(h.store.region(ctx), name, nextState, p.CurrentState)
	}

	protocol.WriteJSON(w, r, http.StatusOK, p.mutationResponse())
}

// ListPipes implements GET /v1/pipes.
func (h *Handler) ListPipes(w http.ResponseWriter, r *http.Request) {
	pipes, aerr := h.store.listPipes(r.Context())
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	summaries := make([]map[string]any, 0, len(pipes))
	for _, p := range pipes {
		summaries = append(summaries, p.summary())
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Pipes": summaries})
}

// ---- State machine ----------------------------------------------------------

// ---- Tag handlers -----------------------------------------------------------

const (
	errTagExceededCode    = "ValidationException"
	errTagInvalidCode     = "ValidationException"
	errTagExceededMessage = "Exceeded maximum number of tags allowed."
)

var pipesTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    errTagExceededCode,
	InvalidCode:     errTagInvalidCode,
	ExceededMessage: errTagExceededMessage,
}

func (h *Handler) pipeNameFromARN(arn string) (string, *protocol.AWSError) {
	prefix := "arn:aws:pipes:"
	idx := strings.LastIndex(arn, "pipe/")
	if idx < 0 || !strings.HasPrefix(arn, prefix) {
		return "", &protocol.AWSError{
			Code: "ValidationException", Message: "Invalid ARN", HTTPStatus: http.StatusBadRequest,
		}
	}
	name := arn[idx+5:]
	if name == "" {
		return "", &protocol.AWSError{
			Code: "ValidationException", Message: "Invalid ARN", HTTPStatus: http.StatusBadRequest,
		}
	}
	return name, nil
}

func (h *Handler) tagResourceARNFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	arn := strings.TrimPrefix(r.URL.Path, "/tags/")
	arn = strings.TrimPrefix(arn, "/")
	arn, _, _ = strings.Cut(arn, "?")
	if arn == "" {
		writeError(w, r, &protocol.AWSError{
			Code: "ValidationException", Message: "Resource ARN is required", HTTPStatus: http.StatusBadRequest,
		})
		return "", false
	}
	return arn, true
}

// TagResource handles POST /tags/{ResourceArn}.
func (h *Handler) TagResource(w http.ResponseWriter, r *http.Request) {
	arn, ok := h.tagResourceARNFromPath(w, r)
	if !ok {
		return
	}

	var req struct {
		Tags map[string]string `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	name, aerr := h.pipeNameFromARN(arn)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}

	if aerr := serviceutil.ValidateTags(pipesTagCfg, req.Tags); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	ctx := r.Context()
	p, aerr := h.store.getPipe(ctx, name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}

	existing := p.Tags
	if existing == nil {
		existing = make(map[string]string)
	}
	for k, v := range req.Tags {
		existing[k] = v
	}
	p.Tags = existing

	if aerr := serviceutil.ValidateTags(pipesTagCfg, p.Tags); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	if aerr := h.store.putPipe(ctx, p); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// UntagResource handles DELETE /tags/{ResourceArn}.
func (h *Handler) UntagResource(w http.ResponseWriter, r *http.Request) {
	arn, ok := h.tagResourceARNFromPath(w, r)
	if !ok {
		return
	}

	type untagBody struct {
		TagKeys []string `json:"TagKeys"`
	}
	var req untagBody
	tagKeys := r.URL.Query()["tagKeys"]
	if len(tagKeys) == 0 {
		if !serviceutil.DecodeJSON(w, r, &req) {
			return
		}
		tagKeys = req.TagKeys
	}

	name, aerr := h.pipeNameFromARN(arn)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}

	ctx := r.Context()
	p, aerr := h.store.getPipe(ctx, name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}

	if p.Tags != nil {
		for _, k := range tagKeys {
			delete(p.Tags, k)
		}
	}

	if aerr := h.store.putPipe(ctx, p); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// ListTagsForResource handles GET /tags/{ResourceArn}.
func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn, ok := h.tagResourceARNFromPath(w, r)
	if !ok {
		return
	}

	name, aerr := h.pipeNameFromARN(arn)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}

	ctx := r.Context()
	p, aerr := h.store.getPipe(ctx, name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}

	tags := p.Tags
	if tags == nil {
		tags = make(map[string]string)
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Tags": tags})
}

// ---- State machine ----------------------------------------------------------

// scheduleTransition advances the named pipe to nextState after the configured
// delay, but only if it is still in the transient state it was left in. Uses
// the injected clock so tests can control timing.
func (h *Handler) scheduleTransition(region, name string, nextState, from PipeState) {
	timer := h.clk.Timer(stateTransitionDelay)
	go func() {
		defer timer.Stop()
		select {
		case <-h.stopCh:
			return
		case <-timer.C:
		}
		ctx := regionContext(context.Background(), region)
		p, aerr := h.store.getPipe(ctx, name)
		if aerr != nil {
			return // pipe was deleted before the transition fired
		}
		// Only advance if nothing else moved the pipe in the meantime.
		if p.CurrentState != from {
			return
		}
		oldState := p.CurrentState
		p.CurrentState = nextState
		p.LastModifiedTime = float64(h.clk.Now().Unix())
		if aerr := h.store.putPipe(ctx, p); aerr != nil {
			log := h.log.WithRecorder(ctx)
			log.Error("pipe state transition failed",
				zap.String("pipe", name), zap.String("state", string(nextState)))
			return
		}
		h.publishStateChange(ctx, name, string(oldState), string(nextState))
	}()
}

// scheduleDeletion removes the pipe from the store after the transition delay.
func (h *Handler) scheduleDeletion(region, name string) {
	timer := h.clk.Timer(stateTransitionDelay)
	go func() {
		defer timer.Stop()
		select {
		case <-h.stopCh:
			return
		case <-timer.C:
		}
		ctx := regionContext(context.Background(), region)
		if aerr := h.store.deletePipe(ctx, name); aerr != nil {
			log := h.log.WithRecorder(ctx)
			log.Error("pipe deletion failed", zap.String("pipe", name))
			return
		}
		h.publishStateChange(ctx, name, string(PipeStateDeleting), "DELETED")
	}()
}

// publishStateChange emits the bus event the console's activity feed reads.
func (h *Handler) publishStateChange(ctx context.Context, name, oldState, newState string) {
	if h.bus == nil {
		return
	}
	h.bus.Publish(ctx, events.Event{
		Type:   events.PipesStateChanged,
		Time:   h.clk.Now(),
		Source: serviceName,
		Payload: events.PipesStateChangedPayload{
			PipeName: name,
			OldState: oldState,
			NewState: newState,
		},
	})
}

// ---- Event delivery ---------------------------------------------------------

// deliverStreamEvent is called by the event bus for every DynamoDBStream*
// event. Every RUNNING pipe whose source stream names the same table executes
// once for the record.
//
// The bus delivers one record at a time, so a DynamoDB-sourced pipe always runs
// a batch of one — BatchSize and MaximumBatchingWindowInSeconds have nothing to
// buffer. The batch is still an array, as AWS's always is.
func (h *Handler) deliverStreamEvent(ctx context.Context, evt events.Event) {
	payload, ok := evt.Payload.(events.DynamoDBStreamPayload)
	if !ok {
		return
	}

	// The bus runs subscribers on pool goroutines carrying the *publishing*
	// request's context, so this execution outlives the DynamoDB write that
	// triggered it. Detach from that request's cancellation — as SNS's fan-out
	// does — or a write that has already answered its client cancels the pipe
	// mid-delivery. The region the context pins is deliberately kept.
	ctx = context.WithoutCancel(ctx)

	pipes, aerr := h.store.listAllPipes(ctx)
	if aerr != nil {
		log := h.log.WithRecorder(ctx)
		log.Error("pipes: delivery: list pipes failed", zap.String("error", aerr.Message))
		return
	}

	for _, p := range pipes {
		if p.CurrentState != PipeStateRunning {
			continue
		}
		if tableNameFromStreamARN(p.SourceArn) != payload.Table {
			continue
		}
		record := dynamoDBStreamRecord(p.SourceArn, payload)
		// A stream record has nowhere to be redelivered from, so a failed
		// delivery is retried in place and dead-lettered rather than dropped.
		h.executeStream(ctx, p, []map[string]any{record})
	}
}

// pipeARN builds the ARN for a pipe.
func (h *Handler) pipeARN(ctx context.Context, name string) string {
	return "arn:aws:pipes:" + middleware.RegionFromContext(ctx, h.cfg.Region) +
		":" + h.cfg.AccountID + ":pipe/" + name
}

// writeError writes a JSON error response for the Pipes REST API.
func writeError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	protocol.WriteJSONError(w, r, aerr)
}

// ---- Tag handlers -----------------------------------------------------------

const (
	errTagExceededCode    = "ValidationException"
	errTagInvalidCode     = "ValidationException"
	errTagExceededMessage = "Exceeded maximum number of tags allowed."
)

var pipesTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    errTagExceededCode,
	InvalidCode:     errTagInvalidCode,
	ExceededMessage: errTagExceededMessage,
}

func (h *Handler) pipeNameFromARN(arn string) (string, *protocol.AWSError) {
	prefix := "arn:aws:pipes:"
	idx := strings.LastIndex(arn, "pipe/")
	if idx < 0 || !strings.HasPrefix(arn, prefix) {
		return "", &protocol.AWSError{
			Code: "ValidationException", Message: "Invalid ARN", HTTPStatus: http.StatusBadRequest,
		}
	}
	name := arn[idx+5:]
	if name == "" {
		return "", &protocol.AWSError{
			Code: "ValidationException", Message: "Invalid ARN", HTTPStatus: http.StatusBadRequest,
		}
	}
	return name, nil
}

func (h *Handler) tagResourceARNFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	arn := strings.TrimPrefix(r.URL.Path, "/tags/")
	arn = strings.TrimPrefix(arn, "/")
	arn, _, _ = strings.Cut(arn, "?")
	if arn == "" {
		writeError(w, r, &protocol.AWSError{
			Code: "ValidationException", Message: "Resource ARN is required", HTTPStatus: http.StatusBadRequest,
		})
		return "", false
	}
	return arn, true
}

func (h *Handler) TagResource(w http.ResponseWriter, r *http.Request) {
	arn, ok := h.tagResourceARNFromPath(w, r)
	if !ok {
		return
	}
	var req struct {
		Tags map[string]string `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	name, aerr := h.pipeNameFromARN(arn)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	if aerr := serviceutil.ValidateTags(pipesTagCfg, req.Tags); aerr != nil {
		writeError(w, r, aerr)
		return
	}
	ctx := r.Context()
	p, aerr := h.store.getPipe(ctx, name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	existing := p.Tags
	if existing == nil {
		existing = make(map[string]string)
	}
	for k, v := range req.Tags {
		existing[k] = v
	}
	p.Tags = existing
	if aerr := serviceutil.ValidateTags(pipesTagCfg, p.Tags); aerr != nil {
		writeError(w, r, aerr)
		return
	}
	if aerr := h.store.putPipe(ctx, p); aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) UntagResource(w http.ResponseWriter, r *http.Request) {
	arn, ok := h.tagResourceARNFromPath(w, r)
	if !ok {
		return
	}
	type untagBody struct {
		TagKeys []string `json:"TagKeys"`
	}
	var req untagBody
	tagKeys := r.URL.Query()["tagKeys"]
	if len(tagKeys) == 0 {
		if !serviceutil.DecodeJSON(w, r, &req) {
			return
		}
		tagKeys = req.TagKeys
	}
	name, aerr := h.pipeNameFromARN(arn)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	ctx := r.Context()
	p, aerr := h.store.getPipe(ctx, name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	if p.Tags != nil {
		for _, k := range tagKeys {
			delete(p.Tags, k)
		}
	}
	if aerr := h.store.putPipe(ctx, p); aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn, ok := h.tagResourceARNFromPath(w, r)
	if !ok {
		return
	}
	name, aerr := h.pipeNameFromARN(arn)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	ctx := r.Context()
	p, aerr := h.store.getPipe(ctx, name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	tags := p.Tags
	if tags == nil {
		tags = make(map[string]string)
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Tags": tags})
}
