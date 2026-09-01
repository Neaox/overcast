package ecs

import (
	"context"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/serviceutil"
	"go.uber.org/zap"
)

type stubSecrets map[string]string

func (s stubSecrets) SecretValue(_ context.Context, id string) (string, bool) {
	v, ok := s[id]
	return v, ok
}

type stubParams map[string]string

func (p stubParams) ParameterValue(_ context.Context, name string) (string, bool) {
	v, ok := p[name]
	return v, ok
}

func secretsHandler(t *testing.T, sm stubSecrets, ps stubParams) *Handler {
	t.Helper()
	return &Handler{
		log:        serviceutil.NewServiceLogger(zap.NewNop(), "ecs"),
		secrets:    sm,
		parameters: ps,
	}
}

func TestSecretEnv_resolvesSecretsManagerAndParameterStore(t *testing.T) {
	const secretArn = "arn:aws:secretsmanager:us-east-1:000000000000:secret:db-creds-AbCdEf"
	h := secretsHandler(t,
		stubSecrets{secretArn: "s3cr3t"},
		stubParams{"/app/api-key": "pk-123"},
	)

	// Given: a container taking one secret from each source, the two forms
	// `ecs.Secret.fromSecretsManager` and `fromSsmParameter` produce.
	cd := ContainerDefinition{Name: "app", Secrets: []ContainerSecret{
		{Name: "DB_PASSWORD", ValueFrom: secretArn},
		{Name: "API_KEY", ValueFrom: "arn:aws:ssm:us-east-1:000000000000:parameter/app/api-key"},
	}}

	// When: the container's environment is built.
	got := h.secretEnv(context.Background(), cd)

	// Then: both arrive as environment variables.
	want := map[string]bool{"DB_PASSWORD=s3cr3t": true, "API_KEY=pk-123": true}
	if len(got) != len(want) {
		t.Fatalf("got %d env entries, want %d: %v", len(got), len(want), got)
	}
	for _, e := range got {
		if !want[e] {
			t.Errorf("unexpected env entry %q", e)
		}
	}
}

func TestSecretEnv_extractsJSONKeyFromSecret(t *testing.T) {
	// Given: a JSON secret, which is what an RDS-generated credential is, and a
	// reference naming one key — the shape
	// `ecs.Secret.fromSecretsManager(secret, "password")` produces.
	const secretArn = "arn:aws:secretsmanager:us-east-1:000000000000:secret:db-AbCdEf"
	h := secretsHandler(t,
		stubSecrets{secretArn: `{"username":"admin","password":"hunter2","port":3306}`},
		nil,
	)

	cd := ContainerDefinition{Name: "app", Secrets: []ContainerSecret{
		{Name: "DB_PASSWORD", ValueFrom: secretArn + ":password::"},
		{Name: "DB_USER", ValueFrom: secretArn + ":username::"},
		{Name: "DB_PORT", ValueFrom: secretArn + ":port::"},
	}}

	got := h.secretEnv(context.Background(), cd)

	// Then: each named key is extracted, including a non-string one.
	joined := strings.Join(got, " ")
	for _, want := range []string{"DB_PASSWORD=hunter2", "DB_USER=admin", "DB_PORT=3306"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in %v", want, got)
		}
	}
}

func TestSecretEnv_unresolvedSecretIsOmittedNotFabricated(t *testing.T) {
	// Given: a reference to a secret that does not exist.
	h := secretsHandler(t, stubSecrets{}, stubParams{})
	cd := ContainerDefinition{Name: "app", Secrets: []ContainerSecret{
		{Name: "MISSING", ValueFrom: "arn:aws:secretsmanager:us-east-1:000000000000:secret:nope-AbCdEf"},
	}}

	// When/Then: nothing is injected — an empty value would look like a secret
	// that resolved to the empty string.
	if got := h.secretEnv(context.Background(), cd); len(got) != 0 {
		t.Errorf("expected no env entries for an unresolved secret, got %v", got)
	}
}

func TestSplitSecretsManagerRef(t *testing.T) {
	const arn = "arn:aws:secretsmanager:us-east-1:000000000000:secret:db-AbCdEf"
	tests := []struct {
		name        string
		in          string
		wantID      string
		wantJSONKey string
	}{
		{"bare arn", arn, arn, ""},
		{"json key", arn + ":password::", arn, "password"},
		{"json key with stage", arn + ":password:AWSCURRENT:", arn, "password"},
		{"empty json key", arn + ":::", arn, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, key := splitSecretsManagerRef(tc.in)
			if id != tc.wantID || key != tc.wantJSONKey {
				t.Errorf("splitSecretsManagerRef(%q) = (%q, %q), want (%q, %q)",
					tc.in, id, key, tc.wantID, tc.wantJSONKey)
			}
		})
	}
}

func TestParameterNameOf(t *testing.T) {
	tests := []struct{ in, want string }{
		{"arn:aws:ssm:us-east-1:000000000000:parameter/app/key", "/app/key"},
		{"arn:aws:ssm:us-east-1:000000000000:parameter/key", "/key"},
		{"/app/key", "/app/key"},
		{"key", "key"},
	}
	for _, tc := range tests {
		if got := parameterNameOf(tc.in); got != tc.want {
			t.Errorf("parameterNameOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
