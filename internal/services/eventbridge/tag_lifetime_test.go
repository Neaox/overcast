package eventbridge

// Tags are keyed by resource ARN in a namespace of their own, so nothing ties
// them to the lifetime of the rule or bus they describe. DeleteRule was already
// careful to take a rule's targets and its fire bookkeeping with it, and left
// its tags — so ListTagsForResource kept answering for a rule that was gone.

import (
	"context"
	"testing"
)

// storedTagCount reports how many tag blobs the whole namespace holds, which is
// what says whether a delete cleaned up or merely stopped pointing at it.
func storedTagCount(t *testing.T, s *Service) int {
	t.Helper()
	kvs, err := s.store.Scan(context.Background(), nsTags, "")
	if err != nil {
		t.Fatalf("scan tags: %v", err)
	}
	return len(kvs)
}

func TestDeleteRule_takesItsTagsWithIt(t *testing.T) {
	// Given: a tagged rule
	s, st := newTagTestService(t)
	ctx := context.Background()
	arn := seedTagTestRule(t, s, st, "doomed")
	if _, aerr := s.tagResourceTyped(ctx, &tagResourceRequest{
		ResourceARN: arn,
		Tags:        []tagEntry{{Key: "env", Value: "prod"}},
	}); aerr != nil {
		t.Fatalf("tagResourceTyped: %v", aerr)
	}
	if got := storedTagCount(t, s); got != 1 {
		t.Fatalf("stored tag blobs = %d, want 1 before the delete", got)
	}

	// When: the rule is deleted
	if _, aerr := s.deleteRuleTyped(ctx, &deleteRuleRequest{Name: "doomed"}); aerr != nil {
		t.Fatalf("deleteRuleTyped: %v", aerr)
	}

	// Then: nothing is left in the store, and a recreated rule of the same name
	// does not inherit the dead one's tags
	if got := storedTagCount(t, s); got != 0 {
		t.Fatalf("stored tag blobs = %d, want 0 after the delete", got)
	}
	seedTagTestRule(t, s, st, "doomed")
	list, aerr := s.listTagsForResourceTyped(ctx, &listTagsForResourceRequest{ResourceARN: arn})
	if aerr != nil {
		t.Fatalf("listTagsForResourceTyped: %v", aerr)
	}
	if len(list.Tags) != 0 {
		t.Fatalf("Tags = %#v, want none — the recreated rule inherited a deleted rule's tags", list.Tags)
	}
}

func TestDeleteEventBus_takesItsTagsWithIt(t *testing.T) {
	// Given: a tagged event bus
	s, _ := newTagTestService(t)
	ctx := context.Background()
	bus, aerr := s.createEventBusTyped(ctx, &createEventBusRequest{Name: "orders"})
	if aerr != nil {
		t.Fatalf("createEventBusTyped: %v", aerr)
	}
	if _, aerr := s.tagResourceTyped(ctx, &tagResourceRequest{
		ResourceARN: bus.EventBusArn,
		Tags:        []tagEntry{{Key: "env", Value: "prod"}},
	}); aerr != nil {
		t.Fatalf("tagResourceTyped: %v", aerr)
	}

	// When: the bus is deleted
	if _, aerr := s.deleteEventBusTyped(ctx, &deleteEventBusRequest{Name: "orders"}); aerr != nil {
		t.Fatalf("deleteEventBusTyped: %v", aerr)
	}

	// Then: its tags went with it
	if got := storedTagCount(t, s); got != 0 {
		t.Fatalf("stored tag blobs = %d, want 0 after the delete", got)
	}
}

// A default-bus rule ARN may be written with or without the bus segment. Both
// spellings name the same rule, so both must reach the same tags — otherwise
// tagging through one hides the tags from the other, and from the delete path,
// which only ever has the rule's name to work with.
func TestRuleTags_areReachableByEitherARNSpelling(t *testing.T) {
	// Given: a rule tagged through the short-form ARN
	s, st := newTagTestService(t)
	ctx := context.Background()
	longARN := seedTagTestRule(t, s, st, "either")
	shortARN := "arn:aws:events:us-east-1:000000000000:rule/either"
	if _, aerr := s.tagResourceTyped(ctx, &tagResourceRequest{
		ResourceARN: shortARN,
		Tags:        []tagEntry{{Key: "env", Value: "prod"}},
	}); aerr != nil {
		t.Fatalf("tagResourceTyped: %v", aerr)
	}

	// When: the tags are read back through the long-form ARN
	list, aerr := s.listTagsForResourceTyped(ctx, &listTagsForResourceRequest{ResourceARN: longARN})
	if aerr != nil {
		t.Fatalf("listTagsForResourceTyped: %v", aerr)
	}

	// Then: they are the same tags
	if len(list.Tags) != 1 || list.Tags[0].Key != "env" {
		t.Fatalf("Tags = %#v, want env=prod through the other ARN spelling", list.Tags)
	}

	// And: deleting the rule still finds them
	if _, aerr := s.deleteRuleTyped(ctx, &deleteRuleRequest{Name: "either"}); aerr != nil {
		t.Fatalf("deleteRuleTyped: %v", aerr)
	}
	if got := storedTagCount(t, s); got != 0 {
		t.Fatalf("stored tag blobs = %d, want 0 after the delete", got)
	}
}
