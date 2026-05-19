package docker

import (
	"context"
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
// same image block until the first pull completes. On error the entry is NOT
// cleared — callers get the cached error rather than hammering the registry.
func (p *ImagePuller) Ensure(ctx context.Context, image string) error {
	v, _ := p.pullOnce.LoadOrStore(image, &pullEntry{})
	e := v.(*pullEntry)
	e.once.Do(func() {
		e.err = p.client.PullImage(ctx, image)
	})
	return e.err
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
