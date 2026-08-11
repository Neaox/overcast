package ecr

// repository_uri_test.go — repositoryUri names wherever the registry is *now*.
//
// The address is not a property of the repository. It is the registry
// container's published port, which is chosen at startup: the fixed port when
// that is free, an ephemeral one when it is not, and nothing at all when Docker
// is unavailable, in which case the only address left to state is Overcast's
// own API base. A repository outlives all three, because repositories are
// persisted and the registry is not.
//
// So a repositoryUri frozen into the record at CreateRepository is a fact about
// the run that created it. `cdk bootstrap` creates the container-asset
// repository once and every later deploy reads it back, so a bootstrap that
// happened before the registry was up sent every subsequent `docker push` at
// Overcast's API port for good:
//
//	fail: docker push localhost.overcast.sh:4566/000000000000/cdk-hnb659fds-…
//	unexpected status from POST request to
//	http://localhost.overcast.sh:4566/v2/…/blobs/uploads/: 405 Method Not Allowed
//
// — while the pull side, which resolves through the live registry rather than
// the stored record, was reaching :4510 in the same deploy. Two addresses for
// one repository, disagreeing exactly as far as the store was stale.

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// uriService builds a service with no Docker, so the registry never starts and
// repositoryUri falls back to the API base — the state a bootstrap run has when
// Docker is not up yet.
func uriService(t *testing.T) *Service {
	t.Helper()
	return New(
		&config.Config{Hostname: "localhost.overcast.sh", Port: 4566, Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(),
		zap.NewNop(),
		clock.New(),
	)
}

func TestDescribeRepositories_reportsTheRegistryTheRunActuallyHas(t *testing.T) {
	ctx := context.Background()
	s := uriService(t)

	// Given: a repository created while there was no registry, so its stored
	// address is Overcast's API base.
	created, aerr := s.createRepositoryTyped(ctx, &createRepositoryRequest{
		RepositoryName: "cdk-hnb659fds-container-assets-000000000000-us-east-1",
	})
	if aerr != nil {
		t.Fatalf("CreateRepository: %v", aerr)
	}
	if got, want := created.Repository.RepositoryUri, "localhost.overcast.sh:4566/000000000000/cdk-hnb659fds-container-assets-000000000000-us-east-1"; got != want {
		t.Fatalf("without a registry, repositoryUri = %q, want the API base %q", got, want)
	}

	// Given: a later run in which the registry is up on the fixed port.
	s.adoptRegistryAddress(4510, true)

	// When: the repository is read back, the way cdk-assets reads it before
	// pushing.
	described, aerr := s.describeRepositoriesTyped(ctx, &describeRepositoriesRequest{
		RepositoryNames: []string{"cdk-hnb659fds-container-assets-000000000000-us-east-1"},
	})
	if aerr != nil {
		t.Fatalf("DescribeRepositories: %v", aerr)
	}

	// Then: it names the registry, not the address of a run that is over.
	want := "localhost:4510/000000000000/cdk-hnb659fds-container-assets-000000000000-us-east-1"
	if got := described.Repositories[0].RepositoryUri; got != want {
		t.Errorf("repositoryUri = %q, want %q — a push to the API port answers 405, not a registry", got, want)
	}
}

func TestDescribeRepositories_unfilteredListReportsTheCurrentRegistry(t *testing.T) {
	ctx := context.Background()
	s := uriService(t)

	// Given: a repository from a run without a registry.
	if _, aerr := s.createRepositoryTyped(ctx, &createRepositoryRequest{RepositoryName: "listed"}); aerr != nil {
		t.Fatalf("CreateRepository: %v", aerr)
	}
	s.adoptRegistryAddress(4510, true)

	// When: repositories are listed rather than named.
	described, aerr := s.describeRepositoriesTyped(ctx, &describeRepositoriesRequest{})
	if aerr != nil {
		t.Fatalf("DescribeRepositories: %v", aerr)
	}

	// Then: the listing agrees with the lookup. The console lists; a publisher
	// looks one up; they must not offer different addresses.
	if len(described.Repositories) != 1 {
		t.Fatalf("expected 1 repository, got %d", len(described.Repositories))
	}
	want := "localhost:4510/000000000000/listed"
	if got := described.Repositories[0].RepositoryUri; got != want {
		t.Errorf("listed repositoryUri = %q, want %q", got, want)
	}
}

func TestDeleteRepository_reportsTheCurrentRegistry(t *testing.T) {
	ctx := context.Background()
	s := uriService(t)

	// Given: a repository from a run without a registry.
	if _, aerr := s.createRepositoryTyped(ctx, &createRepositoryRequest{RepositoryName: "deleted"}); aerr != nil {
		t.Fatalf("CreateRepository: %v", aerr)
	}
	s.adoptRegistryAddress(4510, true)

	// When: it is deleted, which echoes the repository back.
	deleted, aerr := s.deleteRepositoryTyped(ctx, &deleteRepositoryRequest{RepositoryName: "deleted"})
	if aerr != nil {
		t.Fatalf("DeleteRepository: %v", aerr)
	}

	// Then: the echo agrees with every other answer about this repository. One
	// address per run, whichever operation is asked.
	want := "localhost:4510/000000000000/deleted"
	if got := deleted.Repository.RepositoryUri; got != want {
		t.Errorf("deleted repositoryUri = %q, want %q", got, want)
	}
}

func TestDescribeRepositories_leavesTheStoredRecordAlone(t *testing.T) {
	ctx := context.Background()
	s := uriService(t)

	if _, aerr := s.createRepositoryTyped(ctx, &createRepositoryRequest{RepositoryName: "untouched"}); aerr != nil {
		t.Fatalf("CreateRepository: %v", aerr)
	}
	s.adoptRegistryAddress(4510, true)

	// When: the repository is read.
	if _, aerr := s.describeRepositoriesTyped(ctx, &describeRepositoriesRequest{RepositoryNames: []string{"untouched"}}); aerr != nil {
		t.Fatalf("DescribeRepositories: %v", aerr)
	}

	// Then: the read wrote nothing. Repairing the record here would race
	// DeleteRepository and restore a repository that had just been removed,
	// and no client reads the record — they all come through this API.
	raw, found, err := s.store.Get(ctx, repoNamespace, serviceutil.RegionKey("us-east-1", "untouched"))
	if err != nil || !found {
		t.Fatalf("store.Get after describe: found=%v err=%v", found, err)
	}
	if !strings.Contains(raw, "localhost.overcast.sh:4566/000000000000/untouched") {
		t.Errorf("stored record = %s, want the value CreateRepository wrote, unmodified by a read", raw)
	}
}
