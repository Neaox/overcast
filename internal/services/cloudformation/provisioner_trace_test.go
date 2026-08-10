package cloudformation

// provisioner_trace_test.go — every asynchronous provisioning entry point must
// carry the originating request's trace recorder into its goroutine, so the
// internal service-to-service calls it makes are recorded as hops. Create and
// update always did; delete and rollback silently dropped every hop because
// they never received a recorder at all.

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
	"github.com/Neaox/overcast/internal/trace"
)

// recordingRouter stands in for the emulator router. It answers 200 to every
// internal dispatch and keeps the requests it served so a test can assert on
// the headers CloudFormation propagated.
type recordingRouter struct {
	mu       sync.Mutex
	requests []*http.Request
	// respond, when set, writes the response instead of the default empty 200.
	respond func(w http.ResponseWriter, r *http.Request)
}

func (h *recordingRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests = append(h.requests, r.Clone(context.Background()))
	respond := h.respond
	h.mu.Unlock()
	if respond != nil {
		respond(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *recordingRouter) served() []*http.Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*http.Request(nil), h.requests...)
}

// newTracedTestHandler builds a Handler whose provisioner dispatches into
// router. CFNSyncWait is generous so awaitBriefly blocks until the async
// goroutine has finished and every hop it records is already on the recorder.
func newTracedTestHandler(t *testing.T, router http.Handler) (*Handler, *cfnStore) {
	t.Helper()
	cfg := &config.Config{
		Region:      "us-east-1",
		AccountID:   "000000000000",
		Port:        4566,
		CFNSyncWait: 10 * time.Second,
	}
	clk := clock.New()
	st := newCFNStore(state.NewMemoryStore(), cfg.Region, clk)
	log := serviceutil.NewServiceLogger(zap.NewNop(), "cloudformation")
	prov := newProvisioner(cfg, st, clk, log)
	prov.initRouter(router)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		prov.stop(ctx)
	})
	return newHandler(cfg, st, log, clk, prov), st
}

// tracedRequest attaches a recorder to a Query-protocol request so the handler
// sees exactly what the DebugTrace middleware would have installed.
func tracedRequest(req *http.Request, rec *trace.Recorder) *http.Request {
	return req.WithContext(trace.ContextWithRecorder(req.Context(), rec))
}

func newTestRecorder(requestID string) *trace.Recorder {
	return trace.NewRecorder(requestID, time.Unix(0, 0).UTC(), "POST", "/", "localhost", "", http.Header{})
}

// queueResource is a stack resource whose teardown dispatches an internal
// AmazonSQS.DeleteQueue call — one hop per resource.
func queueResource(logicalID, status string) StackResource {
	return StackResource{
		LogicalID:  logicalID,
		PhysicalID: "arn:aws:sqs:us-east-1:000000000000:" + logicalID,
		Type:       "AWS::SQS::Queue",
		Status:     status,
	}
}

func hopTargets(hops []trace.Hop) []string {
	out := make([]string, 0, len(hops))
	for _, h := range hops {
		out = append(out, h.TargetURI)
	}
	return out
}

// ---- DeleteStack -----------------------------------------------------------

func TestDeleteStack_recordsHopsForTheResourcesItTearsDown(t *testing.T) {
	// Given: a stack with two resources, and a DeleteStack request being traced
	router := &recordingRouter{}
	h, st := newTracedTestHandler(t, router)
	seedStack(t, st, "doomed", StatusCreateComplete,
		queueResource("QueueA", ResourceCreateComplete),
		queueResource("QueueB", ResourceCreateComplete),
	)
	rec := newTestRecorder("delete-req-1")

	// When: the stack is deleted through the Query protocol
	w := httptest.NewRecorder()
	h.dispatch(w, tracedRequest(cfnPost("DeleteStack", map[string]string{"StackName": "doomed"}), rec))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// Then: the teardown of each resource is recorded as a hop on the
	// originating request's trace
	hops := rec.Entry().Hops
	if len(hops) != 2 {
		t.Fatalf("hops = %d (%v), want 2", len(hops), hopTargets(hops))
	}
	for _, hop := range hops {
		if hop.Service != "sqs" || hop.Operation != "DeleteQueue" {
			t.Errorf("hop = %s/%s, want sqs/DeleteQueue", hop.Service, hop.Operation)
		}
		if hop.CallerService != "cloudformation" {
			t.Errorf("hop CallerService = %q, want cloudformation", hop.CallerService)
		}
	}
}

func TestDeleteStackTyped_recordsHopsForTheResourcesItTearsDown(t *testing.T) {
	// Given: the same stack, deleted through the typed/JSON protocol path
	router := &recordingRouter{}
	h, st := newTracedTestHandler(t, router)
	seedStack(t, st, "doomed-typed", StatusCreateComplete,
		queueResource("QueueA", ResourceCreateComplete),
	)
	rec := newTestRecorder("delete-req-2")
	ctx := trace.ContextWithRecorder(context.Background(), rec)

	// When: DeleteStack runs
	if _, aerr := h.deleteStackTyped(ctx, &deleteStackReq{StackName: "doomed-typed"}); aerr != nil {
		t.Fatalf("deleteStackTyped: %v", aerr)
	}

	// Then: the hop is recorded against the originating request
	if hops := rec.Entry().Hops; len(hops) != 1 {
		t.Fatalf("hops = %d (%v), want 1", len(hops), hopTargets(hops))
	}
}

// ---- ExecuteChangeSet ------------------------------------------------------

// Re-creating a stack that already failed once starts by dropping the previous
// generation's resource records, and the work that follows is the whole point of
// the deploy: it must still be dispatched under the originating request's
// recorder, so the second attempt's hops are traceable exactly like the first's.
func TestExecuteChangeSet_recreateRecordsHopsForTheResourcesItProvisions(t *testing.T) {
	// Given: a stack left in ROLLBACK_COMPLETE by a create that failed, and a
	// CREATE change set against it being traced
	router := &recordingRouter{}
	h, st := newTracedTestHandler(t, router)
	seedStack(t, st, "redeployed", StatusRollbackComplete, StackResource{
		LogicalID:    "Queue",
		Type:         "AWS::SQS::Queue",
		Status:       ResourceCreateFailed,
		StatusReason: "queue name already in use",
	})
	rec := newTestRecorder("redeploy-req-1")
	const templateBody = `{"Resources":{"Queue":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"orders"}}}}`

	// When: the change set is created and executed
	for _, call := range []struct {
		action string
		params map[string]string
	}{
		{"CreateChangeSet", map[string]string{
			"StackName": "redeployed", "ChangeSetName": "cs-1",
			"ChangeSetType": "CREATE", "TemplateBody": templateBody,
		}},
		{"ExecuteChangeSet", map[string]string{
			"StackName": "redeployed", "ChangeSetName": "cs-1",
		}},
	} {
		w := httptest.NewRecorder()
		h.dispatch(w, tracedRequest(cfnPost(call.action, call.params), rec))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body: %s", call.action, w.Code, w.Body.String())
		}
	}

	// Then: provisioning the queue is recorded as a hop on the originating
	// request's trace
	hops := rec.Entry().Hops
	if len(hops) != 1 {
		t.Fatalf("hops = %d (%v), want 1", len(hops), hopTargets(hops))
	}
	if hops[0].Service != "sqs" || hops[0].Operation != "CreateQueue" {
		t.Errorf("hop = %s/%s, want sqs/CreateQueue", hops[0].Service, hops[0].Operation)
	}
	if hops[0].CallerService != "cloudformation" {
		t.Errorf("hop CallerService = %q, want cloudformation", hops[0].CallerService)
	}
}

// ---- RollbackStack ---------------------------------------------------------

func TestRollbackStack_recordsHopsForTheResourcesItRetires(t *testing.T) {
	// Given: an UPDATE_FAILED stack carrying a resource an earlier automatic
	// rollback could not clean up, and a RollbackStack request being traced
	router := &recordingRouter{}
	h, st := newTracedTestHandler(t, router)
	seedStack(t, st, "messy-traced", StatusUpdateFailed,
		queueResource("Queue", ResourceDeleteFailed),
	)
	rec := newTestRecorder("rollback-req-1")

	// When: the stack is rolled back
	w := httptest.NewRecorder()
	h.dispatch(w, tracedRequest(cfnPost("RollbackStack", map[string]string{"StackName": "messy-traced"}), rec))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp rollbackStackResponse
	if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v; body: %s", err, w.Body.String())
	}

	// Then: retiring the resource is recorded as a hop
	hops := rec.Entry().Hops
	if len(hops) != 1 {
		t.Fatalf("hops = %d (%v), want 1", len(hops), hopTargets(hops))
	}
	if hops[0].Service != "sqs" || hops[0].Operation != "DeleteQueue" {
		t.Errorf("hop = %s/%s, want sqs/DeleteQueue", hops[0].Service, hops[0].Operation)
	}
}

func TestRollbackStack_createPathRecordsHopsForTheResourcesItUnwinds(t *testing.T) {
	// Given: a CREATE_FAILED stack whose partially built resource must be
	// unwound, and a traced RollbackStack request
	router := &recordingRouter{}
	h, st := newTracedTestHandler(t, router)
	seedStack(t, st, "half-built", StatusCreateFailed,
		queueResource("Queue", ResourceCreateComplete),
	)
	rec := newTestRecorder("rollback-req-2")

	// When: the stack is rolled back
	w := httptest.NewRecorder()
	h.dispatch(w, tracedRequest(cfnPost("RollbackStack", map[string]string{"StackName": "half-built"}), rec))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// Then: unwinding the resource is recorded as a hop
	if hops := rec.Entry().Hops; len(hops) != 1 {
		t.Fatalf("hops = %d (%v), want 1", len(hops), hopTargets(hops))
	}
}

// ---- Parent/child request-ID linking ---------------------------------------

// A hop's RequestID is the only link from the parent trace to the child trace
// the dispatched service recorded. Reading it back off the response header made
// that link depend on which helper the target service happened to answer with:
// S3 answers CreateBucket through protocol.WriteEmpty (x-amz-request-id) and
// its idempotent-recreate branch writes no request-ID header at all, so those
// hops linked to nothing.
func TestInternalRequest_hopLinksToTheChildRequestID(t *testing.T) {
	cases := []struct {
		name    string
		respond func(w http.ResponseWriter, r *http.Request)
	}{
		{name: "service reports no request-ID header"},
		{
			name: "service answers with x-amz-request-id like S3",
			respond: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("x-amz-request-id", r.Header.Get("x-amzn-requestid"))
				w.WriteHeader(http.StatusOK)
			},
		},
		{
			name: "service answers with x-amzn-requestid like the JSON protocols",
			respond: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("x-amzn-requestid", r.Header.Get("x-amzn-requestid"))
				w.WriteHeader(http.StatusOK)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a traced dispatch to a service answering as described
			rec := newTestRecorder("parent-req")
			ctx := trace.ContextWithRecorder(context.Background(), rec)
			router := &recordingRouter{respond: tc.respond}

			// When: CloudFormation creates a bucket internally
			if _, err := internalRequest(ctx, router, "us-east-1", http.MethodPut, "/my-bucket", "", nil); err != nil {
				t.Fatalf("internalRequest: %v", err)
			}

			// Then: the hop records the child request's own ID, and the child
			// request was told to use it
			hops := rec.Entry().Hops
			if len(hops) != 1 {
				t.Fatalf("hops = %d, want 1", len(hops))
			}
			served := router.served()
			if len(served) != 1 {
				t.Fatalf("served = %d requests, want 1", len(served))
			}
			childID := served[0].Header.Get("x-amzn-requestid")
			if childID == "" {
				t.Fatal("child request carries no x-amzn-requestid; the hop cannot link to its trace")
			}
			if hops[0].RequestID != childID {
				t.Errorf("hop RequestID = %q, want the child request ID %q", hops[0].RequestID, childID)
			}
			if got := served[0].Header.Get("X-Overcast-Parent-Request-Id"); got != "parent-req" {
				t.Errorf("parent request ID = %q, want parent-req", got)
			}
		})
	}
}

func TestInternalRequest_untracedDispatchMintsNoRequestID(t *testing.T) {
	// Given: no recorder in context (debug tracing off)
	router := &recordingRouter{}

	// When: CloudFormation dispatches internally
	if _, err := internalRequest(context.Background(), router, "us-east-1", http.MethodPut, "/my-bucket", "", nil); err != nil {
		t.Fatalf("internalRequest: %v", err)
	}

	// Then: no trace headers are minted — an untraced deploy pays nothing
	served := router.served()
	if len(served) != 1 {
		t.Fatalf("served = %d requests, want 1", len(served))
	}
	if got := served[0].Header.Get("x-amzn-requestid"); got != "" {
		t.Errorf("x-amzn-requestid = %q, want empty", got)
	}
	if got := served[0].Header.Get("X-Overcast-Parent-Request-Id"); got != "" {
		t.Errorf("parent request ID = %q, want empty", got)
	}
}

// Hop labels are resolved lazily, only once a trace is being recorded. This
// pins the labels each protocol's helper produces so that staying off the
// untraced path does not quietly change what a traced hop says.
func TestInternalDispatch_hopLabelsIdentifyTheTargetOperation(t *testing.T) {
	cases := []struct {
		name                        string
		dispatch                    func(ctx context.Context, router http.Handler) error
		service, operation, targetU string
	}{
		{
			name: "json protocol",
			dispatch: func(ctx context.Context, router http.Handler) error {
				_, err := internalJSON(ctx, router, "us-east-1", "AmazonSQS.CreateQueue", map[string]any{"QueueName": "q"})
				return err
			},
			service: "sqs", operation: "CreateQueue",
			targetU: "POST / (X-Amz-Target: AmazonSQS.CreateQueue)",
		},
		{
			name: "query protocol",
			dispatch: func(ctx context.Context, router http.Handler) error {
				_, err := internalQuery(ctx, router, "us-east-1", map[string]string{"Action": "CreateRole"})
				return err
			},
			service: "iam", operation: "CreateRole",
			targetU: "POST /?Action=CreateRole",
		},
		{
			name: "rest protocol",
			dispatch: func(ctx context.Context, router http.Handler) error {
				_, err := internalRequest(ctx, router, "us-east-1", http.MethodPost, "/2015-03-31/functions", "application/json", nil)
				return err
			},
			service: "lambda", operation: http.MethodPost,
			targetU: "POST /2015-03-31/functions",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a traced context
			rec := newTestRecorder("parent-labels")
			ctx := trace.ContextWithRecorder(context.Background(), rec)

			// When: the call is dispatched
			if err := tc.dispatch(ctx, &recordingRouter{}); err != nil {
				t.Fatalf("dispatch: %v", err)
			}

			// Then: the hop names the service, operation and target
			hops := rec.Entry().Hops
			if len(hops) != 1 {
				t.Fatalf("hops = %d, want 1", len(hops))
			}
			if hops[0].Service != tc.service || hops[0].Operation != tc.operation {
				t.Errorf("hop = %s/%s, want %s/%s",
					hops[0].Service, hops[0].Operation, tc.service, tc.operation)
			}
			if hops[0].TargetURI != tc.targetU {
				t.Errorf("hop TargetURI = %q, want %q", hops[0].TargetURI, tc.targetU)
			}
		})
	}
}

// A large CDK deploy issues thousands of internal dispatches, almost always
// with tracing off. These two benchmarks exist to keep the untraced path free
// of work only a trace would read — hop labels, minted request IDs, clock
// reads. Compare them when changing internalCall.
func BenchmarkInternalDispatch_untraced(b *testing.B) {
	router := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := internalQuery(ctx, router, "us-east-1", map[string]string{"Action": "CreateRole", "RoleName": "r"}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInternalDispatch_traced(b *testing.B) {
	router := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ctx := trace.ContextWithRecorder(context.Background(), newTestRecorder("bench"))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := internalQuery(ctx, router, "us-east-1", map[string]string{"Action": "CreateRole", "RoleName": "r"}); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- Dispatchers that recorded no hops at all ------------------------------

// CloudFront's If-Match/ETag flow and AppSync Events both grew their own
// dispatch helper, and neither recorded a hop or propagated the parent request
// ID. Both now funnel through the one dispatch path.
func TestInternalDispatch_specialisedHelpersRecordHops(t *testing.T) {
	t.Run("cloudfront If-Match flow", func(t *testing.T) {
		// Given: a traced context
		rec := newTestRecorder("parent-cf")
		ctx := trace.ContextWithRecorder(context.Background(), rec)
		router := &recordingRouter{}

		// When: the distribution handler dispatches with an If-Match header
		if _, err := cfInternalRequest(ctx, router, "us-east-1", http.MethodDelete,
			"/2020-05-31/distribution/ABC", "", nil,
			map[string]string{"If-Match": "etag-1"}); err != nil {
			t.Fatalf("cfInternalRequest: %v", err)
		}

		// Then: the hop is recorded and the extra header still travels
		hops := rec.Entry().Hops
		if len(hops) != 1 {
			t.Fatalf("hops = %d, want 1", len(hops))
		}
		if hops[0].Service != "cloudfront" {
			t.Errorf("hop Service = %q, want cloudfront", hops[0].Service)
		}
		if got := router.served()[0].Header.Get("If-Match"); got != "etag-1" {
			t.Errorf("If-Match = %q, want etag-1", got)
		}
	})

	t.Run("appsync events", func(t *testing.T) {
		// Given: a traced context
		rec := newTestRecorder("parent-appsync")
		ctx := trace.ContextWithRecorder(context.Background(), rec)
		router := &recordingRouter{}

		// When: an AppSync Events API is torn down
		if _, err := internalAppSyncEventsRequest(ctx, router, "us-east-1",
			http.MethodDelete, "/v2/apis/api-1", "", nil); err != nil {
			t.Fatalf("internalAppSyncEventsRequest: %v", err)
		}

		// Then: the hop is recorded and the signed Authorization header survives
		hops := rec.Entry().Hops
		if len(hops) != 1 {
			t.Fatalf("hops = %d, want 1", len(hops))
		}
		served := router.served()[0]
		if served.Header.Get("Authorization") == "" {
			t.Error("Authorization header was dropped")
		}
		if served.Header.Get("X-Overcast-Parent-Request-Id") != "parent-appsync" {
			t.Errorf("parent request ID = %q, want parent-appsync",
				served.Header.Get("X-Overcast-Parent-Request-Id"))
		}
	})
}
