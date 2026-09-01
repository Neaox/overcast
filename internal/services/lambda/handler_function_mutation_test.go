package lambda

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
	"go.uber.org/zap"
)

type blockingStoreGet struct {
	state.Store
	namespace string
	mu        sync.Mutex
	armed     bool
	captured  chan struct{}
	release   chan struct{}
}

type functionReadBarrierStore struct {
	state.Store
	mu        sync.Mutex
	remaining int
	arrived   chan struct{}
	release   chan struct{}
}

func newFunctionReadBarrierStore(store state.Store, readers int) *functionReadBarrierStore {
	return &functionReadBarrierStore{
		Store:     store,
		remaining: readers,
		arrived:   make(chan struct{}),
		release:   make(chan struct{}),
	}
}

func (s *functionReadBarrierStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	value, found, err := s.Store.Get(ctx, namespace, key)
	s.mu.Lock()
	block := namespace == nsFunctions && s.remaining > 0
	if block {
		s.remaining--
		if s.remaining == 0 {
			close(s.arrived)
		}
	}
	s.mu.Unlock()
	if block {
		select {
		case <-s.release:
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
	return value, found, err
}

func newBlockingStoreGet(store state.Store, namespace string) *blockingStoreGet {
	return &blockingStoreGet{
		Store:     store,
		namespace: namespace,
		captured:  make(chan struct{}),
		release:   make(chan struct{}),
	}
}

func (s *blockingStoreGet) arm() {
	s.mu.Lock()
	s.armed = true
	s.mu.Unlock()
}

func (s *blockingStoreGet) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	value, found, err := s.Store.Get(ctx, namespace, key)
	s.mu.Lock()
	block := s.armed && namespace == s.namespace
	if block {
		s.armed = false
	}
	s.mu.Unlock()
	if block {
		close(s.captured)
		select {
		case <-s.release:
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
	return value, found, err
}

func mutationRaceHandler(t *testing.T, blockedNamespace ...string) (*Handler, *lambdaStore, *blockingStoreGet) {
	t.Helper()
	clk := clock.NewMock()
	namespace := nsFunctions
	if len(blockedNamespace) > 0 {
		namespace = blockedNamespace[0]
	}
	store := newBlockingStoreGet(state.NewMemoryStore(), namespace)
	ls := newLambdaStore(store, "us-east-1", clk)
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)
	h := &Handler{
		cfg:      &config.Config{Region: "us-east-1"},
		log:      serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk:      clk,
		ls:       ls,
		runtimes: newRuntimeRegistry(nil),
		tracker:  tracker,
	}
	fn := &Function{
		Name:         "race-fn",
		ARN:          "arn:aws:lambda:us-east-1:000000000000:function:race-fn",
		Runtime:      "nodejs22.x",
		Handler:      "index.handler",
		MemorySize:   128,
		Timeout:      3,
		State:        "Active",
		RevisionId:   "old-revision",
		CodeS3Bucket: "old-bucket",
		CodeS3Key:    "old-key",
	}
	fn.setCode([]byte("old deployment package"))
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed function: %s", aerr.Message)
	}
	return h, ls, store
}

func commitCodeUpdate(t *testing.T, ls *lambdaStore, code []byte) {
	t.Helper()
	fn, changed, aerr := ls.mutateFunction(context.Background(), "race-fn", func(current *Function) (bool, *protocol.AWSError) {
		current.setCode(code)
		current.CodeS3Bucket = ""
		current.CodeS3Key = ""
		current.ImageUri = ""
		current.RevisionId = "code-revision"
		return true, nil
	})
	if aerr != nil || !changed || fn == nil {
		t.Fatalf("commit code update: fn=%v changed=%v err=%v", fn, changed, aerr)
	}
}

func waitForCapturedStoreRead(t *testing.T, store *blockingStoreGet) {
	t.Helper()
	select {
	case <-store.captured:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stale function snapshot")
	}
}

func assertLatestCode(t *testing.T, ls *lambdaStore, want []byte) *Function {
	t.Helper()
	fn, aerr := ls.getFunction(context.Background(), "race-fn")
	if aerr != nil || fn == nil {
		t.Fatalf("get function: fn=%v err=%v", fn, aerr)
	}
	if fn.CodeS3Bucket != "" || fn.CodeS3Key != "" {
		t.Fatalf("stale S3 source restored: bucket=%q key=%q", fn.CodeS3Bucket, fn.CodeS3Key)
	}
	if fn.CodeHash != codeHashOf(want) {
		t.Fatalf("CodeHash = %q, want %q", fn.CodeHash, codeHashOf(want))
	}
	if aerr := ls.loadFunctionCode(context.Background(), fn); aerr != nil {
		t.Fatalf("load function code: %s", aerr.Message)
	}
	if !bytes.Equal(fn.CodeZip, want) {
		t.Fatalf("CodeZip = %q, want %q", fn.CodeZip, want)
	}
	return fn
}

func TestUpdateFunctionConfiguration_concurrentCodeUpdate(t *testing.T) {
	// Given: configuration update has read an old function snapshot and is blocked.
	h, ls, store := mutationRaceHandler(t)
	body, _ := json.Marshal(map[string]any{"Description": "new description"})
	req := withFunctionNameParam(httptest.NewRequest(http.MethodPut, "/2015-03-31/functions/race-fn/configuration", bytes.NewReader(body)), "race-fn")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	store.arm()
	go func() {
		defer close(done)
		h.UpdateFunctionConfiguration(rec, req)
	}()
	waitForCapturedStoreRead(t, store)

	// When: a code update commits before the configuration request resumes.
	newCode := []byte("new deployment package")
	commitCodeUpdate(t, ls, newCode)
	close(store.release)
	<-done

	// Then: both updates survive; configuration cannot restore stale code state.
	if rec.Code != http.StatusOK {
		t.Fatalf("UpdateFunctionConfiguration status = %d, body = %s", rec.Code, rec.Body.String())
	}
	fn := assertLatestCode(t, ls, newCode)
	if fn.Description != "new description" {
		t.Fatalf("Description = %q, want new description", fn.Description)
	}
	if fn.RevisionId == "old-revision" || fn.RevisionId == "" {
		t.Fatalf("RevisionId = %q, want a revision newer than the stale snapshot", fn.RevisionId)
	}
}

func TestCreateFunction_concurrentSameName(t *testing.T) {
	// Given: two CreateFunction requests have both observed the same name as absent.
	clk := clock.NewMock()
	store := newFunctionReadBarrierStore(state.NewMemoryStore(), 2)
	ls := newLambdaStore(store, "us-east-1", clk)
	h := &Handler{
		cfg: &config.Config{
			Region:    "us-east-1",
			AccountID: "000000000000",
		},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clk,
		ls:  ls,
	}
	createBody, _ := json.Marshal(map[string]any{
		"FunctionName": "same-name",
		"Runtime":      "nodejs22.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code":         map[string]any{"ZipFile": []byte("code")},
	})
	recorders := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	done := make(chan struct{}, len(recorders))
	for _, rec := range recorders {
		go func(rec *httptest.ResponseRecorder) {
			req := httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", bytes.NewReader(createBody))
			h.CreateFunction(rec, req)
			done <- struct{}{}
		}(rec)
	}
	select {
	case <-store.arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for both creates to observe absence")
	}

	// When: both requests proceed to commit.
	close(store.release)
	for range recorders {
		<-done
	}

	// Then: exactly one creates the function and the other receives AWS's
	// ResourceConflictException instead of overwriting it with a second 201.
	var created, conflicted int
	for _, rec := range recorders {
		switch rec.Code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode conflict: %v", err)
			}
			if body["__type"] != "ResourceConflictException" {
				t.Fatalf("conflict type = %v, want ResourceConflictException", body["__type"])
			}
			conflicted++
		default:
			t.Fatalf("CreateFunction status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}
	if created != 1 || conflicted != 1 {
		t.Fatalf("created=%d conflicted=%d, want one of each", created, conflicted)
	}
}

func TestPutFunctionConcurrency_concurrentCodeUpdate(t *testing.T) {
	// Given: reserved-concurrency update has read an old function snapshot and is blocked.
	h, ls, store := mutationRaceHandler(t)
	body, _ := json.Marshal(map[string]any{"ReservedConcurrentExecutions": 2})
	req := withFunctionNameParam(httptest.NewRequest(http.MethodPut, "/2017-10-31/functions/race-fn/concurrency", bytes.NewReader(body)), "race-fn")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	store.arm()
	go func() {
		defer close(done)
		h.PutFunctionConcurrency(rec, req)
	}()
	waitForCapturedStoreRead(t, store)

	// When: a code update commits before the concurrency request resumes.
	newCode := []byte("new deployment package")
	commitCodeUpdate(t, ls, newCode)
	close(store.release)
	<-done

	// Then: both updates survive; state-only mutation preserves the code revision.
	if rec.Code != http.StatusOK {
		t.Fatalf("PutFunctionConcurrency status = %d, body = %s", rec.Code, rec.Body.String())
	}
	fn := assertLatestCode(t, ls, newCode)
	if fn.ReservedConcurrency == nil || *fn.ReservedConcurrency != 2 {
		t.Fatalf("ReservedConcurrency = %v, want 2", fn.ReservedConcurrency)
	}
	if fn.RevisionId != "code-revision" {
		t.Fatalf("RevisionId = %q, want code-revision", fn.RevisionId)
	}
}

func TestPutFunctionSource_concurrentCodeUpdate(t *testing.T) {
	// Given: the source editor has loaded an old multi-file archive and is
	// blocked before it can package and commit its patch.
	h, ls, store := mutationRaceHandler(t, nsFunctionCode)
	oldCode := zipWith(t, "index.js", "exports.handler = async () => 'old'")
	oldCode, err := patchZipFile(oldCode, "helper.js", "module.exports = 'old'")
	if err != nil {
		t.Fatalf("build multi-file source archive: %v", err)
	}
	_, _, aerr := ls.mutateFunction(context.Background(), "race-fn", func(current *Function) (bool, *protocol.AWSError) {
		current.setCode(oldCode)
		current.SourceCode = "exports.handler = async () => 'old'"
		current.SourceFilename = "index.js"
		current.RevisionId = "source-snapshot"
		return true, nil
	})
	if aerr != nil {
		t.Fatalf("seed source archive: %s", aerr.Message)
	}
	sourceBody, _ := json.Marshal(map[string]any{
		"source":   "exports.handler = async () => 'stale editor patch'",
		"filename": "index.js",
	})
	sourceReq := withFunctionNameParam(httptest.NewRequest(http.MethodPut, "/_overcast/lambda/functions/race-fn/source", bytes.NewReader(sourceBody)), "race-fn")
	sourceRec := httptest.NewRecorder()
	done := make(chan struct{})
	store.arm()
	go func() {
		defer close(done)
		h.PutFunctionSource(sourceRec, sourceReq)
	}()
	waitForCapturedStoreRead(t, store)

	// When: UpdateFunctionCode commits a newer deployment while the editor is blocked.
	newCode := zipWith(t, "index.js", "exports.handler = async () => 'new deployment'")
	codeBody, _ := json.Marshal(map[string]any{"ZipFile": newCode})
	codeReq := withFunctionNameParam(httptest.NewRequest(http.MethodPut, "/2015-03-31/functions/race-fn/code", bytes.NewReader(codeBody)), "race-fn")
	codeRec := httptest.NewRecorder()
	h.UpdateFunctionCode(codeRec, codeReq)
	if codeRec.Code != http.StatusOK {
		t.Fatalf("UpdateFunctionCode status = %d, body = %s", codeRec.Code, codeRec.Body.String())
	}
	committed, aerr := ls.getFunction(context.Background(), "race-fn")
	if aerr != nil || committed == nil {
		t.Fatalf("get committed code revision: fn=%v err=%v", committed, aerr)
	}
	codeRevision := committed.RevisionId
	close(store.release)
	<-done

	// Then: the stale editor commit is rejected and cannot restore its archive,
	// revision, or source metadata over the newer deployment.
	if sourceRec.Code != http.StatusConflict {
		t.Fatalf("PutFunctionSource status = %d, want 409; body = %s", sourceRec.Code, sourceRec.Body.String())
	}
	var conflict map[string]any
	if err := json.Unmarshal(sourceRec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict["__type"] != "ResourceConflictException" {
		t.Fatalf("conflict type = %v, want ResourceConflictException", conflict["__type"])
	}
	fn := assertLatestCode(t, ls, newCode)
	if fn.RevisionId != codeRevision {
		t.Fatalf("RevisionId = %q, want committed code revision %q", fn.RevisionId, codeRevision)
	}
	if fn.SourceCode != "" || fn.SourceFilename != "" {
		t.Fatalf("stale source metadata restored: source=%q filename=%q", fn.SourceCode, fn.SourceFilename)
	}
}
