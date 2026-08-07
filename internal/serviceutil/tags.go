package serviceutil

import (
	"context"
	"encoding/json"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

// ---- Separate-namespace tag helpers (direct store access) ----

// ApplyTagsToStore merges incoming TagPairs into the tag blob at key in
// namespace ns, validates the combined set, and writes it back.
// Returns the full tag map after the merge.
func ApplyTagsToStore(ctx context.Context, cfg TagValidationConfig, ns, key string, incoming []TagPair, store state.Store) (map[string]string, *protocol.AWSError) {
	tags, aerr := tagsFromStore(ctx, store, ns, key)
	if aerr != nil {
		return nil, aerr
	}
	for _, p := range incoming {
		tags[p.Key] = p.Value
	}
	if aerr := ValidateTags(cfg, tags); aerr != nil {
		return nil, aerr
	}
	if err := store.Set(ctx, ns, key, marshalTags(tags)); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return tags, nil
}

// RemoveTagsFromStore removes tagKeys from the tag blob at key in namespace ns.
func RemoveTagsFromStore(ctx context.Context, ns, key string, tagKeys []string, store state.Store) (map[string]string, *protocol.AWSError) {
	tags, aerr := tagsFromStore(ctx, store, ns, key)
	if aerr != nil {
		return nil, aerr
	}
	for _, k := range tagKeys {
		delete(tags, k)
	}
	if err := store.Set(ctx, ns, key, marshalTags(tags)); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return tags, nil
}

// TagsFromStore reads the tag blob for key from namespace ns.
func TagsFromStore(ctx context.Context, store state.Store, ns, key string) (map[string]string, *protocol.AWSError) {
	return tagsFromStore(ctx, store, ns, key)
}

func tagsFromStore(ctx context.Context, store state.Store, ns, key string) (map[string]string, *protocol.AWSError) {
	raw, ok, err := store.Get(ctx, ns, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !ok {
		return map[string]string{}, nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return tags, nil
}

func marshalTags(tags map[string]string) string {
	// map[string]string is always valid JSON — json.Marshal never fails.
	raw, _ := json.Marshal(tags)
	return string(raw)
}

// ---- Inline-tag generic helpers ----

// Taggable is implemented by any resource struct that carries inline tags.
type Taggable interface {
	GetTags() map[string]string
	SetTags(map[string]string)
}

// ApplyTags loads a resource via getter, merges incoming tag pairs, validates,
// and saves back via putter.
// T is the struct type (e.g. WebACL); *T must implement Taggable.
func ApplyTags[T any, PT interface {
	*T
	Taggable
}](
	ctx context.Context,
	cfg TagValidationConfig,
	id string,
	incoming []TagPair,
	getter func(ctx context.Context, key string) (PT, *protocol.AWSError),
	putter func(ctx context.Context, resource PT) *protocol.AWSError,
) *protocol.AWSError {
	resource, aerr := getter(ctx, id)
	if aerr != nil {
		return aerr
	}
	tags := resource.GetTags()
	if tags == nil {
		tags = make(map[string]string)
	}
	for _, p := range incoming {
		tags[p.Key] = p.Value
	}
	resource.SetTags(tags)
	if aerr := ValidateTags(cfg, tags); aerr != nil {
		return aerr
	}
	return putter(ctx, resource)
}

// RemoveTags loads a resource, removes tagKeys, and saves back.
func RemoveTags[T any, PT interface {
	*T
	Taggable
}](
	ctx context.Context,
	id string,
	tagKeys []string,
	getter func(ctx context.Context, key string) (PT, *protocol.AWSError),
	putter func(ctx context.Context, resource PT) *protocol.AWSError,
) *protocol.AWSError {
	resource, aerr := getter(ctx, id)
	if aerr != nil {
		return aerr
	}
	tags := resource.GetTags()
	for _, k := range tagKeys {
		delete(tags, k)
	}
	resource.SetTags(tags)
	return putter(ctx, resource)
}

// ---- TagStore interface (for services preferring abstraction over direct store access) ----

// TagStore abstracts how resource tags are loaded and persisted.
type TagStore interface {
	Load(ctx context.Context, key string) (map[string]string, *protocol.AWSError)
	Save(ctx context.Context, key string, tags map[string]string) *protocol.AWSError
}

// NSStore implements TagStore using a separate state namespace.
type NSStore struct {
	Store state.Store
	NS    string
}

func (s *NSStore) Load(ctx context.Context, key string) (map[string]string, *protocol.AWSError) {
	return tagsFromStore(ctx, s.Store, s.NS, key)
}

func (s *NSStore) Save(ctx context.Context, key string, tags map[string]string) *protocol.AWSError {
	if err := s.Store.Set(ctx, s.NS, key, marshalTags(tags)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ApplyStoreTags merges incoming tags into the existing tag map, validates,
// and saves. key is the resource identifier.
func ApplyStoreTags(ctx context.Context, s TagStore, key string, incoming map[string]string, cfg TagValidationConfig) *protocol.AWSError {
	tags, aerr := s.Load(ctx, key)
	if aerr != nil {
		return aerr
	}
	for k, v := range incoming {
		tags[k] = v
	}
	if aerr := ValidateTags(cfg, tags); aerr != nil {
		return aerr
	}
	return s.Save(ctx, key, tags)
}

// RemoveStoreTags removes keys from the existing tag map and saves.
func RemoveStoreTags(ctx context.Context, s TagStore, key string, keys []string) *protocol.AWSError {
	tags, aerr := s.Load(ctx, key)
	if aerr != nil {
		return aerr
	}
	for _, k := range keys {
		delete(tags, k)
	}
	return s.Save(ctx, key, tags)
}

// ListStoreTags returns the current tags for key.
func ListStoreTags(ctx context.Context, s TagStore, key string) (map[string]string, *protocol.AWSError) {
	return s.Load(ctx, key)
}

// ---- Legacy inline helpers (used by existing refactored services) ----

// ApplyInlineTags loads a resource, merges incoming tags, validates, and saves.
func ApplyInlineTags[T Taggable](
	ctx context.Context,
	key string,
	incoming map[string]string,
	cfg TagValidationConfig,
	load func(ctx context.Context, key string) (T, *protocol.AWSError),
	save func(ctx context.Context, resource T) *protocol.AWSError,
) *protocol.AWSError {
	resource, aerr := load(ctx, key)
	if aerr != nil {
		return aerr
	}
	tags := resource.GetTags()
	if tags == nil {
		tags = make(map[string]string)
	}
	for k, v := range incoming {
		tags[k] = v
	}
	resource.SetTags(tags)
	if aerr := ValidateTags(cfg, tags); aerr != nil {
		return aerr
	}
	return save(ctx, resource)
}

// RemoveInlineTags removes keys from a resource's inline tags and saves.
func RemoveInlineTags[T Taggable](
	ctx context.Context,
	key string,
	keys []string,
	load func(ctx context.Context, key string) (T, *protocol.AWSError),
	save func(ctx context.Context, resource T) *protocol.AWSError,
) *protocol.AWSError {
	resource, aerr := load(ctx, key)
	if aerr != nil {
		return aerr
	}
	tags := resource.GetTags()
	if tags != nil {
		for _, k := range keys {
			delete(tags, k)
		}
		resource.SetTags(tags)
	}
	return save(ctx, resource)
}

// ListInlineTags returns a resource's inline tags.
func ListInlineTags[T Taggable](
	ctx context.Context,
	key string,
	load func(ctx context.Context, key string) (T, *protocol.AWSError),
) (map[string]string, *protocol.AWSError) {
	resource, aerr := load(ctx, key)
	if aerr != nil {
		return nil, aerr
	}
	tags := resource.GetTags()
	if tags == nil {
		return make(map[string]string), nil
	}
	return tags, nil
}
