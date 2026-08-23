package lambda

// container_runtime_ecr_image_test.go — a PackageType=Image function whose
// image was built and pushed by CDK must actually run.
//
// CDK publishes a container asset to the ECR repository it bootstrapped, then
// writes the function's ImageUri as
// "{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}", built from
// AWS::AccountId and AWS::Region rather than read back from the repository.
// That is a real AWS wildcard domain, so pulling it as written leaves the
// machine and is refused anonymously ("no basic auth credentials") while the
// bytes sit in the registry Overcast serves, behind the credentials
// GetAuthorizationToken issues.
//
// ECR answers where its images really are (docker.ImageResolver). These tests
// pin the two halves the Lambda runtime owns: the pull goes to the resolved
// registry with its credentials, and the container is created from the
// reference the daemon stored the bytes under — creating from the requested
// one fails with "No such image" straight after a pull that worked.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

const (
	// cdkImageURI is the reference CDK synthesises for a container asset.
	cdkImageURI = "000000000000.dkr.ecr.us-east-1.amazonaws.com/cdk-hnb659fds-container-assets-000000000000-us-east-1:610a3ca0"
	// servedImageRef is where the bytes actually are: the registry container
	// Overcast runs, addressed by the host:port it published.
	servedImageRef  = "127.0.0.1:5000/cdk-hnb659fds-container-assets-000000000000-us-east-1:610a3ca0"
	servedImageName = "127.0.0.1:5000/cdk-hnb659fds-container-assets-000000000000-us-east-1"
	servedImageTag  = "610a3ca0"
	registryUser    = "AWS"
	registryPass    = "registry-password"
)

// stubImageResolver maps one requested reference onto one served reference,
// the way the ECR service maps its own registry's images. Anything else is
// returned unchanged and unauthenticated, as docker.ImageResolver requires.
type stubImageResolver struct {
	requested string
	served    docker.ImageReference
}

func (s stubImageResolver) ResolveImage(_ context.Context, image string) docker.ImageReference {
	if image == s.requested {
		return s.served
	}
	return docker.ImageReference{Ref: image}
}

func servedReference() docker.ImageReference {
	return docker.ImageReference{
		Ref: servedImageRef,
		Auth: &docker.RegistryAuth{
			Username:      registryUser,
			Password:      registryPass,
			ServerAddress: "127.0.0.1:5000",
		},
	}
}

// pullRecord is what the daemon was asked to fetch, and with whose credentials.
type pullRecord struct {
	fromImage string
	tag       string
	platform  string
	auth      docker.RegistryAuth
}

// recordingDaemon is a fake Docker Engine that records the pulls and container
// creations it is asked for. It reports every image absent, so a pull is always
// attempted, and fails container start so an acquire returns as soon as the
// create it is testing has been observed.
type recordingDaemon struct {
	*httptest.Server

	mu      sync.Mutex
	pulls   []pullRecord
	creates []docker.CreateContainerRequest
}

func newRecordingDaemon(t *testing.T) *recordingDaemon {
	t.Helper()
	d := &recordingDaemon{}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/images/create":
			d.recordPull(t, r)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"Pull complete"}` + "\n"))

		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/create":
			d.recordCreate(t, r)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "0123456789abcdef"})

		// Every image is absent until it has been pulled, so ensureImage always
		// pulls exactly once — and the inspect that follows the pull answers
		// with what a real Lambda image declares, which is what the execution
		// environment's init runs as its child.
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1.45/images/"):
			if !d.hasPulled() {
				http.Error(w, "no such image", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Os":           "linux",
				"Architecture": "amd64",
				"Config":       map[string]any{"Entrypoint": []string{"/lambda-entrypoint.sh"}, "Cmd": []string{"index.handler"}},
			})

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "0123456789abcdef"})

		// Starting is where this daemon gives up, ending the acquire just past
		// the create under test.
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start"):
			http.Error(w, "fake daemon does not start containers", http.StatusInternalServerError)

		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(d.Close)
	return d
}

func (d *recordingDaemon) recordPull(t *testing.T, r *http.Request) {
	t.Helper()
	rec := pullRecord{
		fromImage: r.URL.Query().Get("fromImage"),
		tag:       r.URL.Query().Get("tag"),
		platform:  r.URL.Query().Get("platform"),
	}
	if header := r.Header.Get("X-Registry-Auth"); header != "" {
		raw, err := base64.URLEncoding.DecodeString(header)
		if err != nil {
			t.Errorf("decode X-Registry-Auth: %v", err)
		} else if err := json.Unmarshal(raw, &rec.auth); err != nil {
			t.Errorf("unmarshal X-Registry-Auth: %v", err)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pulls = append(d.pulls, rec)
}

func (d *recordingDaemon) recordCreate(t *testing.T, r *http.Request) {
	t.Helper()
	var req docker.CreateContainerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Errorf("decode container create body: %v", err)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.creates = append(d.creates, req)
}

// hasPulled reports whether this daemon has served a pull yet, which is what
// makes an image present.
func (d *recordingDaemon) hasPulled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pulls) > 0
}

func (d *recordingDaemon) recordedPulls() []pullRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]pullRecord(nil), d.pulls...)
}

func (d *recordingDaemon) recordedCreates() []docker.CreateContainerRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]docker.CreateContainerRequest(nil), d.creates...)
}

// newDaemonContainerRuntime builds a ContainerRuntime pointed at a fake Docker
// daemon, with a resolver that serves cdkImageURI from the local registry.
func newDaemonContainerRuntime(t *testing.T, daemon *httptest.Server) *ContainerRuntime {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for runtime api: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	clk := clock.New()
	runtimeAPI, err := NewRuntimeAPIServerFromListeners(
		[]net.Listener{ln}, ln.Addr().String(), time.Second, zap.NewNop(), clk)
	if err != nil {
		t.Fatalf("runtime api server: %v", err)
	}
	t.Cleanup(func() { _ = runtimeAPI.Stop(context.Background()) })

	cfg := &config.Config{Region: "us-east-1"}
	dc := docker.NewClient("tcp://"+daemon.Listener.Addr().String(), zap.NewNop())
	cr := NewContainerRuntime(cfg, clk, dc, nil, runtimeAPI, zap.NewNop(), 1,
		serviceutil.NewInstanceDomain(state.NewMemoryStore(), nsInstance))
	cr.SetImageResolver(stubImageResolver{requested: cdkImageURI, served: servedReference()})
	return cr
}

func imageFunction() *Function {
	return &Function{
		Name:          "cdk-image-fn",
		ARN:           "arn:aws:lambda:us-east-1:000000000000:function:cdk-image-fn",
		PackageType:   "Image",
		ImageUri:      cdkImageURI,
		Architectures: []string{"x86_64"},
		MemorySize:    512,
	}
}

func TestAcquire_imageFromEmulatedECR_pullsAndRunsTheServedReference(t *testing.T) {
	// Given: a Docker daemon that holds no images, and a container runtime
	// whose resolver serves the function's ECR image from the local registry.
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)
	fn := imageFunction()

	// When: the function is acquired. The fake daemon refuses to start the
	// container, so this returns just past the create under test.
	_, err := cr.acquireContainer(context.Background(), fn, func(string) {}, initTypeOnDemand, false)
	if err == nil {
		t.Fatal("expected the fake daemon's start failure, got a running instance")
	}
	if strings.Contains(err.Error(), "pull image") {
		t.Fatalf("acquire failed at the pull, not at the fake start: %v", err)
	}

	// Then: the pull went to the registry that serves the image, with the
	// credentials that registry issued and the function's platform.
	pulls := daemon.recordedPulls()
	if len(pulls) != 1 {
		t.Fatalf("expected 1 pull, got %d: %+v", len(pulls), pulls)
	}
	got := pulls[0]
	if got.fromImage != servedImageName || got.tag != servedImageTag {
		t.Errorf("pulled %s:%s, want %s:%s", got.fromImage, got.tag, servedImageName, servedImageTag)
	}
	if got.platform != "linux/amd64" {
		t.Errorf("pull platform = %q, want linux/amd64", got.platform)
	}
	if got.auth.Username != registryUser || got.auth.Password != registryPass {
		t.Errorf("pull auth = %q/%q, want %q/%q",
			got.auth.Username, got.auth.Password, registryUser, registryPass)
	}

	// Then: the container was created from the reference the daemon stored the
	// bytes under, not the one the deploy asked for.
	creates := daemon.recordedCreates()
	if len(creates) != 1 {
		t.Fatalf("expected 1 container create, got %d", len(creates))
	}
	if image := creates[0].ContainerConfig.Image; image != servedImageRef {
		t.Errorf("container image = %q, want the served %q", image, servedImageRef)
	}

	// Then: the function still reports the image it was deployed with. The
	// rewrite is how the bytes are fetched, not a change to what was deployed.
	if fn.ImageUri != cdkImageURI {
		t.Errorf("fn.ImageUri = %q, want the deployed %q", fn.ImageUri, cdkImageURI)
	}
}

func TestPrewarmFunction_imageFromEmulatedECR_pullsTheServedReference(t *testing.T) {
	// Given: a container runtime whose resolver serves the function's ECR image.
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)
	fn := imageFunction()

	// When: CreateFunction prewarms the image.
	done := make(chan error, 1)
	cr.PrewarmFunction(fn, func(err error) { done <- err })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prewarm did not complete")
	}

	// Then: the pull went to the registry serving the image, authenticated.
	pulls := daemon.recordedPulls()
	if len(pulls) != 1 {
		t.Fatalf("expected 1 pull, got %d: %+v", len(pulls), pulls)
	}
	if pulls[0].fromImage != servedImageName {
		t.Errorf("pulled %q, want the served %q", pulls[0].fromImage, servedImageName)
	}
	if pulls[0].auth.Password != registryPass {
		t.Errorf("pull auth password = %q, want the registry's", pulls[0].auth.Password)
	}
}

func TestPrewarmFunction_publicImage_isPulledAsWritten(t *testing.T) {
	// Given: a runtime with the same ECR resolver, and a function on a public
	// image the resolver does not serve.
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)
	fn := imageFunction()
	fn.ImageUri = "public.ecr.aws/lambda/nodejs:22"

	// When: the image is prewarmed.
	done := make(chan error, 1)
	cr.PrewarmFunction(fn, func(err error) { done <- err })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prewarm did not complete")
	}

	// Then: it is pulled exactly as written, anonymously — this registry's
	// password is never offered to a registry that did not issue it.
	pulls := daemon.recordedPulls()
	if len(pulls) != 1 {
		t.Fatalf("expected 1 pull, got %d: %+v", len(pulls), pulls)
	}
	if pulls[0].fromImage != "public.ecr.aws/lambda/nodejs" || pulls[0].tag != "22" {
		t.Errorf("pulled %s:%s, want public.ecr.aws/lambda/nodejs:22", pulls[0].fromImage, pulls[0].tag)
	}
	if pulls[0].auth != (docker.RegistryAuth{}) {
		t.Errorf("pull carried credentials %+v to a registry that issued none", pulls[0].auth)
	}
}
