package ecs

// secrets.go — resolving a container definition's `secrets`.
//
// ECS injects each entry as an environment variable whose value it fetches at
// task start, which is how a task gets credentials without them being written
// into the task definition. A container definition that asks for secrets and
// gets none starts missing variables it was promised — usually presenting as
// the application failing to reach its database with no explanation.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// SecretsManagerResolver reads a secret's current string value.
type SecretsManagerResolver interface {
	SecretValue(ctx context.Context, secretID string) (string, bool)
}

// ParameterResolver reads an SSM parameter's latest value.
type ParameterResolver interface {
	ParameterValue(ctx context.Context, name string) (string, bool)
}

// secretEnv resolves a container's secrets into "NAME=value" environment
// entries. Entries that cannot be resolved are reported and left out rather
// than failing the task: that mirrors nothing in AWS (there the task fails),
// but a half-configured emulator should not make a task unstartable when the
// developer can see exactly which secret is missing.
func (h *Handler) secretEnv(ctx context.Context, cd ContainerDefinition) []string {
	if len(cd.Secrets) == 0 {
		return nil
	}
	env := make([]string, 0, len(cd.Secrets))
	var unresolved []string
	for _, s := range cd.Secrets {
		value, ok := h.resolveSecret(ctx, s.ValueFrom)
		if !ok {
			unresolved = append(unresolved, s.Name+" ("+s.ValueFrom+")")
			continue
		}
		env = append(env, s.Name+"="+value)
	}
	if len(unresolved) > 0 {
		h.log.Warn("ecs: container secrets could not be resolved — the container starts without them",
			zap.String("container", cd.Name),
			zap.Strings("secrets", unresolved),
			zap.String("hint", "create the Secrets Manager secret or SSM parameter the task definition names"))
	}
	return env
}

// resolveSecret reads one valueFrom.
//
// AWS accepts two sources, told apart by the ARN's service segment:
//
//	arn:aws:secretsmanager:…:secret:name-AbCdEf[:json-key[:version-stage[:version-id]]]
//	arn:aws:ssm:…:parameter/name   (or a bare parameter name)
//
// The Secrets Manager form may name a key inside a JSON secret, which is the
// shape CDK's `ecs.Secret.fromSecretsManager(secret, "password")` produces and
// therefore the one most task definitions use.
func (h *Handler) resolveSecret(ctx context.Context, valueFrom string) (string, bool) {
	if valueFrom == "" {
		return "", false
	}
	if isSecretsManagerRef(valueFrom) {
		secretID, jsonKey := splitSecretsManagerRef(valueFrom)
		if h.secrets == nil {
			return "", false
		}
		raw, ok := h.secrets.SecretValue(ctx, secretID)
		if !ok {
			return "", false
		}
		if jsonKey == "" {
			return raw, true
		}
		return jsonFieldOf(raw, jsonKey)
	}
	if h.parameters == nil {
		return "", false
	}
	return h.parameters.ParameterValue(ctx, parameterNameOf(valueFrom))
}

func isSecretsManagerRef(valueFrom string) bool {
	return strings.HasPrefix(valueFrom, "arn:") && strings.Contains(valueFrom, ":secretsmanager:")
}

// splitSecretsManagerRef separates the secret ARN from the optional
// json-key/version-stage/version-id suffix.
//
// The ARN itself contains colons, so the suffix cannot be found by counting
// from the left: a Secrets Manager ARN has exactly seven colon-separated
// fields, and anything after the seventh is the suffix.
func splitSecretsManagerRef(valueFrom string) (secretID, jsonKey string) {
	const arnFields = 7
	parts := strings.Split(valueFrom, ":")
	if len(parts) <= arnFields {
		return valueFrom, ""
	}
	secretID = strings.Join(parts[:arnFields], ":")
	return secretID, parts[arnFields]
}

// parameterNameOf reduces an SSM parameter reference to the name the Parameter
// Store API takes, which is the path after "parameter" in an ARN.
func parameterNameOf(valueFrom string) string {
	if !strings.HasPrefix(valueFrom, "arn:") {
		return valueFrom
	}
	if i := strings.Index(valueFrom, ":parameter"); i >= 0 {
		name := valueFrom[i+len(":parameter"):]
		if name != "" && name[0] != '/' {
			return "/" + name
		}
		return name
	}
	return valueFrom
}

// jsonFieldOf extracts one key from a JSON secret, the form
// `Secret.fromSecretsManager(secret, "key")` relies on.
func jsonFieldOf(raw, key string) (string, bool) {
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return "", false
	}
	v, ok := fields[key]
	if !ok {
		return "", false
	}
	switch typed := v.(type) {
	case string:
		return typed, true
	case nil:
		return "", false
	default:
		return fmt.Sprint(typed), true
	}
}
