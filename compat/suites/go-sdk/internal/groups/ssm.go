package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func SSM(c *clients.Clients) ServiceGroup {
	g := &ssmGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"PutParameter":                     g.PutParameter,
			"GetParameter":                     g.GetParameter,
			"PutParameterOverwrite":            g.PutParameterOverwrite,
			"GetParameterHistory":              g.GetParameterHistory,
			"PutMultipleParameters":            g.PutMultipleParameters,
			"GetParameters":                    g.GetParameters,
			"DescribeParameters":               g.DescribeParameters,
			"TagParameter":                     g.TagParameter,
			"ListSSMTagsForResource":           g.ListTagsForResource,
			"DeleteParameters":                 g.DeleteParameters,
			"GetParametersByPath":              g.GetParametersByPath,
			"GetParametersByPathRecursive":     g.GetParametersByPathRecursive,
			"GetParametersByPathPaginated":     g.GetParametersByPathPaginated,
			"PutSecureStringParameter":         g.PutSecureStringParameter,
			"GetSecureStringParameter":         g.GetSecureStringParameter,
			"GetSecureStringWithoutDecryption": g.GetSecureStringWithoutDecryption,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"ssm-parameters": g.setupParameters,
			"ssm-path":       g.setupPath,
			"ssm-secure":     g.setupSecure,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"ssm-parameters": g.teardownParameters,
			"ssm-path":       g.teardownPath,
			"ssm-secure":     g.teardownSecure,
		},
	}
}

type ssmGroup struct{ c *clients.Clients }

func (g *ssmGroup) cl() *ssm.Client { return g.c.SSM() }

// ── ssm-parameters ────────────────────────────────────────────────────────────

func (g *ssmGroup) setupParameters(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("/oc/%s/param", t.RunID)
	if _, err := g.cl().PutParameter(ctx, &ssm.PutParameterInput{
		Name:  aws.String(name),
		Value: aws.String("initial"),
		Type:  types.ParameterTypeString,
	}); err != nil {
		return err
	}
	t.Set("ssm_param", name)
	return nil
}

func (g *ssmGroup) teardownParameters(ctx context.Context, t *harness.TestContext) error {
	names := []string{}
	if name := t.GetString("ssm_param"); name != "" {
		names = append(names, name)
	}
	if extras, ok := t.Get("ssm_extra_params"); ok {
		names = append(names, extras.([]string)...)
	}
	if len(names) > 0 {
		g.cl().DeleteParameters(ctx, &ssm.DeleteParametersInput{Names: names}) //nolint:errcheck
	}
	return nil
}

func (g *ssmGroup) PutParameter(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("/oc/%s/put", t.RunID)
	if _, err := g.cl().PutParameter(ctx, &ssm.PutParameterInput{
		Name: aws.String(name), Value: aws.String("value"), Type: types.ParameterTypeString,
	}); err != nil {
		return err
	}
	g.cl().DeleteParameters(ctx, &ssm.DeleteParametersInput{Names: []string{name}}) //nolint:errcheck
	return nil
}

func (g *ssmGroup) GetParameter(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(t.GetString("ssm_param")),
	})
	if err != nil {
		return err
	}
	if resp.Parameter == nil {
		return fmt.Errorf("GetParameter: nil parameter")
	}
	return nil
}

func (g *ssmGroup) PutParameterOverwrite(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(t.GetString("ssm_param")),
		Value:     aws.String("overwritten"),
		Type:      types.ParameterTypeString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(t.GetString("ssm_param")),
	})
	if err != nil {
		return fmt.Errorf("PutParameterOverwrite: GetParameter verify failed: %w", err)
	}
	if aws.ToString(resp.Parameter.Value) != "overwritten" {
		return fmt.Errorf("PutParameterOverwrite: expected value %q, got %q", "overwritten", aws.ToString(resp.Parameter.Value))
	}
	return nil
}

func (g *ssmGroup) GetParameterHistory(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetParameterHistory(ctx, &ssm.GetParameterHistoryInput{
		Name: aws.String(t.GetString("ssm_param")),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	if len(resp.Parameters) == 0 {
		return fmt.Errorf("GetParameterHistory: expected ≥1 history entry")
	}
	return nil
}

func (g *ssmGroup) PutMultipleParameters(ctx context.Context, t *harness.TestContext) error {
	extras := []string{
		fmt.Sprintf("/oc/%s/multi-a", t.RunID),
		fmt.Sprintf("/oc/%s/multi-b", t.RunID),
	}
	for i, name := range extras {
		if _, err := g.cl().PutParameter(ctx, &ssm.PutParameterInput{
			Name: aws.String(name), Value: aws.String(fmt.Sprintf("val-%d", i)), Type: types.ParameterTypeString,
		}); err != nil {
			return err
		}
	}
	t.Set("ssm_extra_params", extras)
	resp, err := g.cl().GetParameters(ctx, &ssm.GetParametersInput{Names: extras})
	if err != nil {
		return fmt.Errorf("PutMultipleParameters: GetParameters verify failed: %w", err)
	}
	if len(resp.Parameters) != 2 {
		return fmt.Errorf("PutMultipleParameters: expected 2 params, got %d", len(resp.Parameters))
	}
	return nil
}

func (g *ssmGroup) GetParameters(ctx context.Context, t *harness.TestContext) error {
	extras, ok := t.Get("ssm_extra_params")
	if !ok {
		return nil
	}
	resp, err := g.cl().GetParameters(ctx, &ssm.GetParametersInput{
		Names: extras.([]string),
	})
	if err != nil {
		return err
	}
	if len(resp.Parameters) == 0 {
		return fmt.Errorf("GetParameters: no parameters returned")
	}
	return nil
}

func (g *ssmGroup) DescribeParameters(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeParameters(ctx, &ssm.DescribeParametersInput{})
	if err != nil {
		return err
	}
	if len(resp.Parameters) == 0 {
		return fmt.Errorf("DescribeParameters: expected ≥1 parameter")
	}
	return nil
}

func (g *ssmGroup) TagParameter(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().AddTagsToResource(ctx, &ssm.AddTagsToResourceInput{
		ResourceType: types.ResourceTypeForTaggingParameter,
		ResourceId:   aws.String(t.GetString("ssm_param")),
		Tags:         []types.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().ListTagsForResource(ctx, &ssm.ListTagsForResourceInput{
		ResourceType: types.ResourceTypeForTaggingParameter,
		ResourceId:   aws.String(t.GetString("ssm_param")),
	})
	if err != nil {
		return fmt.Errorf("TagParameter: ListTagsForResource verify failed: %w", err)
	}
	for _, tag := range resp.TagList {
		if aws.ToString(tag.Key) == "env" && aws.ToString(tag.Value) == "test" {
			return nil
		}
	}
	return fmt.Errorf("TagParameter: env=test tag not found")
}

func (g *ssmGroup) ListTagsForResource(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListTagsForResource(ctx, &ssm.ListTagsForResourceInput{
		ResourceType: types.ResourceTypeForTaggingParameter,
		ResourceId:   aws.String(t.GetString("ssm_param")),
	})
	if err != nil {
		return err
	}
	for _, tag := range resp.TagList {
		if aws.ToString(tag.Key) == "env" {
			return nil
		}
	}
	return fmt.Errorf("ListTagsForResource: env tag not found")
}

func (g *ssmGroup) DeleteParameters(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("/oc/%s/del", t.RunID)
	g.cl().PutParameter(ctx, &ssm.PutParameterInput{
		Name: aws.String(name), Value: aws.String("v"), Type: types.ParameterTypeString,
	}) //nolint:errcheck
	_, err := g.cl().DeleteParameters(ctx, &ssm.DeleteParametersInput{Names: []string{name}})
	if err != nil {
		return err
	}
	// Verify parameter is gone
	_, gErr := g.cl().GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name)})
	if gErr == nil {
		return fmt.Errorf("DeleteParameters: parameter %q still present", name)
	}
	return nil
}

// ── ssm-path ──────────────────────────────────────────────────────────────────

func (g *ssmGroup) setupPath(ctx context.Context, t *harness.TestContext) error {
	prefix := fmt.Sprintf("/oc/%s/app", t.RunID)
	for _, suffix := range []string{"/db/host", "/db/port", "/api/key"} {
		if _, err := g.cl().PutParameter(ctx, &ssm.PutParameterInput{
			Name: aws.String(prefix + suffix), Value: aws.String("value"), Type: types.ParameterTypeString,
		}); err != nil {
			return err
		}
	}
	t.Set("ssm_prefix", prefix)
	return nil
}

func (g *ssmGroup) teardownPath(ctx context.Context, t *harness.TestContext) error {
	prefix := t.GetString("ssm_prefix")
	if prefix == "" {
		return nil
	}
	for _, suffix := range []string{"/db/host", "/db/port", "/api/key"} {
		g.cl().DeleteParameters(ctx, &ssm.DeleteParametersInput{Names: []string{prefix + suffix}}) //nolint:errcheck
	}
	return nil
}

func (g *ssmGroup) GetParametersByPath(ctx context.Context, t *harness.TestContext) error {
	prefix := t.GetString("ssm_prefix")
	resp, err := g.cl().GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
		Path:      aws.String(prefix),
		Recursive: aws.Bool(true),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	if len(resp.Parameters) == 0 {
		return fmt.Errorf("GetParametersByPath: no parameters returned for %q", prefix)
	}
	return nil
}

func (g *ssmGroup) GetParametersByPathRecursive(ctx context.Context, t *harness.TestContext) error {
	prefix := fmt.Sprintf("/oc/%s/app", t.RunID)
	resp, err := g.cl().GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
		Path:      aws.String(prefix),
		Recursive: aws.Bool(true),
	})
	if err != nil {
		return err
	}
	if len(resp.Parameters) < 3 {
		return fmt.Errorf("GetParametersByPathRecursive: expected ≥3 parameters, got %d", len(resp.Parameters))
	}
	return nil
}

func (g *ssmGroup) GetParametersByPathPaginated(ctx context.Context, t *harness.TestContext) error {
	prefix := fmt.Sprintf("/oc/%s/app/db", t.RunID)
	resp, err := g.cl().GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
		Path:       aws.String(prefix),
		Recursive:  aws.Bool(false),
		MaxResults: aws.Int32(2),
	})
	if err != nil {
		return err
	}
	if len(resp.Parameters) == 0 {
		return fmt.Errorf("GetParametersByPathPaginated: no parameters returned")
	}
	return nil
}

func (g *ssmGroup) SecureStringPut(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("/oc/%s/secure", t.RunID)
	if _, err := g.cl().PutParameter(ctx, &ssm.PutParameterInput{
		Name: aws.String(name), Value: aws.String("secret-value"), Type: types.ParameterTypeSecureString,
	}); err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	t.Set("ssm_secure_param", name)
	return nil
}

func (g *ssmGroup) SecureStringGet(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ssm_secure_param")
	if name == "" {
		return nil
	}
	resp, err := g.cl().GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	if resp.Parameter == nil {
		return fmt.Errorf("SecureStringGet: nil parameter")
	}
	g.cl().DeleteParameters(ctx, &ssm.DeleteParametersInput{Names: []string{name}}) //nolint:errcheck
	return nil
}

// ── ssm-secure ────────────────────────────────────────────────────────────────

func (g *ssmGroup) setupSecure(ctx context.Context, t *harness.TestContext) error {
	// No pre-created state needed; PutSecureStringParameter creates the param.
	return nil
}

func (g *ssmGroup) teardownSecure(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("ssm_secure_secret"); name != "" {
		g.cl().DeleteParameters(ctx, &ssm.DeleteParametersInput{Names: []string{name}}) //nolint:errcheck
	}
	return nil
}

func (g *ssmGroup) PutSecureStringParameter(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("/oc-%s/secure/secret", t.RunID)
	_, err := g.cl().PutParameter(ctx, &ssm.PutParameterInput{
		Name:  aws.String(name),
		Value: aws.String("s3cret"),
		Type:  types.ParameterTypeSecureString,
	})
	if err != nil {
		return err
	}
	t.Set("ssm_secure_secret", name)
	return nil
}

func (g *ssmGroup) GetSecureStringParameter(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ssm_secure_secret")
	if name == "" {
		return fmt.Errorf("GetSecureStringParameter: no secure param created")
	}
	resp, err := g.cl().GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return err
	}
	if resp.Parameter == nil || aws.ToString(resp.Parameter.Value) != "s3cret" {
		return fmt.Errorf("GetSecureStringParameter: expected value %q, got %q", "s3cret", aws.ToString(resp.Parameter.Value))
	}
	return nil
}

func (g *ssmGroup) GetSecureStringWithoutDecryption(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ssm_secure_secret")
	if name == "" {
		return fmt.Errorf("GetSecureStringWithoutDecryption: no secure param created")
	}
	resp, err := g.cl().GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(false),
	})
	if err != nil {
		return err
	}
	if resp.Parameter == nil {
		return fmt.Errorf("GetSecureStringWithoutDecryption: nil parameter")
	}
	if aws.ToString(resp.Parameter.Value) == "s3cret" {
		return fmt.Errorf("GetSecureStringWithoutDecryption: value should be encrypted/masked but got plaintext")
	}
	return nil
}
