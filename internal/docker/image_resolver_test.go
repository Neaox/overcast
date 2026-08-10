package docker

// image_resolver_test.go — an ImagePuller wired to a resolver pulls, and runs,
// the reference the resolver hands back, with the credentials it supplies.
//
// Both halves matter and they fail differently. Pulling the unresolved
// reference reaches the wrong registry; pulling the resolved one without
// credentials reaches the right registry and is refused. Creating the container
// from the unresolved reference fails with "No such image" even after a pull
// that worked, because the daemon stored the bytes under the other name.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// staticResolver resolves one reference and leaves everything else alone, the
// same contract the ECR service implements.
type staticResolver struct {
	from string
	to   ImageReference
}

func (r staticResolver) ResolveImage(_ context.Context, image string) ImageReference {
	if image == r.from {
		return r.to
	}
	return ImageReference{Ref: image}
}

// recordingDaemon is a fake Docker daemon that records what a pull and a
// create were asked for.
type recordingDaemon struct {
	pullQuery  url.Values
	pullAuth   string
	createBody CreateContainerRequest
}

func newRecordingDaemon(t *testing.T) (*recordingDaemon, *Client) {
	t.Helper()
	d := &recordingDaemon{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/images/create"):
			d.pullQuery = r.URL.Query()
			d.pullAuth = r.Header.Get("X-Registry-Auth")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case strings.Contains(r.URL.Path, "/containers/create"):
			_ = json.NewDecoder(r.Body).Decode(&d.createBody)
			_ = json.NewEncoder(w).Encode(CreateContainerResponse{ID: "c1"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return d, &Client{httpClient: srv.Client(), host: srv.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}
}

const (
	awsRef   = "000000000000.dkr.ecr.ap-southeast-2.amazonaws.com/app:v1"
	localRef = "localhost:32771/000000000000/app:v1"
)

func servedLocally() ImageReference {
	return ImageReference{
		Ref:  localRef,
		Auth: &RegistryAuth{Username: "AWS", Password: "s3cret", ServerAddress: "localhost:32771"},
	}
}

func TestImagePullerEnsure_pullsTheResolvedReferenceWithItsCredentials(t *testing.T) {
	// Given: a puller whose resolver serves the reference from a local registry.
	d, c := newRecordingDaemon(t)
	p := NewImagePuller(c).WithResolver(staticResolver{from: awsRef, to: servedLocally()})

	// When: the image is ensured under the name the task definition carries.
	if err := p.Ensure(context.Background(), awsRef); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// Then: the daemon was asked for the resolved reference…
	if got := d.pullQuery.Get("fromImage"); got != "localhost:32771/000000000000/app" {
		t.Errorf("fromImage = %q, want the resolved reference", got)
	}
	if got := d.pullQuery.Get("tag"); got != "v1" {
		t.Errorf("tag = %q, want v1", got)
	}

	// …and given the credentials that registry requires.
	if d.pullAuth == "" {
		t.Fatal("no X-Registry-Auth header; an anonymous pull is refused with 'no basic auth credentials'")
	}
	raw, err := base64.URLEncoding.DecodeString(d.pullAuth)
	if err != nil {
		t.Fatalf("X-Registry-Auth is not base64url: %v", err)
	}
	var auth RegistryAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		t.Fatalf("X-Registry-Auth is not an AuthConfig: %v", err)
	}
	if auth.Username != "AWS" || auth.Password != "s3cret" || auth.ServerAddress != "localhost:32771" {
		t.Errorf("X-Registry-Auth = %+v, want the resolved credentials", auth)
	}
}

func TestCreateContainerWithRetry_runsTheResolvedReference(t *testing.T) {
	// Given: a puller whose resolver serves the reference from a local registry.
	d, c := newRecordingDaemon(t)
	p := NewImagePuller(c).WithResolver(staticResolver{from: awsRef, to: servedLocally()})

	// When: a container is created from the task definition's image.
	if _, err := p.CreateContainerWithRetry(context.Background(), "c", &CreateContainerRequest{
		ContainerConfig: &ContainerConfig{Image: awsRef},
	}); err != nil {
		t.Fatalf("CreateContainerWithRetry: %v", err)
	}

	// Then: the container runs the image the daemon actually stored.
	if got := d.createBody.ContainerConfig.Image; got != localRef {
		t.Errorf("created image = %q, want %q", got, localRef)
	}
}

func TestImagePullerEnsure_unresolvedImagesPullAnonymously(t *testing.T) {
	// Given: a puller whose resolver does not claim this image.
	d, c := newRecordingDaemon(t)
	p := NewImagePuller(c).WithResolver(staticResolver{from: awsRef, to: servedLocally()})

	// When: an unrelated public image is ensured.
	if err := p.Ensure(context.Background(), "alpine:latest"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// Then: it is pulled as written, with no credentials attached — sending the
	// local registry's password to Docker Hub would leak it.
	if got := d.pullQuery.Get("fromImage"); got != "alpine" {
		t.Errorf("fromImage = %q, want alpine", got)
	}
	if d.pullAuth != "" {
		t.Errorf("X-Registry-Auth = %q, want none", d.pullAuth)
	}
}

func TestImagePuller_withoutAResolverIsUnchanged(t *testing.T) {
	// Given: a puller with no resolver wired — every service that pulls a fixed
	// public image.
	d, c := newRecordingDaemon(t)
	p := NewImagePuller(c)

	// When: an image is ensured and a container created from it.
	if err := p.Ensure(context.Background(), awsRef); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := p.CreateContainerWithRetry(context.Background(), "c", &CreateContainerRequest{
		ContainerConfig: &ContainerConfig{Image: awsRef},
	}); err != nil {
		t.Fatalf("CreateContainerWithRetry: %v", err)
	}

	// Then: both use the reference exactly as given.
	if got := d.pullQuery.Get("fromImage"); got != "000000000000.dkr.ecr.ap-southeast-2.amazonaws.com/app" {
		t.Errorf("fromImage = %q, want the reference unchanged", got)
	}
	if got := d.createBody.ContainerConfig.Image; got != awsRef {
		t.Errorf("created image = %q, want %q", got, awsRef)
	}
}
