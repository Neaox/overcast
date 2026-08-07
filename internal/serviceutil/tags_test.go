package serviceutil

import (
	"context"
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
