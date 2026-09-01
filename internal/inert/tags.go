package inert

import (
	"context"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// Tags is the one ARN-keyed tag store every Tier 1 resource in a service
// shares (§3.1's Tag class).
//
// Sharing it is the point. A Create or Update input carrying a Tags member
// writes through to the same store the service's TagResource and
// ListTagsForResource speak to, so Describe*.Tags and ListTagsForResource
// can never disagree — "two stores for one resource is the failure mode to
// design against" (§7.3).
//
// Keys are ARNs by convention, because an ARN is the one identifier every
// AWS tag API agrees on and it is unique across a service's resource types.
// The key is opaque to this type, so a service whose tag API takes a bare
// resource id resolves that id to its ARN first rather than keying on the
// raw id — otherwise two resource types with the same id space would share
// a tag set.
//
// Tag storage deliberately reuses serviceutil.NSStore rather than
// reimplementing it: that type already isolates a malformed tag blob as an
// empty set, and a second implementation of the same three methods is how
// the two drift apart.
type Tags struct {
	ns *serviceutil.NSStore
}

// NewTags returns the tag store for one service. namespace is conventionally
// "<service>:tags".
//
// Tag keys are ARNs, which already carry the region, so the tag namespace is
// never region-scoped the way Store[T]'s keys are.
func NewTags(cfg Config, namespace string) *Tags {
	return &Tags{ns: &serviceutil.NSStore{Store: cfg.Store, NS: namespace}}
}

// Load returns every tag held for key, empty rather than nil for a resource
// that has never been tagged.
func (t *Tags) Load(ctx context.Context, key string) (map[string]string, *protocol.AWSError) {
	return t.ns.Load(ctx, key)
}

// Apply merges incoming into the tags already held for key, validates the
// combined set against cfg, and saves it — the AWS TagResource semantic,
// where tags absent from incoming are left alone.
func (t *Tags) Apply(ctx context.Context, key string, incoming map[string]string, cfg serviceutil.TagValidationConfig) (map[string]string, *protocol.AWSError) {
	return serviceutil.ApplyStoreTags(ctx, t.ns, key, incoming, cfg)
}

// Remove takes keys out of the tags held for key and saves what is left.
// Removing a tag that is not there is not an error.
func (t *Tags) Remove(ctx context.Context, key string, keys []string) (map[string]string, *protocol.AWSError) {
	return serviceutil.RemoveStoreTags(ctx, t.ns, key, keys)
}

// Delete removes every tag for key. Delete paths must call this: namespaced
// tags have nothing tying them to the record's lifetime, so a resource that
// is gone otherwise keeps answering ListTagsForResource.
func (t *Tags) Delete(ctx context.Context, key string) *protocol.AWSError {
	return t.ns.Delete(ctx, key)
}

// Rendering a tag map into a service's own modeled tag element type is
// serviceutil.TagElements' job, not this type's — it already orders by tag
// key, which is load-bearing rather than tidiness: Go randomises map
// iteration, so a response built straight from the map hands a client a
// different order on every call.
