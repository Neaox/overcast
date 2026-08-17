package ec2

// tags.go — EC2's tag path: one store, one parser, one filter.
//
// Tags reach a resource two ways — `TagSpecification.N` on the create call and
// CreateTags afterwards — and every Describe* has to answer for both. They used
// to be separate: create-time tags were written to a `Tags` field on the
// resource's own record, CreateTags wrote to the tag store, and each describe
// read whichever one its author had in mind. Tagging an existing NAT gateway
// therefore succeeded and stayed invisible, while an instance's create-time
// tags were invisible to DescribeTags. The tag store is now the only writer and
// the only reader, so the two paths cannot disagree.
//
// Filtering is the other half. AWS lets a caller select on `tag:<key>`,
// `tag-key` and `tag-value`, and a script that finds-or-creates its own
// resource depends on an unmatched filter returning nothing. Overcast
// implemented none of the three. Tag selectors are parsed here, once, for every
// describe; everything else about a describe's filters — which names it
// implements and what an unrecognised one means — is in filters.go.

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── Reading ──────────────────────────────────────────────────────────────────

// tagIndex holds a region's tags keyed by resource ID.
//
// It is read once per request rather than per resource: a describe lists its
// whole collection and filters in Go, so asking the store for each resource's
// tags in the loop would turn one read into one per resource. The store scan
// behind loadTags is a single range query bounded by the region prefix.
type tagIndex map[string]map[string]string

// loadTags reads every tag in the caller's region.
func (h *Handler) loadTags(ctx context.Context) (tagIndex, *protocol.AWSError) {
	all, aerr := h.store.listAllTags(ctx)
	if aerr != nil {
		return nil, aerr
	}
	return tagIndex(all), nil
}

// tags returns resourceID's tags in AWS's wire shape, ordered by key.
//
// The order is not cosmetic: rendering straight out of a Go map gave a caller a
// different order on every call, which reads as churn to anything diffing two
// responses.
func (ti tagIndex) tags(resourceID string) []Tag {
	return sortedTags(ti[resourceID])
}

// sortedTags converts a tag map to AWS's wire shape, ordered by key.
func sortedTags(tags map[string]string) []Tag {
	if len(tags) == 0 {
		return nil
	}
	out := make([]Tag, 0, len(tags))
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		out = append(out, Tag{Key: k, Value: tags[k]})
	}
	return out
}

// sortTags orders tags by key without disturbing the caller's slice, so a
// create response and the describes that follow it agree on the order.
func sortTags(tags []Tag) []Tag {
	out := slices.Clone(tags)
	slices.SortFunc(out, func(a, b Tag) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// ── Writing ──────────────────────────────────────────────────────────────────

// putResourceTags merges tags into resourceID's existing tags, which is what
// both CreateTags and a create call's TagSpecification.N do. Absent tags are
// left alone; DeleteTags is the way to remove one.
func (h *Handler) putResourceTags(ctx context.Context, resourceID string, tags []Tag) *protocol.AWSError {
	if len(tags) == 0 {
		return nil
	}
	existing, aerr := h.store.getTags(ctx, resourceID)
	if aerr != nil {
		return aerr
	}
	if existing == nil {
		existing = make(map[string]string, len(tags))
	}
	for _, t := range tags {
		existing[t.Key] = t.Value
	}
	return h.store.putTags(ctx, resourceID, existing)
}

// parseTagSpecifications parses TagSpecification.N.Tag.M.{Key,Value} for the
// given resource type. An empty resourceType accepts every specification,
// which is what a create call that makes exactly one kind of resource wants.
func parseTagSpecifications(r *http.Request, resourceType string) []Tag {
	var tags []Tag
	for n := 1; ; n++ {
		rt := r.FormValue(fmt.Sprintf("TagSpecification.%d.ResourceType", n))
		if rt == "" {
			break
		}
		if resourceType != "" && rt != resourceType {
			continue
		}
		for m := 1; ; m++ {
			key := r.FormValue(fmt.Sprintf("TagSpecification.%d.Tag.%d.Key", n, m))
			if key == "" {
				break
			}
			tags = append(tags, Tag{
				Key:   key,
				Value: r.FormValue(fmt.Sprintf("TagSpecification.%d.Tag.%d.Value", n, m)),
			})
		}
	}
	return tags
}

// ── Rendering ────────────────────────────────────────────────────────────────

// vpcNetworkStatusTag names the diagnostic tag DescribeVpcs adds to every VPC,
// reporting the state of its backing Docker network. It is Overcast's own, not
// the caller's, and is the one tag key a caller cannot usefully set.
const vpcNetworkStatusTag = "overcast:network-status"

// xmlTagsOf renders tags into the Query protocol's tagSet shape.
func xmlTagsOf(tags []Tag) []xmlTag {
	if len(tags) == 0 {
		return nil
	}
	out := make([]xmlTag, 0, len(tags))
	for _, t := range tags {
		out = append(out, xmlTag(t))
	}
	return out
}

// typedTagsOf renders tags for the typed operations' tagSet.
func typedTagsOf(tags []Tag) []typedTagXML {
	if len(tags) == 0 {
		return nil
	}
	out := make([]typedTagXML, 0, len(tags))
	for _, t := range tags {
		out = append(out, typedTagXML(t))
	}
	return out
}

// ── Filtering ────────────────────────────────────────────────────────────────

const (
	tagKeyFilter    = "tag-key"
	tagValueFilter  = "tag-value"
	tagFilterPrefix = "tag:"
)

// tagFilters are the tag selectors of a Describe* request. Each is AND-ed with
// the others and with the request's non-tag filters; the values within one are
// OR-ed. That is AWS's rule for every filter.
//
// Values match exactly. AWS also accepts `*` and `?` wildcards in a filter
// value; those are not implemented, and a value containing one will not match
// the way it would on AWS.
type tagFilters struct {
	// byKey holds `tag:<key>` selectors: the tag must exist and hold one of
	// the listed values.
	byKey map[string][]string
	// keys holds `tag-key`: the resource must carry a tag with one of these
	// keys, whatever its value.
	keys []string
	// values holds `tag-value`: the resource must carry a tag with one of
	// these values, whatever its key.
	values []string
}

// isTagFilter reports whether a filter name is a tag selector, and so is
// matched by tagFilters rather than against a resource's own attributes.
//
// filterSpec.parse consults it before deciding a name is unrecognised, but only
// for an operation that matches tags: on one that does not, a tag selector is
// as unanswerable as any other name it has no accessor for.
func isTagFilter(name string) bool {
	return strings.HasPrefix(name, tagFilterPrefix) ||
		strings.EqualFold(name, tagKeyFilter) ||
		strings.EqualFold(name, tagValueFilter)
}

// active reports whether the request selected on tags at all.
func (tf tagFilters) active() bool {
	return len(tf.byKey) > 0 || len(tf.keys) > 0 || len(tf.values) > 0
}

// matches reports whether a resource's tags satisfy every tag selector.
func (tf tagFilters) matches(tags []Tag) bool {
	for key, allowed := range tf.byKey {
		if !slices.ContainsFunc(tags, func(t Tag) bool {
			return t.Key == key && slices.Contains(allowed, t.Value)
		}) {
			return false
		}
	}
	if len(tf.keys) > 0 && !slices.ContainsFunc(tags, func(t Tag) bool {
		return slices.Contains(tf.keys, t.Key)
	}) {
		return false
	}
	if len(tf.values) > 0 && !slices.ContainsFunc(tags, func(t Tag) bool {
		return slices.Contains(tf.values, t.Value)
	}) {
		return false
	}
	return true
}

// ── The describe-side view ───────────────────────────────────────────────────

// tagView is what a Describe* needs to answer about tags: the region's tags and
// the request's tag selectors, resolved once for the whole response.
//
// Handlers use it through keep, so that "which tags does this resource have"
// and "does it pass the tag filters" cannot drift apart across ~10 describes.
type tagView struct {
	index   tagIndex
	filters tagFilters
}

// tagViewFor resolves the request's tag selectors and reads the region's tags.
//
// The read is skipped when the request neither filters on tags nor renders
// them, so a describe that has no use for tags pays nothing for them.
func (h *Handler) tagViewFor(ctx context.Context, r *http.Request, renders bool) (tagView, *protocol.AWSError) {
	filters := parseTagFilters(r)
	if !renders && !filters.active() {
		return tagView{filters: filters}, nil
	}
	index, aerr := h.loadTags(ctx)
	if aerr != nil {
		return tagView{}, aerr
	}
	return tagView{index: index, filters: filters}, nil
}

// keep returns the tags to render for a resource and whether it passes the
// request's tag filters.
func (tv tagView) keep(resourceID string) ([]Tag, bool) {
	tags := tv.index.tags(resourceID)
	return tags, tv.filters.matches(tags)
}

// keepWith is keep for a resource that carries tags Overcast synthesises rather
// than the caller setting them — the VPC network-status diagnostic. The extra
// tags are rendered and filtered on alongside the stored ones; a stored tag of
// the same key is overridden, since the synthetic value is the true one.
func (tv tagView) keepWith(resourceID string, extra ...Tag) ([]Tag, bool) {
	merged := maps.Clone(tv.index[resourceID])
	if merged == nil {
		merged = make(map[string]string, len(extra))
	}
	for _, t := range extra {
		merged[t.Key] = t.Value
	}
	tags := sortedTags(merged)
	return tags, tv.filters.matches(tags)
}

// ── Parsing ──────────────────────────────────────────────────────────────────

// parseTagFilters extracts the tag selectors from Filter.N.Name /
// Filter.N.Value.M. Non-tag filters are left to the caller's own matching.
func parseTagFilters(r *http.Request) tagFilters {
	var tf tagFilters
	for name, values := range eachFilter(r) {
		switch {
		case strings.EqualFold(name, tagKeyFilter):
			tf.keys = append(tf.keys, values...)
		case strings.EqualFold(name, tagValueFilter):
			tf.values = append(tf.values, values...)
		case strings.HasPrefix(name, tagFilterPrefix):
			key := strings.TrimPrefix(name, tagFilterPrefix)
			if tf.byKey == nil {
				tf.byKey = map[string][]string{}
			}
			tf.byKey[key] = append(tf.byKey[key], values...)
		}
	}
	return tf
}
