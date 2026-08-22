package secretsmanager

import (
	"context"
	"testing"
)

// An unrecognised Filter.Key must be refused, matching real Secrets Manager's
// InvalidParameterException — one of ListSecrets' and BatchGetSecretValue's
// own declared errors — rather than silently matching nothing. See
// docs/plans/ec2-filter-rule-cross-service.md, finding 3, and #1188.

func TestListSecrets_unrecognisedFilterKeyIsRefused(t *testing.T) {
	h, _ := newRotationTestHandler(t)
	seedSecret(t, h, "some-secret", "value")

	_, aerr := h.listSecretsTyped(context.Background(), &listSecretsRequest{
		Filters: []secretFilter{{Key: "bogus-key", Values: []string{"x"}}},
	})
	if aerr == nil {
		t.Fatal("ListSecrets with an unrecognised Filter.Key: got no error, want InvalidParameterException")
	}
	if aerr.Code != "InvalidParameterException" {
		t.Errorf("Code = %q, want InvalidParameterException", aerr.Code)
	}
}

func TestListSecrets_recognisedFilterKeyMatches(t *testing.T) {
	h, _ := newRotationTestHandler(t)
	seedSecret(t, h, "findable-secret", "value")

	out, aerr := h.listSecretsTyped(context.Background(), &listSecretsRequest{
		Filters: []secretFilter{{Key: "name", Values: []string{"findable"}}},
	})
	if aerr != nil {
		t.Fatalf("ListSecrets: %v", aerr)
	}
	if len(out.SecretList) != 1 || out.SecretList[0].Name != "findable-secret" {
		t.Fatalf("SecretList = %+v, want exactly [findable-secret]", out.SecretList)
	}
}

func TestListSecrets_freeTextFilterValueThatMatchesNothingStaysEmpty(t *testing.T) {
	h, _ := newRotationTestHandler(t)
	seedSecret(t, h, "some-secret", "value")

	out, aerr := h.listSecretsTyped(context.Background(), &listSecretsRequest{
		Filters: []secretFilter{{Key: "name", Values: []string{"no-such-prefix"}}},
	})
	if aerr != nil {
		t.Fatalf("ListSecrets: %v", aerr)
	}
	if len(out.SecretList) != 0 {
		t.Fatalf("SecretList = %+v, want empty — a bogus VALUE for a recognised key matches nothing, unchanged", out.SecretList)
	}
}

func TestBatchGetSecretValue_unrecognisedFilterKeyIsRefused(t *testing.T) {
	h, _ := newRotationTestHandler(t)
	seedSecret(t, h, "some-secret", "value")

	_, aerr := h.batchGetSecretValueTyped(context.Background(), &batchGetSecretValueRequest{
		Filters: []secretFilter{{Key: "bogus-key", Values: []string{"x"}}},
	})
	if aerr == nil {
		t.Fatal("BatchGetSecretValue with an unrecognised Filter.Key: got no error, want InvalidParameterException")
	}
	if aerr.Code != "InvalidParameterException" {
		t.Errorf("Code = %q, want InvalidParameterException", aerr.Code)
	}
}
