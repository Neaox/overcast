package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/shield"
)

func Shield(c *clients.Clients) ServiceGroup {
	g := &shieldGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"DescribeSubscription": g.DescribeSubscription,
			"CreateProtection":     g.CreateProtection,
			"ListProtections":      g.ListProtections,
			"DescribeProtection":   g.DescribeProtection,
			"DeleteProtection":     g.DeleteProtection,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"shield-protections": g.setupProtections,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"shield-protections": g.teardownProtections,
		},
	}
}

type shieldGroup struct{ c *clients.Clients }

func (g *shieldGroup) cl() *shield.Client { return g.c.Shield() }

func (g *shieldGroup) setupProtections(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *shieldGroup) teardownProtections(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("shield_protection_id"); id != "" {
		g.cl().DeleteProtection(ctx, &shield.DeleteProtectionInput{ProtectionId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

func (g *shieldGroup) DescribeSubscription(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DescribeSubscription(ctx, &shield.DescribeSubscriptionInput{})
	return err
}

func (g *shieldGroup) CreateProtection(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	resourceArn := fmt.Sprintf("arn:aws:ec2:us-east-1:000000000000:eip/eipalloc-%s", t.RunID)
	resp, err := g.cl().CreateProtection(ctx, &shield.CreateProtectionInput{
		Name:        aws.String(name),
		ResourceArn: aws.String(resourceArn),
	})
	if err != nil {
		return err
	}
	if resp.ProtectionId == nil {
		return fmt.Errorf("CreateProtection: missing ProtectionId")
	}
	t.Set("shield_protection_id", *resp.ProtectionId)
	return nil
}

func (g *shieldGroup) ListProtections(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListProtections(ctx, &shield.ListProtectionsInput{})
	return err
}

func (g *shieldGroup) DescribeProtection(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("shield_protection_id")
	if id == "" {
		return fmt.Errorf("DescribeProtection: no protection_id")
	}
	resp, err := g.cl().DescribeProtection(ctx, &shield.DescribeProtectionInput{
		ProtectionId: aws.String(id),
	})
	if err != nil {
		return err
	}
	if resp.Protection == nil || aws.ToString(resp.Protection.Id) != id {
		return fmt.Errorf("DescribeProtection: id mismatch")
	}
	return nil
}

func (g *shieldGroup) DeleteProtection(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("shield_protection_id")
	if id == "" {
		return nil
	}
	_, err := g.cl().DeleteProtection(ctx, &shield.DeleteProtectionInput{
		ProtectionId: aws.String(id),
	})
	return err
}
