package lambda

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

// failOnNthSet wraps a Store and makes the Nth Set call return a simulated error,
// reproducing SQLite "database is locked" contention during heavy CDK asset uploads.
type failOnNthSet struct {
	state.Store
	mu     sync.Mutex
	count  int
	failOn int // 1-indexed: which Set call to fail
}

func (s *failOnNthSet) Set(ctx context.Context, ns, key, value string) error {
	s.mu.Lock()
	s.count++
	n := s.count
	s.mu.Unlock()
	if n == s.failOn {
		return errors.New("simulated: database is locked")
	}
	return s.Store.Set(ctx, ns, key, value)
}

// TestCreateFunction_prewarmerOnReadyRetryPutFunctionOnContention verifies that
// when the background onReady callback's putFunction call fails (e.g. SQLite
// contention from concurrent CDK asset uploads), the function still transitions
// from Pending to Active via a retry rather than being silently dropped.
func TestCreateFunction_prewarmerOnReadyRetryPutFunctionOnContention(t *testing.T) {
	// Given: a store where the second Set call (onReady's putFunction) fails once,
	// simulating SQLite contention during CDK asset uploads.
	clk := clock.NewMock()
	inner := state.NewMemoryStore()
	store := &failOnNthSet{Store: inner, failOn: 2}
	ls := newLambdaStore(store, "us-east-1", clk)

	h := &Handler{
		cfg: &config.Config{Region: "us-east-1"},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clk,
		ls:  ls,
		// prewarmer calls onReady synchronously with no error (image already cached).
		prewarmer: func(fn *Function, onReady func(err error)) {
			onReady(nil)
		},
	}

	body, _ := json.Marshal(map[string]any{
		"FunctionName": "contended-fn",
		"Runtime":      "nodejs22.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code":         map[string]any{},
	})
	req := httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// When: CreateFunction is called (prewarmer fires synchronously before returning).
	h.CreateFunction(w, req)

	// Then: 201 response.
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateFunction status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	// And: despite the store contention on the first putFunction attempt inside
	// onReady, the function eventually transitions to Active after a retry.
	got, aerr := ls.getFunction(context.Background(), "contended-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if got.State != "Active" {
		t.Errorf("State = %q, want Active (function stuck in %q — onReady dropped state update under contention)", got.State, got.State)
	}
	if got.StateReason != "" {
		t.Errorf("StateReason = %q, want empty", got.StateReason)
	}
}

const invalidLambdaRuntimeMessage = "Value not-a-runtime at 'runtime' failed to satisfy constraint: Member must satisfy enum value set: [nodejs, nodejs4.3, nodejs6.10, nodejs8.10, nodejs10.x, nodejs12.x, nodejs14.x, nodejs16.x, nodejs18.x, nodejs20.x, nodejs22.x, nodejs24.x, java8, java8.al2, java11, java17, java21, java25, python2.7, python3.6, python3.7, python3.8, python3.9, python3.10, python3.11, python3.12, python3.13, python3.14, dotnetcore1.0, dotnetcore2.0, dotnetcore2.1, dotnetcore3.1, dotnet6, dotnet8, dotnet10, nodejs4.3-edge, go1.x, ruby2.5, ruby2.7, ruby3.2, ruby3.3, ruby3.4, ruby4.0, provided, provided.al2, provided.al2023, java8.al2023, java11.al2023, java17.al2023] or be a valid ARN"

func TestCreateFunction_runtimeValidationDoesNotPersist(t *testing.T) {
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	h := &Handler{
		cfg: &config.Config{Region: "us-east-1", AccountID: "000000000000"},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clk,
		ls:  ls,
	}
	body, _ := json.Marshal(map[string]any{
		"FunctionName": "invalid-runtime",
		"Runtime":      "not-a-runtime",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code":         map[string]any{"ZipFile": []byte("code")},
	})
	rec := httptest.NewRecorder()
	h.CreateFunction(rec, httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", bytes.NewReader(body)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["__type"] != "InvalidParameterValueException" || got["message"] != invalidLambdaRuntimeMessage {
		t.Fatalf("response = %#v, want type InvalidParameterValueException and message %q", got, invalidLambdaRuntimeMessage)
	}
	if rec.Header().Get("x-amzn-requestid") == "" {
		t.Fatal("x-amzn-requestid is empty")
	}
	stored, aerr := ls.getFunction(context.Background(), "invalid-runtime")
	if aerr != nil || stored != nil {
		t.Fatalf("invalid function persisted: fn=%v err=%v", stored, aerr)
	}
}

func TestCreateFunction_modeledRuntimeWithoutExecutionImageIsUnsupported(t *testing.T) {
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	h := &Handler{
		cfg: &config.Config{Region: "us-east-1", AccountID: "000000000000"},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clk,
		ls:  ls,
	}
	body, _ := json.Marshal(map[string]any{
		"FunctionName": "modeled-runtime",
		"Runtime":      "python3.14",
		"Handler":      "lambda_function.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code":         map[string]any{"ZipFile": []byte("code")},
	})
	rec := httptest.NewRecorder()
	h.CreateFunction(rec, httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", bytes.NewReader(body)))

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("x-emulator-unsupported") != "true" {
		t.Fatalf("x-emulator-unsupported = %q, want true", rec.Header().Get("x-emulator-unsupported"))
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["__type"] != "NotImplemented" || got["message"] != "This operation is not yet emulated. Check docs/services/ for the support matrix." {
		t.Fatalf("response = %#v, want AWS-shaped NotImplemented", got)
	}
	stored, aerr := ls.getFunction(context.Background(), "modeled-runtime")
	if aerr != nil || stored != nil {
		t.Fatalf("unsupported function persisted: fn=%v err=%v", stored, aerr)
	}
}

func TestCreateFunction_runtimeVersionARNIsUnsupportedWithoutPersistence(t *testing.T) {
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	h := &Handler{
		cfg: &config.Config{Region: "us-east-1", AccountID: "000000000000"},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clk,
		ls:  ls,
	}
	body, _ := json.Marshal(map[string]any{
		"FunctionName": "runtime-version-arn",
		"Runtime":      "arn:aws:lambda:us-east-1::runtime:0123456789abcdef",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code":         map[string]any{"ZipFile": []byte("code")},
	})
	rec := httptest.NewRecorder()
	h.CreateFunction(rec, httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", bytes.NewReader(body)))

	if rec.Code != http.StatusNotImplemented || rec.Header().Get("x-emulator-unsupported") != "true" {
		t.Fatalf("status=%d unsupported=%q, want 501/true; body: %s", rec.Code, rec.Header().Get("x-emulator-unsupported"), rec.Body.String())
	}
	stored, aerr := ls.getFunction(context.Background(), "runtime-version-arn")
	if aerr != nil || stored != nil {
		t.Fatalf("runtime ARN function persisted: fn=%v err=%v", stored, aerr)
	}
}

func TestUpdateFunctionConfiguration_runtimeValidation(t *testing.T) {
	tests := []struct {
		name        string
		runtime     string
		wantStatus  int
		wantMessage string
	}{
		{name: "invalid runtime", runtime: "not-a-runtime", wantStatus: http.StatusBadRequest, wantMessage: invalidLambdaRuntimeMessage},
		{name: "valid runtime ARN unavailable", runtime: "arn:aws:lambda:us-east-1::runtime:0123456789abcdef", wantStatus: http.StatusNotImplemented},
		{name: "modeled runtime unavailable", runtime: "python3.14", wantStatus: http.StatusNotImplemented},
		{name: "supported runtime", runtime: "nodejs24.x", wantStatus: http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := lifecycleTestHandler(t)
			seedLifecycleFunction(t, h, nil)
			rec := updateFunctionConfiguration(t, h, "lifecycle-fn", map[string]any{
				"Runtime":     tc.runtime,
				"Description": "updated",
			})
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantMessage != "" {
				var got map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if got["__type"] != "InvalidParameterValueException" || got["message"] != tc.wantMessage {
					t.Fatalf("response = %#v, want exact InvalidParameterValueException", got)
				}
			}
			if tc.wantStatus == http.StatusNotImplemented && rec.Header().Get("x-emulator-unsupported") != "true" {
				t.Fatalf("x-emulator-unsupported = %q, want true", rec.Header().Get("x-emulator-unsupported"))
			}
			stored, aerr := h.ls.getFunction(context.Background(), "lifecycle-fn")
			if aerr != nil || stored == nil {
				t.Fatalf("get function: fn=%v err=%v", stored, aerr)
			}
			if tc.wantStatus == http.StatusOK {
				if stored.Runtime != tc.runtime || stored.Description != "updated" {
					t.Fatalf("supported update not applied: runtime=%q description=%q", stored.Runtime, stored.Description)
				}
			} else if stored.Runtime != "nodejs22.x" || stored.Description != "" {
				t.Fatalf("rejected update mutated function: runtime=%q description=%q", stored.Runtime, stored.Description)
			}
		})
	}
}

func TestCreateFunction_prewarmerPublishedDuringCreate(t *testing.T) {
	// Given: CreateFunction is already in flight but blocked on its initial
	// existence read while Docker initialization publishes the prewarmer.
	clk := clock.NewMock()
	store := newFunctionReadBarrierStore(state.NewMemoryStore(), 1)
	ls := newLambdaStore(store, "us-east-1", clk)
	h := &Handler{
		cfg: &config.Config{Region: "us-east-1", AccountID: "000000000000"},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clk,
		ls:  ls,
	}
	body, _ := json.Marshal(map[string]any{
		"FunctionName": "published-prewarmer",
		"Runtime":      "nodejs22.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code":         map[string]any{},
	})
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.CreateFunction(rec, httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", bytes.NewReader(body)))
	}()
	select {
	case <-store.arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("CreateFunction did not reach the publication barrier")
	}

	// When: initialization publishes a callback that itself republishes the
	// next callback. The nested publication proves no handler mutex is held
	// while invoking the stable local callback.
	called := make(chan struct{})
	h.setPrewarmer(func(fn *Function, ready func(error)) {
		h.setPrewarmer(func(*Function, func(error)) {})
		close(called)
		ready(nil)
	})
	close(store.release)

	// Then: this create observes the published callback and completes without
	// deadlocking publication against callback invocation.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CreateFunction deadlocked with prewarmer publication")
	}
	select {
	case <-called:
	default:
		t.Fatal("CreateFunction missed prewarmer published during the request")
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateFunction status = %d, body = %s", rec.Code, rec.Body.String())
	}
	fn, aerr := ls.getFunction(context.Background(), "published-prewarmer")
	if aerr != nil || fn == nil || fn.State != "Active" {
		t.Fatalf("function = %#v, err=%v; want Active", fn, aerr)
	}
}

func TestInvokeLayerCheck_foreignAccountCachedLayer(t *testing.T) {
	// Given: a function references a real AWS-managed layer ARN from a foreign
	// account, and the documented friendly-name layer zip exists in the cache.
	clk := clock.NewMock()
	cacheDir := t.TempDir()
	writeTestLayerZip(t, filepath.Join(cacheDir, "AWS-Parameters-and-Secrets-Lambda-Extension_11.zip"))
	ls := newLambdaStore(state.NewMemoryStore(), "ap-southeast-2", clk)
	h := &Handler{
		cfg: &config.Config{
			Region:              "ap-southeast-2",
			AccountID:           "000000000000",
			LambdaLayerCacheDir: cacheDir,
		},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clk,
		ls:  ls,
	}
	fn := &Function{
		Name: "uses-managed-layer",
		Layers: []LayerVersionLink{{
			ARN: "arn:aws:lambda:ap-southeast-2:665172237481:layer:AWS-Parameters-and-Secrets-Lambda-Extension:11",
		}},
	}

	// When: invoke-time layer existence validation runs before the container cold start.
	missing := h.checkLayerVersionsExist(context.Background(), fn)

	// Then: the cached foreign-account layer is accepted instead of failing before
	// layer content resolution can use the documented cache.
	if missing != "" {
		t.Fatalf("checkLayerVersionsExist returned missing layer %q, want cached foreign-account layer accepted", missing)
	}
}

func writeTestLayerZip(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir layer cache: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("nodejs/node_modules/example/index.js")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := f.Write([]byte("module.exports = {}\n")); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write layer zip: %v", err)
	}
}

// TestCreateFunction_prewarmerOnReadySetsFailed verifies that when the image
// pull fails, the function transitions to Failed (not stays Pending).
func TestCreateFunction_prewarmerOnReadySetsFailedOnPullError(t *testing.T) {
	// Given: a standard in-memory store (no contention).
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)

	pullErr := errors.New("no such image: public.ecr.aws/lambda/nodejs:99")
	h := &Handler{
		cfg: &config.Config{Region: "us-east-1"},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clk,
		ls:  ls,
		// prewarmer calls onReady synchronously with a pull error.
		prewarmer: func(fn *Function, onReady func(err error)) {
			onReady(pullErr)
		},
	}

	body, _ := json.Marshal(map[string]any{
		"FunctionName": "fail-fn",
		"Runtime":      "nodejs22.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code":         map[string]any{},
	})
	req := httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateFunction(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("CreateFunction status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	got, aerr := ls.getFunction(context.Background(), "fail-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if got.State != "Failed" {
		t.Errorf("State = %q, want Failed", got.State)
	}
	if got.StateReasonCode != "ImagePullError" {
		t.Errorf("StateReasonCode = %q, want ImagePullError", got.StateReasonCode)
	}
}

// createPendingTestFunction drives CreateFunction for a zip-less function and
// asserts the 201. With a prewarmer wired the record is left "Pending" until
// the captured onReady fires.
func createPendingTestFunction(t *testing.T, h *Handler, name string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"FunctionName": name,
		"Runtime":      "nodejs22.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code":         map[string]any{},
	})
	req := httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CreateFunction(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateFunction status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
}

// TestDeleteFunction_duringImagePull_staysDeleted reproduces the compat
// lambda-crud/DeleteFunction flake (#414): CreateFunction kicks off a
// background image pull, and the function is deleted before the pull
// completes. The completion callback used to persist the create-time snapshot
// unconditionally, resurrecting the deleted record as "Active" — exactly what
// the python-sdk and cli suites observed ("function still exists" on the
// post-delete verification read).
func TestDeleteFunction_duringImagePull_staysDeleted(t *testing.T) {
	// Given: a handler whose image pull completes only when the test fires it.
	h, _ := lifecycleTestHandler(t)
	var onReady func(err error)
	h.prewarmer = func(fn *Function, ready func(err error)) { onReady = ready }
	createPendingTestFunction(t, h, "pull-fn")

	// When: the function is deleted while the pull is still in flight,
	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/pull-fn", nil)
	req = withFunctionNameParam(req, "pull-fn")
	rec := httptest.NewRecorder()
	h.DeleteFunction(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteFunction status = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// and the pull then completes.
	onReady(nil)

	// Then: the delete stays deleted.
	got, aerr := h.ls.getFunction(context.Background(), "pull-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if got != nil {
		t.Fatalf("function exists in state %q after DeleteFunction — pull completion re-persisted the deleted record", got.State)
	}
}

// A completion callback may only transition the exact image and platform it
// pulled. A newer Pending deployment needs its own pull before becoming Active.
func TestCreateFunction_prewarmerOnReadyImageChanged(t *testing.T) {
	// Given: a function created with its image pull held open.
	h, _ := lifecycleTestHandler(t)
	var callbacks []func(error)
	h.prewarmer = func(fn *Function, ready func(err error)) { callbacks = append(callbacks, ready) }
	createPendingTestFunction(t, h, "revised-fn")

	// When: the function changes to a different runtime image mid-pull,
	ctx := context.Background()
	fn, aerr := h.ls.getFunction(ctx, "revised-fn")
	if aerr != nil || fn == nil {
		t.Fatalf("getFunction: %v, %v", fn, aerr)
	}
	fn.Description = "revised while the pull ran"
	fn.Runtime = "nodejs20.x"
	fn.RevisionId = "revised-revision"
	if aerr := h.ls.putFunction(ctx, fn); aerr != nil {
		t.Fatalf("putFunction: %v", aerr)
	}

	// and the pull completes.
	callbacks[0](nil)

	// Then: the new image generation survives and remains Pending for its own pull.
	got, aerr := h.ls.getFunction(ctx, "revised-fn")
	if aerr != nil || got == nil {
		t.Fatalf("getFunction: %v, %v", got, aerr)
	}
	if got.Description != "revised while the pull ran" || got.RevisionId != "revised-revision" {
		t.Errorf("Description = %q, RevisionId = %q — pull completion clobbered the concurrent revision", got.Description, got.RevisionId)
	}
	if got.State != "Pending" {
		t.Errorf("State = %q, want Pending until the new image is pulled", got.State)
	}
	if len(callbacks) != 2 {
		t.Fatalf("prewarm calls = %d, want a pull for the replacement generation", len(callbacks))
	}
	callbacks[1](nil)
	got, aerr = h.ls.getFunction(ctx, "revised-fn")
	if aerr != nil || got == nil || got.State != "Active" {
		t.Fatalf("replacement after own pull = %#v, err=%v; want Active", got, aerr)
	}
}

func TestCreateFunction_prewarmerOnReadyDescriptionChanged(t *testing.T) {
	// Given: a function's image pull is held open.
	h, _ := lifecycleTestHandler(t)
	var onReady func(err error)
	h.prewarmer = func(fn *Function, ready func(err error)) { onReady = ready }
	createPendingTestFunction(t, h, "description-fn")

	// When: a description-only update rotates RevisionId without changing the
	// pulled image, platform, or creation identity.
	ctx := context.Background()
	_, changed, aerr := h.ls.mutateFunction(ctx, "description-fn", func(current *Function) (bool, *protocol.AWSError) {
		current.Description = "updated while pulling"
		current.RevisionId = "description-revision"
		return true, nil
	})
	if aerr != nil || !changed {
		t.Fatalf("update description: changed=%v err=%v", changed, aerr)
	}
	onReady(nil)

	// Then: the valid pull completion merges onto the description update.
	got, aerr := h.ls.getFunction(ctx, "description-fn")
	if aerr != nil || got == nil {
		t.Fatalf("getFunction: %v, %v", got, aerr)
	}
	if got.Description != "updated while pulling" || got.RevisionId != "description-revision" || got.State != "Active" {
		t.Fatalf("description=%q revision=%q state=%q, want updated/description-revision/Active", got.Description, got.RevisionId, got.State)
	}
}

func TestCreateFunction_prewarmerOnReadyDeletedAndRecreated(t *testing.T) {
	// Given: a create-time image pull is held open.
	h, _ := lifecycleTestHandler(t)
	var callbacks []func(error)
	h.prewarmer = func(fn *Function, ready func(err error)) { callbacks = append(callbacks, ready) }
	createPendingTestFunction(t, h, "recreated-fn")

	// When: the function is deleted and a different Pending generation is
	// created under the same name before the old pull completes.
	ctx := context.Background()
	if aerr := h.ls.deleteFunction(ctx, "recreated-fn"); aerr != nil {
		t.Fatalf("deleteFunction: %v", aerr)
	}
	recreated := &Function{
		Name:          "recreated-fn",
		ARN:           "arn:aws:lambda:us-east-1:000000000000:function:recreated-fn",
		Runtime:       "nodejs20.x",
		Handler:       "index.handler",
		PackageType:   "Zip",
		Architectures: []string{"x86_64"},
		State:         "Pending",
		RevisionId:    "recreated-revision",
		CreationID:    "recreated-creation",
	}
	created, aerr := h.ls.createFunction(ctx, recreated)
	if aerr != nil || !created {
		t.Fatalf("recreate function: created=%v err=%v", created, aerr)
	}
	callbacks[0](nil)

	// Then: completion for the deleted generation cannot activate the replacement.
	got, aerr := h.ls.getFunction(ctx, "recreated-fn")
	if aerr != nil || got == nil {
		t.Fatalf("getFunction: %v, %v", got, aerr)
	}
	if got.RevisionId != "recreated-revision" || got.State != "Pending" || len(callbacks) != 2 {
		t.Fatalf("replacement revision=%q state=%q pulls=%d, want recreated-revision/Pending/2", got.RevisionId, got.State, len(callbacks))
	}
	callbacks[1](nil)
	got, aerr = h.ls.getFunction(ctx, "recreated-fn")
	if aerr != nil || got == nil || got.State != "Active" {
		t.Fatalf("replacement after own pull = %#v, err=%v; want Active", got, aerr)
	}
}
