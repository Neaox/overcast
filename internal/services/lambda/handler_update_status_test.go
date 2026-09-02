package lambda

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// The lifecycle fields AWS reports next to State, and the waiters that read
// them: `aws lambda wait function-updated` and the SDKs' FunctionUpdated
// waiters poll GetFunctionConfiguration for LastUpdateStatus, and a response
// that omits it leaves every one of them polling to its attempt budget (#1550).

func updateStatusHandler(t *testing.T, prewarmer func(*Function, func(error))) (*Handler, *lambdaStore) {
	t.Helper()
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)
	return &Handler{
		cfg:       &config.Config{Region: "us-east-1"},
		log:       serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk:       clk,
		ls:        ls,
		runtimes:  newRuntimeRegistry(nil),
		tracker:   tracker,
		prewarmer: prewarmer,
	}, ls
}

// seedActiveFunction stores a function that has finished being created, which
// is the state every update below starts from.
func seedActiveFunction(t *testing.T, ls *lambdaStore, fn *Function) {
	t.Helper()
	fn.ARN = "arn:aws:lambda:us-east-1:000000000000:function:" + fn.Name
	fn.State = "Active"
	fn.LastUpdateStatus = lastUpdateSuccessful
	fn.RevisionId = "seed-revision"
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed function %s: %s", fn.Name, aerr.Message)
	}
}

func seedActiveZipFunction(t *testing.T, ls *lambdaStore, name string) {
	t.Helper()
	fn := &Function{
		Name:        name,
		Runtime:     "nodejs22.x",
		Handler:     "index.handler",
		PackageType: "Zip",
		MemorySize:  128,
		Timeout:     3,
	}
	fn.setCode([]byte("old deployment package"))
	seedActiveFunction(t, ls, fn)
}

func seedActiveImageFunction(t *testing.T, ls *lambdaStore, name, imageUri string) {
	t.Helper()
	seedActiveFunction(t, ls, &Function{
		Name:        name,
		Runtime:     "image",
		PackageType: "Image",
		ImageUri:    imageUri,
		MemorySize:  128,
		Timeout:     3,
	})
}

func updateFunctionCode(t *testing.T, h *Handler, name string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal update body: %v", err)
	}
	req := withFunctionNameParam(httptest.NewRequest(http.MethodPut,
		"/2015-03-31/functions/"+name+"/code", bytes.NewReader(raw)), name)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateFunctionCode(rec, req)
	return rec
}

// decodeConfig reads a FunctionConfiguration off a response, failing the test
// on any status but the one expected.
func decodeConfig(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) functionConfiguration {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, wantStatus, rec.Body.String())
	}
	var cfg functionConfiguration
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode configuration: %v; body: %s", err, rec.Body.String())
	}
	return cfg
}

func getFunctionConfiguration(t *testing.T, h *Handler, name string) functionConfiguration {
	t.Helper()
	req := withFunctionNameParam(httptest.NewRequest(http.MethodGet,
		"/2015-03-31/functions/"+name+"/configuration", nil), name)
	rec := httptest.NewRecorder()
	h.GetFunctionConfiguration(rec, req)
	return decodeConfig(t, rec, http.StatusOK)
}

func getFunction(t *testing.T, h *Handler, name string) functionConfiguration {
	t.Helper()
	req := withFunctionNameParam(httptest.NewRequest(http.MethodGet,
		"/2015-03-31/functions/"+name, nil), name)
	rec := httptest.NewRecorder()
	h.GetFunction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetFunction status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Configuration functionConfiguration `json:"Configuration"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GetFunction: %v; body: %s", err, rec.Body.String())
	}
	return resp.Configuration
}

func TestUpdateFunctionCode_zipReportsSuccessfulImmediately(t *testing.T) {
	// Given: a zip function that has finished being created.
	h, ls := updateStatusHandler(t, nil)
	seedActiveZipFunction(t, ls, "zip-fn")

	// When: its deployment package is replaced.
	cfg := decodeConfig(t, updateFunctionCode(t, h, "zip-fn", map[string]any{
		"ZipFile": []byte("new deployment package"),
	}), http.StatusOK)

	// Then: the update call itself reports the outcome — a zip deployment is
	// durably applied before it answers, so there is no InProgress to report —
	// and State is untouched.
	if cfg.LastUpdateStatus != lastUpdateSuccessful {
		t.Errorf("UpdateFunctionCode LastUpdateStatus = %q, want %q", cfg.LastUpdateStatus, lastUpdateSuccessful)
	}
	if cfg.LastUpdateStatusReason != "" || cfg.LastUpdateStatusReasonCode != "" {
		t.Errorf("successful update carried reason %q/%q, want neither",
			cfg.LastUpdateStatusReason, cfg.LastUpdateStatusReasonCode)
	}
	if cfg.State != "Active" {
		t.Errorf("State = %q, want Active — an update does not un-deploy a function", cfg.State)
	}

	// And: both read paths agree with it, which is where the waiters look.
	if got := getFunctionConfiguration(t, h, "zip-fn").LastUpdateStatus; got != lastUpdateSuccessful {
		t.Errorf("GetFunctionConfiguration LastUpdateStatus = %q, want %q", got, lastUpdateSuccessful)
	}
	if got := getFunction(t, h, "zip-fn").LastUpdateStatus; got != lastUpdateSuccessful {
		t.Errorf("GetFunction LastUpdateStatus = %q, want %q", got, lastUpdateSuccessful)
	}
}

func TestUpdateFunctionConfiguration_reportsSuccessfulImmediately(t *testing.T) {
	// Given: a zip function that has finished being created.
	h, ls := updateStatusHandler(t, nil)
	seedActiveZipFunction(t, ls, "cfg-fn")

	// When: its configuration changes.
	cfg := decodeConfig(t, updateFunctionConfiguration(t, h, "cfg-fn", map[string]any{
		"Timeout": 30,
	}), http.StatusOK)

	// Then: the change and the status it reports land together — configuration
	// never waits on an image, so it settles before the call answers.
	if cfg.LastUpdateStatus != lastUpdateSuccessful {
		t.Errorf("UpdateFunctionConfiguration LastUpdateStatus = %q, want %q", cfg.LastUpdateStatus, lastUpdateSuccessful)
	}
	if cfg.Timeout != 30 {
		t.Errorf("Timeout = %d, want 30", cfg.Timeout)
	}
	if got := getFunctionConfiguration(t, h, "cfg-fn").LastUpdateStatus; got != lastUpdateSuccessful {
		t.Errorf("GetFunctionConfiguration LastUpdateStatus = %q, want %q", got, lastUpdateSuccessful)
	}
}

func TestUpdateFunctionCode_newImagePullReportsInProgressThenSuccessful(t *testing.T) {
	// Given: an image function, and a container runtime whose pull has not
	// finished — the one update Overcast really does run asynchronously.
	var onReady func(error)
	h, ls := updateStatusHandler(t, func(_ *Function, ready func(error)) { onReady = ready })
	seedActiveImageFunction(t, ls, "img-fn", "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v1")

	// When: the function is pointed at a new image.
	cfg := decodeConfig(t, updateFunctionCode(t, h, "img-fn", map[string]any{
		"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v2",
	}), http.StatusOK)

	// Then: the caller is told the update is running, and reads agree.
	if cfg.LastUpdateStatus != lastUpdateInProgress {
		t.Fatalf("UpdateFunctionCode LastUpdateStatus = %q, want %q", cfg.LastUpdateStatus, lastUpdateInProgress)
	}
	if cfg.State != "Active" {
		t.Errorf("State = %q, want Active — an image pull moves LastUpdateStatus, not State", cfg.State)
	}
	if got := getFunctionConfiguration(t, h, "img-fn").LastUpdateStatus; got != lastUpdateInProgress {
		t.Fatalf("GetFunctionConfiguration LastUpdateStatus = %q, want %q", got, lastUpdateInProgress)
	}

	// When: the pull lands.
	if onReady == nil {
		t.Fatal("update did not start an image pull")
	}
	onReady(nil)

	// Then: the waiter's next poll sees the update finish.
	got := getFunctionConfiguration(t, h, "img-fn")
	if got.LastUpdateStatus != lastUpdateSuccessful {
		t.Errorf("LastUpdateStatus = %q, want %q", got.LastUpdateStatus, lastUpdateSuccessful)
	}
	if got.ImageUri != "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v2" {
		t.Errorf("ImageUri = %q, want the updated image", got.ImageUri)
	}
}

func TestUpdateFunctionCode_imagePullFailureReportsFailed(t *testing.T) {
	// Given: an image function whose next pull will be refused by the registry.
	var onReady func(error)
	h, ls := updateStatusHandler(t, func(_ *Function, ready func(error)) { onReady = ready })
	seedActiveImageFunction(t, ls, "bad-img-fn", "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v1")

	// When: the update's pull fails.
	decodeConfig(t, updateFunctionCode(t, h, "bad-img-fn", map[string]any{
		"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:missing",
	}), http.StatusOK)
	if onReady == nil {
		t.Fatal("update did not start an image pull")
	}
	onReady(errors.New("pull access denied for demo, repository does not exist"))

	// Then: the failure is reported where the waiter fails out on it, with one
	// of AWS's own reason codes and the registry's own words.
	got := getFunctionConfiguration(t, h, "bad-img-fn")
	if got.LastUpdateStatus != lastUpdateFailed {
		t.Fatalf("LastUpdateStatus = %q, want %q", got.LastUpdateStatus, lastUpdateFailed)
	}
	if got.LastUpdateStatusReasonCode != "ImageAccessDenied" {
		t.Errorf("LastUpdateStatusReasonCode = %q, want ImageAccessDenied", got.LastUpdateStatusReasonCode)
	}
	if got.LastUpdateStatusReason == "" {
		t.Error("LastUpdateStatusReason is empty, want the pull failure")
	}
	// And: State is untouched — AWS fails the update, not the function.
	if got.State != "Active" {
		t.Errorf("State = %q, want Active", got.State)
	}
}

func TestUpdateFunctionCode_refusesASecondUpdateWhileOneIsInProgress(t *testing.T) {
	// Given: an image function whose pull is still running.
	var onReady func(error)
	h, ls := updateStatusHandler(t, func(_ *Function, ready func(error)) { onReady = ready })
	seedActiveImageFunction(t, ls, "busy-fn", "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v1")
	decodeConfig(t, updateFunctionCode(t, h, "busy-fn", map[string]any{
		"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v2",
	}), http.StatusOK)

	// When: a second update arrives before it settles.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"UpdateFunctionCode": updateFunctionCode(t, h, "busy-fn", map[string]any{
			"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v3",
		}),
		"UpdateFunctionConfiguration": updateFunctionConfiguration(t, h, "busy-fn", map[string]any{
			"Timeout": 30,
		}),
	} {
		// Then: AWS's ResourceConflictException, not a silent overwrite.
		if rec.Code != http.StatusConflict {
			t.Errorf("%s status = %d, want 409; body: %s", name, rec.Code, rec.Body.String())
			continue
		}
		var body struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Header().Get("x-amzn-errortype") == "" && body.Message == "" {
			t.Errorf("%s conflict carried no AWS error shape; body: %s", name, rec.Body.String())
		}
	}

	// And: the in-flight update still settles, so the function is not wedged.
	onReady(nil)
	if got := getFunctionConfiguration(t, h, "busy-fn").LastUpdateStatus; got != lastUpdateSuccessful {
		t.Errorf("LastUpdateStatus = %q, want %q once the pull lands", got, lastUpdateSuccessful)
	}
	if rec := updateFunctionConfiguration(t, h, "busy-fn", map[string]any{"Timeout": 30}); rec.Code != http.StatusOK {
		t.Errorf("update after the pull settled = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateFunction_lastUpdateStatusFirstSetWhenCreationCompletes(t *testing.T) {
	// Given: a create whose image pull has not finished.
	var onReady func(error)
	h, _ := updateStatusHandler(t, func(_ *Function, ready func(error)) { onReady = ready })

	// When: CreateFunction answers.
	createPendingTestFunction(t, h, "new-fn")

	// Then: the function is Pending and carries no LastUpdateStatus at all —
	// AWS first sets it when creation completes, and an omitted field is what
	// keeps a FunctionUpdated waiter polling rather than passing early.
	pending := getFunctionConfiguration(t, h, "new-fn")
	if pending.State != "Pending" {
		t.Fatalf("State = %q, want Pending", pending.State)
	}
	if pending.LastUpdateStatus != "" {
		t.Errorf("LastUpdateStatus = %q while creating, want it absent", pending.LastUpdateStatus)
	}

	// When: the pull lands.
	if onReady == nil {
		t.Fatal("create did not start an image pull")
	}
	onReady(nil)

	// Then: both halves of the lifecycle report the function is ready.
	active := getFunctionConfiguration(t, h, "new-fn")
	if active.State != "Active" {
		t.Errorf("State = %q, want Active", active.State)
	}
	if active.LastUpdateStatus != lastUpdateSuccessful {
		t.Errorf("LastUpdateStatus = %q, want %q", active.LastUpdateStatus, lastUpdateSuccessful)
	}
}

func TestCreateFunction_failedCreateReportsFailedOnBothHalves(t *testing.T) {
	// Given: a create whose image cannot be pulled.
	h, _ := updateStatusHandler(t, func(_ *Function, ready func(error)) {
		ready(errors.New("no such image: public.ecr.aws/lambda/nodejs:99"))
	})

	// When: the create runs to completion.
	createPendingTestFunction(t, h, "doomed-fn")

	// Then: LastUpdateStatus fails with the create, so a FunctionUpdated waiter
	// aimed at a function that never came up fails out instead of polling to
	// its attempt budget.
	got := getFunctionConfiguration(t, h, "doomed-fn")
	if got.State != "Failed" {
		t.Fatalf("State = %q, want Failed", got.State)
	}
	if got.LastUpdateStatus != lastUpdateFailed {
		t.Errorf("LastUpdateStatus = %q, want %q", got.LastUpdateStatus, lastUpdateFailed)
	}
	if got.LastUpdateStatusReasonCode != "ImageAccessDenied" {
		t.Errorf("LastUpdateStatusReasonCode = %q, want ImageAccessDenied", got.LastUpdateStatusReasonCode)
	}
}

func TestImagePullReasonCode(t *testing.T) {
	// AWS's own LastUpdateStatusReasonCode vocabulary: an image it could not
	// fetch, one it fetched and could not use, and everything else.
	cases := map[string]string{
		"pull access denied for demo, repository does not exist": "ImageAccessDenied",
		"manifest unknown":                        "ImageAccessDenied",
		"no such image: demo:v2":                  "ImageAccessDenied",
		"unauthorized: authentication required":   "ImageAccessDenied",
		"no matching manifest for linux/arm64":    "InvalidImage",
		"invalid image config: unsupported media": "InvalidImage",
		"docker daemon connection reset":          "InternalError",
	}
	for msg, want := range cases {
		if got := imagePullReasonCode(errors.New(msg)); got != want {
			t.Errorf("imagePullReasonCode(%q) = %q, want %q", msg, got, want)
		}
	}
	if got := imagePullReasonCode(nil); got != "" {
		t.Errorf("imagePullReasonCode(nil) = %q, want empty", got)
	}
}

// ─── Waiter semantics ────────────────────────────────────────────────────────

// waiterOutcome is what a botocore waiter reports back: success, failure, or
// the attempt budget running out.
type waiterOutcome string

const (
	waiterSuccess   waiterOutcome = "success"
	waiterFailure   waiterOutcome = "failure"
	waiterExhausted waiterOutcome = "max attempts exceeded"
)

// runWaiter is botocore's acceptor loop, minus the sleeping: poll
// GetFunctionConfiguration, read one field, and match it against the waiter's
// success and failure words. Anything else is a retry, which is exactly how a
// field no response carries turns into "Max attempts exceeded" (#1550).
func runWaiter(t *testing.T, h *Handler, name, field, success, failure string, attempts int, tick func(attempt int)) waiterOutcome {
	t.Helper()
	for attempt := range attempts {
		if tick != nil {
			tick(attempt)
		}
		var raw map[string]any
		req := withFunctionNameParam(httptest.NewRequest(http.MethodGet,
			"/2015-03-31/functions/"+name+"/configuration", nil), name)
		rec := httptest.NewRecorder()
		h.GetFunctionConfiguration(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("waiter poll status = %d; body: %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatalf("waiter poll decode: %v", err)
		}
		switch raw[field] {
		case success:
			return waiterSuccess
		case failure:
			return waiterFailure
		}
	}
	return waiterExhausted
}

func TestWaiterFunctionUpdated_returnsAfterUpdateFunctionCode(t *testing.T) {
	// Given: an image function mid-pull — `aws lambda wait function-updated`
	// polls LastUpdateStatus, so this is the case it has to survive.
	var onReady func(error)
	h, ls := updateStatusHandler(t, func(_ *Function, ready func(error)) { onReady = ready })
	seedActiveImageFunction(t, ls, "waited-fn", "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v1")
	decodeConfig(t, updateFunctionCode(t, h, "waited-fn", map[string]any{
		"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v2",
	}), http.StatusOK)

	// When: the waiter runs, and the pull lands on its third poll.
	got := runWaiter(t, h, "waited-fn", "LastUpdateStatus", "Successful", "Failed", 60, func(attempt int) {
		if attempt == 2 {
			onReady(nil)
		}
	})

	// Then: it returns, rather than spending its whole budget on a field no
	// response carries.
	if got != waiterSuccess {
		t.Fatalf("FunctionUpdated waiter = %q, want %q", got, waiterSuccess)
	}
}

func TestWaiterFunctionUpdated_failsOnAFailedUpdate(t *testing.T) {
	// Given: an image function whose update pull is about to fail.
	var onReady func(error)
	h, ls := updateStatusHandler(t, func(_ *Function, ready func(error)) { onReady = ready })
	seedActiveImageFunction(t, ls, "failed-update-fn", "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v1")
	decodeConfig(t, updateFunctionCode(t, h, "failed-update-fn", map[string]any{
		"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:gone",
	}), http.StatusOK)

	// When: the waiter is running when the pull fails.
	got := runWaiter(t, h, "failed-update-fn", "LastUpdateStatus", "Successful", "Failed", 60, func(attempt int) {
		if attempt == 1 {
			onReady(errors.New("manifest unknown"))
		}
	})

	// Then: it fails out on the acceptor rather than waiting for a success that
	// is never coming.
	if got != waiterFailure {
		t.Fatalf("FunctionUpdated waiter = %q, want %q", got, waiterFailure)
	}
}

func TestWaiterFunctionUpdated_returnsImmediatelyForASynchronousUpdate(t *testing.T) {
	// Given: a zip function — nothing about its update is asynchronous.
	h, ls := updateStatusHandler(t, nil)
	seedActiveZipFunction(t, ls, "sync-fn")
	decodeConfig(t, updateFunctionCode(t, h, "sync-fn", map[string]any{
		"ZipFile": []byte("new deployment package"),
	}), http.StatusOK)

	// When/Then: the waiter's very first poll is enough.
	if got := runWaiter(t, h, "sync-fn", "LastUpdateStatus", "Successful", "Failed", 1, nil); got != waiterSuccess {
		t.Fatalf("FunctionUpdated waiter = %q, want %q on the first poll", got, waiterSuccess)
	}
}

func TestWaiterFunctionActive_returnsWhenCreationCompletes(t *testing.T) {
	// Given: a create whose image pull has not finished — the State half of the
	// same lifecycle, which `aws lambda wait function-active` polls.
	var onReady func(error)
	h, _ := updateStatusHandler(t, func(_ *Function, ready func(error)) { onReady = ready })
	createPendingTestFunction(t, h, "active-fn")

	// When: the waiter runs and the pull lands under it.
	got := runWaiter(t, h, "active-fn", "State", "Active", "Failed", 60, func(attempt int) {
		if attempt == 2 {
			onReady(nil)
		}
	})

	// Then: it returns on Active.
	if got != waiterSuccess {
		t.Fatalf("FunctionActive waiter = %q, want %q", got, waiterSuccess)
	}
}

func TestWaiterFunctionActive_failsWhenCreationFails(t *testing.T) {
	// Given: a create whose image cannot be pulled.
	h, _ := updateStatusHandler(t, func(_ *Function, ready func(error)) {
		ready(errors.New("no such image"))
	})
	createPendingTestFunction(t, h, "never-active-fn")

	// When/Then: the waiter fails out on State rather than polling on.
	if got := runWaiter(t, h, "never-active-fn", "State", "Active", "Failed", 1, nil); got != waiterFailure {
		t.Fatalf("FunctionActive waiter = %q, want %q", got, waiterFailure)
	}
}

func TestPublishVersion_snapshotReportsSuccessful(t *testing.T) {
	// Given: a function whose update has settled.
	h, ls := updateStatusHandler(t, nil)
	seedActiveZipFunction(t, ls, "versioned-fn")

	// When: a version is published.
	req := withFunctionNameParam(httptest.NewRequest(http.MethodPost,
		"/2015-03-31/functions/versioned-fn/versions", nil), "versioned-fn")
	rec := httptest.NewRecorder()
	h.PublishVersion(rec, req)
	cfg := decodeConfig(t, rec, http.StatusCreated)

	// Then: the immutable snapshot reports a settled update, because nothing
	// can update it any more.
	if cfg.LastUpdateStatus != lastUpdateSuccessful {
		t.Errorf("published version LastUpdateStatus = %q, want %q", cfg.LastUpdateStatus, lastUpdateSuccessful)
	}
}

func TestPublishVersion_refusedWhileAnUpdateIsInProgress(t *testing.T) {
	// Given: an image function whose update pull is still running.
	var onReady func(error)
	h, ls := updateStatusHandler(t, func(_ *Function, ready func(error)) { onReady = ready })
	seedActiveImageFunction(t, ls, "mid-update-fn", "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v1")
	decodeConfig(t, updateFunctionCode(t, h, "mid-update-fn", map[string]any{
		"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/demo:v2",
	}), http.StatusOK)

	// When: a version is published before it settles.
	req := withFunctionNameParam(httptest.NewRequest(http.MethodPost,
		"/2015-03-31/functions/mid-update-fn/versions", nil), "mid-update-fn")
	rec := httptest.NewRecorder()
	h.PublishVersion(rec, req)

	// Then: a version cannot snapshot a function mid-update.
	if rec.Code != http.StatusConflict {
		t.Fatalf("PublishVersion status = %d, want 409; body: %s", rec.Code, rec.Body.String())
	}
	onReady(nil)
}
