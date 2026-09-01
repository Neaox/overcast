package eventbridge

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

func newTagTestService(t *testing.T) (*Service, state.Store) {
	t.Helper()
	st := state.NewMemoryStore()
	s := New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, st, zap.NewNop(), clock.NewMock())
	return s, st
}

func seedTagTestRule(t *testing.T, s *Service, st state.Store, name string) string {
	t.Helper()
	resp, aerr := s.putRuleTyped(context.Background(), &putRuleRequest{
		Name:         name,
		EventPattern: `{"source":["test"]}`,
	})
	if aerr != nil {
		t.Fatalf("putRuleTyped: %v", aerr)
	}
	return resp.RuleArn
}

// TestTagResourceTyped_corruptTagBlobDoesNotPanic: a corrupt persisted tag
// blob previously made `existing[k]=v` write to a nil map and panic. The
// malformed record must be isolated instead.
func TestTagResourceTyped_corruptTagBlobDoesNotPanic(t *testing.T) {
	// Given: a rule whose stored tag blob is corrupt
	s, st := newTagTestService(t)
	ctx := context.Background()
	arn := seedTagTestRule(t, s, st, "corrupt-typed")
	if err := st.Set(ctx, nsTags, arn, "{not json"); err != nil {
		t.Fatalf("seed corrupt blob: %v", err)
	}

	// When: the rule is tagged (must not panic)
	if _, aerr := s.tagResourceTyped(ctx, &tagResourceRequest{
		ResourceARN: arn,
		Tags:        []tagEntry{{Key: "env", Value: "prod"}},
	}); aerr != nil {
		t.Fatalf("tagResourceTyped: %v", aerr)
	}

	// Then: the tag replaced the corrupt blob
	list, aerr := s.listTagsForResourceTyped(ctx, &listTagsForResourceRequest{ResourceARN: arn})
	if aerr != nil {
		t.Fatalf("listTagsForResourceTyped: %v", aerr)
	}
	if len(list.Tags) != 1 || list.Tags[0].Key != "env" || list.Tags[0].Value != "prod" {
		t.Fatalf("Tags = %#v, want env=prod", list.Tags)
	}
}

// TestTagResourceTyped_missingResource: tagging a rule that does not exist
// returns ResourceNotFoundException.
func TestTagResourceTyped_missingResource(t *testing.T) {
	s, _ := newTagTestService(t)

	_, aerr := s.tagResourceTyped(context.Background(), &tagResourceRequest{
		ResourceARN: "arn:aws:events:us-east-1:000000000000:rule/absent",
		Tags:        []tagEntry{{Key: "env", Value: "prod"}},
	})
	if aerr == nil || aerr.Code != "ResourceNotFoundException" {
		t.Fatalf("aerr = %v, want ResourceNotFoundException", aerr)
	}
}

// TestTagResourceTyped_invalidTag: reserved aws: keys are rejected with
// ValidationException.
func TestTagResourceTyped_invalidTag(t *testing.T) {
	s, st := newTagTestService(t)
	arn := seedTagTestRule(t, s, st, "validated-typed")

	_, aerr := s.tagResourceTyped(context.Background(), &tagResourceRequest{
		ResourceARN: arn,
		Tags:        []tagEntry{{Key: "aws:reserved", Value: "x"}},
	})
	if aerr == nil || aerr.Code != "ValidationException" {
		t.Fatalf("aerr = %v, want ValidationException", aerr)
	}
}

// TestUntagResourceTyped_missingResource: untagging a missing resource also
// returns ResourceNotFoundException, as on AWS.
func TestUntagResourceTyped_missingResource(t *testing.T) {
	s, _ := newTagTestService(t)

	_, aerr := s.untagResourceTyped(context.Background(), &untagResourceRequest{
		ResourceARN: "arn:aws:events:us-east-1:000000000000:event-bus/absent",
		TagKeys:     []string{"env"},
	})
	if aerr == nil || aerr.Code != "ResourceNotFoundException" {
		t.Fatalf("aerr = %v, want ResourceNotFoundException", aerr)
	}
}
