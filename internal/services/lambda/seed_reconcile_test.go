package lambda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

// fakeDockerServer returns an httptest.Server that stubs the Docker Engine API
// endpoints used by ensureImage (ImageExists + PullImage). failImages is the
// set of image names for which PullImage returns a 500.
func fakeDockerServer(t *testing.T, failImages map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// ImageExists: GET /v1.45/images/{image}/json
		case r.Method == http.MethodGet && len(r.URL.Path) > len("/v1.45/images/") && r.URL.Path != "/v1.45/images/json":
			// Return 200 = image exists. ensureImage will skip pull.
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"Id": "sha256:fake"})

		// PullImage: POST /v1.45/images/create
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/images/create":
			img := r.URL.Query().Get("fromImage")
			if failImages[img] {
				http.Error(w, "pull failed", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"Pull complete"}` + "\n"))

		// PruneDanglingImages: POST /v1.45/images/prune
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/images/prune":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ImagesDeleted":null,"SpaceReclaimed":0}`))

		default:
			http.Error(w, "unexpected request: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
}

func TestSeedPersistedFunctionImages_ReconcilesPendingToActive(t *testing.T) {
	// Given: a function persisted in Pending state with a valid runtime.
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clock.New())
	fn := &Function{
		Name:            "pending-fn",
		ARN:             "arn:aws:lambda:us-east-1:000000000000:function:pending-fn",
		Runtime:         "nodejs20.x",
		State:           "Pending",
		StateReason:     "The function is being created.",
		StateReasonCode: "Creating",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed: %v", aerr)
	}

	// And: a fake Docker server where the image already exists.
	srv := fakeDockerServer(t, nil)
	defer srv.Close()

	dc := docker.NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
	cr := &ContainerRuntime{docker: dc, logger: zap.NewNop(), cfg: &config.Config{Region: "us-east-1"}}

	svc := &Service{ls: ls, log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda")}

	// When: seedPersistedFunctionImages runs (as it does on startup).
	svc.seedPersistedFunctionImages(cr)

	// Then: the function transitions to Active.
	got, aerr := ls.getFunction(context.Background(), "pending-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if got.State != "Active" {
		t.Errorf("State = %q, want %q", got.State, "Active")
	}
	if got.StateReason != "" {
		t.Errorf("StateReason = %q, want empty", got.StateReason)
	}
	if got.StateReasonCode != "" {
		t.Errorf("StateReasonCode = %q, want empty", got.StateReasonCode)
	}
}

func TestSeedPersistedFunctionImages_DoesNotTouchActiveFunction(t *testing.T) {
	// Given: an Active function.
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clock.New())
	fn := &Function{
		Name:    "active-fn",
		ARN:     "arn:aws:lambda:us-east-1:000000000000:function:active-fn",
		Runtime: "nodejs20.x",
		State:   "Active",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed: %v", aerr)
	}

	srv := fakeDockerServer(t, nil)
	defer srv.Close()

	dc := docker.NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
	cr := &ContainerRuntime{docker: dc, logger: zap.NewNop(), cfg: &config.Config{Region: "us-east-1"}}

	svc := &Service{ls: ls, log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda")}

	// When: reconciliation runs.
	svc.seedPersistedFunctionImages(cr)

	// Then: the function remains Active (no state change).
	got, aerr := ls.getFunction(context.Background(), "active-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if got.State != "Active" {
		t.Errorf("State = %q, want %q", got.State, "Active")
	}
}

func TestSeedPersistedFunctionImages_ReconcilesPendingToFailed(t *testing.T) {
	// Given: a Pending function whose image cannot be pulled.
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clock.New())
	fn := &Function{
		Name:            "fail-fn",
		ARN:             "arn:aws:lambda:us-east-1:000000000000:function:fail-fn",
		Runtime:         "nodejs20.x",
		PackageType:     "Image",
		ImageUri:        "my-custom-image:latest",
		State:           "Pending",
		StateReason:     "The function is being created.",
		StateReasonCode: "Creating",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed: %v", aerr)
	}

	// Docker server that fails pulls for this image but says image doesn't exist.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && len(r.URL.Path) > len("/v1.45/images/"):
			// Image does not exist locally.
			http.Error(w, "not found", http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/images/create":
			// Pull fails.
			http.Error(w, "pull failed", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	dc := docker.NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
	cr := &ContainerRuntime{docker: dc, logger: zap.NewNop(), cfg: &config.Config{Region: "us-east-1"}}

	svc := &Service{ls: ls, log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda")}

	// When: reconciliation runs.
	svc.seedPersistedFunctionImages(cr)

	// Then: the function transitions to Failed.
	got, aerr := ls.getFunction(context.Background(), "fail-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if got.State != "Failed" {
		t.Errorf("State = %q, want %q", got.State, "Failed")
	}
	if got.StateReasonCode != "ImagePullError" {
		t.Errorf("StateReasonCode = %q, want %q", got.StateReasonCode, "ImagePullError")
	}
}

func TestSeedPersistedFunctionImages_ReconcilesCrossRegion(t *testing.T) {
	// Given: a Pending function in ap-southeast-2, while default region is us-east-1.
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clock.New())
	fn := &Function{
		Name:            "cross-region-fn",
		ARN:             "arn:aws:lambda:ap-southeast-2:000000000000:function:cross-region-fn",
		Runtime:         "nodejs20.x",
		State:           "Pending",
		StateReason:     "The function is being created.",
		StateReasonCode: "Creating",
	}
	// Store it under ap-southeast-2 region key.
	regionCtx := middleware.ContextWithRegion(context.Background(), "ap-southeast-2")
	if aerr := ls.putFunction(regionCtx, fn); aerr != nil {
		t.Fatalf("seed: %v", aerr)
	}

	// Verify it's NOT visible under default region.
	defaultFns, _ := ls.listFunctions(context.Background())
	if len(defaultFns) != 0 {
		t.Fatalf("expected 0 functions in default region, got %d", len(defaultFns))
	}

	srv := fakeDockerServer(t, nil)
	defer srv.Close()

	dc := docker.NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
	cr := &ContainerRuntime{docker: dc, logger: zap.NewNop(), cfg: &config.Config{Region: "us-east-1"}}

	svc := &Service{ls: ls, log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda")}

	// When: reconciliation runs.
	svc.seedPersistedFunctionImages(cr)

	// Then: the function transitions to Active (reconciliation found it across regions).
	got, aerr := ls.getFunction(regionCtx, "cross-region-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if got.State != "Active" {
		t.Errorf("State = %q, want %q", got.State, "Active")
	}
	if got.StateReason != "" {
		t.Errorf("StateReason = %q, want empty", got.StateReason)
	}
}

// Startup reconciliation lists functions once, then pulls images, which can
// take a long time while requests are already being served. If the listed
// generation is deleted and replaced, its pull must not transition the new
// record; reconciliation must arrange the replacement's own pull instead.
func TestSeedPersistedFunctionImages_deletedAndRecreatedDuringPull(t *testing.T) {
	// Given: a Pending function persisted from a previous session,
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clock.New())
	fn := &Function{
		Name:    "pending-del-fn",
		ARN:     "arn:aws:lambda:us-east-1:000000000000:function:pending-del-fn",
		Runtime: "nodejs20.x",
		State:   "Pending",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed: %v", aerr)
	}

	// and a Docker server that blocks the image check until released, so the
	// pull window is under test control.
	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1.45/images/") && r.URL.Path != "/v1.45/images/json" {
			once.Do(func() { close(reached) })
			<-release
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"Id": "sha256:fake"})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dc := docker.NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
	cr := &ContainerRuntime{docker: dc, logger: zap.NewNop(), cfg: &config.Config{Region: "us-east-1"}}
	log := serviceutil.NewServiceLogger(zap.NewNop(), "lambda")
	prewarmed := make(chan functionImagePullGeneration, 1)
	h := &Handler{ls: ls, log: log}
	h.prewarmer = func(fn *Function, ready func(error)) {
		generation, err := imagePullGenerationForFunction(fn)
		if err != nil {
			t.Errorf("resolve replacement generation: %v", err)
			ready(err)
			return
		}
		prewarmed <- generation
		ready(nil)
	}
	svc := &Service{ls: ls, log: log, handler: h}

	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.seedPersistedFunctionImages(cr)
	}()

	// When: the function is deleted while the image check is in flight,
	<-reached
	if aerr := ls.deleteFunction(context.Background(), "pending-del-fn"); aerr != nil {
		t.Fatalf("delete: %v", aerr)
	}
	recreated := &Function{
		Name:          "pending-del-fn",
		ARN:           "arn:aws:lambda:us-east-1:000000000000:function:pending-del-fn",
		Runtime:       "nodejs22.x",
		PackageType:   "Zip",
		Architectures: []string{"x86_64"},
		State:         "Pending",
		RevisionId:    "recreated-revision",
		CreationID:    "recreated-creation",
	}
	created, aerr := ls.createFunction(context.Background(), recreated)
	if aerr != nil || !created {
		t.Fatalf("recreate: created=%v err=%v", created, aerr)
	}
	close(release)
	<-done

	// Then: the old pull cannot transition the replacement generation; startup
	// arranges the replacement's own pull, which activates it.
	select {
	case generation := <-prewarmed:
		if generation.creationID != "recreated-creation" {
			t.Fatalf("prewarmed creation = %q, want recreated-creation", generation.creationID)
		}
	default:
		t.Fatal("replacement generation was left Pending without its own pull")
	}
	got, aerr := ls.getFunction(context.Background(), "pending-del-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if got == nil || got.RevisionId != "recreated-revision" || got.State != "Active" {
		t.Fatalf("replacement = %#v, want recreated-revision/Active", got)
	}
}
