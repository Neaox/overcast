package ecr

// registry_sync_test.go — the image inventory must not outlive the registry.
//
// Nothing tells Overcast about a `docker push`: the daemon talks straight to
// the registry container, so the only way ECR's image records exist at all is
// the sweep in syncRepoImagesFromRegistry, run when a client asks what is in a
// repository. Those records are then persisted, and the registry's contents
// are not — it is an AutoRemove container with no volume, so an Overcast
// restart brings up an empty one under the same repositoryUri.
//
// A sweep that only ever adds therefore keeps answering "published" for bytes
// that are gone, which is the worst possible answer: cdk-assets skips the push
// on it, and the ECS task that runs the asset dies on a 404 from the registry
// with no sign of how the manifest went missing. The registry is the authority
// on what it serves, so a record it 404s is deleted.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	syncRegion   = "us-east-1"
	syncRepo     = "cdk-hnb659fds-container-assets-000000000000-us-east-1"
	syncManifest = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`
)

// fakeRegistry serves the subset of the Distribution API the sweep uses: the
// repository's tag list, and a manifest per tag or digest. Anything it was not
// given answers 404, exactly as registry:2 does for a manifest it does not
// hold.
type fakeRegistry struct {
	// tags maps tag → digest, as /tags/list and a manifest GET report them.
	tags map[string]string
	// untagged are digests the registry still holds without a tag pointing at
	// them — reachable by digest only.
	untagged map[string]bool
	// absent records whether the repository exists in the registry at all.
	// A registry that has never been pushed to 404s the tag list.
	absent bool
	// manifestRequests counts every per-image question the sweep asked, so a
	// test can hold the sweep to asking none when one request already settled
	// the matter.
	manifestRequests atomic.Int32
}

func (f *fakeRegistry) start(t *testing.T) string {
	t.Helper()
	prefix := "/v2/000000000000/" + syncRepo
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != ecrRegistryUser || password != syncPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == prefix+"/tags/list":
			if f.absent {
				http.Error(w, `{"errors":[{"code":"NAME_UNKNOWN"}]}`, http.StatusNotFound)
				return
			}
			names := make([]string, 0, len(f.tags))
			for tag := range f.tags {
				names = append(names, tag)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"name": syncRepo, "tags": names})
		case strings.HasPrefix(r.URL.Path, prefix+"/manifests/"):
			f.manifestRequests.Add(1)
			ref := strings.TrimPrefix(r.URL.Path, prefix+"/manifests/")
			digest, tagged := f.tags[ref]
			if !tagged && (f.untagged[ref] || slicesContains(f.tags, ref)) {
				digest = ref
			} else if !tagged {
				http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, http.StatusNotFound)
				return
			}
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			_, _ = w.Write([]byte(syncManifest))
		default:
			http.Error(w, `{"errors":[{"code":"UNSUPPORTED"}]}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// slicesContains reports whether any tag points at digest.
func slicesContains(tags map[string]string, digest string) bool {
	for _, d := range tags {
		if d == digest {
			return true
		}
	}
	return false
}

const syncPassword = "sync-registry-password"

// syncService wires a Service to a fake registry, with the registry already
// "started" so the sweep never reaches Docker.
func syncService(t *testing.T, registryURL string) *Service {
	t.Helper()
	s := &Service{
		cfg:   &config.Config{Region: syncRegion, AccountID: "000000000000"},
		store: state.NewMemoryStore(),
		log:   serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		// Non-nil so the sweep runs; never dialled, because the registry is
		// recorded as already up.
		docker:             docker.NewClient("tcp://127.0.0.1:1", zap.NewNop()),
		registryHostPort:   4510,
		registryPassword:   syncPassword,
		registryClientBase: registryURL,
	}
	return s
}

// seedImage writes an image record as one of the two sources would.
func seedImage(t *testing.T, s *Service, digest, tag string, fromRegistry bool) {
	t.Helper()
	rec := imageRecord{
		Image: Image{
			RegistryId:     s.accountID(),
			RepositoryName: syncRepo,
			ImageManifest:  syncManifest,
			ImageId:        ImageIdentifier{ImageDigest: digest, ImageTag: tag},
		},
		FromRegistry: fromRegistry,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal seeded image: %v", err)
	}
	if err := s.store.Set(context.Background(), imageNamespace, imageKey(syncRegion, syncRepo, digest), string(raw)); err != nil {
		t.Fatalf("seed image: %v", err)
	}
}

// storedTags returns the tags of every image record held for the repository.
func storedTags(t *testing.T, s *Service) []string {
	t.Helper()
	kvs, err := s.store.Scan(context.Background(), imageNamespace, serviceutil.RegionKey(syncRegion, syncRepo+"/"))
	if err != nil {
		t.Fatalf("scan images: %v", err)
	}
	tags := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		var img Image
		if err := json.Unmarshal([]byte(kv.Value), &img); err != nil {
			t.Fatalf("unmarshal stored image: %v", err)
		}
		tags = append(tags, fmt.Sprintf("%s@%s", img.ImageId.ImageTag, img.ImageId.ImageDigest))
	}
	return tags
}

func TestSyncRepoImages_recordsWhatTheRegistryServes(t *testing.T) {
	// Given: a registry holding one tagged image, and no records for it.
	reg := &fakeRegistry{tags: map[string]string{"asset-tag": "sha256:aaa"}}
	s := syncService(t, reg.start(t))

	// When: the repository is swept.
	if err := s.syncRepoImagesFromRegistry(context.Background(), syncRegion, syncRepo); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Then: the push the emulator never saw is now on record.
	if got := storedTags(t, s); len(got) != 1 || got[0] != "asset-tag@sha256:aaa" {
		t.Fatalf("stored images = %v, want [asset-tag@sha256:aaa]", got)
	}
}

func TestSyncRepoImages_dropsAnImageTheRegistryNoLongerHas(t *testing.T) {
	// Given: a record from a previous run's push, and a registry that has since
	// been replaced by an empty one — the AutoRemove container does not survive
	// an Overcast restart, but the persisted record does.
	reg := &fakeRegistry{absent: true}
	s := syncService(t, reg.start(t))
	seedImage(t, s, "sha256:gone", "asset-tag", true)

	// When: the repository is swept.
	if err := s.syncRepoImagesFromRegistry(context.Background(), syncRegion, syncRepo); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Then: the record goes with the bytes. Left in place it would tell CDK the
	// asset is published, and the push that would republish it never runs.
	if got := storedTags(t, s); len(got) != 0 {
		t.Fatalf("stored images = %v, want none — the registry no longer serves them", got)
	}

	// Then: it took one request to establish that, not one per image. A
	// registry with no repository under the name has no manifest under it
	// either, by tag or by digest, so there is nothing left to ask — and this
	// is the restart case, where the store is at its fullest.
	if got := reg.manifestRequests.Load(); got != 0 {
		t.Errorf("manifest requests = %d, want 0 — the tag list already settled it", got)
	}
}

func TestSyncRepoImages_keepsAnUntaggedImageTheRegistryStillHas(t *testing.T) {
	// Given: a record whose tag has moved to a newer push, leaving the older
	// manifest in the registry reachable by digest alone.
	reg := &fakeRegistry{
		tags:     map[string]string{"asset-tag": "sha256:new"},
		untagged: map[string]bool{"sha256:old": true},
	}
	s := syncService(t, reg.start(t))
	seedImage(t, s, "sha256:old", "asset-tag", true)

	// When: the repository is swept.
	if err := s.syncRepoImagesFromRegistry(context.Background(), syncRegion, syncRepo); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Then: both survive. Absence from the tag list is not absence from the
	// registry, and real ECR keeps untagged images too — only a manifest the
	// registry answers 404 for is gone.
	got := storedTags(t, s)
	if len(got) != 2 {
		t.Fatalf("stored images = %v, want the new and the untagged old one", got)
	}
}

func TestSyncRepoImages_keepsARecordPutThroughTheAPI(t *testing.T) {
	// Given: an image registered with PutImage, which stores a manifest without
	// any bytes ever reaching the registry.
	reg := &fakeRegistry{absent: true}
	s := syncService(t, reg.start(t))
	seedImage(t, s, "sha256:api", "v1", false)

	// When: the repository is swept.
	if err := s.syncRepoImagesFromRegistry(context.Background(), syncRegion, syncRepo); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Then: it survives. The registry is the authority only on records that
	// came from it; a caller's own PutImage is not the sweep's to revoke.
	if got := storedTags(t, s); len(got) != 1 || got[0] != "v1@sha256:api" {
		t.Fatalf("stored images = %v, want the PutImage record intact", got)
	}
}

func TestSyncRepoImages_keepsEverythingWhenTheRegistryIsUnreachable(t *testing.T) {
	// Given: records from a live registry that has stopped answering — the
	// sweep can prove nothing about what it holds.
	s := syncService(t, "http://127.0.0.1:1")
	seedImage(t, s, "sha256:aaa", "asset-tag", true)

	// When: the repository is swept.
	if err := s.syncRepoImagesFromRegistry(context.Background(), syncRegion, syncRepo); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Then: nothing is deleted. Only a definite 404 is evidence of absence;
	// unreachable is evidence of nothing.
	if got := storedTags(t, s); len(got) != 1 {
		t.Fatalf("stored images = %v, want the record kept while the registry is unreachable", got)
	}
}
