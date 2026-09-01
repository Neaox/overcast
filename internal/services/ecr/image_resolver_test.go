package ecr

// image_resolver_test.go — the mapping from the ECR image reference a
// CloudFormation template carries to the one this environment can pull.
//
// CDK writes a container asset's image into an ECS task definition as
// "{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}", synthesised from
// AWS::AccountId / AWS::Region rather than read back from the repository. That
// name resolves — the ECR endpoint is a wildcard — so a pull of it reaches real
// AWS, is refused anonymously, and the task dies with CannotPullContainerError.
// The bytes are in the registry Overcast serves, under the repositoryUri ECR
// minted and behind the credentials GetAuthorizationToken hands out.

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// resolverService returns an ECR service standing in for one whose registry
// container is up on hostPort with password as its htpasswd secret.
func resolverService(hostPort int, password string) *Service {
	s := &Service{
		cfg: &config.Config{Hostname: "localhost", Port: 4566, AccountID: "000000000000", Region: "ap-southeast-2"},
		// Every Service has a logger — the address helpers log when they have
		// no registry to name, and a literal without one would panic there
		// rather than in anything this test is about.
		log: serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
	}
	s.registryPassword = password
	// The address as startup records it, daemon-proved — the only way the
	// registry's host and port are ever set.
	s.adoptRegistryAddress(hostPort, true)
	return s
}

func TestResolveImage_ecrURIBecomesTheServedRegistry(t *testing.T) {
	// Given: a running local registry, and the image reference CDK emits for a
	// container asset published to the CDK bootstrap repository.
	s := resolverService(32771, "s3cret")
	const image = "000000000000.dkr.ecr.ap-southeast-2.amazonaws.com/" +
		"cdk-hnb659fds-container-assets-000000000000-ap-southeast-2:610a3ca0"

	// When: the image is resolved for a pull.
	got := s.ResolveImage(context.Background(), image)

	// Then: it names the registry Overcast serves, on the same repositoryUri
	// path the push went to, carrying the registry's credentials.
	want := "localhost:32771/000000000000/cdk-hnb659fds-container-assets-000000000000-ap-southeast-2:610a3ca0"
	if got.Ref != want {
		t.Errorf("Ref = %q, want %q", got.Ref, want)
	}
	if got.Auth == nil {
		t.Fatal("Auth is nil; the registry requires basic auth and an anonymous pull is refused")
	}
	if got.Auth.Username != "AWS" || got.Auth.Password != "s3cret" {
		t.Errorf("Auth = %+v, want AWS/s3cret", got.Auth)
	}
	if got.Auth.ServerAddress != "localhost:32771" {
		t.Errorf("Auth.ServerAddress = %q, want localhost:32771", got.Auth.ServerAddress)
	}
}

func TestResolveImage_digestPinnedKeepsItsDigest(t *testing.T) {
	// Given: an image pinned by digest rather than tag.
	s := resolverService(32771, "s3cret")
	const digest = "sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

	// When: it is resolved.
	got := s.ResolveImage(context.Background(), "000000000000.dkr.ecr.ap-southeast-2.amazonaws.com/app@"+digest)

	// Then: the digest survives — dropping it would run a different image.
	want := "localhost:32771/000000000000/app@" + digest
	if got.Ref != want {
		t.Errorf("Ref = %q, want %q", got.Ref, want)
	}
}

func TestResolveImage_foreignReferencesAreLeftAlone(t *testing.T) {
	// Given: a running local registry.
	s := resolverService(32771, "s3cret")

	// When/Then: images this registry does not serve are returned untouched and
	// unauthenticated — sending the local registry's credentials to Docker Hub
	// would leak them, and rewriting the reference would break the pull.
	for _, image := range []string{
		"alpine:latest",
		"public.ecr.aws/lambda/nodejs:22",
		"registry.k8s.io/sig-storage/nfs-provisioner:v4.0.8",
		// Another account's registry is genuinely somewhere else.
		"111111111111.dkr.ecr.ap-southeast-2.amazonaws.com/app:v1",
		// A repository named after the ECR grammar but hosted elsewhere.
		"example.com/000000000000.dkr.ecr.ap-southeast-2.amazonaws.com/app:v1",
	} {
		got := s.ResolveImage(context.Background(), image)
		if got.Ref != image {
			t.Errorf("ResolveImage(%q).Ref = %q, want it unchanged", image, got.Ref)
		}
		if got.Auth != nil {
			t.Errorf("ResolveImage(%q).Auth = %+v, want nil", image, got.Auth)
		}
	}
}

func TestResolveImage_anyRegionOfThisAccountResolves(t *testing.T) {
	// Given: a service configured for ap-southeast-2, and an image published to
	// this account's registry in another region. Overcast serves one registry
	// for every region, so the region in the hostname does not decide whether
	// the image is ours — a stack deployed to us-east-1 pushed to the same one.
	s := resolverService(32771, "s3cret")

	// When: a us-east-1 reference for this account is resolved.
	got := s.ResolveImage(context.Background(), "000000000000.dkr.ecr.us-east-1.amazonaws.com/app:v1")

	// Then: it resolves to the served registry.
	if want := "localhost:32771/000000000000/app:v1"; got.Ref != want {
		t.Errorf("Ref = %q, want %q", got.Ref, want)
	}
}

func TestResolveImage_withoutARunningRegistryNothingIsClaimed(t *testing.T) {
	// Given: no registry container — Docker is unavailable, so nothing was ever
	// pushed here and there is nothing to serve.
	s := resolverService(0, "")
	const image = "000000000000.dkr.ecr.ap-southeast-2.amazonaws.com/app:v1"

	// When: the image is resolved.
	got := s.ResolveImage(context.Background(), image)

	// Then: it is left alone. Pointing it at a registry that is not running
	// would replace a comprehensible failure with a confusing one.
	if got.Ref != image {
		t.Errorf("Ref = %q, want it unchanged", got.Ref)
	}
	if got.Auth != nil {
		t.Errorf("Auth = %+v, want nil", got.Auth)
	}
}
