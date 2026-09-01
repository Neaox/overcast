package lambda

// handler_esm.go — Lambda event source mapping (ESM) handlers.
//
// Implements:
//   - POST   /2015-03-31/event-source-mappings           CreateEventSourceMapping
//   - GET    /2015-03-31/event-source-mappings           ListEventSourceMappings
//   - GET    /2015-03-31/event-source-mappings/{uuid}    GetEventSourceMapping
//   - PUT    /2015-03-31/event-source-mappings/{uuid}    UpdateEventSourceMapping
//   - DELETE /2015-03-31/event-source-mappings/{uuid}    DeleteEventSourceMapping
//
// Supported event sources: SQS queues, DynamoDB Streams.
// Other sources return a descriptive 400 error.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// createESMRequest is the wire request body for CreateEventSourceMapping.
type createESMRequest struct {
	FunctionName                        string             `json:"FunctionName"`
	EventSourceArn                      string             `json:"EventSourceArn"`
	BatchSize                           *int               `json:"BatchSize"`
	StartingPosition                    string             `json:"StartingPosition"`
	MaximumBatchingWindowInSeconds      int                `json:"MaximumBatchingWindowInSeconds"`
	FilterCriteria                      *FilterCriteria    `json:"FilterCriteria"`
	MaximumRecordAgeInSeconds           *int               `json:"MaximumRecordAgeInSeconds"`
	MaximumRetryAttempts                *int               `json:"MaximumRetryAttempts"`
	TumblingWindowInSeconds             int                `json:"TumblingWindowInSeconds"`
	BisectBatchOnFunctionError          bool               `json:"BisectBatchOnFunctionError"`
	DestinationConfig                   *DestinationConfig `json:"DestinationConfig"`
	ScalingConfig                       *ScalingConfig     `json:"ScalingConfig"`
	Enabled                             *bool              `json:"Enabled"` // nil == true (default enabled)
	FunctionResponseTypes               json.RawMessage    `json:"FunctionResponseTypes"`
	ParallelizationFactor               json.RawMessage    `json:"ParallelizationFactor"`
	StartingPositionTimestamp           json.RawMessage    `json:"StartingPositionTimestamp"`
	SourceAccessConfigurations          json.RawMessage    `json:"SourceAccessConfigurations"`
	SelfManagedEventSource              json.RawMessage    `json:"SelfManagedEventSource"`
	Topics                              json.RawMessage    `json:"Topics"`
	Queues                              json.RawMessage    `json:"Queues"`
	KMSKeyArn                           json.RawMessage    `json:"KMSKeyArn"`
	MetricsConfig                       json.RawMessage    `json:"MetricsConfig"`
	ProvisionedPollerConfig             json.RawMessage    `json:"ProvisionedPollerConfig"`
	AmazonManagedKafkaEventSourceConfig json.RawMessage    `json:"AmazonManagedKafkaEventSourceConfig"`
	DocumentDBEventSourceConfig         json.RawMessage    `json:"DocumentDBEventSourceConfig"`
	LoggingConfig                       json.RawMessage    `json:"LoggingConfig"`
	SelfManagedKafkaEventSourceConfig   json.RawMessage    `json:"SelfManagedKafkaEventSourceConfig"`
	// Tags are stored in the mapping's own tag namespace, not on the record:
	// EventSourceMappingConfiguration has no Tags member, so they are readable
	// only through ListTags. CloudFormation merges the stack's tags into this
	// on every tagged deploy, so it is on the ordinary path, not an edge case.
	Tags map[string]string `json:"Tags"`
}

const (
	defaultSQSBatchSize          = 10
	defaultStreamBatchSize       = 100
	maximumESMBatchSize          = 10000
	maximumFIFOBatchSize         = 10
	maximumBatchingWindowSeconds = 300
)

func eventSourceBatchSize(eventSourceARN string, requested *int) int {
	if requested != nil {
		return *requested
	}
	if strings.Contains(strings.ToLower(eventSourceARN), ":sqs:") {
		return defaultSQSBatchSize
	}
	return defaultStreamBatchSize
}

func validateEventSourceBatching(eventSourceARN string, batchSize, maximumBatchingWindow int, batchSizeSet bool) *protocol.AWSError {
	if batchSize < 1 || batchSize > maximumESMBatchSize {
		return smithyIntegerConstraint("batchSize", batchSize, 1, maximumESMBatchSize)
	}
	if maximumBatchingWindow < 0 || maximumBatchingWindow > maximumBatchingWindowSeconds {
		return smithyIntegerConstraint("maximumBatchingWindowInSeconds", maximumBatchingWindow, 0, maximumBatchingWindowSeconds)
	}

	source := strings.ToLower(eventSourceARN)
	isSQS := strings.Contains(source, ":sqs:")
	isFIFO := isSQS && strings.HasSuffix(source, ".fifo")
	if isFIFO {
		if batchSize > maximumFIFOBatchSize {
			return lambdaInvalidParameter("Batch size cannot be greater than 10 for an SQS FIFO queue.")
		}
		if maximumBatchingWindow != 0 {
			return lambdaInvalidParameter("Maximum batching window is not supported for SQS FIFO queues.")
		}
	}
	isStream := strings.Contains(source, ":kinesis:") || strings.Contains(source, ":dynamodb:")
	if batchSizeSet && (isSQS || isStream) && batchSize > defaultSQSBatchSize && maximumBatchingWindow < 1 {
		// Matches a publicly reported AWS Lambda response surfaced through
		// CloudFormation; exact direct-API text still needs an approved capture:
		// https://forum.serverless.com/t/maximumbatchingwindow-not-passed-to-aws/13837
		return lambdaInvalidParameter("Maximum batch window in seconds must be greater than 0 if maximum batch size is greater than 10")
	}
	return nil
}

// updateESMRequest is the wire request body for UpdateEventSourceMapping.
type updateESMRequest struct {
	FunctionName                        *string            `json:"FunctionName"`
	BatchSize                           *int               `json:"BatchSize"`
	MaximumBatchingWindowInSeconds      *int               `json:"MaximumBatchingWindowInSeconds"`
	FilterCriteria                      *FilterCriteria    `json:"FilterCriteria"`
	MaximumRecordAgeInSeconds           *int               `json:"MaximumRecordAgeInSeconds"`
	MaximumRetryAttempts                *int               `json:"MaximumRetryAttempts"`
	TumblingWindowInSeconds             *int               `json:"TumblingWindowInSeconds"`
	BisectBatchOnFunctionError          *bool              `json:"BisectBatchOnFunctionError"`
	DestinationConfig                   *DestinationConfig `json:"DestinationConfig"`
	ScalingConfig                       *ScalingConfig     `json:"ScalingConfig"`
	Enabled                             *bool              `json:"Enabled"`
	FunctionResponseTypes               json.RawMessage    `json:"FunctionResponseTypes"`
	ParallelizationFactor               json.RawMessage    `json:"ParallelizationFactor"`
	SourceAccessConfigurations          json.RawMessage    `json:"SourceAccessConfigurations"`
	KMSKeyArn                           json.RawMessage    `json:"KMSKeyArn"`
	MetricsConfig                       json.RawMessage    `json:"MetricsConfig"`
	ProvisionedPollerConfig             json.RawMessage    `json:"ProvisionedPollerConfig"`
	Topics                              json.RawMessage    `json:"Topics"`
	Queues                              json.RawMessage    `json:"Queues"`
	AmazonManagedKafkaEventSourceConfig json.RawMessage    `json:"AmazonManagedKafkaEventSourceConfig"`
	DocumentDBEventSourceConfig         json.RawMessage    `json:"DocumentDBEventSourceConfig"`
	LoggingConfig                       json.RawMessage    `json:"LoggingConfig"`
	SelfManagedKafkaEventSourceConfig   json.RawMessage    `json:"SelfManagedKafkaEventSourceConfig"`
}

func (req *createESMRequest) unsupportedMembers() unsupportedRequestMembers {
	return unsupportedRequestMembers{
		"ParallelizationFactor":               rawRequestField(req.ParallelizationFactor),
		"StartingPositionTimestamp":           rawRequestField(req.StartingPositionTimestamp),
		"SourceAccessConfigurations":          rawRequestField(req.SourceAccessConfigurations),
		"SelfManagedEventSource":              rawRequestField(req.SelfManagedEventSource),
		"Topics":                              rawRequestField(req.Topics),
		"Queues":                              rawRequestField(req.Queues),
		"KMSKeyArn":                           rawRequestField(req.KMSKeyArn),
		"MetricsConfig":                       rawRequestField(req.MetricsConfig),
		"ProvisionedPollerConfig":             rawRequestField(req.ProvisionedPollerConfig),
		"AmazonManagedKafkaEventSourceConfig": rawRequestField(req.AmazonManagedKafkaEventSourceConfig),
		"DocumentDBEventSourceConfig":         rawRequestField(req.DocumentDBEventSourceConfig),
		"LoggingConfig":                       rawRequestField(req.LoggingConfig),
		"SelfManagedKafkaEventSourceConfig":   rawRequestField(req.SelfManagedKafkaEventSourceConfig),
	}
}

func (req *updateESMRequest) unsupportedMembers() unsupportedRequestMembers {
	return unsupportedRequestMembers{
		"ParallelizationFactor":               rawRequestField(req.ParallelizationFactor),
		"SourceAccessConfigurations":          rawRequestField(req.SourceAccessConfigurations),
		"KMSKeyArn":                           rawRequestField(req.KMSKeyArn),
		"MetricsConfig":                       rawRequestField(req.MetricsConfig),
		"ProvisionedPollerConfig":             rawRequestField(req.ProvisionedPollerConfig),
		"Topics":                              rawRequestField(req.Topics),
		"Queues":                              rawRequestField(req.Queues),
		"AmazonManagedKafkaEventSourceConfig": rawRequestField(req.AmazonManagedKafkaEventSourceConfig),
		"DocumentDBEventSourceConfig":         rawRequestField(req.DocumentDBEventSourceConfig),
		"LoggingConfig":                       rawRequestField(req.LoggingConfig),
		"SelfManagedKafkaEventSourceConfig":   rawRequestField(req.SelfManagedKafkaEventSourceConfig),
	}
}

// parseFunctionResponseTypes validates the FunctionResponseTypes member and
// returns the list to store on the mapping.
//
// set is false when the member was omitted or sent as null, which leaves an
// existing value alone on update. An explicit empty list is set: it is how
// CloudFormation clears the property back to its default.
//
// Only poll-based sources support the feature, and both sources Overcast
// accepts for a mapping — an SQS queue and a DynamoDB stream — are poll-based,
// so there is no source to refuse it for here.
func parseFunctionResponseTypes(raw json.RawMessage) (values []string, set bool, aerr *protocol.AWSError) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil, false, nil
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false, lambdaInvalidParameter("FunctionResponseTypes must be a list of strings")
	}
	for _, value := range values {
		if value != functionResponseTypeReportBatchItemFailures {
			return nil, false, smithyEnumConstraint(
				"functionResponseTypes",
				"["+strings.Join(values, ", ")+"]",
				functionResponseTypeReportBatchItemFailures,
			)
		}
	}
	return values, true, nil
}

// listESMResponse is the wire response for ListEventSourceMappings.
type listESMResponse struct {
	EventSourceMappings []*EventSourceMapping `json:"EventSourceMappings"`
	NextMarker          *string               `json:"NextMarker,omitempty"`
}

// CreateEventSourceMapping handles POST /2015-03-31/event-source-mappings.
func (h *Handler) CreateEventSourceMapping(w http.ResponseWriter, r *http.Request) {
	var req createESMRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.unsupportedMembers().requested() {
		protocol.NotImplementedJSON(w, r)
		return
	}
	if req.FunctionName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ValidationException", Message: "FunctionName is required", HTTPStatus: http.StatusBadRequest})
		return
	}
	if req.EventSourceArn == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ValidationException", Message: "EventSourceArn is required", HTTPStatus: http.StatusBadRequest})
		return
	}
	// Tag constraints are request-shape validation, so they are checked with
	// the rest of it — before anything is looked up or written.
	if aerr := serviceutil.ValidateTags(lambdaTagCfg, req.Tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Validate event source type: only SQS and DynamoDB Streams are supported.
	sourceLower := strings.ToLower(req.EventSourceArn)
	isSQS := strings.Contains(sourceLower, ":sqs:")
	isDynamoDBStream := strings.Contains(sourceLower, ":dynamodb:") && strings.Contains(sourceLower, "/stream/")
	if !isSQS && !isDynamoDBStream {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ValidationException", Message: "Unsupported event source type. EventSourceArn must be an SQS queue ARN or a DynamoDB Streams ARN", HTTPStatus: http.StatusBadRequest})
		return
	}
	batchSize := eventSourceBatchSize(req.EventSourceArn, req.BatchSize)
	if aerr := validateEventSourceBatching(req.EventSourceArn, batchSize, req.MaximumBatchingWindowInSeconds, req.BatchSize != nil); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	responseTypes, _, aerr := parseFunctionResponseTypes(req.FunctionResponseTypes)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Resolve function name → full ARN.
	funcName := functionNameFromARN(req.FunctionName) // no-op if already a plain name
	if funcName == "" {
		funcName = req.FunctionName
	}
	fn, aerr := h.ls.getFunction(r.Context(), funcName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("Function not found: %s", req.FunctionName), HTTPStatus: http.StatusNotFound})
		return
	}
	funcARN := protocol.LambdaARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, fn.Name)

	// Real AWS rejects cross-region ESMs: the event source (SQS queue or
	// DynamoDB stream) must be in the same region as the Lambda function.
	if sourceRegion := serviceutil.ARNRegion(req.EventSourceArn); sourceRegion != "" {
		if fnRegion := serviceutil.ARNRegion(funcARN); fnRegion != "" && fnRegion != sourceRegion {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "InvalidParameterValueException",
				Message:    "The provided ARNs don't belong to the same region.",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	initialState := esmStateEnabled
	if !enabled {
		initialState = esmStateDisabled
	}

	esm := &EventSourceMapping{
		UUID:                           uuid.New().String(),
		FunctionArn:                    funcARN,
		EventSourceArn:                 req.EventSourceArn,
		State:                          initialState,
		StateTransitionReason:          "USER_INITIATED",
		BatchSize:                      batchSize,
		BatchSizeExplicit:              req.BatchSize != nil,
		StartingPosition:               req.StartingPosition,
		MaximumBatchingWindowInSeconds: req.MaximumBatchingWindowInSeconds,
		FilterCriteria:                 req.FilterCriteria,
		MaximumRecordAgeInSeconds:      req.MaximumRecordAgeInSeconds,
		MaximumRetryAttempts:           req.MaximumRetryAttempts,
		TumblingWindowInSeconds:        req.TumblingWindowInSeconds,
		BisectBatchOnFunctionError:     req.BisectBatchOnFunctionError,
		DestinationConfig:              req.DestinationConfig,
		ScalingConfig:                  req.ScalingConfig,
		FunctionResponseTypes:          responseTypes,
		LastModified:                   float64(h.clk.Now().UnixMilli()) / 1000,
		LastProcessingResult:           "No records processed",
	}
	esm.EventSourceMappingArn = protocol.ARN(
		middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, "lambda", "event-source-mapping:"+esm.UUID,
	)

	// Tags go in before the mapping record: while the record is absent nothing
	// can observe the mapping, so a create that gets as far as being visible is
	// always fully tagged. The reverse order would expose an untagged mapping.
	// deleteESM tears the two down in the mirror order for the same reason.
	if len(req.Tags) > 0 {
		if aerr := h.ls.putESMTags(r.Context(), esm.UUID, req.Tags); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}
	if aerr := h.esm.putESM(r.Context(), esm); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Notify delivery manager (if wired) so event delivery begins immediately.
	if h.esmDelivery != nil {
		h.esmDelivery.Start(esm)
	}

	protocol.WriteRESTJSON(w, r, http.StatusAccepted, esm)
}

// ListEventSourceMappings handles GET /2015-03-31/event-source-mappings.
// This replaces the stub in handler_stubs.go.
func (h *Handler) ListEventSourceMappings(w http.ResponseWriter, r *http.Request) {
	funcName := r.URL.Query().Get("FunctionName")
	if funcName != "" {
		funcName = functionNameFromARN(funcName)
		if funcName == "" {
			funcName = r.URL.Query().Get("FunctionName")
		}
	}
	eventSourceArn := r.URL.Query().Get("EventSourceArn")

	mappings, aerr := h.esm.listESMs(r.Context(), funcName, eventSourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if mappings == nil {
		mappings = []*EventSourceMapping{}
	}
	for _, mapping := range mappings {
		h.ensureEventSourceMappingARN(r, mapping)
	}

	protocol.WriteRESTJSON(w, r, http.StatusOK, listESMResponse{EventSourceMappings: mappings})
}

// GetEventSourceMapping handles GET /2015-03-31/event-source-mappings/{uuid}.
func (h *Handler) GetEventSourceMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "uuid")
	esm, aerr := h.esm.getESM(r.Context(), id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if esm == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("The event source arn (%s) and/or function provided is incorrect", id), HTTPStatus: http.StatusNotFound})
		return
	}
	h.ensureEventSourceMappingARN(r, esm)

	protocol.WriteRESTJSON(w, r, http.StatusOK, esm)
}

// UpdateEventSourceMapping handles PUT /2015-03-31/event-source-mappings/{uuid}.
func (h *Handler) UpdateEventSourceMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "uuid")
	esm, aerr := h.esm.getESM(r.Context(), id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if esm == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("The event source arn (%s) and/or function provided is incorrect", id), HTTPStatus: http.StatusNotFound})
		return
	}
	h.ensureEventSourceMappingARN(r, esm)

	var req updateESMRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.unsupportedMembers().requested() {
		protocol.NotImplementedJSON(w, r)
		return
	}

	batchSize := esm.BatchSize
	if req.BatchSize != nil {
		batchSize = *req.BatchSize
	}
	maximumBatchingWindow := esm.MaximumBatchingWindowInSeconds
	if req.MaximumBatchingWindowInSeconds != nil {
		maximumBatchingWindow = *req.MaximumBatchingWindowInSeconds
	}
	batchSizeExplicit := esm.BatchSizeExplicit || req.BatchSize != nil
	if aerr := validateEventSourceBatching(esm.EventSourceArn, batchSize, maximumBatchingWindow, batchSizeExplicit); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	responseTypes, responseTypesSet, aerr := parseFunctionResponseTypes(req.FunctionResponseTypes)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if req.FunctionName != nil {
		funcName := functionNameFromARN(*req.FunctionName)
		if funcName == "" {
			funcName = *req.FunctionName
		}
		fn, aerr := h.ls.getFunction(r.Context(), funcName)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		if fn == nil {
			protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("Function not found: %s", *req.FunctionName), HTTPStatus: http.StatusNotFound})
			return
		}
		esm.FunctionArn = protocol.LambdaARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, fn.Name)
	}
	if req.BatchSize != nil {
		esm.BatchSize = batchSize
		esm.BatchSizeExplicit = true
	}
	if req.MaximumBatchingWindowInSeconds != nil {
		esm.MaximumBatchingWindowInSeconds = maximumBatchingWindow
	}
	if req.FilterCriteria != nil {
		esm.FilterCriteria = req.FilterCriteria
	}
	if req.MaximumRecordAgeInSeconds != nil {
		esm.MaximumRecordAgeInSeconds = req.MaximumRecordAgeInSeconds
	}
	if req.MaximumRetryAttempts != nil {
		esm.MaximumRetryAttempts = req.MaximumRetryAttempts
	}
	if req.TumblingWindowInSeconds != nil {
		esm.TumblingWindowInSeconds = *req.TumblingWindowInSeconds
	}
	if req.BisectBatchOnFunctionError != nil {
		esm.BisectBatchOnFunctionError = *req.BisectBatchOnFunctionError
	}
	if req.DestinationConfig != nil {
		esm.DestinationConfig = req.DestinationConfig
	}
	if req.ScalingConfig != nil {
		esm.ScalingConfig = req.ScalingConfig
	}
	if responseTypesSet {
		esm.FunctionResponseTypes = responseTypes
	}
	if req.Enabled != nil {
		if *req.Enabled {
			esm.State = esmStateEnabled
		} else {
			esm.State = esmStateDisabled
		}
		esm.StateTransitionReason = "USER_INITIATED"
	}
	esm.LastModified = float64(h.clk.Now().UnixMilli()) / 1000

	if aerr := h.esm.putESM(r.Context(), esm); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Notify delivery manager of state change.
	if h.esmDelivery != nil {
		if esm.State == esmStateEnabled {
			h.esmDelivery.Start(esm)
		} else {
			h.esmDelivery.Stop(id)
		}
	}

	protocol.WriteRESTJSON(w, r, http.StatusAccepted, esm)
}

func (h *Handler) ensureEventSourceMappingARN(r *http.Request, esm *EventSourceMapping) {
	if esm == nil || esm.EventSourceMappingArn != "" {
		return
	}
	esm.EventSourceMappingArn = protocol.ARN(
		middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, "lambda", "event-source-mapping:"+esm.UUID,
	)
}

// DeleteEventSourceMapping handles DELETE /2015-03-31/event-source-mappings/{uuid}.
func (h *Handler) DeleteEventSourceMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "uuid")
	esm, aerr := h.esm.getESM(r.Context(), id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if esm == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("The event source arn (%s) and/or function provided is incorrect", id), HTTPStatus: http.StatusNotFound})
		return
	}
	h.ensureEventSourceMappingARN(r, esm)

	// Mark as deleting, persist, then remove.
	esm.State = esmStateDeleting
	esm.StateTransitionReason = "USER_INITIATED"
	_ = h.esm.putESM(r.Context(), esm)

	if h.esmDelivery != nil {
		h.esmDelivery.Stop(id)
	}

	if aerr := h.esm.deleteESM(r.Context(), id); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteRESTJSON(w, r, http.StatusOK, esm)
}
