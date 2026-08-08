package serviceutil

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

// A corrupt persisted tag blob must be isolated — treated as empty — rather
// than turning every tag operation on that resource into a 500. See
// AGENTS.md § Malformed persisted state must be isolated.

func TestTagsFromStore_corruptBlobIsIsolated(t *testing.T) {
	// Given: a corrupt tag blob
	ctx := context.Background()
	st := state.NewMemoryStore()
	if err := st.Set(ctx, "svc:tags", "arn:x", "{not json"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// When: the tags are read
	tags, aerr := TagsFromStore(ctx, st, "svc:tags", "arn:x")

	// Then: the record reads as empty instead of an internal error
	if aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}
	if len(tags) != 0 {
		t.Fatalf("tags = %v, want empty", tags)
	}
}

func TestApplyTagsToStore_corruptBlobIsReplaced(t *testing.T) {
	// Given: a corrupt tag blob
	ctx := context.Background()
	st := state.NewMemoryStore()
	if err := st.Set(ctx, "svc:tags", "arn:x", "[[["); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// When: tags are applied over it
	cfg := TagValidationConfig{ExceededCode: "X", InvalidCode: "X"}
	tags, aerr := ApplyTagsToStore(ctx, cfg, "svc:tags", "arn:x", []TagPair{{Key: "env", Value: "prod"}}, st)
	if aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}
	if tags["env"] != "prod" {
		t.Fatalf("tags = %v, want env=prod", tags)
	}

	// Then: the blob is healed — a follow-up read returns the new tags
	got, aerr := TagsFromStore(ctx, st, "svc:tags", "arn:x")
	if aerr != nil {
		t.Fatalf("re-read aerr = %v", aerr)
	}
	if got["env"] != "prod" {
		t.Fatalf("re-read tags = %v, want env=prod", got)
	}
}

func TestNSStoreLoad_corruptBlobIsIsolated(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemoryStore()
	if err := st.Set(ctx, "svc:tags", "arn:y", "42x"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ns := &NSStore{Store: st, NS: "svc:tags"}
	tags, aerr := ns.Load(ctx, "arn:y")
	if aerr != nil {
		t.Fatalf("aerr = %v, want nil", aerr)
	}
	if len(tags) != 0 {
		t.Fatalf("tags = %v, want empty", tags)
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
