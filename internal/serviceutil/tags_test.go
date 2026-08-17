package serviceutil

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

// A corrupt persisted tag blob must be isolated — treated as empty — rather
// than turning every tag operation on that resource into a 500. See
// AGENTS.md § Malformed persisted state must be isolated.

// tagNS returns a namespaced tag store over a fresh memory store, optionally
// seeded with a raw blob at "arn:x".
func tagNS(t *testing.T, seed string) (context.Context, *NSStore) {
	t.Helper()
	ctx := context.Background()
	st := state.NewMemoryStore()
	if seed != "" {
		if err := st.Set(ctx, "svc:tags", "arn:x", seed); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return ctx, &NSStore{Store: st, NS: "svc:tags"}
}

func TestNSStoreLoad_corruptBlobIsIsolated(t *testing.T) {
	// Given: a corrupt tag blob
	ctx, ns := tagNS(t, "{not json")

	// When: the tags are read
	tags, aerr := ns.Load(ctx, "arn:x")

	// Then: the record reads as empty instead of an internal error
	if aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}
	if len(tags) != 0 {
		t.Fatalf("tags = %v, want empty", tags)
	}
}

func TestNSStoreLoad_persistedNullIsNotANilMap(t *testing.T) {
	// Given: a tag blob that decodes to a nil map
	ctx, ns := tagNS(t, "null")

	// When: the tags are read
	tags, aerr := ns.Load(ctx, "arn:x")

	// Then: callers index into the result, so they are handed an empty map
	if aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}
	if tags == nil {
		t.Fatal("tags = nil, want an empty map")
	}
}

func TestApplyStoreTags_corruptBlobIsReplaced(t *testing.T) {
	// Given: a corrupt tag blob
	ctx, ns := tagNS(t, "[[[")

	// When: tags are applied over it
	cfg := TagValidationConfig{ExceededCode: "X", InvalidCode: "X"}
	tags, aerr := ApplyStoreTags(ctx, ns, "arn:x", map[string]string{"env": "prod"}, cfg)
	if aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}
	if tags["env"] != "prod" {
		t.Fatalf("tags = %v, want env=prod", tags)
	}

	// Then: the blob is healed — a follow-up read returns the new tags
	got, aerr := ns.Load(ctx, "arn:x")
	if aerr != nil {
		t.Fatalf("re-read aerr = %v", aerr)
	}
	if got["env"] != "prod" {
		t.Fatalf("re-read tags = %v, want env=prod", got)
	}
}

func TestApplyStoreTags_mergesAndRejectsWithoutWriting(t *testing.T) {
	// Given: a resource that already carries a tag
	ctx, ns := tagNS(t, "")
	cfg := TagValidationConfig{ExceededCode: "X", InvalidCode: "Invalid"}
	if _, aerr := ApplyStoreTags(ctx, ns, "arn:x", map[string]string{"env": "prod"}, cfg); aerr != nil {
		t.Fatalf("seed apply: %v", aerr)
	}

	// When: a second call adds a tag, leaving the first alone
	tags, aerr := ApplyStoreTags(ctx, ns, "arn:x", map[string]string{"team": "core"}, cfg)
	if aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}
	if tags["env"] != "prod" || tags["team"] != "core" {
		t.Fatalf("tags = %v, want both env and team", tags)
	}

	// Then: a rejected set is not written — the resource keeps what it had
	if _, aerr := ApplyStoreTags(ctx, ns, "arn:x", map[string]string{"aws:owner": "x"}, cfg); aerr == nil {
		t.Fatal("aerr = nil, want the reserved-prefix rejection")
	}
	got, _ := ns.Load(ctx, "arn:x")
	if len(got) != 2 {
		t.Fatalf("tags after rejection = %v, want the two that were already there", got)
	}
}

// Namespaced tags are keyed by resource ID with nothing tying them to the
// record's lifetime, so a delete path needs a way to take them with it.
func TestNSStoreDelete_removesEveryTag(t *testing.T) {
	// Given: a tagged resource
	ctx, ns := tagNS(t, "")
	cfg := TagValidationConfig{ExceededCode: "X", InvalidCode: "X"}
	if _, aerr := ApplyStoreTags(ctx, ns, "arn:x", map[string]string{"env": "prod"}, cfg); aerr != nil {
		t.Fatalf("seed: %v", aerr)
	}

	// When: its tags are deleted
	if aerr := ns.Delete(ctx, "arn:x"); aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}

	// Then: nothing is left to report
	tags, aerr := ns.Load(ctx, "arn:x")
	if aerr != nil {
		t.Fatalf("re-read aerr = %v", aerr)
	}
	if len(tags) != 0 {
		t.Fatalf("tags = %v, want empty", tags)
	}
}

// A delete path calls Delete whether or not the resource was ever tagged, so an
// untagged resource must not be a special case its author has to remember.
func TestNSStoreDelete_untaggedResourceIsNotAnError(t *testing.T) {
	ctx, ns := tagNS(t, "")
	if aerr := ns.Delete(ctx, "arn:never-tagged"); aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}
}

func TestRemoveStoreTags_leavesTheRest(t *testing.T) {
	// Given: a resource with two tags
	ctx, ns := tagNS(t, "")
	cfg := TagValidationConfig{ExceededCode: "X", InvalidCode: "X"}
	if _, aerr := ApplyStoreTags(ctx, ns, "arn:x", map[string]string{"env": "prod", "team": "core"}, cfg); aerr != nil {
		t.Fatalf("seed: %v", aerr)
	}

	// When: one is removed, alongside a key that was never there
	tags, aerr := RemoveStoreTags(ctx, ns, "arn:x", []string{"env", "absent"})
	if aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}

	// Then: the other survives and the absent key is not an error
	if len(tags) != 1 || tags["team"] != "core" {
		t.Fatalf("tags = %v, want just team=core", tags)
	}
}

// ---- Rendering --------------------------------------------------------------

// Go randomises map iteration, so a renderer that ranges a tag map straight
// into a response hands a client a different order on every call. Every render
// site goes through here, so the order is a property of the helper rather than
// something each of them has to remember.
func TestTagElements_ordersByKeyEveryTime(t *testing.T) {
	// Given: a tag map big enough that map iteration order will vary
	tags := map[string]string{
		"zebra": "1", "alpha": "2", "mike": "3", "bravo": "4",
		"yankee": "5", "delta": "6", "kilo": "7", "echo": "8",
	}
	want := []string{"alpha", "bravo", "delta", "echo", "kilo", "mike", "yankee", "zebra"}

	// When: it is rendered repeatedly
	for i := 0; i < 20; i++ {
		got := TagElements(tags, func(k, _ string) string { return k })

		// Then: the order is the same, and it is sorted by key
		if !slices.Equal(got, want) {
			t.Fatalf("run %d rendered %v, want %v", i, got, want)
		}
	}
}

func TestTagElements_carriesKeyAndValue(t *testing.T) {
	got := TagsToList(map[string]string{"b": "two", "a": "one"})
	want := []TagPair{{Key: "a", Value: "one"}, {Key: "b", Value: "two"}}
	if !slices.Equal(got, want) {
		t.Fatalf("TagsToList = %v, want %v", got, want)
	}
}

// A response shape that distinguishes an empty tag list from an absent one
// needs the empty list, not nil.
func TestTagElements_emptyMapRendersAnEmptyList(t *testing.T) {
	for _, tags := range []map[string]string{nil, {}} {
		if got := TagsToList(tags); got == nil {
			t.Fatalf("TagsToList(%v) = nil, want an empty list", tags)
		} else if len(got) != 0 {
			t.Fatalf("TagsToList(%v) = %v, want empty", tags, got)
		}
	}
}

// ---- ValidateTags ----------------------------------------------------------

var validateTagsCfg = TagValidationConfig{
	ExceededCode:    "TooManyTags",
	ExceededMessage: "too many tags",
	InvalidCode:     "InvalidTag",
}

func TestValidateTags_rules(t *testing.T) {
	// Given: tag maps that are each valid or violate exactly one AWS rule
	atLimit := make(map[string]string, MaxTags)
	for i := 0; i < MaxTags; i++ {
		atLimit[strconv.Itoa(i)] = "v"
	}
	tooMany := make(map[string]string, MaxTags+1)
	for k, v := range atLimit {
		tooMany[k] = v
	}
	tooMany[strconv.Itoa(MaxTags)] = "v"
	cases := []struct {
		name     string
		tags     map[string]string
		wantCode string
	}{
		{name: "empty map", tags: map[string]string{}},
		{name: "nil map", tags: nil},
		{name: "ordinary pair", tags: map[string]string{"env": "prod"}},
		{name: "empty value is legal", tags: map[string]string{"env": ""}},
		{name: "key at the limit", tags: map[string]string{strings.Repeat("k", 128): "v"}},
		{name: "value at the limit", tags: map[string]string{"k": strings.Repeat("v", 256)}},
		{name: "at the tag-count limit", tags: atLimit},
		{name: "over the tag-count limit", tags: tooMany, wantCode: "TooManyTags"},
		{name: "empty key", tags: map[string]string{"": "v"}, wantCode: "InvalidTag"},
		{name: "key over 128", tags: map[string]string{strings.Repeat("k", 129): "v"}, wantCode: "InvalidTag"},
		{name: "value over 256", tags: map[string]string{"k": strings.Repeat("v", 257)}, wantCode: "InvalidTag"},
		{name: "reserved aws: prefix", tags: map[string]string{"aws:owner": "v"}, wantCode: "InvalidTag"},

		// The charset AWS documents for every service's tags:
		// ^([\p{L}\p{Z}\p{N}_.:/=+\-@]*)$.
		{name: "every punctuation mark the pattern allows", tags: map[string]string{"_.:/=+-@": "_.:/=+-@"}},
		{name: "spaces are legal", tags: map[string]string{"cost centre": "team one"}},
		{name: "letters outside ASCII are legal", tags: map[string]string{"środowisko": "produkcja"}},
		{name: "a CDK-shaped key", tags: map[string]string{"aws-cdk:subnet-name": "Private"}},
		{name: "illegal character in a key", tags: map[string]string{"env!": "prod"}, wantCode: "InvalidTag"},
		{name: "illegal character in a value", tags: map[string]string{"env": "prod(1)"}, wantCode: "InvalidTag"},
		{name: "a tab is not one of the separators", tags: map[string]string{"env": "pr\tod"}, wantCode: "InvalidTag"},
		{name: "a newline is not one of the separators", tags: map[string]string{"env": "pr\nod"}, wantCode: "InvalidTag"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the map is validated
			aerr := ValidateTags(validateTagsCfg, tc.tags)

			// Then: the configured error code (or none) is returned
			switch {
			case tc.wantCode == "" && aerr != nil:
				t.Fatalf("aerr = %+v, want nil", aerr)
			case tc.wantCode != "" && aerr == nil:
				t.Fatalf("aerr = nil, want %s", tc.wantCode)
			case tc.wantCode != "" && aerr.Code != tc.wantCode:
				t.Fatalf("aerr.Code = %q, want %q", aerr.Code, tc.wantCode)
			}
		})
	}
}

// Go randomises map iteration order, so a map with more than one violation
// must still report the same one every time or the error is untestable.
func TestValidateTags_multipleViolationsReportDeterministically(t *testing.T) {
	// Given: a map whose keys each break a different rule
	tags := map[string]string{
		"":                       "v",
		"aws:owner":              "v",
		strings.Repeat("k", 129): "v",
		"ok":                     strings.Repeat("v", 257),
	}

	// When: it is validated repeatedly
	first := ValidateTags(validateTagsCfg, tags)
	if first == nil {
		t.Fatal("aerr = nil, want a violation")
	}

	// Then: every run reports the same message
	for i := 0; i < 20; i++ {
		if got := ValidateTags(validateTagsCfg, tags); got == nil || got.Message != first.Message {
			t.Fatalf("run %d reported %+v, want %+v", i, got, first)
		}
	}
}
