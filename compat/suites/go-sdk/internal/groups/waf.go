package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	waftypes "github.com/aws/aws-sdk-go-v2/service/wafv2/types"
)

func WAF(c *clients.Clients) ServiceGroup {
	g := &wafGroup{c: c}
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

type wafGroup struct{ c *clients.Clients }

func (g *wafGroup) cl() *wafv2.Client { return g.c.WAFv2() }

func (g *wafGroup) setupACLs(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *wafGroup) teardownACLs(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("waf_acl_id")
	name := t.GetString("waf_acl_name")
	lockToken := t.GetString("waf_acl_lock_token")
	if id != "" && name != "" && lockToken != "" {
		g.cl().DeleteWebACL(ctx, &wafv2.DeleteWebACLInput{ //nolint:errcheck
			Id:        aws.String(id),
			Name:      aws.String(name),
			Scope:     waftypes.ScopeRegional,
			LockToken: aws.String(lockToken),
		})
	}
	return nil
}

func (g *wafGroup) CreateWebACL(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	metricName := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.cl().CreateWebACL(ctx, &wafv2.CreateWebACLInput{
		Name:          aws.String(name),
		Scope:         waftypes.ScopeRegional,
		DefaultAction: &waftypes.DefaultAction{Allow: &waftypes.AllowAction{}},
		VisibilityConfig: &waftypes.VisibilityConfig{
			SampledRequestsEnabled:   false,
			CloudWatchMetricsEnabled: false,
			MetricName:               aws.String(metricName),
		},
		Rules: []waftypes.Rule{},
	})
	if err != nil {
		return err
	}
	if resp.Summary == nil || resp.Summary.Id == nil {
		return fmt.Errorf("CreateWebACL: missing Id")
	}
	t.Set("waf_acl_id", *resp.Summary.Id)
	t.Set("waf_acl_name", name)
	if resp.Summary.LockToken != nil {
		t.Set("waf_acl_lock_token", *resp.Summary.LockToken)
	}
	return nil
}

func (g *wafGroup) GetWebACL(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("waf_acl_id")
	name := t.GetString("waf_acl_name")
	if id == "" || name == "" {
		return fmt.Errorf("GetWebACL: no ACL from CreateWebACL")
	}
	resp, err := g.cl().GetWebACL(ctx, &wafv2.GetWebACLInput{
		Id:    aws.String(id),
		Name:  aws.String(name),
		Scope: waftypes.ScopeRegional,
	})
	if err != nil {
		return err
	}
	if resp.WebACL == nil || resp.WebACL.Id == nil {
		return fmt.Errorf("GetWebACL: missing Id")
	}
	return nil
}

func (g *wafGroup) ListWebACLs(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListWebACLs(ctx, &wafv2.ListWebACLsInput{
		Scope: waftypes.ScopeRegional,
	})
	return err
}

func (g *wafGroup) DeleteWebACL(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("waf_acl_id")
	name := t.GetString("waf_acl_name")
	lockToken := t.GetString("waf_acl_lock_token")
	if id == "" || name == "" || lockToken == "" {
		return nil
	}
	_, err := g.cl().DeleteWebACL(ctx, &wafv2.DeleteWebACLInput{
		Id:        aws.String(id),
		Name:      aws.String(name),
		Scope:     waftypes.ScopeRegional,
		LockToken: aws.String(lockToken),
	})
	return err
}
