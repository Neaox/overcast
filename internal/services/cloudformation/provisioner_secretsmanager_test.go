package cloudformation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
)

type secretGenerationRouter struct {
	calls int
}

func (r *secretGenerationRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.calls++
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"RandomPassword": "fixed-password"})
}

func callGeneratedSecretString(gen map[string]any, router http.Handler) (value string, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	value, err = generatedSecretString(context.Background(), router, &resolveContext{Region: "us-east-1"}, gen)
	return
}

func TestGeneratedSecretStringRejectsInvalidTemplatesBeforeDispatch(t *testing.T) {
	tests := []struct {
		name string
		gen  map[string]any
	}{
		{
			name: "JSON null",
			gen: map[string]any{
				"SecretStringTemplate": "null",
				"GenerateStringKey":    "password",
			},
		},
		{
			name: "JSON array",
			gen: map[string]any{
				"SecretStringTemplate": `[]`,
				"GenerateStringKey":    "password",
			},
		},
		{
			name: "generate key collision",
			gen: map[string]any{
				"SecretStringTemplate": `{"password":"keep-me"}`,
				"GenerateStringKey":    "password",
			},
		},
		{
			name: "template without key",
			gen: map[string]any{
				"SecretStringTemplate": `{"username":"admin"}`,
			},
		},
		{
			name: "key without template",
			gen: map[string]any{
				"GenerateStringKey": "password",
			},
		},
		{
			name: "fractional password length",
			gen: map[string]any{
				"PasswordLength": 12.5,
			},
		},
		{
			name: "invalid boolean",
			gen: map[string]any{
				"ExcludeNumbers": "sometimes",
			},
		},
		{
			name: "unknown nested member",
			gen: map[string]any{
				"UnexpectedOption": true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := &secretGenerationRouter{}
			_, err, panicked := callGeneratedSecretString(tc.gen, router)
			if panicked {
				t.Fatal("generatedSecretString panicked")
			}
			if err == nil {
				t.Fatal("generatedSecretString succeeded, want validation error")
			}
			if router.calls != 0 {
				t.Fatalf("service calls = %d, want 0 before validation succeeds", router.calls)
			}
		})
	}
}

type secretsUpdateRouter struct {
	targets        []string
	bodies         []map[string]any
	targetRequests map[string]int
	failAt         map[string]map[int]int
}

func (r *secretsUpdateRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)
	target := req.Header.Get("X-Amz-Target")
	r.targets = append(r.targets, target)
	r.bodies = append(r.bodies, body)
	if r.targetRequests == nil {
		r.targetRequests = make(map[string]int)
	}
	r.targetRequests[target]++
	if status := r.failAt[target][r.targetRequests[target]]; status != 0 {
		http.Error(w, "injected failure", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

func secretsUpdateProps(description, kmsKey string, tags ...map[string]any) map[string]any {
	props := map[string]any{"Name": "transactional", "Description": description, "KmsKeyId": kmsKey}
	if tags != nil {
		values := make([]any, len(tags))
		for i := range tags {
			values[i] = tags[i]
		}
		props["Tags"] = values
	}
	return props
}

func assertCleanTerminalUpdateFailure(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Update returned nil error")
	}
	var terminal updateFailure
	if !errors.As(err, &terminal) {
		t.Fatalf("error type = %T, want terminal updateFailure", err)
	}
	if isDirtyUpdateFailure(err) {
		t.Fatal("successfully rejected or compensated update was marked dirty")
	}
}

func TestSecretsManagerSecretUpdate_serviceErrorsAreTerminal(t *testing.T) {
	router := &secretsUpdateRouter{failAt: map[string]map[int]int{
		"secretsmanager.UpdateSecret": {1: http.StatusInternalServerError},
	}}
	_, _, err := (&secretsManagerSecretHandler{}).Update(context.Background(), router, nil,
		"arn:aws:secretsmanager:us-east-1:000000000000:secret:transactional-abcdef",
		secretsUpdateProps("new", "new-key"), secretsUpdateProps("old", "old-key"),
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"})
	assertCleanTerminalUpdateFailure(t, err)
}

func TestSecretsManagerSecretUpdate_tagFailureCompensatesMetadataAndKMS(t *testing.T) {
	router := &secretsUpdateRouter{failAt: map[string]map[int]int{
		"secretsmanager.TagResource": {1: http.StatusInternalServerError},
	}}
	_, _, err := (&secretsManagerSecretHandler{}).Update(context.Background(), router, nil,
		"arn:aws:secretsmanager:us-east-1:000000000000:secret:transactional-abcdef",
		secretsUpdateProps("new", "new-key", map[string]any{"Key": "new", "Value": "value"}),
		secretsUpdateProps("old", "old-key"),
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"})
	assertCleanTerminalUpdateFailure(t, err)
	wantTargets := []string{"secretsmanager.UpdateSecret", "secretsmanager.TagResource", "secretsmanager.UpdateSecret"}
	if !reflect.DeepEqual(router.targets, wantTargets) {
		t.Fatalf("targets = %v, want %v", router.targets, wantTargets)
	}
	if got := router.bodies[2]; got["Description"] != "old" || got["KmsKeyId"] != "old-key" {
		t.Fatalf("UpdateSecret compensation body = %#v", got)
	}
}

func TestSecretsManagerSecretUpdate_untagFailureCompensatesTagsThenMetadata(t *testing.T) {
	router := &secretsUpdateRouter{failAt: map[string]map[int]int{
		"secretsmanager.UntagResource": {1: http.StatusInternalServerError},
	}}
	_, _, err := (&secretsManagerSecretHandler{}).Update(context.Background(), router, nil,
		"arn:aws:secretsmanager:us-east-1:000000000000:secret:transactional-abcdef",
		secretsUpdateProps("new", "new-key", map[string]any{"Key": "new", "Value": "value"}),
		secretsUpdateProps("old", "old-key", map[string]any{"Key": "old", "Value": "value"}),
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"})
	assertCleanTerminalUpdateFailure(t, err)
	wantTargets := []string{
		"secretsmanager.UpdateSecret", "secretsmanager.TagResource", "secretsmanager.UntagResource",
		"secretsmanager.TagResource", "secretsmanager.UntagResource", "secretsmanager.UpdateSecret",
	}
	if !reflect.DeepEqual(router.targets, wantTargets) {
		t.Fatalf("targets = %v, want reverse compensation %v", router.targets, wantTargets)
	}
}

func TestSecretsManagerSecretUpdate_removalOnlyFailureRestoresRemovedTags(t *testing.T) {
	router := &secretsUpdateRouter{failAt: map[string]map[int]int{
		"secretsmanager.UntagResource": {1: http.StatusInternalServerError},
	}}
	_, _, err := (&secretsManagerSecretHandler{}).Update(context.Background(), router, nil,
		"arn:aws:secretsmanager:us-east-1:000000000000:secret:transactional-abcdef",
		map[string]any{"Name": "transactional"},
		map[string]any{"Name": "transactional", "Tags": []any{map[string]any{"Key": "old", "Value": "value"}}},
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"})
	assertCleanTerminalUpdateFailure(t, err)
	wantTargets := []string{"secretsmanager.UntagResource", "secretsmanager.TagResource"}
	if !reflect.DeepEqual(router.targets, wantTargets) {
		t.Fatalf("targets = %v, want removed-tag restoration %v", router.targets, wantTargets)
	}
}

func TestSecretsManagerSecretUpdate_compensationFailureIsDirty(t *testing.T) {
	router := &secretsUpdateRouter{failAt: map[string]map[int]int{
		"secretsmanager.TagResource":  {1: http.StatusInternalServerError},
		"secretsmanager.UpdateSecret": {2: http.StatusInternalServerError},
	}}
	_, _, err := (&secretsManagerSecretHandler{}).Update(context.Background(), router, nil,
		"arn:aws:secretsmanager:us-east-1:000000000000:secret:transactional-abcdef",
		secretsUpdateProps("new", "new-key", map[string]any{"Key": "new", "Value": "value"}),
		secretsUpdateProps("old", "old-key"),
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"})
	if err == nil || !isDirtyUpdateFailure(err) {
		t.Fatalf("error = %v, want dirty terminal update failure", err)
	}
}

func TestSecretsManagerSecretUpdate_tagCompensationFailureIsDirty(t *testing.T) {
	router := &secretsUpdateRouter{failAt: map[string]map[int]int{
		"secretsmanager.TagResource":   {2: http.StatusInternalServerError},
		"secretsmanager.UntagResource": {1: http.StatusInternalServerError},
	}}
	_, _, err := (&secretsManagerSecretHandler{}).Update(context.Background(), router, nil,
		"arn:aws:secretsmanager:us-east-1:000000000000:secret:transactional-abcdef",
		secretsUpdateProps("new", "new-key", map[string]any{"Key": "new", "Value": "value"}),
		secretsUpdateProps("old", "old-key", map[string]any{"Key": "old", "Value": "value"}),
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"})
	if err == nil || !isDirtyUpdateFailure(err) {
		t.Fatalf("error = %v, want dirty terminal update failure", err)
	}
	wantTargets := []string{
		"secretsmanager.UpdateSecret", "secretsmanager.TagResource", "secretsmanager.UntagResource",
		"secretsmanager.TagResource", "secretsmanager.UpdateSecret",
	}
	if !reflect.DeepEqual(router.targets, wantTargets) {
		t.Fatalf("targets = %v, want best-effort reverse compensation %v", router.targets, wantTargets)
	}
}

func TestSecretsManagerSecretUpdate_kmsKeyRemovalSendsExplicitReset(t *testing.T) {
	router := &secretsUpdateRouter{}
	props := map[string]any{"Name": "transactional"}
	prior := map[string]any{"Name": "transactional", "KmsKeyId": "old-key"}
	_, _, err := (&secretsManagerSecretHandler{}).Update(context.Background(), router, nil,
		"arn:aws:secretsmanager:us-east-1:000000000000:secret:transactional-abcdef", props, prior,
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(router.bodies) != 1 || router.bodies[0]["KmsKeyId"] != "" {
		t.Fatalf("requests = %#v, want explicit empty KmsKeyId", router.bodies)
	}
}

func TestSecretsManagerSecretCreateRejectsLiteralAndGeneratedValuesBeforeDispatch(t *testing.T) {
	router := &secretGenerationRouter{}
	_, _, err := (&secretsManagerSecretHandler{}).Create(context.Background(), router, nil, map[string]any{
		"Name":                 "both-values",
		"SecretString":         "literal",
		"GenerateSecretString": map[string]any{},
	}, &resolveContext{Region: "us-east-1"})
	if err == nil {
		t.Fatal("Create succeeded, want mutual-exclusivity error")
	}
	if router.calls != 0 {
		t.Fatalf("service calls = %d, want 0", router.calls)
	}
}

func TestGeneratedSecretStringMergesValidatedTemplate(t *testing.T) {
	router := &secretGenerationRouter{}
	value, err, panicked := callGeneratedSecretString(map[string]any{
		"SecretStringTemplate": `{"username":"admin"}`,
		"GenerateStringKey":    "password",
	}, router)
	if panicked {
		t.Fatal("generatedSecretString panicked")
	}
	if err != nil {
		t.Fatalf("generatedSecretString: %v", err)
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(value), &fields); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if fields["username"] != "admin" || fields["password"] != "fixed-password" {
		t.Fatalf("generated fields = %v", fields)
	}
	if router.calls != 1 {
		t.Fatalf("service calls = %d, want 1", router.calls)
	}
}
