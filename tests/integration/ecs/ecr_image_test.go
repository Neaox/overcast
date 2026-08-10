package ecs_test

// ecr_image_test.go — a task definition whose image was built and pushed by
// CDK must actually start.
//
// CDK publishes a container asset to the ECR repository it bootstrapped, then
// writes the image into the task definition as
// "{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}", built from
// AWS::AccountId and AWS::Region rather than read back from the repository. The
// ECR endpoint is a real wildcard domain, so pulling that name leaves the
// machine, reaches AWS, and is refused anonymously — the service never
// stabilises and reports:
//
//	CannotPullContainerError: … pull access denied, repository does not exist
//	or may require authorization: authorization failed: no basic auth credentials
//
// while the bytes are sitting in the registry Overcast serves, behind the
// credentials GetAuthorizationToken hands out.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// cdkAssetRepository is the repository name `cdk bootstrap` creates for
// container assets, and the tag is a content hash as cdk-assets computes it.
const (
	cdkAssetRepository = "cdk-hnb659fds-container-assets-000000000000-us-east-1"
	cdkAssetTag        = "610a3ca08b333a5ac4b8d23e4df2830ddffeac82"
)

func TestRunTask_withDocker_pullsCDKContainerAssetFromTheServedRegistry(t *testing.T) {
	dc := skipWithoutDocker(t)

	// Given: a server with Docker wired into both ECR and ECS, and the CDK
	// bootstrap repository for container assets.
	srv := helpers.NewTestServer(t,
		helpers.WithLambdaDocker(),
		helpers.WithECSDocker(),
		helpers.WithRegion("us-east-1"),
		helpers.WithAccountID("000000000000"),
	)
	helpers.PullOrSkip(t, dc, "registry:2")

	repoURI := createECRRepository(t, srv, cdkAssetRepository)

	// Given: an image published to it exactly as cdk-assets publishes one —
	// authenticate with the ECR token, then push to repositoryUri.
	user, password, proxy := ecrAuthorization(t, srv)
	dockerLoginOrSkip(t, proxy, user, password)

	pushed := repoURI + ":" + cdkAssetTag
	runDocker(t, "tag", "registry:2", pushed)
	runDocker(t, "push", pushed)
	// Drop the local tag so the launch has to fetch the image from the registry
	// rather than finding it already sitting in the daemon. Without this the
	// pull could fail and the task would still start, which is the one thing
	// this test must not be able to conclude from.
	runDocker(t, "image", "rm", "-f", pushed)

	// Given: a task definition naming the image the way CDK writes it.
	cdkImage := "000000000000.dkr.ecr.us-east-1.amazonaws.com/" + cdkAssetRepository + ":" + cdkAssetTag
	create := ecsCall(t, srv, "CreateCluster", map[string]any{"clusterName": "cdk-cluster"})
	helpers.AssertStatus(t, create, http.StatusOK)
	create.Body.Close()

	reg := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family": "cdk-asset-task",
		"containerDefinitions": []map[string]any{{
			"name":       "app",
			"image":      cdkImage,
			"entryPoint": []string{"/bin/sh", "-c"},
			"command":    []string{"sleep 60"},
		}},
	})
	helpers.AssertStatus(t, reg, http.StatusOK)
	reg.Body.Close()

	// When: the task is placed.
	run := ecsCall(t, srv, "RunTask", map[string]any{
		"cluster":        "cdk-cluster",
		"taskDefinition": "cdk-asset-task",
	})
	helpers.AssertStatus(t, run, http.StatusOK)
	var out struct {
		Tasks []struct {
			LastStatus    string `json:"lastStatus"`
			StopCode      string `json:"stopCode"`
			StoppedReason string `json:"stoppedReason"`
			Containers    []struct {
				Image string `json:"image"`
			} `json:"containers"`
		} `json:"tasks"`
	}
	helpers.DecodeJSON(t, run, &out)
	run.Body.Close()

	// Then: it is running, not stopped for want of a registry it can reach.
	if len(out.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(out.Tasks))
	}
	task := out.Tasks[0]
	if task.LastStatus == "STOPPED" {
		t.Fatalf("task failed to start: stopCode=%s reason=%s", task.StopCode, task.StoppedReason)
	}

	// Then: the task still reports the image its definition asked for. The
	// rewrite is how the bytes are fetched, not a change to what was deployed.
	if got := task.Containers[0].Image; got != cdkImage {
		t.Errorf("container image = %q, want the task definition's %q", got, cdkImage)
	}
}

// createECRRepository creates repo and returns the repositoryUri ECR minted —
// the address a push is meant to go to.
func createECRRepository(t *testing.T, srv *helpers.TestServer, repo string) string {
	t.Helper()
	resp := awsJSONCall(t, srv, "AmazonEC2ContainerRegistry_V20150921.CreateRepository", map[string]any{
		"repositoryName": repo,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Repository struct {
			RepositoryUri string `json:"repositoryUri"`
		} `json:"repository"`
	}
	helpers.DecodeJSON(t, resp, &out)
	resp.Body.Close()
	if out.Repository.RepositoryUri == "" {
		t.Fatal("CreateRepository returned no repositoryUri")
	}
	return out.Repository.RepositoryUri
}

// ecrAuthorization returns the credentials and endpoint a client logs in with.
func ecrAuthorization(t *testing.T, srv *helpers.TestServer) (user, password, proxy string) {
	t.Helper()
	resp := awsJSONCall(t, srv, "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken", map[string]any{})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		AuthorizationData []struct {
			AuthorizationToken string `json:"authorizationToken"`
			ProxyEndpoint      string `json:"proxyEndpoint"`
		} `json:"authorizationData"`
	}
	helpers.DecodeJSON(t, resp, &out)
	resp.Body.Close()
	if len(out.AuthorizationData) != 1 {
		t.Fatalf("expected one authorizationData entry, got %d", len(out.AuthorizationData))
	}
	decoded, err := base64.StdEncoding.DecodeString(out.AuthorizationData[0].AuthorizationToken)
	if err != nil {
		t.Fatalf("decode authorizationToken: %v", err)
	}
	user, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		t.Fatalf("unexpected authorizationToken format %q", decoded)
	}
	return user, password, out.AuthorizationData[0].ProxyEndpoint
}

// awsJSONCall dispatches an AWS JSON 1.1 request by X-Amz-Target, for the
// services this ECS test has to set up alongside ECS itself.
func awsJSONCall(t *testing.T, srv *helpers.TestServer, target string, body map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", target, err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("build %s request: %v", target, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	return resp
}

// dockerLoginOrSkip authenticates the Docker CLI against the emulated registry,
// skipping when the daemon refuses plain HTTP to it. That is a daemon
// configuration this test cannot change and says nothing about Overcast — the
// same gate the ECR push/pull round-trip test applies.
func dockerLoginOrSkip(t *testing.T, proxy, user, password string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "docker", "login", proxy, "-u", user, "--password-stdin")
	cmd.Stdin = strings.NewReader(password + "\n")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	msg := string(out)
	if strings.Contains(msg, "server gave HTTP response to HTTPS client") ||
		strings.Contains(msg, "https://") ||
		strings.Contains(msg, "context deadline exceeded") {
		t.Skipf("docker daemon will not talk plain HTTP to %s: %s", proxy, strings.TrimSpace(msg))
	}
	t.Fatalf("docker login %s: %v\n%s", proxy, err, out)
}

// runDocker runs a Docker CLI command, failing the test if it does not succeed.
func runDocker(t *testing.T, args ...string) {
	t.Helper()
	if args[0] == "tag" {
		// Every tag this test creates is removed with the server, so the daemon
		// is not left holding an image named after a run that is over.
		t.Cleanup(func() {
			_ = exec.CommandContext(context.Background(), "docker", "image", "rm", "-f", args[2]).Run()
		})
	}
	out, err := exec.CommandContext(t.Context(), "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
