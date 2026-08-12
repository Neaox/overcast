package lambda

// handler_event_invoke.go — the FunctionEventInvokeConfig family.
//
// Five operations on one resource, all under AWS's /2019-09-25 base:
//
//	PUT    …/event-invoke-config       PutFunctionEventInvokeConfig     (overwrite)
//	POST   …/event-invoke-config       UpdateFunctionEventInvokeConfig  (merge)
//	GET    …/event-invoke-config       GetFunctionEventInvokeConfig
//	DELETE …/event-invoke-config       DeleteFunctionEventInvokeConfig  (204)
//	GET    …/event-invoke-config/list  ListFunctionEventInvokeConfigs
//
// Put and Update differ in exactly one way, and it is the reason both exist:
// "If you exclude any settings, they are removed" for Put, against Update
// leaving omitted members alone. Everything else about them is identical, so
// they share a body and a validator and diverge on one flag.
//
// What the settings *do* lives elsewhere: invokeAsync reads the retry policy
// (handler_functions.go) and dead_letter.go delivers the destinations.

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/eventtarget"
	"github.com/Neaox/overcast/internal/protocol"
)

// AWS's modeled ranges for the two limits.
const (
	minRetryAttempts     = 0
	maxRetryAttempts     = 2
	minEventAgeInSeconds = 60
	maxEventAgeInSeconds = 21600
)

// eventInvokeConfigRequest is the body of both Put and Update. Every member is
// a pointer so an omitted one is distinguishable from an explicit zero, which
// is what lets Update leave omissions alone and Put clear them.
type eventInvokeConfigRequest struct {
	MaximumRetryAttempts     *int                   `json:"MaximumRetryAttempts"`
	MaximumEventAgeInSeconds *int                   `json:"MaximumEventAgeInSeconds"`
	DestinationConfig        *destinationConfigWire `json:"DestinationConfig"`
}

type destinationConfigWire struct {
	OnSuccess *destinationWire `json:"OnSuccess,omitempty"`
	OnFailure *destinationWire `json:"OnFailure,omitempty"`
}

type destinationWire struct {
	Destination string `json:"Destination,omitempty"`
}

// eventInvokeConfigResponse is AWS's FunctionEventInvokeConfig shape.
//
// LastModified is Unix seconds as a number, which is this resource's own
// convention — FunctionConfiguration.LastModified is an RFC 3339 string, and
// an SDK decodes the two differently.
type eventInvokeConfigResponse struct {
	LastModified             int64                  `json:"LastModified"`
	FunctionArn              string                 `json:"FunctionArn"`
	MaximumRetryAttempts     *int                   `json:"MaximumRetryAttempts,omitempty"`
	MaximumEventAgeInSeconds *int                   `json:"MaximumEventAgeInSeconds,omitempty"`
	DestinationConfig        *destinationConfigWire `json:"DestinationConfig,omitempty"`
}

type listEventInvokeConfigsResponse struct {
	FunctionEventInvokeConfigs []*eventInvokeConfigResponse `json:"FunctionEventInvokeConfigs"`
	NextMarker                 *string                      `json:"NextMarker,omitempty"`
}

// PutFunctionEventInvokeConfig handles PUT /2019-09-25/functions/{name}/event-invoke-config.
func (h *Handler) PutFunctionEventInvokeConfig(w http.ResponseWriter, r *http.Request) {
	h.writeEventInvokeConfig(w, r, false)
}

// UpdateFunctionEventInvokeConfig handles POST on the same path. It merges
// rather than replaces.
func (h *Handler) UpdateFunctionEventInvokeConfig(w http.ResponseWriter, r *http.Request) {
	h.writeEventInvokeConfig(w, r, true)
}

// writeEventInvokeConfig is Put and Update. merge selects which one: Update
// keeps what the request omitted, Put drops it.
func (h *Handler) writeEventInvokeConfig(w http.ResponseWriter, r *http.Request, merge bool) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	var req eventInvokeConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}
	if aerr := validateEventInvokeConfig(&req); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// An S3 on-failure destination is modeled by AWS and not emulated here, so
	// it is refused rather than stored: accepting it would promise that every
	// failed invocation is archived to the bucket, and the caller would find
	// the bucket empty with nothing to explain it. Validation runs first so a
	// request that is invalid on AWS is still reported as invalid.
	if isS3Destination(req.DestinationConfig.onFailure()) {
		log.Debug("event invoke config: S3 destination refused", zap.String("function", name))
		protocol.NotImplementedJSON(w, r)
		return
	}

	fn, qualifier, aerr := h.resolveEventInvokeTarget(r, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	current, aerr := h.ls.getEventInvokeConfig(ctx, fn.Name, qualifier)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	cfg := &EventInvokeConfig{
		FunctionName: fn.Name,
		Qualifier:    qualifier,
		FunctionArn:  eventInvokeARN(fn, qualifier),
	}
	if merge && current != nil {
		cfg.MaximumRetryAttempts = current.MaximumRetryAttempts
		cfg.MaximumEventAgeInSeconds = current.MaximumEventAgeInSeconds
		cfg.DestinationConfig = current.DestinationConfig
	}
	if req.MaximumRetryAttempts != nil {
		cfg.MaximumRetryAttempts = req.MaximumRetryAttempts
	}
	if req.MaximumEventAgeInSeconds != nil {
		cfg.MaximumEventAgeInSeconds = req.MaximumEventAgeInSeconds
	}
	if req.DestinationConfig != nil {
		cfg.DestinationConfig = req.DestinationConfig.domain()
	}
	cfg.LastModifiedUnix = h.clk.Now().Unix()

	if aerr := h.ls.putEventInvokeConfig(ctx, cfg); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	log.Debug("event invoke config written",
		zap.String("function", fn.Name), zap.String("qualifier", qualifier), zap.Bool("merge", merge))
	protocol.WriteRESTJSON(w, r, http.StatusOK, eventInvokeConfigToWire(cfg))
}

// GetFunctionEventInvokeConfig handles GET /2019-09-25/functions/{name}/event-invoke-config.
func (h *Handler) GetFunctionEventInvokeConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	fn, qualifier, aerr := h.resolveEventInvokeTarget(r, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	cfg, aerr := h.ls.getEventInvokeConfig(r.Context(), fn.Name, qualifier)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if cfg == nil {
		protocol.WriteJSONError(w, r, errNoEventInvokeConfig(fn.ARN, qualifier))
		return
	}
	protocol.WriteRESTJSON(w, r, http.StatusOK, eventInvokeConfigToWire(cfg))
}

// DeleteFunctionEventInvokeConfig handles DELETE on the same path, answering
// 204 with no body.
func (h *Handler) DeleteFunctionEventInvokeConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	fn, qualifier, aerr := h.resolveEventInvokeTarget(r, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	cfg, aerr := h.ls.getEventInvokeConfig(r.Context(), fn.Name, qualifier)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// Deleting a configuration that is not there is a 404 rather than a no-op:
	// the resource this operation names does not exist, which is what the
	// modeled ResourceNotFoundException is for.
	if cfg == nil {
		protocol.WriteJSONError(w, r, errNoEventInvokeConfig(fn.ARN, qualifier))
		return
	}
	if aerr := h.ls.deleteEventInvokeConfig(r.Context(), fn.Name, qualifier); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListFunctionEventInvokeConfigs handles
// GET /2019-09-25/functions/{name}/event-invoke-config/list.
//
// Pagination is a single page: MaxItems is validated so a caller that asks for
// an illegal one is told, but NextMarker is never returned, matching how the
// other list operations in this service behave at emulator scale.
func (h *Handler) ListFunctionEventInvokeConfigs(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if raw := r.URL.Query().Get("MaxItems"); raw != "" {
		maxItems, err := strconv.Atoi(raw)
		if err != nil || maxItems < 1 || maxItems > 50 {
			protocol.WriteJSONError(w, r, smithyIntegerConstraint("maxItems", maxItemsOrZero(raw), 1, 50))
			return
		}
	}
	// The list is per function, not per qualifier, so it resolves the function
	// without a qualifier even when one is supplied.
	fn, aerr := h.requireFunctionForEventInvoke(r, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	configs, aerr := h.ls.listEventInvokeConfigs(r.Context(), fn.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	out := make([]*eventInvokeConfigResponse, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, eventInvokeConfigToWire(cfg))
	}
	protocol.WriteRESTJSON(w, r, http.StatusOK, listEventInvokeConfigsResponse{FunctionEventInvokeConfigs: out})
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// resolveEventInvokeTarget finds the function and settles the qualifier, which
// may arrive either in the Qualifier query parameter or appended to the name as
// `my-function:live` — AWS accepts both spellings for this family.
func (h *Handler) resolveEventInvokeTarget(r *http.Request, name string) (*Function, string, *protocol.AWSError) {
	bare, embedded := splitFunctionIdentifier(name, r.URL.Query().Get("Qualifier"))
	fn, aerr := h.requireFunctionForEventInvoke(r, bare)
	if aerr != nil {
		return nil, "", aerr
	}
	// $LATEST and the unqualified function are the same resource on AWS, so
	// they must not become two rows.
	if embedded == "$LATEST" {
		embedded = ""
	}
	return fn, embedded, nil
}

// requireFunctionForEventInvoke loads the function, turning absence into the
// ResourceNotFoundException every operation in this family models.
func (h *Handler) requireFunctionForEventInvoke(r *http.Request, name string) (*Function, *protocol.AWSError) {
	bare, _ := splitFunctionIdentifier(name, "")
	fn, aerr := h.ls.getFunction(r.Context(), bare)
	if aerr != nil {
		return nil, aerr
	}
	if fn == nil {
		return nil, lambdaFunctionNotFound(bare)
	}
	return fn, nil
}

// errNoEventInvokeConfig is what AWS answers for a function that has one no
// configuration: the resource named by the request does not exist.
func errNoEventInvokeConfig(functionARN, qualifier string) *protocol.AWSError {
	target := functionARN
	if qualifier != "" {
		target += ":" + qualifier
	}
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    "The function " + target + " doesn't have an EventInvokeConfig",
		HTTPStatus: http.StatusNotFound,
	}
}

// eventInvokeARN names exactly what was configured: the bare function ARN when
// the configuration is unqualified, and the qualified form otherwise. It cannot
// use qualifiedFunctionARN, which always appends — provisioned concurrency, its
// only other caller, is never unqualified.
func eventInvokeARN(fn *Function, qualifier string) string {
	if qualifier == "" {
		return fn.ARN
	}
	return qualifiedFunctionARN(fn, qualifier)
}

// maxItemsOrZero renders an unparseable MaxItems as 0 for the error message,
// which is what the constraint text needs and never what a valid value is.
func maxItemsOrZero(raw string) int {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func (c *destinationConfigWire) onFailure() string {
	if c == nil || c.OnFailure == nil {
		return ""
	}
	return c.OnFailure.Destination
}

func (c *destinationConfigWire) onSuccess() string {
	if c == nil || c.OnSuccess == nil {
		return ""
	}
	return c.OnSuccess.Destination
}

// domain converts the wire shape to the stored one, dropping a block whose
// sides are both empty so an explicitly empty DestinationConfig clears rather
// than storing two empty strings.
func (c *destinationConfigWire) domain() *DestinationConfig {
	if c == nil {
		return nil
	}
	out := &DestinationConfig{}
	if c.OnSuccess != nil && c.OnSuccess.Destination != "" {
		out.OnSuccess = &OnSuccess{Destination: c.OnSuccess.Destination}
	}
	if c.OnFailure != nil && c.OnFailure.Destination != "" {
		out.OnFailure = &OnFailure{Destination: c.OnFailure.Destination}
	}
	if out.OnSuccess == nil && out.OnFailure == nil {
		return nil
	}
	return out
}

func eventInvokeConfigToWire(cfg *EventInvokeConfig) *eventInvokeConfigResponse {
	out := &eventInvokeConfigResponse{
		LastModified:             cfg.LastModifiedUnix,
		FunctionArn:              cfg.FunctionArn,
		MaximumRetryAttempts:     cfg.MaximumRetryAttempts,
		MaximumEventAgeInSeconds: cfg.MaximumEventAgeInSeconds,
	}
	if cfg.DestinationConfig != nil {
		wire := &destinationConfigWire{}
		if cfg.DestinationConfig.OnSuccess != nil {
			wire.OnSuccess = &destinationWire{Destination: cfg.DestinationConfig.OnSuccess.Destination}
		}
		if cfg.DestinationConfig.OnFailure != nil {
			wire.OnFailure = &destinationWire{Destination: cfg.DestinationConfig.OnFailure.Destination}
		}
		out.DestinationConfig = wire
	}
	return out
}

// validateEventInvokeConfig enforces AWS's modeled ranges and the destination
// rules. Being laxer than AWS here is the dangerous direction: a retry policy
// that is accepted locally and rejected by the account fails in production
// rather than on the developer's machine.
func validateEventInvokeConfig(req *eventInvokeConfigRequest) *protocol.AWSError {
	if req.MaximumRetryAttempts != nil {
		if v := *req.MaximumRetryAttempts; v < minRetryAttempts || v > maxRetryAttempts {
			return smithyIntegerConstraint("maximumRetryAttempts", v, minRetryAttempts, maxRetryAttempts)
		}
	}
	if req.MaximumEventAgeInSeconds != nil {
		if v := *req.MaximumEventAgeInSeconds; v < minEventAgeInSeconds || v > maxEventAgeInSeconds {
			return smithyIntegerConstraint("maximumEventAgeInSeconds", v, minEventAgeInSeconds, maxEventAgeInSeconds)
		}
	}
	// AWS: "S3 buckets are supported only for on-failure destinations." An S3
	// on-success destination is invalid on AWS, so it is invalid here — that is
	// fidelity, not an emulator limit, and it gets a 400 rather than the 501
	// the on-failure case gets.
	if isS3Destination(req.DestinationConfig.onSuccess()) {
		return lambdaInvalidParameter("1 validation error detected: Value '" +
			req.DestinationConfig.onSuccess() +
			"' at 'destinationConfig.onSuccess.destination' failed to satisfy constraint: " +
			"S3 buckets are supported only for on-failure destinations")
	}
	if aerr := validateDestinationARN(req.DestinationConfig.onSuccess(), "onSuccess"); aerr != nil {
		return aerr
	}
	return validateDestinationARN(req.DestinationConfig.onFailure(), "onFailure")
}

// validateDestinationARN rejects a destination whose ARN names a service that
// cannot receive an invocation record. AWS allows a function, queue, topic,
// bucket or event bus; anything else is a caller error there and here.
func validateDestinationARN(arn, side string) *protocol.AWSError {
	if arn == "" || isS3Destination(arn) {
		return nil
	}
	kind, err := eventtarget.Classify(arn)
	if err != nil || !destinationKindAllowed(kind) {
		return lambdaInvalidParameter("1 validation error detected: Value '" + arn +
			"' at 'destinationConfig." + side + ".destination' failed to satisfy constraint: " +
			"Member must be the ARN of an SQS queue, SNS topic, Lambda function, or EventBridge event bus")
	}
	return nil
}

// destinationKindAllowed reports the target types AWS accepts for an
// asynchronous invocation destination, minus S3 which is handled separately
// because it is refused rather than rejected.
func destinationKindAllowed(kind eventtarget.Kind) bool {
	switch kind { //nolint:exhaustive // the default arm is the answer for every other kind: AWS allows these four and no more
	case eventtarget.KindSQS, eventtarget.KindSNS, eventtarget.KindLambda, eventtarget.KindEventBus:
		return true
	default:
		return false
	}
}

// isS3Destination reports an S3 bucket ARN, which has no region or account
// segment and so does not classify through eventtarget.
func isS3Destination(arn string) bool {
	return len(arn) >= len("arn:aws:s3:") && arn[:len("arn:aws:s3:")] == "arn:aws:s3:"
}
