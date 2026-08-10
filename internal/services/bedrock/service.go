// Package bedrock provides a minimal emulation of Amazon Bedrock Runtime.
//
// Implemented operations: InvokeModel and Converse, each registered at the
// method and URI the pinned model binds it to. Neither runs a model: both are
// answered from a canned response, which is enough for an application wired to
// Bedrock to connect, route and parse a reply instead of failing at the socket.
// capabilities_dev.go records what that costs per operation.
//
// The two operations differ in kind, not only in URL suffix. Converse is an
// ordinary restJson1 operation whose request and response are fully modeled by
// AWS. InvokeModel is not: its request and response bodies are opaque
// @httpPayload blobs whose format belongs to the *model*, with Content-Type and
// Accept carrying the negotiation. Overcast reproduces the operation's own
// contract and cannot reproduce a model's payload — see InvokeModel.
package bedrock

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName = "bedrock"

	// restJSONContentType is what a restJson1 operation answers with, and for
	// InvokeModel it is a modeled response member rather than a convention:
	// InvokeModelResponse binds contentType to the Content-Type header and
	// marks it @required, so the caller reads it to decide how to decode the
	// payload. Overcast's payload is JSON, so it says so — it does not echo the
	// request's Accept, which would claim a format it cannot produce.
	restJSONContentType = "application/json"

	// cannedText is the assistant text Converse answers with. It names itself
	// on purpose: a caller that reaches this string has connected to an
	// emulator, not to a model.
	cannedText = "This is a canned response from the Overcast Bedrock emulator."
)

// Service implements router.Service for Bedrock Runtime.
//
// It holds nothing. Bedrock performs no inference and stores no resources, so
// there is no state to keep and New's store, logger and clock are discarded —
// as config.ServiceOverrideIneffective already documents for the store.
type Service struct{}

// New returns a configured Bedrock Runtime Service.
func New(_ *config.Config, _ state.Store, _ *zap.Logger, _ clock.Clock) *Service {
	return &Service{}
}

func (s *Service) Name() string { return serviceName }

// RegisterRoutes registers the REST endpoints for Bedrock Runtime.
//
// modelId is a non-greedy @httpLabel, so it is one path segment even when it
// carries an inference-profile or provisioned-throughput ARN: the SDK
// percent-encodes the ARN's separators and chi matches on the raw path. A
// greedy route would be wrong twice over — it would not match what a client
// sends, and it would swallow the streaming and token-counting siblings
// (invoke-with-response-stream, converse-stream, count-tokens), answering them
// with a shape their callers cannot parse instead of an honest 501.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Post("/model/{modelId}/invoke", s.InvokeModel)
	r.Post("/model/{modelId}/converse", s.Converse)
}

// PathPrefixes satisfies router.PathPrefixService.
func (s *Service) PathPrefixes() []string { return []string{"/model"} }

// ─── Handlers ─────────────────────────────────────────────────

// InvokeModel answers POST /model/{modelId}/invoke.
//
// The request body is an opaque blob in the model's own format — Anthropic's
// Messages document, Titan's {"inputText":…}, Llama's {"prompt":…} — so it is
// not parsed. The response body is likewise the model's, and Overcast runs no
// model: any family-specific shape it picked would be wrong for every other
// family, so it answers with one self-describing field rather than a shape that
// looks real and is not. Everything the *operation* binds is reproduced: the
// required payload in, a payload and its Content-Type out.
func (s *Service) InvokeModel(w http.ResponseWriter, r *http.Request) {
	// body is @required on InvokeModelRequest, and presence is the only part of
	// it Overcast can check. Reading one byte establishes that without
	// buffering a prompt nothing will read.
	var probe [1]byte
	if n, _ := io.ReadFull(r.Body, probe[:]); n == 0 {
		protocol.WriteJSONError(w, r, validationError("The request must contain a body: InvokeModel's payload is required."))
		return
	}

	protocol.WriteAWSJSON(w, r, http.StatusOK, invokeModelPayload{
		OvercastEmulator: "No model was invoked. InvokeModel's response body is the model's own format, " +
			"which Overcast does not emulate; use Converse for a response shape AWS defines.",
	}, restJSONContentType)
}

// Converse answers POST /model/{modelId}/converse.
//
// Unlike InvokeModel this shape is AWS's, so the response carries every member
// ConverseResponse marks @required and nothing that is not modeled.
func (s *Service) Converse(w http.ResponseWriter, r *http.Request) {
	protocol.WriteAWSJSON(w, r, http.StatusOK, converseResponse{
		Output: converseOutput{
			Message: message{
				Role:    roleAssistant,
				Content: []contentBlock{{Text: cannedText}},
			},
		},
		StopReason: stopReasonEndTurn,
		// No inference ran, so nothing was tokenised and nothing was waited on.
		// Zero is the truthful reading of all four, and it keeps totalTokens
		// consistent with its two components; inventing plausible counts would
		// make the emulator's usage figures look like measurements.
		Usage:   tokenUsage{},
		Metrics: converseMetrics{},
	}, restJSONContentType)
}

// validationError builds the 400 AWS answers when a request fails the
// constraints its input shape declares.
func validationError(detail string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationException",
		Message:    detail,
		HTTPStatus: http.StatusBadRequest,
	}
}

// ─── Wire shapes ──────────────────────────────────────────────

// invokeModelPayload is the JSON document InvokeModel answers with.
//
// It deliberately has no counterpart in any AWS shape: InvokeModelResponse
// binds body as an opaque blob, so the schema inside it is the model's and
// there is nothing here for Overcast to be faithful to. The one field is named
// so that it cannot be mistaken for a model's own.
type invokeModelPayload struct {
	OvercastEmulator string `json:"overcastEmulator"`
}

// ConversationRole and StopReason are Smithy enums whose wire values are
// lower-case, unlike the member names the model spells in upper case.
const (
	roleAssistant     = "assistant"
	stopReasonEndTurn = "end_turn"
)

type converseResponse struct {
	Output     converseOutput  `json:"output"`
	StopReason string          `json:"stopReason"`
	Usage      tokenUsage      `json:"usage"`
	Metrics    converseMetrics `json:"metrics"`
}

// converseOutput is a union with a single modeled member here; restJson1
// encodes a union as an object carrying exactly one of its members.
type converseOutput struct {
	Message message `json:"message"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

// contentBlock is also a union. Only its text member is produced.
type contentBlock struct {
	Text string `json:"text"`
}

// tokenUsage marks inputTokens, outputTokens and totalTokens @required, so all
// three are always emitted rather than omitted when zero.
type tokenUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

type converseMetrics struct {
	LatencyMs int64 `json:"latencyMs"`
}
