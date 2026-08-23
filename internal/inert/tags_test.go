package inert_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/inert"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

var tagRules = serviceutil.TagValidationConfig{
	ExceededCode:    "ConstraintViolationException",
	ExceededMessage: "too many tags",
	InvalidCode:     "InvalidInputException",
}

func newTags(t *testing.T) *inert.Tags {
	t.Helper()
	cfg, _ := newConfig(t, clock.NewMock(), nil)
	return inert.NewTags(cfg, "test:tags")
}

func TestTags_ApplyMergesRatherThanReplaces(t *testing.T) {
	// Given: a resource carrying one tag.
	ctx := context.Background()
	tags := newTags(t)
	const arn = "arn:aws:test::000000000000:widget/w-1"
	if _, aerr := tags.Apply(ctx, arn, map[string]string{"env": "dev"}, tagRules); aerr != nil {
		t.Fatalf("Apply: %v", aerr)
	}

	// When: a second, disjoint tag is applied.
	got, aerr := tags.Apply(ctx, arn, map[string]string{"team": "platform"}, tagRules)

	// Then: both survive — merging is the AWS TagResource semantic.
	if aerr != nil {
		t.Fatalf("Apply: %v", aerr)
	}
	if got["env"] != "dev" || got["team"] != "platform" {
		t.Fatalf("Apply = %v, want both env and team", got)
	}
}

func TestTags_RemoveTakesOnlyTheNamedKeys(t *testing.T) {
	ctx := context.Background()
	tags := newTags(t)
	const arn = "arn:aws:test::000000000000:widget/w-1"
	if _, aerr := tags.Apply(ctx, arn, map[string]string{"env": "dev", "team": "platform"}, tagRules); aerr != nil {
		t.Fatalf("Apply: %v", aerr)
	}

	// Removing a tag that was never set is not an error.
	got, aerr := tags.Remove(ctx, arn, []string{"env", "never-set"})
	if aerr != nil {
		t.Fatalf("Remove: %v", aerr)
	}
	if _, still := got["env"]; still {
		t.Fatalf("Remove left env in place: %v", got)
	}
	if got["team"] != "platform" {
		t.Fatalf("Remove dropped an unnamed key: %v", got)
	}
}

// TestTags_DeleteStopsThemOutlivingTheResource is why Delete exists at all:
// namespaced tags have nothing tying them to the record's lifetime, so a
// service that forgets to remove them keeps answering ListTagsForResource
// for a resource that is gone.
func TestTags_DeleteStopsThemOutlivingTheResource(t *testing.T) {
	ctx := context.Background()
	tags := newTags(t)
	const arn = "arn:aws:test::000000000000:widget/w-1"
	if _, aerr := tags.Apply(ctx, arn, map[string]string{"env": "dev"}, tagRules); aerr != nil {
		t.Fatalf("Apply: %v", aerr)
	}
	if aerr := tags.Delete(ctx, arn); aerr != nil {
		t.Fatalf("Delete: %v", aerr)
	}

	got, aerr := tags.Load(ctx, arn)
	if aerr != nil {
		t.Fatalf("Load: %v", aerr)
	}
	if len(got) != 0 {
		t.Fatalf("Load after Delete = %v, want empty", got)
	}
}

func TestTags_LoadOfAnUntaggedResourceIsEmptyNotNil(t *testing.T) {
	got, aerr := newTags(t).Load(context.Background(), "arn:aws:test::000000000000:widget/never-tagged")
	if aerr != nil {
		t.Fatalf("Load: %v", aerr)
	}
	if got == nil {
		t.Fatal("Load returned a nil map — callers index into the result")
	}
}

// TestPageError_MapsTheInvalidTokenSentinel is the mapping every generated
// List handler relies on: a garbage token becomes the service's own modeled
// error, and a storage failure stays an InternalError.
func TestPageError_MapsTheInvalidTokenSentinel(t *testing.T) {
	modeled := &protocol.AWSError{Code: "InvalidInputException", Message: "bad token", HTTPStatus: http.StatusBadRequest}

	if got := inert.PageError(nil, modeled); got != nil {
		t.Fatalf("PageError(nil) = %v, want nil", got)
	}

	got := inert.PageError(serviceutil.ErrInvalidPageToken, modeled)
	if got == nil || got.Code != modeled.Code || got.HTTPStatus != modeled.HTTPStatus {
		t.Fatalf("PageError(ErrInvalidPageToken) = %v, want %s/%d", got, modeled.Code, modeled.HTTPStatus)
	}

	got = inert.PageError(context.DeadlineExceeded, modeled)
	if got == nil || got.Code != protocol.ErrInternalError.Code {
		t.Fatalf("PageError(storage failure) = %v, want %s", got, protocol.ErrInternalError.Code)
	}
}

func TestStorageError_PreservesTheCause(t *testing.T) {
	if got := inert.StorageError(nil); got != nil {
		t.Fatalf("StorageError(nil) = %v, want nil", got)
	}
	got := inert.StorageError(context.DeadlineExceeded)
	if got.Code != protocol.ErrInternalError.Code {
		t.Fatalf("StorageError code = %q, want %q", got.Code, protocol.ErrInternalError.Code)
	}
	if protocol.Cause(got) != context.DeadlineExceeded {
		t.Fatalf("StorageError dropped the cause: %v", protocol.Cause(got))
	}
}
