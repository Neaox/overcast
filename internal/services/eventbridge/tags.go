package eventbridge

import (
	"context"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ebTagCfg tunes shared tag validation to EventBridge's error shape.
var ebTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "ValidationException",
	InvalidCode:     "ValidationException",
	ExceededMessage: "Exceeded maximum number of tags allowed.",
}

func errEBResourceNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    "Resource " + arn + " does not exist.",
		HTTPStatus: http.StatusBadRequest,
	}
}

// requireTaggableResource checks that the ARN names a rule or event bus that
// actually exists. Real EventBridge refuses tag operations on unknown
// resources instead of minting a tag store for any string.
func (s *Service) requireTaggableResource(ctx context.Context, arn string) *protocol.AWSError {
	// arn:aws:events:<region>:<account>:<resource>
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" || parts[2] != "events" {
		return &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "Parameter ResourceARN is not a valid ARN.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	resource := parts[5]
	switch {
	case strings.HasPrefix(resource, "rule/"):
		// Rules are stored under "<busName>/<ruleName>"; a default-bus rule
		// ARN may omit the bus segment.
		key := strings.TrimPrefix(resource, "rule/")
		if !strings.Contains(key, "/") {
			key = "default/" + key
		}
		if _, found, err := s.store.Get(ctx, nsRules, serviceutil.RegionKey(s.region(ctx), key)); err != nil {
			return protocol.ErrInternalError
		} else if !found {
			return errEBResourceNotFound(arn)
		}
		return nil
	case strings.HasPrefix(resource, "event-bus/"):
		name := strings.TrimPrefix(resource, "event-bus/")
		if name == "default" {
			return nil
		}
		if _, found, err := s.store.Get(ctx, nsBuses, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
			return protocol.ErrInternalError
		} else if !found {
			return errEBResourceNotFound(arn)
		}
		return nil
	default:
		return errEBResourceNotFound(arn)
	}
}

// applyResourceTags validates and merges tags into the resource's tag blob.
// Shared by the legacy JSON and typed CBOR paths so both stay in lockstep.
func (s *Service) applyResourceTags(ctx context.Context, arn string, incoming map[string]string) *protocol.AWSError {
	if aerr := s.requireTaggableResource(ctx, arn); aerr != nil {
		return aerr
	}
	tagStore := &serviceutil.NSStore{Store: s.store, NS: nsTags}
	existing, aerr := tagStore.Load(ctx, arn)
	if aerr != nil {
		return aerr
	}
	for k, v := range incoming {
		existing[k] = v
	}
	if aerr := serviceutil.ValidateTags(ebTagCfg, existing); aerr != nil {
		return aerr
	}
	return tagStore.Save(ctx, arn, existing)
}

// removeResourceTags removes the keys from the resource's tag blob.
func (s *Service) removeResourceTags(ctx context.Context, arn string, keys []string) *protocol.AWSError {
	if aerr := s.requireTaggableResource(ctx, arn); aerr != nil {
		return aerr
	}
	tagStore := &serviceutil.NSStore{Store: s.store, NS: nsTags}
	existing, aerr := tagStore.Load(ctx, arn)
	if aerr != nil {
		return aerr
	}
	for _, k := range keys {
		delete(existing, k)
	}
	return tagStore.Save(ctx, arn, existing)
}

// listResourceTags returns the resource's current tags.
func (s *Service) listResourceTags(ctx context.Context, arn string) (map[string]string, *protocol.AWSError) {
	if aerr := s.requireTaggableResource(ctx, arn); aerr != nil {
		return nil, aerr
	}
	tagStore := &serviceutil.NSStore{Store: s.store, NS: nsTags}
	return tagStore.Load(ctx, arn)
}
