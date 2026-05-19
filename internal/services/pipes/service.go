// Package pipes emulates the AWS EventBridge Pipes service.
//
// EventBridge Pipes connect a source (e.g. a DynamoDB stream) to a target
// (e.g. an SQS queue) with optional filtering and enrichment. In overcast,
// only the DynamoDB Streams → SQS path is implemented.
//
// REST API (follows the real AWS EventBridge Pipes path prefix):
//
//	POST   /v1/pipes/{name}   — CreatePipe
//	GET    /v1/pipes/{name}   — DescribePipe
//	DELETE /v1/pipes/{name}   — DeletePipe
//	PATCH  /v1/pipes/{name}   — UpdatePipe (501)
//	GET    /v1/pipes          — ListPipes
//
// State machine:
//
//	CreatePipe  → CREATING → (timer) → RUNNING
//	DeletePipe  → DELETING → (timer) → (removed from store)
//
// Delivery: on every DynamoDBStream* bus event, each RUNNING pipe whose source
// table matches the event table enqueues one SQS message per record with the
// standard EventBridge Pipes / Lambda ESM DynamoDB event format.
package pipes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// stateTransitionDelay is how long after CreatePipe / DeletePipe before the
// state machine advances. Short enough to be invisible in real usage;
// observable in tests via srv.Clock.Add(stateTransitionDelay).
const stateTransitionDelay = 50 * time.Millisecond

const serviceName = "pipes"

// Service implements router.Service for EventBridge Pipes.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured Pipes Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   store,
		log:     log,
		handler: newHandler(cfg, store, log, clk),
	}
}

// Name returns the service identifier used in config and logging.
func (s *Service) Name() string { return serviceName }

// TargetPrefix returns "" — pipes uses REST routing, not X-Amz-Target dispatch.
func (s *Service) TargetPrefix() string { return "" }

// RegisterRoutes mounts the Pipes REST API under /v1/pipes.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Route("/v1/pipes", func(r chi.Router) {
		r.Get("/", s.handler.ListPipes)
		r.Post("/{name}", s.handler.CreatePipe)
		r.Get("/{name}", s.handler.DescribePipe)
		r.Delete("/{name}", s.handler.DeletePipe)
		r.Patch("/{name}", s.handler.UpdatePipe)
	})
}

// InitDelivery wires the event bus and SQS enqueuer for stream delivery.
// Must be called after both the DynamoDB service and SQS service are running.
// Safe to call multiple times (idempotent — each call adds a new subscriber,
// so call exactly once from the router).
func (s *Service) InitDelivery(bus *events.Bus, enqueuer events.MessageEnqueuer) {
	s.handler.enqueuer = enqueuer
	s.handler.bus = bus
	bus.Subscribe(events.DynamoDBStreamInsert, s.handler.deliverStreamEvent)
	bus.Subscribe(events.DynamoDBStreamModify, s.handler.deliverStreamEvent)
	bus.Subscribe(events.DynamoDBStreamRemove, s.handler.deliverStreamEvent)
}

// ---- Handler ----------------------------------------------------------------

// Handler holds Pipes handler dependencies.
type Handler struct {
	cfg      *config.Config
	store    *pipesStore
	log      *serviceutil.ServiceLogger
	clk      clock.Clock
	enqueuer events.MessageEnqueuer // injected by InitDelivery
	bus      *events.Bus            // injected by InitDelivery; nil until then
}

func newHandler(cfg *config.Config, st state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	return &Handler{cfg: cfg, store: newPipesStore(st, cfg.Region), log: log, clk: clk}
}

// ---- Request / response types -----------------------------------------------

type createPipeRequest struct {
	// Source and Target match the real AWS Pipes API wire format.
	Source string `json:"Source"`
	Target string `json:"Target"`
}

type updatePipeRequest struct {
	// DesiredState optionally changes the desired lifecycle state (RUNNING or STOPPED).
	DesiredState PipeState `json:"DesiredState,omitempty"`
	// Description is an optional free-text description.
	Description *string `json:"Description,omitempty"`
}

type listPipesResponse struct {
	Pipes     []*Pipe `json:"Pipes"`
	NextToken string  `json:"NextToken,omitempty"`
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
	p := &Pipe{
		Name:             name,
		Arn:              h.pipeARN(ctx, name),
		SourceArn:        req.Source,
		TargetArn:        req.Target,
		SourceName:       tableNameFromStreamARN(req.Source),
		TargetName:       queueNameFromARN(req.Target),
		CurrentState:     PipeStateCreating,
		DesiredState:     PipeStateRunning,
		CreationTime:     float64(now.Unix()),
		LastModifiedTime: float64(now.Unix()),
	}

	if aerr := h.store.putPipe(ctx, p); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	h.log.Info("pipe created", zap.String("pipe", name), zap.String("source", req.Source), zap.String("target", req.Target))

	// Advance state CREATING → RUNNING asynchronously.
	h.scheduleStateTransition(h.store.region(ctx), name, PipeStateRunning)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(p)
}

// DescribePipe implements GET /v1/pipes/{name}.
func (h *Handler) DescribePipe(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	p, aerr := h.store.getPipe(r.Context(), name)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
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

	h.log.Info("pipe deleting", zap.String("pipe", name))

	// Remove from store asynchronously after the transition delay.
	h.scheduleDeletion(h.store.region(ctx), name)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// UpdatePipe implements PATCH /v1/pipes/{name}.
// Allows updating DesiredState (RUNNING or STOPPED) and Description.
// The pipe transitions asynchronously when DesiredState changes.
func (h *Handler) UpdatePipe(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	var req updatePipeRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
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
			HTTPStatus: 409,
		})
		return
	}

	now := h.clk.Now()
	changed := false

	if req.Description != nil {
		p.Description = *req.Description
		changed = true
	}

	if req.DesiredState != "" && req.DesiredState != p.DesiredState {
		switch req.DesiredState { //nolint:exhaustive // only RUNNING/STOPPED are valid transitions
		case PipeStateRunning, PipeStateStopped:
			// valid transitions
		default:
			writeError(w, r, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    "DesiredState must be RUNNING or STOPPED.",
				HTTPStatus: 400,
			})
			return
		}
		p.DesiredState = req.DesiredState
		changed = true
		// Choose the transient state based on the target.
		if req.DesiredState == PipeStateRunning {
			p.CurrentState = PipeStateStarting
		} else {
			p.CurrentState = PipeStateStopping
		}
	}

	if !changed {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p)
		return
	}

	p.LastModifiedTime = float64(now.Unix())
	if aerr := h.store.putPipe(ctx, p); aerr != nil {
		writeError(w, r, aerr)
		return
	}

	h.log.Info("pipe updated", zap.String("pipe", name),
		zap.String("desiredState", string(p.DesiredState)))

	// Advance to the desired state asynchronously.
	h.scheduleUpdateTransition(h.store.region(ctx), name, p.DesiredState)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// ListPipes implements GET /v1/pipes.
func (h *Handler) ListPipes(w http.ResponseWriter, r *http.Request) {
	pipes, aerr := h.store.listPipes(r.Context())
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(listPipesResponse{Pipes: pipes})
}

// ---- State machine ----------------------------------------------------------

// scheduleStateTransition advances the named pipe to nextState after the
// configured delay. Uses the injected clock so tests can control timing.
func (h *Handler) scheduleStateTransition(region, name string, nextState PipeState) {
	timer := h.clk.Timer(stateTransitionDelay)
	go func() {
		defer timer.Stop()
		<-timer.C
		ctx := middleware.ContextWithRegion(context.Background(), region)
		p, aerr := h.store.getPipe(ctx, name)
		if aerr != nil {
			return // pipe was deleted before transition fired
		}
		// Only advance if the pipe hasn't been deleted / stopped externally.
		if p.CurrentState != PipeStateCreating {
			return
		}
		oldState := p.CurrentState
		p.CurrentState = nextState
		p.LastModifiedTime = float64(h.clk.Now().Unix())
		if aerr := h.store.putPipe(ctx, p); aerr != nil {
			h.log.Error("pipe state transition failed", zap.String("pipe", name), zap.String("state", string(nextState)))
			return
		}
		if h.bus != nil {
			h.bus.Publish(ctx, events.Event{
				Type:   events.PipesStateChanged,
				Time:   h.clk.Now(),
				Source: "pipes",
				Payload: events.PipesStateChangedPayload{
					PipeName: name,
					OldState: string(oldState),
					NewState: string(nextState),
				},
			})
		}
	}()
}

// scheduleUpdateTransition advances a pipe to its desired state after UpdatePipe.
// Uses STARTING/STOPPING transient states matching the real AWS Pipes state machine.
func (h *Handler) scheduleUpdateTransition(region, name string, desiredState PipeState) {
	timer := h.clk.Timer(stateTransitionDelay)
	go func() {
		defer timer.Stop()
		<-timer.C
		ctx := middleware.ContextWithRegion(context.Background(), region)
		p, aerr := h.store.getPipe(ctx, name)
		if aerr != nil {
			return
		}
		// Only advance if still in the transient state.
		if p.CurrentState != PipeStateStarting && p.CurrentState != PipeStateStopping {
			return
		}
		oldState := p.CurrentState
		p.CurrentState = desiredState
		p.LastModifiedTime = float64(h.clk.Now().Unix())
		if aerr := h.store.putPipe(ctx, p); aerr != nil {
			h.log.Error("pipe update transition failed", zap.String("pipe", name))
			return
		}
		if h.bus != nil {
			h.bus.Publish(ctx, events.Event{
				Type:   events.PipesStateChanged,
				Time:   h.clk.Now(),
				Source: "pipes",
				Payload: events.PipesStateChangedPayload{
					PipeName: name,
					OldState: string(oldState),
					NewState: string(desiredState),
				},
			})
		}
	}()
}

// scheduleDeletion removes the pipe from the store after the transition delay.
func (h *Handler) scheduleDeletion(region, name string) {
	timer := h.clk.Timer(stateTransitionDelay)
	go func() {
		defer timer.Stop()
		<-timer.C
		ctx := middleware.ContextWithRegion(context.Background(), region)
		if aerr := h.store.deletePipe(ctx, name); aerr != nil {
			h.log.Error("pipe deletion failed", zap.String("pipe", name))
			return
		}
		if h.bus != nil {
			h.bus.Publish(ctx, events.Event{
				Type:   events.PipesStateChanged,
				Time:   h.clk.Now(),
				Source: "pipes",
				Payload: events.PipesStateChangedPayload{
					PipeName: name,
					OldState: string(PipeStateDeleting),
					NewState: "DELETED",
				},
			})
		}
	}()
}

// ---- Event delivery ---------------------------------------------------------

// deliverStreamEvent is called by the event bus for every DynamoDBStream* event.
// It finds all RUNNING pipes whose source table matches, then enqueues one SQS
// message per event onto the target queue.
func (h *Handler) deliverStreamEvent(ctx context.Context, evt events.Event) {
	if h.enqueuer == nil {
		return
	}
	payload, ok := evt.Payload.(events.DynamoDBStreamPayload)
	if !ok {
		return
	}

	pipes, aerr := h.store.listAllPipes(ctx)
	if aerr != nil {
		h.log.Error("delivery: list pipes failed", zap.Error(fmt.Errorf("%s", aerr.Message)))
		return
	}

	for _, p := range pipes {
		if p.CurrentState != PipeStateRunning {
			continue
		}
		// Match source: pipe SourceArn must reference the same table.
		if tableNameFromStreamARN(p.SourceArn) != payload.Table {
			continue
		}

		body := buildSQSMessageBody(p.SourceArn, evt, payload)
		queueName := queueNameFromARN(p.TargetArn)
		msgID := uuid.New().String()
		if err := h.enqueuer.EnqueueRaw(ctx, queueName, body); err != nil {
			h.log.Error("delivery: enqueue failed",
				zap.String("pipe", p.Name),
				zap.String("queue", queueName),
				zap.Error(err),
			)
			continue
		}
		if h.bus != nil {
			h.bus.Publish(ctx, events.Event{
				Type:   events.PipesDelivered,
				Time:   h.clk.Now(),
				Source: "pipes",
				Payload: events.PipesDeliveryPayload{
					PipeName:    p.Name,
					SourceTable: tableNameFromStreamARN(p.SourceArn),
					TargetQueue: queueName,
					EventName:   payload.EventName,
					MessageID:   msgID,
				},
			})
		}
	}
}

// buildSQSMessageBody formats a DynamoDB stream event as the standard
// EventBridge Pipes / Lambda ESM DynamoDB record JSON.
func buildSQSMessageBody(sourceARN string, evt events.Event, payload events.DynamoDBStreamPayload) string {
	type dynamoDBRecord struct {
		ApproximateCreationDateTime float64 `json:"ApproximateCreationDateTime"`
		Keys                        any     `json:"Keys,omitempty"`
		NewImage                    any     `json:"NewImage,omitempty"`
		OldImage                    any     `json:"OldImage,omitempty"`
		SequenceNumber              string  `json:"SequenceNumber"`
		StreamViewType              string  `json:"StreamViewType"`
	}

	type record struct {
		EventID        string         `json:"eventID"`
		EventVersion   string         `json:"eventVersion"`
		EventSource    string         `json:"eventSource"`
		EventSourceARN string         `json:"eventSourceARN"`
		AwsRegion      string         `json:"awsRegion"`
		EventName      string         `json:"eventName"`
		DynamoDB       dynamoDBRecord `json:"dynamodb"`
	}

	// Derive region from the source ARN (arn:aws:dynamodb:REGION:...).
	region := "us-east-1"
	parts := splitARN(sourceARN)
	if len(parts) >= 4 {
		region = parts[3]
	}

	// SequenceNumber zero-padded to 21 digits, matching real DynamoDB Streams format.
	seqNum := fmt.Sprintf("%021d", payload.SequenceNumber)

	r := record{
		EventID:        seqNum,
		EventVersion:   "1.1",
		EventSource:    "aws:dynamodb",
		EventSourceARN: sourceARN,
		AwsRegion:      region,
		EventName:      payload.EventName,
		DynamoDB: dynamoDBRecord{
			ApproximateCreationDateTime: float64(payload.CreatedAt) / 1000.0,
			Keys:                        payload.Keys,
			NewImage:                    payload.NewImage,
			OldImage:                    payload.OldImage,
			SequenceNumber:              seqNum,
			StreamViewType:              "NEW_AND_OLD_IMAGES",
		},
	}

	b, _ := json.Marshal(r)
	return string(b)
}

// splitARN splits an ARN string on ":" for region extraction.
func splitARN(arn string) []string {
	result := make([]string, 0, 6)
	start := 0
	for i, c := range arn {
		if c == ':' {
			result = append(result, arn[start:i])
			start = i + 1
			if len(result) == 5 {
				break
			}
		}
	}
	if start < len(arn) {
		result = append(result, arn[start:])
	}
	return result
}

// pipeARN builds the ARN for a pipe.
func (h *Handler) pipeARN(ctx context.Context, name string) string {
	return fmt.Sprintf("arn:aws:pipes:%s:%s:pipe/%s", middleware.RegionFromContext(ctx, h.cfg.Region), h.cfg.AccountID, name)
}

// writeError writes a JSON error response for the Pipes REST API.
func writeError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	protocol.WriteJSONError(w, r, aerr)
}
