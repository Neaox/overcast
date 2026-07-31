package docker

import (
	"context"
	"strings"
	"sync"
)

// ImagePuller deduplicates Docker image pulls. It ensures each image is
// pulled at most once per process lifetime. Services that run containers
// (RDS, ECS, Lambda) should share an ImagePuller rather than duplicating
// the sync.Map + sync.Once pattern.
type ImagePuller struct {
	client   *Client
	pullOnce sync.Map // image → *pullEntry
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

// Ensure pulls image if it hasn't been pulled yet. Concurrent calls for the
// same image block until the first pull completes and share its result. A
// FAILED pull drops the entry, so the next launch attempt retries instead of
// serving the cached error until restart — pulls are driven by user actions
// (RunTask, StartDBInstance, Invoke), not loops, so this cannot hammer a
// registry; what it prevents is one transient network failure bricking an
// image for the process lifetime.
func (p *ImagePuller) Ensure(ctx context.Context, image string) error {
	v, _ := p.pullOnce.LoadOrStore(image, &pullEntry{})
	e := v.(*pullEntry)
	e.once.Do(func() {
		e.err = p.client.PullImage(ctx, image)
		if e.err != nil {
			p.pullOnce.Delete(image)
		}
	})
	return e.err
}

// Invalidate forgets that image was pulled, so the next Ensure pulls again.
// Call when the daemon proves the cached knowledge wrong — a create failing
// with "No such image" after the image was removed behind our back.
func (p *ImagePuller) Invalidate(image string) {
	p.pullOnce.Delete(image)
}

// IsImageMissingErr reports whether a container-create failure means the
// image is gone from the daemon ("No such image": removed after it was
// pulled or verified present).
func IsImageMissingErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such image")
}

// CreateContainerWithRetry creates a container and, when the daemon reports
// its image missing — removed behind our back after the pull was recorded —
// forgets the stale pull record, re-pulls, and retries the create once.
// Without this, `docker rmi` of a task or DB image makes every later launch
// fail until restart, because the spent pull record short-circuits Ensure.
func (p *ImagePuller) CreateContainerWithRetry(ctx context.Context, name string, req *CreateContainerRequest) (string, error) {
	id, err := p.client.CreateContainer(ctx, name, req)
	if err != nil && IsImageMissingErr(err) && req != nil && req.ContainerConfig != nil {
		p.Invalidate(req.ContainerConfig.Image)
		if pullErr := p.Ensure(ctx, req.ContainerConfig.Image); pullErr == nil {
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
