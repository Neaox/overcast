package secretsmanager

import (
	"context"
	"testing"
)

func TestServiceSecretValue_readsCurrentVersionWithoutCaching(t *testing.T) {
	// Given: a consumer-facing resolver backed by a secret with an initial
	// AWSCURRENT version.
	h, _ := newRotationTestHandler(t)
	secret := seedSecret(t, h, "consumer-secret", "before")
	service := &Service{handler: h}
	ctx := context.Background()
	if got, ok := service.SecretValue(ctx, secret.Name); !ok || got != "before" {
		t.Fatalf("initial SecretValue = %q, %v; want before, true", got, ok)
	}

	// When: PutSecretValue's shared staging logic moves AWSCURRENT to a new
	// version after the resolver has already been used.
	if _, aerr := h.stageVersion(secret, "", "after", "", nil); aerr != nil {
		t.Fatalf("stageVersion: %v", aerr)
	}
	if aerr := h.store.putSecret(ctx, secret); aerr != nil {
		t.Fatalf("putSecret: %v", aerr)
	}

	// Then: the next cross-service read observes AWSCURRENT directly from the
	// store rather than returning a value cached by the prior read.
	if got, ok := service.SecretValue(ctx, secret.Name); !ok || got != "after" {
		t.Fatalf("updated SecretValue = %q, %v; want after, true", got, ok)
	}
}
