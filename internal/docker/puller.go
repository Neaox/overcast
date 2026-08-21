package docker

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// UtilityImage is the image Overcast runs its own infrastructure containers
// from — the ones that are not a user's workload and that a user never named.
// Small, multi-arch, and long-lived upstream.
//
// One constant rather than one per service, because the reason for the choice
// is the same everywhere and the daemon only has to hold one of them: EFS's
// root-directory materializer (internal/services/efs) and the network
// namespace container an awsvpc task's containers share
// (internal/services/ecs) both run from it, so a machine that has pulled it
// once can start either offline.
const UtilityImage = "busybox:1.36"

// ImagePuller deduplicates Docker image pulls. It ensures each image is
// pulled at most once per process lifetime. Services that run containers
// (RDS, ECS, Lambda) should share an ImagePuller rather than duplicating
// the sync.Map + sync.Once pattern.
type ImagePuller struct {
	client   *Client
	resolver ImageResolver
	pullOnce sync.Map // resolved image reference → *pullEntry
}

// pullEntry pairs a sync.Once with the error it produced.
type pullEntry struct {
	once sync.Once
	err  error
}

// NewImagePuller creates a puller backed by the given Docker client.
func NewImagePuller(c *Client) *ImagePuller {
	return &ImagePuller{client: c}
}

// WithResolver wires an ImageResolver so references to a registry Overcast
// serves are pulled — and run — from where they actually live. Returns the
// puller for chaining at construction. Without one every reference is taken
// at face value, which is right for the fixed public images most services use.
func (p *ImagePuller) WithResolver(r ImageResolver) *ImagePuller {
	p.resolver = r
	return p
}

// resolve maps image onto the reference this daemon can fetch. Pulls are keyed
// on the result rather than the input, so a reference whose resolution changes
// — the local registry coming up, or coming back on a new port — is fetched
// again rather than served from a record that no longer describes it.
func (p *ImagePuller) resolve(ctx context.Context, image string) ImageReference {
	if p.resolver == nil {
		return ImageReference{Ref: image}
	}
	return p.resolver.ResolveImage(ctx, image)
}

// Ensure pulls image if it hasn't been pulled yet. Concurrent calls for the
// same image block until the first pull completes and share its result. A
// FAILED pull drops the entry, so the next launch attempt retries instead of
// serving the cached error until restart — pulls are driven by user actions
// (RunTask, StartDBInstance, Invoke), not loops, so this cannot hammer a
// registry; what it prevents is one transient network failure bricking an
// image for the process lifetime.
func (p *ImagePuller) Ensure(ctx context.Context, image string) error {
	ref := p.resolve(ctx, image)
	err := p.ensure(ctx, ref)
	if err != nil && ref.Ref != image {
		// The failure reaches a user as a task's stoppedReason, so it has to
		// name the image they asked for as well as the one it was fetched as —
		// neither alone explains what happened.
		return fmt.Errorf("pull image %s, served here as %s: %w", image, ref.Ref, err)
	}
	return err
}

// ensure is Ensure on an already-resolved reference, so a caller that has one
// in hand does not resolve twice — and cannot lose the credentials by
// re-resolving a reference that no longer looks like the original.
func (p *ImagePuller) ensure(ctx context.Context, ref ImageReference) error {
	v, _ := p.pullOnce.LoadOrStore(ref.Ref, &pullEntry{})
	e := v.(*pullEntry)
	e.once.Do(func() {
		e.err = p.client.PullImageWithOptions(ctx, ref.Ref, PullOptions{Auth: ref.Auth})
		if e.err != nil && p.imagePresent(ctx, ref.Ref) {
			// The pull failed but the daemon already has the image, so the
			// container can still start. This is the difference between "you
			// are offline" and "you cannot run anything": a registry that is
			// unreachable, rate-limiting, or (on Docker Desktop's containerd
			// image store) returning a stale-lease 404 must not stop a local
			// image being used, which is the whole point of having pulled it.
			e.err = nil
		}
		if e.err != nil {
			p.invalidate(ref)
		}
	})
	return e.err
}

// imagePresent reports whether the daemon already holds image locally.
func (p *ImagePuller) imagePresent(ctx context.Context, image string) bool {
	present, err := p.client.ImageExists(ctx, image)
	return err == nil && present
}

// invalidate forgets that ref was pulled, so the next ensure pulls it again.
// Called when the pull failed, and when the daemon proves the record wrong by
// failing a create with "No such image".
func (p *ImagePuller) invalidate(ref ImageReference) {
	p.pullOnce.Delete(ref.Ref)
}

// IsImageMissingErr reports whether a container-create failure means the
// image is gone from the daemon ("No such image": removed after it was
// pulled or verified present).
func IsImageMissingErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such image")
}

// CreateContainerWithRetry creates a container from the resolved form of the
// requested image and, when the daemon reports that image missing — removed
// behind our back after the pull was recorded — forgets the stale pull record,
// re-pulls, and retries the create once. Without this, `docker rmi` of a task
// or DB image makes every later launch fail until restart, because the spent
// pull record short-circuits Ensure.
//
// The request's image is rewritten in place to the resolved reference: the
// daemon stored the bytes under that name, so creating from the original would
// fail with "No such image" straight after a pull that worked.
func (p *ImagePuller) CreateContainerWithRetry(ctx context.Context, name string, req *CreateContainerRequest) (string, error) {
	if req == nil || req.ContainerConfig == nil {
		return p.client.CreateContainer(ctx, name, req)
	}
	ref := p.resolve(ctx, req.ContainerConfig.Image)
	req.ContainerConfig.Image = ref.Ref

	id, err := p.client.CreateContainer(ctx, name, req)
	if err != nil && IsImageMissingErr(err) {
		p.invalidate(ref)
		if pullErr := p.ensure(ctx, ref); pullErr == nil {
			id, err = p.client.CreateContainer(ctx, name, req)
		}
	}
	return id, err
}

// Prewarm starts Ensure in a background goroutine using a detached context
// so the pull is not tied to any caller's request deadline. Safe to call
// from request handlers at resource-creation time (CreateFunction,
// RegisterTaskDefinition, CreateDBInstance). If the same image is requested
// again on the invoke path, the caller blocks on the same sync.Once and
// reuses the in-flight pull.
func (p *ImagePuller) Prewarm(image string) {
	if image == "" {
		return
	}
	go func() {
		_ = p.Ensure(context.Background(), image)
	}()
}
