package serviceutil

// tags.go — the shared tag vocabulary: merge in, take out, list, delete, render.
//
// Two storage strategies exist and both are legitimate. Tags live either in a
// namespace of their own keyed by resource ID, or inline on the resource's own
// record. Each has exactly one set of entry points here, because four sets is
// how a reviewer stops being able to say which one is right and the next
// service adds a fifth.
//
// The namespaced strategy is reached through TagStore, so a service names its
// namespace once and then speaks in tags rather than in blobs and keys. The
// inline strategy is reached through Taggable, which needs no store at all.
//
// Deleting is part of the vocabulary, not an afterthought. Namespaced tags are
// keyed by resource ID with nothing tying them to the record's lifetime, so a
// service that forgets to remove them keeps answering ListTagsForResource for a
// resource that is gone. There was no delete helper here at all, and half the
// services that needed one had not written it.

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/state"
)

// ---- Namespaced tags --------------------------------------------------------

// TagStore is how a service reaches the tags of one resource: the whole set for
// a key, replaced wholesale, or removed with the resource.
//
// Load returns an empty map rather than nil for a resource that has never been
// tagged, so callers can index into the result without checking.
type TagStore interface {
	Load(ctx context.Context, key string) (map[string]string, *protocol.AWSError)
	Save(ctx context.Context, key string, tags map[string]string) *protocol.AWSError
	Delete(ctx context.Context, key string) *protocol.AWSError
}

// NSStore implements TagStore over a state namespace of its own, which is the
// storage strategy for every service whose tags are not inline on the record.
type NSStore struct {
	Store state.Store
	NS    string
}

func (s *NSStore) Load(ctx context.Context, key string) (map[string]string, *protocol.AWSError) {
	raw, ok, err := s.Store.Get(ctx, s.NS, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !ok {
		return map[string]string{}, nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		// Malformed persisted state is isolated, not escalated: one corrupt
		// tag blob must not turn every tag operation on this resource into a
		// 500 (AGENTS.md § Malformed persisted state must be isolated). The
		// record reads as empty; the next tag write replaces it wholesale.
		return map[string]string{}, nil
	}
	if tags == nil {
		// A persisted JSON "null" decodes to a nil map; callers index into
		// the result, so never hand one back.
		return map[string]string{}, nil
	}
	return tags, nil
}

func (s *NSStore) Save(ctx context.Context, key string, tags map[string]string) *protocol.AWSError {
	// map[string]string is always valid JSON — json.Marshal never fails.
	raw, _ := json.Marshal(tags)
	if err := s.Store.Set(ctx, s.NS, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// Delete removes every tag for key. Deleting a key that was never tagged is not
// an error: a delete path calls this whether or not the resource was tagged.
func (s *NSStore) Delete(ctx context.Context, key string) *protocol.AWSError {
	if err := s.Store.Delete(ctx, s.NS, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ApplyStoreTags merges incoming into the tags already held for key, validates
// the combined set, and saves it. It returns the merged set, which is what the
// AWS responses that echo a resource's tags back need.
//
// Merging is the AWS semantic: tags absent from incoming are left alone, and
// RemoveStoreTags is the way to take one away.
func ApplyStoreTags(ctx context.Context, s TagStore, key string, incoming map[string]string, cfg TagValidationConfig) (map[string]string, *protocol.AWSError) {
	tags, aerr := s.Load(ctx, key)
	if aerr != nil {
		return nil, aerr
	}
	maps.Copy(tags, incoming)
	if aerr := ValidateTags(cfg, tags); aerr != nil {
		return nil, aerr
	}
	if aerr := s.Save(ctx, key, tags); aerr != nil {
		return nil, aerr
	}
	return tags, nil
}

// RemoveStoreTags takes keys out of the tags held for key and saves what is
// left, which it returns. Removing a tag that is not there is not an error.
func RemoveStoreTags(ctx context.Context, s TagStore, key string, keys []string) (map[string]string, *protocol.AWSError) {
	tags, aerr := s.Load(ctx, key)
	if aerr != nil {
		return nil, aerr
	}
	for _, k := range keys {
		delete(tags, k)
	}
	if aerr := s.Save(ctx, key, tags); aerr != nil {
		return nil, aerr
	}
	return tags, nil
}

// ---- Inline tags ------------------------------------------------------------

// Taggable is implemented by any resource record that carries its tags inline.
// Tags stored this way are removed with the record and so cannot outlive it.
type Taggable interface {
	GetTags() map[string]string
	SetTags(map[string]string)
}

// ApplyInlineTags loads a resource, merges incoming into its tags, validates the
// combined set, and saves the resource back.
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
		tags = make(map[string]string, len(incoming))
	}
	maps.Copy(tags, incoming)
	if aerr := ValidateTags(cfg, tags); aerr != nil {
		return aerr
	}
	resource.SetTags(tags)
	return save(ctx, resource)
}

// RemoveInlineTags takes keys out of a resource's inline tags and saves it back.
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
	if tags := resource.GetTags(); tags != nil {
		for _, k := range keys {
			delete(tags, k)
		}
		resource.SetTags(tags)
	}
	return save(ctx, resource)
}

// ListInlineTags returns a resource's inline tags, empty rather than nil for a
// resource that has never been tagged.
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
		return map[string]string{}, nil
	}
	return tags, nil
}

// ---- Rendering --------------------------------------------------------------

// TagElements renders a tag map into a service's own tag element type, ordered
// by key.
//
// The order is the point. Tags are held in a map, Go randomises map iteration,
// and a renderer that ranges one straight into a response hands a client a
// different order on every call — churn for anything diffing two responses, and
// untestable against a golden file. Going through here means a service cannot
// render an unordered map by accident:
//
//	items := serviceutil.TagElements(tags, func(k, v string) xmlTag {
//	    return xmlTag{Key: k, Value: v}
//	})
//
// The result is never nil, so a response shape that distinguishes an empty tag
// list from an absent one still renders the empty list.
func TagElements[T any](tags map[string]string, elem func(key, value string) T) []T {
	out := make([]T, 0, len(tags))
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		out = append(out, elem(k, tags[k]))
	}
	return out
}

// TagsToList converts an internal tag map to the common Key/Value tag-array
// response shape, ordered by key.
func TagsToList(tags map[string]string) []TagPair {
	return TagElements(tags, func(k, v string) TagPair {
		return TagPair{Key: k, Value: v}
	})
}

// TagsFromList converts a tag-array request shape to an internal tag map.
func TagsFromList(pairs []TagPair) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p.Key] = p.Value
	}
	return out
}
