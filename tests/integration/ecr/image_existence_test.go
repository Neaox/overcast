package ecr_test

// image_existence_test.go — DescribeImages is how a publisher asks "is this
// image already here?", and the answer decides whether anything gets pushed.
//
// cdk-assets, the publisher behind `cdk deploy`, asks exactly that before
// building and pushing a container asset
// (cdklabs/cdk-assets, lib/private/handlers/container-images.ts):
//
//	async function imageExists(ecr, repositoryName, imageTag) {
//	  try {
//	    await ecr.describeImages({ repositoryName, imageIds: [{ imageTag }] });
//	    return true;
//	  } catch (e) {
//	    if (e.name !== 'ImageNotFoundException') { throw e; }
//	    return false;
//	  }
//	}
//
// A response — any response — means the image is there and the push is skipped.
// So an emulator that answers an absent tag with 200 and an empty imageDetails
// list tells CDK the asset is published when nothing was ever pushed, and the
// ECS service that runs it dies at pull time with the registry's honest answer:
//
//	CannotPullContainerError: … status 404: {"message":"failed to resolve
//	reference … : not found"}
//
// Real ECR raises ImageNotFoundException when a requested imageId is absent
// (AWS ECR API Reference, DescribeImages § Errors: "The image requested does
// not exist in the specified repository."), and only returns an empty list when
// no imageIds were requested at all.

import (
	"context"
	"net/http"
	"os/exec"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestDescribeImages_absentTagIsImageNotFound(t *testing.T) {
	// Given: a bootstrapped repository that has never been pushed to — the
	// state `cdk bootstrap` leaves behind before the first container asset.
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{
		"repositoryName": "cdk-hnb659fds-container-assets-000000000000-us-east-1",
	}).Body.Close()

	// When: a publisher asks whether the asset's tag is already there.
	resp := ecrCall(t, srv, "DescribeImages", map[string]any{
		"repositoryName": "cdk-hnb659fds-container-assets-000000000000-us-east-1",
		"imageIds":       []map[string]any{{"imageTag": "10fe2b8f652c7385af52e5854ae3be2848572867"}},
	})
	defer resp.Body.Close()

	// Then: it is told the image is not there, in the one way a client can act
	// on. A 200 with no details reads as "published" and the asset never is.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an absent tag, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "ImageNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

func TestDescribeImages_absentDigestIsImageNotFound(t *testing.T) {
	// Given: a repository holding one image.
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "digest-lookup"}).Body.Close()
	ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName": "digest-lookup",
		"imageManifest":  `{"schemaVersion":2}`,
		"imageTag":       "v1",
	}).Body.Close()

	// When: a different image is asked for by digest.
	resp := ecrCall(t, srv, "DescribeImages", map[string]any{
		"repositoryName": "digest-lookup",
		"imageIds": []map[string]any{
			{"imageDigest": "sha256:0000000000000000000000000000000000000000000000000000000000000000"},
		},
	})
	defer resp.Body.Close()

	// Then: absent by digest is as absent as absent by tag.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an absent digest, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "ImageNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

func TestDescribeImages_oneAbsentIDFailsTheWholeRequest(t *testing.T) {
	// Given: a repository holding v1 but not v2.
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "partial"}).Body.Close()
	ecrCall(t, srv, "PutImage", map[string]any{
		"repositoryName": "partial",
		"imageManifest":  `{"schemaVersion":2}`,
		"imageTag":       "v1",
	}).Body.Close()

	// When: both are asked for at once.
	resp := ecrCall(t, srv, "DescribeImages", map[string]any{
		"repositoryName": "partial",
		"imageIds":       []map[string]any{{"imageTag": "v1"}, {"imageTag": "v2"}},
	})
	defer resp.Body.Close()

	// Then: the request fails rather than quietly returning the half it found.
	// DescribeImages has no per-image failure list — unlike BatchGetImage — so
	// a partial answer is indistinguishable from a complete one.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when one of two ids is absent, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["__type"] != "ImageNotFoundException" {
		t.Fatalf("unexpected error type: %v", body["__type"])
	}
}

func TestDescribeImages_emptyRepositoryWithoutImageIDsIsEmpty(t *testing.T) {
	// Given: an empty repository.
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "unfiltered"}).Body.Close()

	// When: its images are listed with no imageIds to resolve.
	resp := ecrCall(t, srv, "DescribeImages", map[string]any{"repositoryName": "unfiltered"})
	defer resp.Body.Close()

	// Then: nothing was asked for by name, so nothing is missing — an empty
	// list, not an error. Only a requested id can fail to resolve.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for an unfiltered describe, got %d", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	details, ok := body["imageDetails"].([]any)
	if !ok || len(details) != 0 {
		t.Fatalf("expected an empty imageDetails list, got %#v", body["imageDetails"])
	}
}

func TestDescribeImages_withDocker_pushedImageStaysFoundAcrossReads(t *testing.T) {
	skipWithoutDocker(t)

	// Given: an image pushed to the registry Overcast serves.
	srv := helpers.NewTestServer(t,
		helpers.WithLambdaDocker(),
		helpers.WithRegion("us-east-1"),
		helpers.WithAccountID("000000000000"),
		helpers.WithECRRegistryPort(helpers.ReserveTCPPort(t)),
	)
	createResp := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "still-there"})
	repoURI, _ := mustDecode(t, createResp)["repository"].(map[string]any)["repositoryUri"].(string)
	if repoURI == "" {
		t.Fatal("missing repositoryUri")
	}
	dockerLoginOrSkip(t, srv)
	target := repoURI + ":kept"
	runDockerCommand(t, "tag", "registry:2", target)
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "image", "rm", "-f", target).Run()
	})
	runDockerCommand(t, "push", target)

	// When: the repository is read twice. The first read records the push; the
	// second re-examines that record against the registry, which is where a
	// too-eager reconciliation would delete an image that is still served.
	for attempt := 1; attempt <= 2; attempt++ {
		resp := ecrCall(t, srv, "DescribeImages", map[string]any{
			"repositoryName": "still-there",
			"imageIds":       []map[string]any{{"imageTag": "kept"}},
		})
		status := resp.StatusCode
		body := mustDecode(t, resp)

		// Then: the image is found both times. If the second read reported it
		// missing, every deploy would re-push an asset that never left.
		if status != http.StatusOK {
			t.Fatalf("read %d: expected 200 for a pushed image, got %d (%v)", attempt, status, body["__type"])
		}
		details, ok := body["imageDetails"].([]any)
		if !ok || len(details) != 1 {
			t.Fatalf("read %d: expected one imageDetails entry, got %#v", attempt, body["imageDetails"])
		}
	}
}

func TestDescribeImages_absentTagIsImageNotFoundOverCBOR(t *testing.T) {
	// Given: an empty repository, addressed over the RPC v2 CBOR protocol,
	// which dispatches to the typed operation rather than the JSON handler.
	srv := helpers.NewTestServer(t, helpers.WithRegion("us-east-1"), helpers.WithAccountID("000000000000"))
	ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "cbor-images"}).Body.Close()

	// When: an absent tag is asked for.
	resp := ecrCBORCall(t, srv, "DescribeImages", map[string]any{
		"repositoryName": "cbor-images",
		"imageIds":       []map[string]any{{"imageTag": "nope"}},
	})
	defer resp.Body.Close()

	// Then: the same verdict reaches a CBOR client. Both wire formats answer
	// the same question and must not disagree about what is published.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an absent tag over CBOR, got %d", resp.StatusCode)
	}
	var out struct {
		Type string `cbor:"__type"`
	}
	decodeCBOR(t, resp, &out)
	if out.Type != "ImageNotFoundException" {
		t.Fatalf("unexpected error type over CBOR: %q", out.Type)
	}
}
