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
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// cdkAssetRepository is the repository name `cdk bootstrap` creates for
// container assets, and the tag is a content hash as cdk-assets computes it.
const (
	cdkAssetRepository = "cdk-hnb659fds-container-assets-000000000000-us-east-1"
	cdkAssetTag        = "610a3ca08b333a5ac4b8d23e4df2830ddffeac82"
	// The second scenario publishes its own asset so the two tests cannot
	// satisfy each other through the registry they share.
	cdkPublishTag = "10fe2b8f652c7385af52e5854ae3be28485728678193bc08b5d5f29ef9b0b43a"
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
		// A fixed port, as production runs: on Docker Desktop the daemon
		// cannot reach an ephemeral publish, so the harness default would skip
		// this test on the platform most users deploy from.
		helpers.WithECRRegistryPort(helpers.ReserveTCPPort(t)),
	)
	// The probe that wires Docker into ECS runs in a goroutine started with the
	// server, so RunTask below would otherwise race it. The registry setup that
	// follows happens to be slow enough to hide that today — which is not a
	// property this test should be resting on.
	helpers.WaitForECSDocker(t, srv)
	helpers.PullOrSkip(t, dc, "registry:2")

	repoURI := createECRRepository(t, srv, cdkAssetRepository)

	// Given: an image published to it exactly as cdk-assets publishes one —
	// authenticate with the ECR token, then push to repositoryUri.
	user, password, proxy := ecrAuthorization(t, srv)
	helpers.DockerLoginOrSkip(t, proxy, user, password)

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
			TaskArn    string `json:"taskArn"`
			Containers []struct {
				Image string `json:"image"`
			} `json:"containers"`
		} `json:"tasks"`
	}
	helpers.DecodeJSON(t, run, &out)
	run.Body.Close()

	if len(out.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(out.Tasks))
	}
	task := out.Tasks[0]

	// Then: it reaches RUNNING, rather than stopping for want of a registry it
	// can reach.
	//
	// RUNNING is the assertion rather than "not STOPPED" because only a task
	// with containers behind it gets there: the PROVISIONING → RUNNING
	// transition is scheduled solely when Docker is wired, so a metadata-only
	// placement sits at PROVISIONING for good — and would satisfy a
	// "not STOPPED" check having started no container and pulled no image.
	awaitTaskRunning(t, srv, "cdk-cluster", task.TaskArn)

	// Then: the task still reports the image its definition asked for. The
	// rewrite is how the bytes are fetched, not a change to what was deployed.
	if got := task.Containers[0].Image; got != cdkImage {
		t.Errorf("container image = %q, want the task definition's %q", got, cdkImage)
	}
}

// awaitTaskRunning blocks until the task reports RUNNING.
//
// RunTask answers while the task is still PROVISIONING — the transition is
// scheduled behind it — so the status in its response says nothing about
// whether the containers started. A task that could not pull its image stops
// rather than staying put, so STOPPED fails immediately with the reason ECS
// recorded instead of waiting out the timeout and reporting only that RUNNING
// never arrived.
//
// The cluster is required: DescribeTasks is cluster-scoped and falls back to
// the default cluster when none is given, which is not where this task is. It
// would report no tasks at all and the wait would time out having never
// actually looked at the task.
func awaitTaskRunning(t *testing.T, srv *helpers.TestServer, cluster, taskArn string) {
	t.Helper()
	helpers.Eventually(t, 60*time.Second, 100*time.Millisecond, func() bool {
		resp := ecsCall(t, srv, "DescribeTasks", map[string]any{
			"cluster": cluster,
			"tasks":   []string{taskArn},
		})
		defer resp.Body.Close()
		var out struct {
			Tasks []struct {
				LastStatus    string `json:"lastStatus"`
				StopCode      string `json:"stopCode"`
				StoppedReason string `json:"stoppedReason"`
			} `json:"tasks"`
		}
		helpers.DecodeJSON(t, resp, &out)
		if len(out.Tasks) != 1 {
			return false
		}
		if out.Tasks[0].LastStatus == "STOPPED" {
			t.Fatalf("task failed to start: stopCode=%s reason=%s",
				out.Tasks[0].StopCode, out.Tasks[0].StoppedReason)
		}
		return out.Tasks[0].LastStatus == "RUNNING"
	}, "the task never reached RUNNING, so it was left at PROVISIONING with no containers started for it")
}

// TestRunTask_withDocker_runsAnAssetPublishedTheWayCDKPublishesOne is the whole
// chain, from `cdk deploy`'s point of view: the publisher decides whether to
// push by asking ECR whether the image is already there, and the task then has
// to run whatever that decision left in the registry.
//
// The previous test pushes unconditionally, so it could only ever prove the
// pull. It passed while every real deploy failed:
//
//	CannotPullContainerError: ecs: pull image 000000000000.dkr.ecr.…:<tag>,
//	served here as localhost.overcast.sh:4510/000000000000/…:<tag>: status 404:
//	{"message":"failed to resolve reference … : not found"}
//
// because DescribeImages answered the existence check with 200 and an empty
// list, cdk-assets read that as "already published", and nothing was ever
// pushed. The gap between the two tests is exactly the bug.
func TestRunTask_withDocker_runsAnAssetPublishedTheWayCDKPublishesOne(t *testing.T) {
	dc := skipWithoutDocker(t)

	// Given: a bootstrapped container-asset repository and nothing in it.
	srv := helpers.NewTestServer(t,
		helpers.WithLambdaDocker(),
		helpers.WithECSDocker(),
		helpers.WithRegion("us-east-1"),
		helpers.WithAccountID("000000000000"),
		helpers.WithECRRegistryPort(helpers.ReserveTCPPort(t)),
	)
	helpers.WaitForECSDocker(t, srv)
	helpers.PullOrSkip(t, dc, "registry:2")
	repoURI := createECRRepository(t, srv, cdkAssetRepository)

	// When: the asset is published the way cdk-assets publishes one.
	pushed := publishCDKContainerAsset(t, srv, cdkAssetRepository, repoURI, cdkPublishTag)

	// Then: it was actually published. A publisher told the image is already
	// there does not build, does not push, and reports success — the deploy
	// looks clean right up until the task tries to pull.
	if !pushed {
		t.Fatal("the asset was never pushed: ECR reported it already published to a repository that had never been pushed to")
	}

	// Given: a task definition naming the image the way CDK writes it.
	cdkImage := "000000000000.dkr.ecr.us-east-1.amazonaws.com/" + cdkAssetRepository + ":" + cdkPublishTag
	create := ecsCall(t, srv, "CreateCluster", map[string]any{"clusterName": "cdk-publish-cluster"})
	helpers.AssertStatus(t, create, http.StatusOK)
	create.Body.Close()

	reg := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family": "cdk-published-task",
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
		"cluster":        "cdk-publish-cluster",
		"taskDefinition": "cdk-published-task",
	})
	helpers.AssertStatus(t, run, http.StatusOK)
	var out struct {
		Tasks []struct {
			TaskArn string `json:"taskArn"`
		} `json:"tasks"`
	}
	helpers.DecodeJSON(t, run, &out)
	run.Body.Close()

	// Then: it reaches RUNNING. The bytes are where the publisher put them and
	// where the task definition's name resolves to.
	if len(out.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(out.Tasks))
	}
	awaitTaskRunning(t, srv, "cdk-publish-cluster", out.Tasks[0].TaskArn)
}

// publishCDKContainerAsset publishes an image to repoURI the way cdk-assets
// does, and reports whether it pushed anything.
//
// The shape is cdk-assets' own (cdklabs/cdk-assets,
// lib/private/handlers/container-images.ts): DescribeImages for the asset's
// tag, and a push only if that call throws ImageNotFoundException. Any other
// answer — including a 200 carrying no image details — means "already
// published" and the whole build-and-push is skipped.
func publishCDKContainerAsset(t *testing.T, srv *helpers.TestServer, repo, repoURI, tag string) (pushed bool) {
	t.Helper()

	resp := awsJSONCall(t, srv, "AmazonEC2ContainerRegistry_V20150921.DescribeImages", map[string]any{
		"repositoryName": repo,
		"imageIds":       []map[string]any{{"imageTag": tag}},
	})
	var describe struct {
		Type string `json:"__type"`
	}
	helpers.DecodeJSON(t, resp, &describe)
	status := resp.StatusCode
	resp.Body.Close()
	if status == http.StatusOK {
		return false
	}
	if describe.Type != "ImageNotFoundException" {
		t.Fatalf("DescribeImages failed with %d %s; cdk-assets rethrows anything that is not ImageNotFoundException", status, describe.Type)
	}

	user, password, proxy := ecrAuthorization(t, srv)
	helpers.DockerLoginOrSkip(t, proxy, user, password)

	image := repoURI + ":" + tag
	runDocker(t, "tag", "registry:2", image)
	runDocker(t, "push", image)
	// Drop the local tag so the launch has to fetch the image from the registry
	// rather than finding it already sitting in the daemon.
	runDocker(t, "image", "rm", "-f", image)
	return true
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
