package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// SSM returns the SSM service group.
func SSM() ServiceGroup {
	g := &ssmGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// ssm-parameters
			"PutParameter":           g.PutParameter,
			"GetParameter":           g.GetParameter,
			"PutParameterOverwrite":  g.PutParameterOverwrite,
			"GetParameterHistory":    g.GetParameterHistory,
			"PutMultipleParameters":  g.PutMultipleParameters,
			"GetParameters":          g.GetParameters,
			"DescribeParameters":     g.DescribeParameters,
			"TagParameter":           g.TagParameter,
			"ListSSMTagsForResource": g.ListTagsForResource,
			"DeleteParameters":       g.DeleteParameters,
			// ssm-secure
			"PutSecureStringParameter":         g.PutSecureStringParameter,
			"GetSecureStringParameter":         g.GetSecureStringParameter,
			"GetSecureStringWithoutDecryption": g.GetSecureStringWithoutDecryption,
			// ssm-path
			"GetParametersByPath":          g.GetParametersByPath,
			"GetParametersByPathRecursive": g.GetParametersByPathRecursive,
			"GetParametersByPathPaginated": g.GetParametersByPathPaginated,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"ssm-parameters": g.setupParameters,
			"ssm-secure":     g.setupSecure,
			"ssm-path":       g.setupPath,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"ssm-parameters": g.teardownParameters,
			"ssm-secure":     g.teardownSecure,
			"ssm-path":       g.teardownPath,
		},
	}
}

type ssmGroup struct{}

func (g *ssmGroup) paramName(t *harness.TestContext) string {
	return fmt.Sprintf("/oc/%s/param1", t.RunID)
}
func (g *ssmGroup) secureName(t *harness.TestContext) string {
	return fmt.Sprintf("/oc/%s/secure", t.RunID)
}
func (g *ssmGroup) pathPrefix(t *harness.TestContext) string {
	return fmt.Sprintf("/oc/%s", t.RunID)
}

// ─── ssm-parameters ──────────────────────────────────────────────────────────

func (g *ssmGroup) setupParameters(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *ssmGroup) PutParameter(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"ssm", "put-parameter",
		"--name", g.paramName(t),
		"--value", "hello-world",
		"--type", "String",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameter",
		"--name", g.paramName(t),
	)
	if err != nil {
		return fmt.Errorf("ssm PutParameter: get-parameter failed: %w", err)
	}
	param, _ := out["Parameter"].(map[string]any)
	if param["Value"] != "hello-world" {
		return fmt.Errorf("ssm PutParameter: expected hello-world, got %v", param["Value"])
	}
	return nil
}

func (g *ssmGroup) GetParameter(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameter",
		"--name", g.paramName(t),
	)
	if err != nil {
		return err
	}
	param, _ := out["Parameter"].(map[string]any)
	if param == nil {
		return fmt.Errorf("ssm GetParameter: missing Parameter")
	}
	return nil
}

func (g *ssmGroup) PutParameterOverwrite(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"ssm", "put-parameter",
		"--name", g.paramName(t),
		"--value", "updated-value",
		"--type", "String",
		"--overwrite",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameter",
		"--name", g.paramName(t),
	)
	if err != nil {
		return fmt.Errorf("ssm PutParameterOverwrite: get-parameter failed: %w", err)
	}
	param, _ := out["Parameter"].(map[string]any)
	if param["Value"] != "updated-value" {
		return fmt.Errorf("ssm PutParameterOverwrite: expected Value=updated-value, got %v", param["Value"])
	}
	return nil
}

func (g *ssmGroup) GetParameterHistory(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameter-history",
		"--name", g.paramName(t),
	)
	if err != nil {
		return err
	}
	params, _ := out["Parameters"].([]any)
	if len(params) == 0 {
		return fmt.Errorf("ssm GetParameterHistory: no history entries")
	}
	return nil
}

func (g *ssmGroup) PutMultipleParameters(_ context.Context, t *harness.TestContext) error {
	for _, suffix := range []string{"p2", "p3"} {
		name := fmt.Sprintf("/oc/%s/%s", t.RunID, suffix)
		if err := awscli.Run(t.Endpoint, t.Region,
			"ssm", "put-parameter",
			"--name", name,
			"--value", fmt.Sprintf("val-%s", suffix),
			"--type", "String",
		); err != nil {
			return err
		}
	}
	return nil
}

func (g *ssmGroup) GetParameters(_ context.Context, t *harness.TestContext) error {
	p2 := fmt.Sprintf("/oc/%s/p2", t.RunID)
	p3 := fmt.Sprintf("/oc/%s/p3", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameters",
		"--names", p2, p3,
	)
	if err != nil {
		return err
	}
	params, _ := out["Parameters"].([]any)
	if len(params) == 0 {
		return fmt.Errorf("ssm GetParameters: no parameters returned")
	}
	return nil
}

func (g *ssmGroup) DescribeParameters(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ssm", "describe-parameters")
	if err != nil {
		return err
	}
	params, _ := out["Parameters"].([]any)
	if len(params) == 0 {
		return fmt.Errorf("ssm DescribeParameters: no parameters returned")
	}
	return nil
}

func (g *ssmGroup) TagParameter(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"ssm", "add-tags-to-resource",
		"--resource-type", "Parameter",
		"--resource-id", g.paramName(t),
		"--tags", `[{"Key":"env","Value":"test"}]`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "list-tags-for-resource",
		"--resource-type", "Parameter",
		"--resource-id", g.paramName(t),
	)
	if err != nil {
		return fmt.Errorf("ssm TagParameter: list-tags failed: %w", err)
	}
	tags, _ := out["TagList"].([]any)
	for _, raw := range tags {
		if m, ok := raw.(map[string]any); ok && m["Key"] == "env" && m["Value"] == "test" {
			return nil
		}
	}
	return fmt.Errorf("ssm TagParameter: tag env=test not found")
}

func (g *ssmGroup) ListTagsForResource(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "list-tags-for-resource",
		"--resource-type", "Parameter",
		"--resource-id", g.paramName(t),
	)
	if err != nil {
		return err
	}
	tags, _ := out["TagList"].([]any)
	if len(tags) == 0 {
		return fmt.Errorf("ssm ListTagsForResource: no tags returned")
	}
	return nil
}

func (g *ssmGroup) DeleteParameters(_ context.Context, t *harness.TestContext) error {
	names := []string{
		g.paramName(t),
		fmt.Sprintf("/oc/%s/p2", t.RunID),
		fmt.Sprintf("/oc/%s/p3", t.RunID),
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "delete-parameters",
		"--names", names[0], names[1], names[2],
	)
	if err != nil {
		return err
	}
	deleted, _ := out["DeletedParameters"].([]any)
	if len(deleted) == 0 {
		return fmt.Errorf("ssm DeleteParameters: no DeletedParameters returned")
	}
	return nil
}

func (g *ssmGroup) teardownParameters(_ context.Context, t *harness.TestContext) error {
	for _, suffix := range []string{"param1", "p2", "p3"} {
		awscli.Run(t.Endpoint, t.Region, "ssm", "delete-parameter", //nolint:errcheck
			"--name", fmt.Sprintf("/oc/%s/%s", t.RunID, suffix))
	}
	return nil
}

// ─── ssm-secure ──────────────────────────────────────────────────────────────

func (g *ssmGroup) setupSecure(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *ssmGroup) PutSecureStringParameter(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"ssm", "put-parameter",
		"--name", g.secureName(t),
		"--value", "top-secret",
		"--type", "SecureString",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameter",
		"--name", g.secureName(t),
		"--with-decryption",
	)
	if err != nil {
		return fmt.Errorf("ssm PutSecureStringParameter: get-parameter failed: %w", err)
	}
	param, _ := out["Parameter"].(map[string]any)
	if param["Type"] != "SecureString" {
		return fmt.Errorf("ssm PutSecureStringParameter: expected Type=SecureString, got %v", param["Type"])
	}
	return nil
}

func (g *ssmGroup) GetSecureStringParameter(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameter",
		"--name", g.secureName(t),
		"--with-decryption",
	)
	if err != nil {
		return err
	}
	param, _ := out["Parameter"].(map[string]any)
	if param == nil {
		return fmt.Errorf("ssm GetSecureStringParameter: missing Parameter")
	}
	return nil
}

func (g *ssmGroup) GetSecureStringWithoutDecryption(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameter",
		"--name", g.secureName(t),
	)
	if err != nil {
		return err
	}
	param, _ := out["Parameter"].(map[string]any)
	if param["Type"] != "SecureString" {
		return fmt.Errorf("ssm GetSecureStringWithoutDecryption: expected Type=SecureString, got %v", param["Type"])
	}
	return nil
}

func (g *ssmGroup) teardownSecure(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "ssm", "delete-parameter", "--name", g.secureName(t)) //nolint:errcheck
	return nil
}

// ─── ssm-path ────────────────────────────────────────────────────────────────

func (g *ssmGroup) setupPath(_ context.Context, t *harness.TestContext) error {
	// Seed parameters under a common path prefix.
	for _, suffix := range []string{"a/p1", "a/p2", "b/p1"} {
		name := fmt.Sprintf("%s/%s", g.pathPrefix(t), suffix)
		if err := awscli.Run(t.Endpoint, t.Region,
			"ssm", "put-parameter",
			"--name", name,
			"--value", suffix,
			"--type", "String",
		); err != nil {
			return err
		}
	}
	return nil
}

func (g *ssmGroup) GetParametersByPath(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameters-by-path",
		"--path", g.pathPrefix(t)+"/a",
	)
	if err != nil {
		return err
	}
	params, _ := out["Parameters"].([]any)
	if len(params) == 0 {
		return fmt.Errorf("ssm GetParametersByPath: no parameters returned")
	}
	return nil
}

func (g *ssmGroup) GetParametersByPathRecursive(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameters-by-path",
		"--path", g.pathPrefix(t),
		"--recursive",
	)
	if err != nil {
		return err
	}
	params, _ := out["Parameters"].([]any)
	if len(params) == 0 {
		return fmt.Errorf("ssm GetParametersByPathRecursive: no parameters returned")
	}
	return nil
}

func (g *ssmGroup) GetParametersByPathPaginated(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"ssm", "get-parameters-by-path",
		"--path", g.pathPrefix(t),
		"--recursive",
		"--max-results", "2",
	)
	if err != nil {
		return err
	}
	params, _ := out["Parameters"].([]any)
	if len(params) == 0 {
		return fmt.Errorf("ssm GetParametersByPathPaginated: no parameters returned")
	}
	return nil
}

func (g *ssmGroup) teardownPath(_ context.Context, t *harness.TestContext) error {
	for _, suffix := range []string{"a/p1", "a/p2", "b/p1"} {
		awscli.Run(t.Endpoint, t.Region, "ssm", "delete-parameter", //nolint:errcheck
			"--name", fmt.Sprintf("%s/%s", g.pathPrefix(t), suffix))
	}
	return nil
}
