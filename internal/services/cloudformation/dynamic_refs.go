package cloudformation

// dynamic_refs.go — CloudFormation dynamic references.
//
// A dynamic reference is a plain string in a template that CloudFormation
// substitutes from another service at deploy time:
//
//	{{resolve:secretsmanager:my-secret:SecretString:username}}
//	{{resolve:ssm:/app/tier}}
//	{{resolve:ssm-secure:/app/api-key:3}}
//
// It is not an intrinsic function — there is no map to match on, just text
// inside a property value — so the intrinsic resolver walked straight past it
// and handed the literal reference to the service. An RDS instance deployed
// that way came up with a MasterUsername of
// "{{resolve:secretsmanager:arn:aws:secretsmanager:…:SecretString:username::}}",
// which is not a username any engine will accept: the container's entrypoint
// failed and the instance was unusable, with the web UI faithfully displaying
// the reference text it had been given.
//
// Expansion runs as a separate pass after the intrinsics rather than inside
// them, so every string is expanded exactly once. That ordering matters for
// more than tidiness: a resolved secret's own content must never be rescanned
// for references, or a secret whose value happened to contain "{{resolve:"
// would reach back into the resolver.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/middleware"
)

// dynamicRefPattern matches one whole {{resolve:...}} reference. Neither a
// secret ARN nor an SSM parameter name may contain a brace, so excluding them
// keeps the match from running past the end of a reference.
var dynamicRefPattern = regexp.MustCompile(`\{\{resolve:[^{}]*\}\}`)

// secretARNFields is the number of colon-separated fields in a Secrets Manager
// ARN: arn:aws:secretsmanager:<region>:<account>:secret:<name-suffix>. The
// secret ID is the first field of a reference and may be either a bare name or
// a full ARN, so splitting a reference on ":" has to know how deep an ARN goes.
const secretARNFields = 7

// dynamicRef is a parsed dynamic reference.
type dynamicRef struct {
	// Raw is the reference exactly as it appeared, for error messages.
	Raw string
	// Service is the resolver name: "secretsmanager", "ssm" or "ssm-secure".
	Service string
	// Fields are the service-specific parts, in template order, with a leading
	// ARN kept whole rather than split across its own colons.
	Fields []string
}

// field returns Fields[i], or "" when the reference omitted it. The trailing
// fields of a reference are routinely left empty — "…:SecretString:username::"
// is how CDK emits a reference with no version stage or ID.
func (r dynamicRef) field(i int) string {
	if i < 0 || i >= len(r.Fields) {
		return ""
	}
	return r.Fields[i]
}

// parseDynamicRef parses one "{{resolve:...}}" reference. It reports false for
// text that is not a dynamic reference at all.
func parseDynamicRef(raw string) (dynamicRef, bool) {
	inner, ok := strings.CutPrefix(raw, "{{")
	if !ok {
		return dynamicRef{}, false
	}
	inner, ok = strings.CutSuffix(inner, "}}")
	if !ok {
		return dynamicRef{}, false
	}
	rest, ok := strings.CutPrefix(inner, "resolve:")
	if !ok {
		return dynamicRef{}, false
	}
	service, payload, hasPayload := strings.Cut(rest, ":")
	if service == "" || !hasPayload || payload == "" {
		return dynamicRef{}, false
	}
	return dynamicRef{Raw: raw, Service: service, Fields: splitRefFields(payload)}, true
}

// splitRefFields splits a reference payload on ":" while keeping a leading ARN
// — itself six colons deep — in one piece.
func splitRefFields(payload string) []string {
	if !strings.HasPrefix(payload, "arn:") {
		return strings.Split(payload, ":")
	}
	parts := strings.SplitN(payload, ":", secretARNFields+1)
	if len(parts) <= secretARNFields {
		// The whole payload is the ARN and nothing follows it.
		return []string{payload}
	}
	arn := strings.Join(parts[:secretARNFields], ":")
	return append([]string{arn}, strings.Split(parts[secretARNFields], ":")...)
}

// ── Expansion ──────────────────────────────────────────────────────────────

// expandDynamicRefs walks an already-resolved value and replaces every dynamic
// reference in every string it holds. References that cannot be resolved are
// left in place and recorded on the context; the provisioner turns that into a
// resource failure rather than letting the literal text be persisted.
func expandDynamicRefs(v any, ctx *resolveContext) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[k] = expandDynamicRefs(item, ctx)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = expandDynamicRefs(item, ctx)
		}
		return out
	case string:
		return ctx.expandDynamicRefsInString(val)
	default:
		return v
	}
}

// expandDynamicRefsInString substitutes every dynamic reference in one string.
// A reference may be embedded in surrounding text and a string may hold several.
func (c *resolveContext) expandDynamicRefsInString(s string) string {
	if c == nil || !strings.Contains(s, "{{resolve:") {
		return s
	}
	return dynamicRefPattern.ReplaceAllStringFunc(s, func(raw string) string {
		ref, ok := parseDynamicRef(raw)
		if !ok {
			c.recordDynamicRefErr(fmt.Errorf("malformed dynamic reference %s", raw))
			return raw
		}
		if c.DynamicRef == nil {
			c.recordDynamicRefErr(fmt.Errorf("dynamic reference %s cannot be resolved in this context", raw))
			return raw
		}
		value, err := c.DynamicRef(ref)
		if err != nil {
			c.recordDynamicRefErr(fmt.Errorf("resolve %s: %w", raw, err))
			return raw
		}
		return value
	})
}

// recordDynamicRefErr keeps the first failure. The first one is the one worth
// reporting: later references in the same resource are usually collateral.
func (c *resolveContext) recordDynamicRefErr(err error) {
	if c.dynamicRefErr == nil {
		c.dynamicRefErr = err
	}
}

// takeDynamicRefErr returns and clears any recorded failure. Callers must take
// it at the point they resolve properties — leaving it set would attribute the
// failure to whichever resource is provisioned next.
func (c *resolveContext) takeDynamicRefErr() error {
	if c == nil {
		return nil
	}
	err := c.dynamicRefErr
	c.dynamicRefErr = nil
	return err
}

// ── Resolution against the emulated services ───────────────────────────────

// dynamicRefResolver returns the resolver installed on a stack's resolve
// context. It dispatches through the emulator's own router, so a reference
// reads exactly what the corresponding GetSecretValue or GetParameter call
// would return.
func (p *provisioner) dynamicRefResolver(region string) func(dynamicRef) (string, error) {
	return func(ref dynamicRef) (string, error) {
		p.mu.Lock()
		router := p.router
		p.mu.Unlock()
		if router == nil {
			return "", fmt.Errorf("router not initialised")
		}
		ctx := middleware.ContextWithRegion(p.ctx, region)

		switch ref.Service {
		case "secretsmanager":
			return resolveSecretsManagerRef(ctx, router, region, ref)
		case "ssm", "ssm-secure":
			return p.resolveSSMRef(ctx, router, region, ref)
		default:
			// "resolve:s3" and "resolve:cloudformation" exist on AWS but have
			// no counterpart here; naming the service beats a generic failure.
			return "", fmt.Errorf("dynamic reference service %q is not supported", ref.Service)
		}
	}
}

// resolveSecretsManagerRef resolves
// {{resolve:secretsmanager:secret-id:secret-string:json-key:version-stage:version-id}}.
func resolveSecretsManagerRef(ctx context.Context, router http.Handler, region string, ref dynamicRef) (string, error) {
	secretID := ref.field(0)
	if secretID == "" {
		return "", fmt.Errorf("no secret ID")
	}
	// AWS defines exactly one value for the secret-string field, and rejects
	// anything else rather than guessing.
	if sel := ref.field(1); sel != "" && sel != "SecretString" {
		return "", fmt.Errorf("secret value selector %q is not valid (only SecretString is)", sel)
	}

	body := map[string]string{"SecretId": secretID}
	if stage := ref.field(3); stage != "" {
		body["VersionStage"] = stage
	}
	if versionID := ref.field(4); versionID != "" {
		body["VersionId"] = versionID
	}
	rec, err := internalJSON(ctx, router, region, "secretsmanager.GetSecretValue", body)
	if err != nil {
		return "", err
	}
	var out struct {
		SecretString string `json:"SecretString"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		return "", fmt.Errorf("decode GetSecretValue response: %w", err)
	}

	jsonKey := ref.field(2)
	if jsonKey == "" {
		return out.SecretString, nil
	}
	// Error text below deliberately describes the secret without quoting it —
	// a stack event is not a place to leak a secret value.
	var fields map[string]any
	if err := json.Unmarshal([]byte(out.SecretString), &fields); err != nil {
		return "", fmt.Errorf("secret %s is not a JSON document, so key %q cannot be selected from it", secretID, jsonKey)
	}
	value, ok := fields[jsonKey]
	if !ok {
		return "", fmt.Errorf("secret %s has no key %q", secretID, jsonKey)
	}
	if s, ok := value.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", value), nil
}

// resolveSSMRef resolves {{resolve:ssm:parameter-name:version}} and its
// ssm-secure counterpart.
//
// Both read through GetParameter with decryption on. Overcast keeps parameter
// history but GetParameter has no version selector, so an explicit version
// resolves to the current value and says so in the log — failing the stack over
// it would break templates that pin a version they are already on, which is the
// overwhelmingly common case.
func (p *provisioner) resolveSSMRef(ctx context.Context, router http.Handler, region string, ref dynamicRef) (string, error) {
	name := ref.field(0)
	if name == "" {
		return "", fmt.Errorf("no parameter name")
	}
	if version := ref.field(1); version != "" {
		p.log.Warn("cfn: dynamic reference pins an SSM parameter version — resolving the current value instead",
			zap.String("parameter", name),
			zap.String("version", version))
	}
	rec, err := internalJSON(ctx, router, region, "AmazonSSM.GetParameter", map[string]any{
		"Name":           name,
		"WithDecryption": true,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Parameter struct {
			Value string `json:"Value"`
		} `json:"Parameter"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		return "", fmt.Errorf("decode GetParameter response: %w", err)
	}
	return out.Parameter.Value, nil
}
