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

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
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

// TestCreateFunction_prewarmerOnReadyMergesIntoConcurrentRevision covers the
// sibling lost-update: the function is revised while the create-time pull is
// still running (UpdateFunctionCode/Configuration bump RevisionId without
// touching State). The completion callback must land the Pending→Active
// transition on the revised record, not write the create-time snapshot over
// it.
func TestCreateFunction_prewarmerOnReadyMergesIntoConcurrentRevision(t *testing.T) {
	// Given: a function created with its image pull held open.
	h, _ := lifecycleTestHandler(t)
	var onReady func(err error)
	h.prewarmer = func(fn *Function, ready func(err error)) { onReady = ready }
	createPendingTestFunction(t, h, "revised-fn")

	// When: the function is revised mid-pull,
	ctx := context.Background()
	fn, aerr := h.ls.getFunction(ctx, "revised-fn")
	if aerr != nil || fn == nil {
		t.Fatalf("getFunction: %v, %v", fn, aerr)
	}
	fn.Description = "revised while the pull ran"
	fn.RevisionId = "revised-revision"
	if aerr := h.ls.putFunction(ctx, fn); aerr != nil {
		t.Fatalf("putFunction: %v", aerr)
	}

	// and the pull completes.
	onReady(nil)

	// Then: the revision survives, and the state transition still lands on it.
	got, aerr := h.ls.getFunction(ctx, "revised-fn")
	if aerr != nil || got == nil {
		t.Fatalf("getFunction: %v, %v", got, aerr)
	}
	if got.Description != "revised while the pull ran" || got.RevisionId != "revised-revision" {
		t.Errorf("Description = %q, RevisionId = %q — pull completion clobbered the concurrent revision", got.Description, got.RevisionId)
	}
	if got.State != "Active" {
		t.Errorf("State = %q, want Active", got.State)
	}
}
