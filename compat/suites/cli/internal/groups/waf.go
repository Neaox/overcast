package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// WAF returns the WAFv2 service group.
func WAF() ServiceGroup {
	g := &wafCliGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateWebACL": g.CreateWebACL,
			"GetWebACL":    g.GetWebACL,
			"ListWebACLs":  g.ListWebACLs,
			"DeleteWebACL": g.DeleteWebACL,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"waf-webacls": g.setupACLs,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"waf-webacls": g.teardownACLs,
		},
	}
}

type wafCliGroup struct{}

const wafVisibilityConfig = `{"SampledRequestsEnabled":false,"CloudWatchMetricsEnabled":false,"MetricName":"compat"}`

func (g *wafCliGroup) setupACLs(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *wafCliGroup) teardownACLs(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("acl_id")
	name := t.GetString("acl_name")
	token := t.GetString("acl_token")
	if id != "" && name != "" && token != "" {
		awscli.Run(t.Endpoint, t.Region, "wafv2", "delete-web-acl", //nolint:errcheck
			"--id", id,
			"--name", name,
			"--scope", "REGIONAL",
			"--lock-token", token,
		)
	}
	return nil
}

func (g *wafCliGroup) CreateWebACL(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "wafv2", "create-web-acl",
		"--name", name,
		"--scope", "REGIONAL",
		"--default-action", `{"Allow":{}}`,
		"--visibility-config", wafVisibilityConfig,
	)
	if err != nil {
		return err
	}
	summary, _ := out["Summary"].(map[string]interface{})
	id, _ := summary["Id"].(string)
	if id == "" {
		return fmt.Errorf("CreateWebACL: missing Id")
	}
	t.Set("acl_id", id)
	t.Set("acl_name", name)
	t.Set("acl_token", summary["LockToken"])
	return nil
}

func (g *wafCliGroup) GetWebACL(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("acl_id")
	name := t.GetString("acl_name")
	if id == "" || name == "" {
		return fmt.Errorf("GetWebACL: no acl_id/acl_name from CreateWebACL")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "wafv2", "get-web-acl",
		"--id", id,
		"--name", name,
		"--scope", "REGIONAL",
	)
	if err != nil {
		return err
	}
	if out["WebACL"] == nil {
		return fmt.Errorf("GetWebACL: missing WebACL")
	}
	// Refresh lock token
	t.Set("acl_token", out["LockToken"])
	return nil
}

func (g *wafCliGroup) ListWebACLs(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "wafv2", "list-web-acls", "--scope", "REGIONAL")
	return err
}

func (g *wafCliGroup) DeleteWebACL(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("acl_id")
	name := t.GetString("acl_name")
	token := t.GetString("acl_token")
	if id == "" || name == "" || token == "" {
		return fmt.Errorf("DeleteWebACL: missing acl_id, acl_name, or acl_token")
	}
	return awscli.Run(t.Endpoint, t.Region, "wafv2", "delete-web-acl",
		"--id", id,
		"--name", name,
		"--scope", "REGIONAL",
		"--lock-token", token,
	)
}
