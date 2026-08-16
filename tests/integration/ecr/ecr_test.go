// Package ecr_test contains integration tests for the ECR emulator.
//
// Run: go test ./tests/integration/ecr/...
package ecr_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	cborlib "github.com/fxamacker/cbor/v2"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/tests/helpers"
)

// ecrCall performs an ECR JSON 1.1 dispatch request.
func ecrCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonEC2ContainerRegistry_V20150921."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ecrCall %s: %v", operation, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	resp.Body.Close()
	if err := json.Unmarshal(buf.Bytes(), dst); err != nil {
		t.Fatalf("decode JSON: %v\nbody: %s", err, buf.Bytes())
	}
}

func mustDecode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	decodeJSON(t, resp, &m)
	return m
}

func skipWithoutDocker(t *testing.T) *docker.Client {
	t.Helper()
	// The same endpoint the test server will use, so this gate speaks for the
	// server rather than for a daemon the server cannot reach.
	dc := docker.NewClient(helpers.TestDockerSocket(), zap.NewNop())
	if err := dc.Ping(t.Context()); err != nil {
		t.Skip("Docker not available, skipping Docker-dependent ECR test")
	}
	// Every Docker-dependent ECR test drives the shared registry container, so
	// fetch its image here. That warms the daemon's cache — the emulator's own
	// pull then resolves locally instead of racing Docker Hub — and turns an
	// unreachable registry into an honest skip rather than a bare "expected
	// shared ECR registry container to be created" 60s later.
	helpers.PullOrSkip(t, dc, "registry:2")
	return dc
}

func runDockerCommand(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func runDockerCommandEventually(t *testing.T, args ...string) string {
	t.Helper()
	var lastOut []byte
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		cmd := exec.CommandContext(t.Context(), "docker", args...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return string(out)
		}
		lastOut = out
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	t.Fatalf("docker %s failed after retries: %v\n%s", strings.Join(args, " "), lastErr, lastOut)
	return ""
}

// findManagedRegistries returns every running registry container Overcast
// manages, by label. Registries carry per-claim names (a port or random
// suffix), so labels — not a well-known name — are how one is recognised.
func findManagedRegistries(t *testing.T, dc *docker.Client) []docker.ContainerSummary {
	t.Helper()
	summaries, err := dc.ListContainers(t.Context(), "ecr")
	if err != nil {
		t.Fatalf("list ecr containers: %v", err)
	}
	registries := summaries[:0]
	for _, c := range summaries {
		if c.Labels[docker.LabelResourceID] == "registry" && c.State == "running" {
			registries = append(registries, c)
		}
	}
	return registries
}

// registryIDSet snapshots the IDs of the managed registries currently running,
// so a test can later tell its own server's registry from a sibling's.
func registryIDSet(t *testing.T, dc *docker.Client) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	for _, c := range findManagedRegistries(t, dc) {
		ids[c.ID] = true
	}
	return ids
}

// newRegistriesSince returns the managed registries not in before.
func newRegistriesSince(t *testing.T, dc *docker.Client, before map[string]bool) []docker.ContainerSummary {
	t.Helper()
	var fresh []docker.ContainerSummary
	for _, c := range findManagedRegistries(t, dc) {
		if !before[c.ID] {
			fresh = append(fresh, c)
		}
	}
	return fresh
}

// waitForNewManagedRegistry polls until a managed registry not in before is
// running, and returns its inspect result.
//
// What it establishes is that a registry appeared, and nothing more. "New since
// a snapshot" is a statement about timing, not about ownership: parallel test
// packages share this daemon and start registries of their own, so the
// container it returns may well be a sibling's. A test that needs *its own*
// server's registry — to assert its address, its lifetime, or its removal —
// must ask ownRegistryContainer instead.
func waitForNewManagedRegistry(t *testing.T, dc *docker.Client, before map[string]bool) *docker.ContainerInspect {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range newRegistriesSince(t, dc, before) {
			info, err := dc.InspectContainer(t.Context(), c.ID)
			if err == nil && info != nil {
				return info
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("expected a new managed ECR registry container to be created")
	return nil
}

func waitForManagedRegistry(t *testing.T, dc *docker.Client) *docker.ContainerInspect {
	t.Helper()
	return waitForNewManagedRegistry(t, dc, nil)
}

// ownRegistryContainer returns the registry container srv started, identified
// by the host port srv advertises to its clients.
//
// The port is what makes this ownership rather than coincidence. A published
// host port is exclusive — one container on this daemon can hold it — so the
// port the server hands out names the container the server itself created, and
// names no other. Identifying it by which registry appeared while the server
// was starting cannot say that: under a loaded full-suite run a sibling package
// starts its own registry inside the same window, the diff returns whichever
// the daemon listed first, and an assertion about this server's registry ends
// up watching a container this server never created and will never remove.
//
// GetAuthorizationToken is the question asked because it waits for the registry
// to be up before answering, so the endpoint it reports is a started
// container's rather than the port-less fallback.
func ownRegistryContainer(t *testing.T, dc *docker.Client, srv *helpers.TestServer) docker.ContainerSummary {
	t.Helper()
	port := advertisedRegistryPort(t, srv)
	for _, c := range findManagedRegistries(t, dc) {
		for _, p := range c.Ports {
			if p.HostPort == port {
				return c
			}
		}
	}
	t.Fatalf("no managed registry container publishes host port %d, the one the server advertises", port)
	return docker.ContainerSummary{}
}

// advertisedRegistryPort returns the host port in the server's proxyEndpoint,
// failing the test when the server is serving the registry-less fallback — its
// own API port, which no registry container publishes.
func advertisedRegistryPort(t *testing.T, srv *helpers.TestServer) int {
	t.Helper()
	resp := ecrCall(t, srv, "GetAuthorizationToken", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200 from GetAuthorizationToken, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	data, _ := body["authorizationData"].([]any)
	if len(data) == 0 {
		t.Fatalf("expected authorizationData from GetAuthorizationToken, got %#v", body)
	}
	entry, _ := data[0].(map[string]any)
	proxy, _ := entry["proxyEndpoint"].(string)
	u, err := url.Parse(proxy)
	if err != nil {
		t.Fatalf("parse proxyEndpoint %q: %v", proxy, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("proxyEndpoint %q carries no host port: %v", proxy, err)
	}
	if port == srv.Config.Port {
		t.Fatalf("the server started no registry: proxyEndpoint %q names its own API port", proxy)
	}
	return port
}

//nolint:unused // Kept for Docker CLI tests that need stdin.
func runDockerCommandWithInput(t *testing.T, input string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "docker", args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// ── CreateRepository ───────────────────────────────────────────────────────────

func TestCreateRepository_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithAccountID("000000000000"))
	resp := ecrCall(t, srv, "CreateRepository", map[string]any{
		"repositoryName": "my-app",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	repo, ok := body["repository"].(map[string]any)
	if !ok {
		t.Fatalf("missing repository in response: %#v", body)
	}
	if repo["repositoryName"] != "my-app" {
		t.Fatalf("unexpected repositoryName: %v", repo["repositoryName"])
	}
	if repo["repositoryArn"] == "" {
		t.Fatalf("missing repositoryArn")
	}
	if repo["repositoryUri"] == "" {
		t.Fatalf("missing repositoryUri")
	}
	if repo["registryId"] != "000000000000" {
		t.Fatalf("unexpected registryId: %v", repo["registryId"])
	}
}

func TestCreateRepository_duplicate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	resp := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "RepositoryAlreadyExistsException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

func TestCreateRepository_nameRequired(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := ecrCall(t, srv, "CreateRepository", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// ── DescribeRepositories ───────────────────────────────────────────────────────

func TestDescribeRepositories_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := ecrCall(t, srv, "DescribeRepositories", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	repos, ok := body["repositories"].([]any)
	if !ok {
		t.Fatalf("expected repositories array, got %T: %v", body["repositories"], body["repositories"])
	}
	if len(repos) != 0 {
		t.Fatalf("expected empty list, got %d", len(repos))
	}
}

func TestDescribeRepositories_afterCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "repo-a"})
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "repo-b"})

	resp := ecrCall(t, srv, "DescribeRepositories", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	repos := body["repositories"].([]any)
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
}

func TestDescribeRepositories_filterByName(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "repo-a"})
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "repo-b"})

	resp := ecrCall(t, srv, "DescribeRepositories", map[string]any{
		"repositoryNames": []string{"repo-a"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	repos := body["repositories"].([]any)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
}

func TestDescribeRepositories_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := ecrCall(t, srv, "DescribeRepositories", map[string]any{
		"repositoryNames": []string{"nonexistent"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "RepositoryNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

// ── DeleteRepository ───────────────────────────────────────────────────────────

func TestDeleteRepository_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "to-delete"})

	resp := ecrCall(t, srv, "DeleteRepository", map[string]any{
		"repositoryName": "to-delete",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	repo, ok := body["repository"].(map[string]any)
	if !ok {
		t.Fatalf("missing repository in response: %#v", body)
	}
	if repo["repositoryName"] != "to-delete" {
		t.Fatalf("unexpected repositoryName: %v", repo["repositoryName"])
	}

	// Confirm gone.
	listResp := ecrCall(t, srv, "DescribeRepositories", map[string]any{})
	listBody := mustDecode(t, listResp)
	repos := listBody["repositories"].([]any)
	if len(repos) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(repos))
	}
}

func TestDeleteRepository_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := ecrCall(t, srv, "DeleteRepository", map[string]any{
		"repositoryName": "nonexistent",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "RepositoryNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

// TestDeleteRepository_notEmpty pins the guard AWS puts on the destructive
// call: a repository holding images is not deletable without force, and the
// refusal must leave the repository and its images alone.
func TestDeleteRepository_notEmpty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "holds-images"})
	putResp := ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName": "holds-images",
		"imageTag":       "v1",
		"imageManifest":  `{"schemaVersion":2}`,
	})
	putResp.Body.Close()

	resp := ecrCall(t, srv, "DeleteRepository", map[string]any{
		"repositoryName": "holds-images",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "RepositoryNotEmptyException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}

	// The refusal must not have deleted anything.
	listBody := mustDecode(t, ecrCall(t, srv, "DescribeRepositories", map[string]any{}))
	if repos, _ := listBody["repositories"].([]any); len(repos) != 1 {
		t.Fatalf("expected the repository to survive a refused delete, got %d", len(repos))
	}
	imagesBody := mustDecode(t, ecrCall(t, srv, "ListImages", map[string]any{
		"repositoryName": "holds-images",
	}))
	if ids, _ := imagesBody["imageIds"].([]any); len(ids) != 1 {
		t.Fatalf("expected the image to survive a refused delete, got %d", len(ids))
	}
}

// TestDeleteRepository_forceNonEmpty is the other half: force is what the
// error tells the caller to reach for, so it has to work.
func TestDeleteRepository_forceNonEmpty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "force-delete"})
	putResp := ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName": "force-delete",
		"imageTag":       "v1",
		"imageManifest":  `{"schemaVersion":2}`,
	})
	putResp.Body.Close()

	resp := ecrCall(t, srv, "DeleteRepository", map[string]any{
		"repositoryName": "force-delete",
		"force":          true,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	listBody := mustDecode(t, ecrCall(t, srv, "DescribeRepositories", map[string]any{}))
	if repos, _ := listBody["repositories"].([]any); len(repos) != 0 {
		t.Fatalf("expected the repository to be gone, got %d", len(repos))
	}
}

// ── GetAuthorizationToken ──────────────────────────────────────────────────────

func TestGetAuthorizationToken_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithAccountID("000000000000"))
	resp := ecrCall(t, srv, "GetAuthorizationToken", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	data, ok := body["authorizationData"].([]any)
	if !ok || len(data) == 0 {
		t.Fatalf("missing authorizationData: %#v", body)
	}
	token, ok := data[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected authorizationData entry: %T", data[0])
	}
	if token["authorizationToken"] == "" {
		t.Fatalf("missing authorizationToken")
	}
	if token["proxyEndpoint"] == "" {
		t.Fatalf("missing proxyEndpoint")
	}
	if token["expiresAt"] == nil {
		t.Fatalf("missing expiresAt")
	}

	tokenStr, _ := token["authorizationToken"].(string)
	decoded, err := base64.StdEncoding.DecodeString(tokenStr)
	if err != nil {
		t.Fatalf("decode authorizationToken: %v", err)
	}
	if !strings.HasPrefix(string(decoded), "AWS:") {
		t.Fatalf("expected token payload to start with AWS:, got %q", string(decoded))
	}
}

func TestDescribeRegistry_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithAccountID("000000000000"))
	resp := ecrCall(t, srv, "DescribeRegistry", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["registryId"] != "000000000000" {
		t.Fatalf("unexpected registryId: %v", body["registryId"])
	}
	repl, ok := body["replicationConfiguration"].(map[string]any)
	if !ok {
		t.Fatalf("missing replicationConfiguration: %#v", body)
	}
	rules, ok := repl["rules"].([]any)
	if !ok {
		t.Fatalf("missing rules: %#v", repl)
	}
	if len(rules) != 0 {
		t.Fatalf("expected empty rules, got %d", len(rules))
	}
}

// ── ListImages ────────────────────────────────────────────────────────────────

func TestListImages_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})

	resp := ecrCall(t, srv, "ListImages", map[string]any{
		"repositoryName": "my-app",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	ids, ok := body["imageIds"].([]any)
	if !ok {
		t.Fatalf("expected imageIds array: %#v", body)
	}
	if len(ids) != 0 {
		t.Fatalf("expected empty, got %d", len(ids))
	}
}

func TestListImages_repositoryNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := ecrCall(t, srv, "ListImages", map[string]any{
		"repositoryName": "nonexistent",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "RepositoryNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

func TestDescribeImages_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName": "my-app",
		"imageManifest":  `{"schemaVersion":2}`,
		"imageTag":       "v1",
	})

	resp := ecrCall(t, srv, "DescribeImages", map[string]any{"repositoryName": "my-app"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	details, ok := body["imageDetails"].([]any)
	if !ok || len(details) != 1 {
		t.Fatalf("expected one imageDetails entry, got %#v", body["imageDetails"])
	}
	detail, ok := details[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected detail type: %T", details[0])
	}
	if detail["registryId"] != "000000000000" {
		t.Fatalf("unexpected registryId: %v", detail["registryId"])
	}
	if detail["repositoryName"] != "my-app" {
		t.Fatalf("unexpected repositoryName: %v", detail["repositoryName"])
	}
	if detail["imageDigest"] == "" {
		t.Fatalf("expected imageDigest in detail: %#v", detail)
	}
}

func TestDescribeImages_repositoryNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := ecrCall(t, srv, "DescribeImages", map[string]any{"repositoryName": "nope"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "RepositoryNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

// ── PutImage / BatchGetImage / BatchDeleteImage ────────────────────────────────

func TestPutImage_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})

	resp := ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName":         "my-app",
		"imageManifest":          `{"schemaVersion":2}`,
		"imageTag":               "latest",
		"imageManifestMediaType": "application/vnd.docker.distribution.manifest.v2+json",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	img, ok := body["image"].(map[string]any)
	if !ok {
		t.Fatalf("missing image in response: %#v", body)
	}
	imgID, ok := img["imageId"].(map[string]any)
	if !ok {
		t.Fatalf("missing imageId: %#v", img)
	}
	if imgID["imageTag"] != "latest" {
		t.Fatalf("unexpected imageTag: %v", imgID["imageTag"])
	}
}

func TestPutImage_autoDigestIsManifestSHA256(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "digest-app"})

	manifest := `{"schemaVersion":2,"config":{"mediaType":"application/vnd.docker.container.image.v1+json","digest":"sha256:abc"}}`
	resp := ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName": "digest-app",
		"imageManifest":  manifest,
		"imageTag":       "latest",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	img := body["image"].(map[string]any)
	imgID := img["imageId"].(map[string]any)
	gotDigest, _ := imgID["imageDigest"].(string)

	sum := sha256.Sum256([]byte(manifest))
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	if gotDigest != wantDigest {
		t.Fatalf("unexpected digest: got %s want %s", gotDigest, wantDigest)
	}
}

func TestBatchGetImage_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName": "my-app",
		"imageManifest":  `{"schemaVersion":2}`,
		"imageTag":       "v1.0",
	})

	resp := ecrCall(t, srv, "BatchGetImage", map[string]any{
		"repositoryName": "my-app",
		"imageIds": []map[string]any{
			{"imageTag": "v1.0"},
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	images, ok := body["images"].([]any)
	if !ok || len(images) == 0 {
		t.Fatalf("expected images array: %#v", body)
	}
}

func TestBatchDeleteImage_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName": "my-app",
		"imageManifest":  `{"schemaVersion":2}`,
		"imageTag":       "old",
	})

	resp := ecrCall(t, srv, "BatchDeleteImage", map[string]any{
		"repositoryName": "my-app",
		"imageIds": []map[string]any{
			{"imageTag": "old"},
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	deleted, ok := body["imageIds"].([]any)
	if !ok {
		t.Fatalf("expected imageIds in response: %#v", body)
	}
	if len(deleted) != 1 {
		t.Fatalf("expected 1 deleted image, got %d", len(deleted))
	}
}

// ── Repository policy ──────────────────────────────────────────────────────────

func TestSetRepositoryPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})

	policy := `{"Version":"2012-10-17","Statement":[]}`
	resp := ecrCall(t, srv, "SetRepositoryPolicy", map[string]any{
		"repositoryName": "my-app",
		"policyText":     policy,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGetRepositoryPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	policy := `{"Version":"2012-10-17","Statement":[]}`
	ecrCall(t, srv, "SetRepositoryPolicy", map[string]any{
		"repositoryName": "my-app",
		"policyText":     policy,
	})

	resp := ecrCall(t, srv, "GetRepositoryPolicy", map[string]any{
		"repositoryName": "my-app",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["policyText"] != policy {
		t.Fatalf("unexpected policyText: %v", body["policyText"])
	}
}

func TestGetRepositoryPolicy_notSet(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})

	resp := ecrCall(t, srv, "GetRepositoryPolicy", map[string]any{
		"repositoryName": "my-app",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when no policy set, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "RepositoryPolicyNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

func TestPutGetDeleteLifecyclePolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})

	lifecycle := `{"rules":[{"rulePriority":1,"description":"expire old","selection":{"tagStatus":"any","countType":"imageCountMoreThan","countNumber":10},"action":{"type":"expire"}}]}`

	putResp := ecrCall(t, srv, "PutLifecyclePolicy", map[string]any{
		"repositoryName":      "my-app",
		"lifecyclePolicyText": lifecycle,
	})
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for PutLifecyclePolicy, got %d", putResp.StatusCode)
	}

	getResp := ecrCall(t, srv, "GetLifecyclePolicy", map[string]any{
		"repositoryName": "my-app",
	})
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for GetLifecyclePolicy, got %d", getResp.StatusCode)
	}
	getBody := mustDecode(t, getResp)
	if getBody["lifecyclePolicyText"] != lifecycle {
		t.Fatalf("unexpected lifecyclePolicyText: %v", getBody["lifecyclePolicyText"])
	}

	delResp := ecrCall(t, srv, "DeleteLifecyclePolicy", map[string]any{
		"repositoryName": "my-app",
	})
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for DeleteLifecyclePolicy, got %d", delResp.StatusCode)
	}

	getAfterDelete := ecrCall(t, srv, "GetLifecyclePolicy", map[string]any{
		"repositoryName": "my-app",
	})
	defer getAfterDelete.Body.Close()
	if getAfterDelete.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 after delete, got %d", getAfterDelete.StatusCode)
	}
	body := mustDecode(t, getAfterDelete)
	if body["__type"] != "LifecyclePolicyNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

func TestGetLifecyclePolicy_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})

	resp := ecrCall(t, srv, "GetLifecyclePolicy", map[string]any{
		"repositoryName": "my-app",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "LifecyclePolicyNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

func TestDeleteRepositoryPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	ecrCall(t, srv, "SetRepositoryPolicy", map[string]any{
		"repositoryName": "my-app",
		"policyText":     `{"Version":"2012-10-17","Statement":[]}`,
	})

	resp := ecrCall(t, srv, "DeleteRepositoryPolicy", map[string]any{
		"repositoryName": "my-app",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// ── Tag operations ─────────────────────────────────────────────────────────────

func TestTagResource_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithAccountID("000000000000"))
	createResp := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	createBody := mustDecode(t, createResp)
	arn := createBody["repository"].(map[string]any)["repositoryArn"].(string)

	resp := ecrCall(t, srv, "TagResource", map[string]any{
		"resourceArn": arn,
		"tags": []map[string]any{
			{"Key": "env", "Value": "test"},
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestListTagsForResource_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithAccountID("000000000000"))
	createResp := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	createBody := mustDecode(t, createResp)
	arn := createBody["repository"].(map[string]any)["repositoryArn"].(string)

	ecrCall(t, srv, "TagResource", map[string]any{
		"resourceArn": arn,
		"tags":        []map[string]any{{"Key": "team", "Value": "platform"}},
	})

	resp := ecrCall(t, srv, "ListTagsForResource", map[string]any{"resourceArn": arn})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) != 1 {
		t.Fatalf("expected 1 tag, got %#v", body["tags"])
	}
}

func TestUntagResource_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithAccountID("000000000000"))
	createResp := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "my-app"})
	createBody := mustDecode(t, createResp)
	arn := createBody["repository"].(map[string]any)["repositoryArn"].(string)

	ecrCall(t, srv, "TagResource", map[string]any{
		"resourceArn": arn,
		"tags":        []map[string]any{{"Key": "remove-me", "Value": "yes"}},
	})
	resp := ecrCall(t, srv, "UntagResource", map[string]any{
		"resourceArn": arn,
		"tagKeys":     []string{"remove-me"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	listResp := ecrCall(t, srv, "ListTagsForResource", map[string]any{"resourceArn": arn})
	listBody := mustDecode(t, listResp)
	tags := listBody["tags"].([]any)
	if len(tags) != 0 {
		t.Fatalf("expected no tags after untag, got %d", len(tags))
	}
}

// ── DescribeImageScanFindings ────────────────────────────────────────────────

func TestDescribeImageScanFindings_supportedNotScanned(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "scan-me"})
	ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName": "scan-me",
		"imageManifest":  `{"schemaVersion":2}`,
		"imageTag":       "latest",
	})

	resp := ecrCall(t, srv, "DescribeImageScanFindings", map[string]any{
		"repositoryName": "scan-me",
		"imageId":        map[string]any{"imageTag": "latest"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	status, ok := body["imageScanStatus"].(map[string]any)
	if !ok {
		t.Fatalf("missing imageScanStatus: %#v", body)
	}
	if status["status"] != "UNSUPPORTED_IMAGE" {
		t.Fatalf("unexpected scan status: %v", status["status"])
	}
	findings, ok := body["imageScanFindings"].(map[string]any)
	if !ok {
		t.Fatalf("missing imageScanFindings: %#v", body)
	}
	if _, ok := findings["findingSeverityCounts"].(map[string]any); !ok {
		t.Fatalf("expected findingSeverityCounts object: %#v", findings)
	}
}

func TestDescribeImageScanFindings_imageNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "any"})
	resp := ecrCall(t, srv, "DescribeImageScanFindings", map[string]any{
		"repositoryName": "any",
		"imageId":        map[string]any{"imageTag": "latest"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing image, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "ImageNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

func TestGetAuthorizationToken_withDocker_lazyStartsSharedRegistry(t *testing.T) {
	dc := skipWithoutDocker(t)

	// Registries existing before the server are other processes' — parallel
	// test packages share this daemon — so "lazy" is proven by a NEW registry
	// appearing only after the first auth call, not by global absence.
	before := registryIDSet(t, dc)

	srv := helpers.NewTestServer(t,
		helpers.WithHostname("overcast"),
		helpers.WithLambdaDocker(),
	)
	if fresh := newRegistriesSince(t, dc, before); len(fresh) != 0 {
		t.Fatalf("expected no managed ECR registry before the first auth call, found %d", len(fresh))
	}

	resp := ecrCall(t, srv, "GetAuthorizationToken", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	data := body["authorizationData"].([]any)
	entry := data[0].(map[string]any)
	proxy, _ := entry["proxyEndpoint"].(string)
	if proxy == "" {
		t.Fatalf("missing proxyEndpoint")
	}
	// Which host the endpoint names is settled by the daemon-reachability
	// probe — "localhost" when the daemon answered there, the configured
	// hostname when it could not be shown to — and this test cannot say which
	// happened without re-running the probe. So it pins what it is actually
	// about: a registry exists, and the endpoint names it rather than the
	// emulator, whose port is what GetAuthorizationToken falls back to when
	// there is no registry at all. Both branches of the host rule are covered
	// deterministically in internal/services/ecr/registry_advertised_host_test.go.
	if !strings.HasPrefix(proxy, "http://") {
		t.Fatalf("proxyEndpoint is not an http endpoint: %q", proxy)
	}
	if strings.HasSuffix(proxy, fmt.Sprintf(":%d", srv.Config.Port)) {
		t.Fatalf("proxyEndpoint names the emulator's API port rather than the registry: %q", proxy)
	}

	waitForNewManagedRegistry(t, dc, before)
}

func TestGetAuthorizationToken_withDocker_tokenAuthenticatesRegistry(t *testing.T) {
	dc := skipWithoutDocker(t)

	srv := helpers.NewTestServer(t,
		helpers.WithLambdaDocker(),
	)

	resp := ecrCall(t, srv, "GetAuthorizationToken", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	data := body["authorizationData"].([]any)
	entry := data[0].(map[string]any)
	tokenB64, _ := entry["authorizationToken"].(string)
	proxy, _ := entry["proxyEndpoint"].(string)

	decoded, err := base64.StdEncoding.DecodeString(tokenB64)
	if err != nil {
		t.Fatalf("decode authorizationToken: %v", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected decoded token format: %q", string(decoded))
	}
	user, pass := parts[0], parts[1]
	if proxy == "" {
		t.Fatalf("missing proxyEndpoint")
	}
	if user != "AWS" {
		t.Fatalf("expected token username AWS, got %q", user)
	}

	waitForManagedRegistry(t, dc)

	// The registry itself is the judge: it holds the credentials, and how they
	// were installed is its business. Asking it directly also catches the
	// failure a file check cannot see — an htpasswd that never arrived, which
	// is what a bind mount produced whenever Overcast's own filesystem was not
	// the daemon's (i.e. every dockerized Overcast).
	if code := registryPing(t, proxy, user, pass); code != http.StatusOK {
		t.Fatalf("registry rejected the authorization token: GET /v2/ = %d", code)
	}
	if code := registryPing(t, proxy, user, pass+"-wrong"); code != http.StatusUnauthorized {
		t.Fatalf("registry accepted a wrong password: GET /v2/ = %d, want 401", code)
	}
}

// registryPing performs the registry API's own credential check — GET /v2/ —
// against the endpoint ECR advertised, and returns its status.
func registryPing(t *testing.T, proxy, user, password string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, strings.TrimSuffix(proxy, "/")+"/v2/", nil)
	if err != nil {
		t.Fatalf("build registry request: %v", err)
	}
	req.SetBasicAuth(user, password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("call registry %s: %v", proxy, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// dockerLoginOrSkip authenticates the Docker CLI against this server's registry
// with the credentials GetAuthorizationToken hands out, skipping the test when
// the daemon refuses plain HTTP to it. That refusal is daemon configuration the
// test cannot change and says nothing about Overcast.
func dockerLoginOrSkip(t *testing.T, srv *helpers.TestServer) {
	t.Helper()

	authResp := ecrCall(t, srv, "GetAuthorizationToken", map[string]any{})
	if authResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 getting auth token, got %d", authResp.StatusCode)
	}
	authBody := mustDecode(t, authResp)
	authData, ok := authBody["authorizationData"].([]any)
	if !ok || len(authData) != 1 {
		t.Fatalf("expected one authorizationData entry, got %#v", authBody["authorizationData"])
	}
	authEntry := authData[0].(map[string]any)
	tokenB64, _ := authEntry["authorizationToken"].(string)
	proxy, _ := authEntry["proxyEndpoint"].(string)
	decoded, err := base64.StdEncoding.DecodeString(tokenB64)
	if err != nil {
		t.Fatalf("decode authorizationToken: %v", err)
	}
	user, password, found := strings.Cut(string(decoded), ":")
	if !found {
		t.Fatalf("unexpected decoded token format: %q", string(decoded))
	}
	helpers.DockerLoginOrSkip(t, proxy, user, password)
}

func TestECR_withDocker_pushListGetAndPullRoundTrip(t *testing.T) {
	skipWithoutDocker(t)

	srv := helpers.NewTestServer(t,
		helpers.WithLambdaDocker(),
		helpers.WithRegion("us-east-1"),
		helpers.WithAccountID("000000000000"),
		// The daemon does the pushing here, and on Docker Desktop it cannot
		// reach an ephemeral publish — see WithECRRegistryPort.
		helpers.WithECRRegistryPort(helpers.ReserveTCPPort(t)),
	)

	createResp := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "roundtrip"})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 creating repository, got %d", createResp.StatusCode)
	}
	createBody := mustDecode(t, createResp)
	repo := createBody["repository"].(map[string]any)
	repoURI, _ := repo["repositoryUri"].(string)
	if repoURI == "" {
		t.Fatal("missing repositoryUri")
	}

	dockerLoginOrSkip(t, srv)
	// Use the already-present registry image as a tiny local source image.
	sourceImage := "registry:2"
	targetImage := repoURI + ":roundtrip"

	runDockerCommand(t, "tag", sourceImage, targetImage)
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "image", "rm", "-f", targetImage).Run()
	})
	runDockerCommand(t, "push", targetImage)

	listResp := ecrCall(t, srv, "ListImages", map[string]any{"repositoryName": "roundtrip"})
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing images, got %d", listResp.StatusCode)
	}
	listBody := mustDecode(t, listResp)
	imageIDs := listBody["imageIds"].([]any)
	if len(imageIDs) == 0 {
		t.Fatalf("expected pushed image to appear in ListImages, got %#v", listBody)
	}
	firstID := imageIDs[0].(map[string]any)
	if firstID["imageTag"] != "roundtrip" {
		t.Fatalf("expected roundtrip tag in ListImages, got %#v", firstID)
	}
	digest, _ := firstID["imageDigest"].(string)
	if strings.TrimSpace(digest) == "" {
		t.Fatalf("expected image digest in ListImages, got %#v", firstID)
	}

	batchResp := ecrCall(t, srv, "BatchGetImage", map[string]any{
		"repositoryName": "roundtrip",
		"imageIds":       []map[string]any{{"imageTag": "roundtrip"}},
	})
	if batchResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 batch getting image, got %d", batchResp.StatusCode)
	}
	batchBody := mustDecode(t, batchResp)
	images := batchBody["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("expected one image from BatchGetImage, got %#v", batchBody)
	}
	image := images[0].(map[string]any)
	imageID := image["imageId"].(map[string]any)
	if imageID["imageDigest"] != digest {
		t.Fatalf("expected batch image digest %q, got %#v", digest, imageID)
	}
	if manifest, _ := image["imageManifest"].(string); strings.TrimSpace(manifest) == "" {
		t.Fatalf("expected non-empty imageManifest from BatchGetImage, got %#v", image)
	}

	// Remove the local tag and prove it can be pulled back from the shared registry.
	runDockerCommand(t, "image", "rm", "-f", targetImage)
	runDockerCommandEventually(t, "pull", targetImage)
	if inspectOut := runDockerCommand(t, "image", "inspect", "--format", "{{json .RepoTags}} {{json .RepoDigests}}", targetImage); !strings.Contains(inspectOut, targetImage) {
		t.Fatalf("expected pulled image to include target tag %q, got %q", targetImage, inspectOut)
	}
}

func TestECR_withDocker_registryContainerRemovedOnServerShutdown(t *testing.T) {
	dc := skipWithoutDocker(t)

	// The server's own registry is the one whose removal is asserted; a
	// sibling process's registry on this daemon must survive our shutdown, so
	// the assertion tracks the container this server created — the one holding
	// the port it advertises — by ID.
	var ownID string

	// Register the final assertion first so it runs after helper server cleanup.
	//
	// It looks once, and is entitled to: shutdown does not request removal and
	// leave, it polls the daemon until the container is gone before returning,
	// so by the time this runs the daemon has already answered the question.
	// A grace period here would only hide a shutdown that gave up.
	t.Cleanup(func() {
		if ownID == "" {
			return // the body failed before it identified a container
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		summaries, err := dc.ListContainers(ctx, "ecr")
		if err != nil {
			t.Fatalf("list ecr containers after shutdown: %v", err)
		}
		for _, c := range summaries {
			if c.ID == ownID {
				t.Fatalf("expected ecr registry container to be removed on shutdown, still present: id=%s state=%s", ownID, c.State)
			}
		}
	})

	srv := helpers.NewTestServer(t,
		helpers.WithLambdaDocker(),
	)

	resp := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "shutdown-check"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 creating repository, got %d", resp.StatusCode)
	}

	ownID = ownRegistryContainer(t, dc, srv).ID
}

// ---- RPC v2 CBOR tests ----

func TestRPCv2CBOR_DescribeRegistry(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ecrCBORCall(t, srv, "DescribeRegistry", map[string]any{})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/cbor")
	helpers.AssertHeader(t, resp, "Smithy-Protocol", "rpc-v2-cbor")

	var out struct {
		RegistryId string `cbor:"registryId"`
	}
	decodeCBOR(t, resp, &out)
	if out.RegistryId != "000000000000" {
		t.Fatalf("RegistryId = %q, want 000000000000", out.RegistryId)
	}
}

func TestRPCv2CBOR_CreateAndDescribeRepositories(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Given: a repository created via CBOR
	resp := ecrCBORCall(t, srv, "CreateRepository", map[string]any{
		"repositoryName": "cbor-repo",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: DescribeRepositories over CBOR
	resp2 := ecrCBORCall(t, srv, "DescribeRepositories", map[string]any{
		"repositoryNames": []string{"cbor-repo"},
	})
	defer resp2.Body.Close()

	// Then: repository is returned in CBOR format
	helpers.AssertStatus(t, resp2, http.StatusOK)
	helpers.AssertHeader(t, resp2, "Content-Type", "application/cbor")

	var out struct {
		Repositories []struct {
			RepositoryName string `cbor:"repositoryName"`
		} `cbor:"repositories"`
	}
	decodeCBOR(t, resp2, &out)
	if len(out.Repositories) != 1 || out.Repositories[0].RepositoryName != "cbor-repo" {
		t.Fatalf("expected cbor-repo, got %v", out.Repositories)
	}
}

func TestRPCv2CBOR_TagResource(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Given: a repository created via JSON, then tagged via CBOR
	resp := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "cbor-tags"})
	resp.Body.Close()

	resp2 := ecrCBORCall(t, srv, "TagResource", map[string]any{
		"resourceArn": "arn:aws:ecr:us-east-1:000000000000:repository/cbor-tags",
		"tags":        []map[string]string{{"Key": "env", "Value": "test"}},
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	// When: list tags via CBOR
	resp3 := ecrCBORCall(t, srv, "ListTagsForResource", map[string]any{
		"resourceArn": "arn:aws:ecr:us-east-1:000000000000:repository/cbor-tags",
	})
	defer resp3.Body.Close()
	helpers.AssertStatus(t, resp3, http.StatusOK)
	helpers.AssertHeader(t, resp3, "Content-Type", "application/cbor")

	var tags struct {
		Tags []struct {
			Key   string `cbor:"Key"`
			Value string `cbor:"Value"`
		} `cbor:"tags"`
	}
	decodeCBOR(t, resp3, &tags)
	if len(tags.Tags) != 1 || tags.Tags[0].Key != "env" {
		t.Fatalf("expected 1 tag 'env', got %v", tags.Tags)
	}
}

func ecrCBORCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()

	payload, err := cborlib.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CBOR %s body: %v", operation, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/ecr/operation/"+operation, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build CBOR request: %v", err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do CBOR request %s: %v", operation, err)
	}
	return resp
}

func decodeCBOR(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := cborlib.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode CBOR response: %v", err)
	}
}
