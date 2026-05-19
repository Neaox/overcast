package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// Shield returns the Shield Advanced service group.
func Shield() ServiceGroup {
	g := &shieldCliGroup{}
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

type shieldCliGroup struct{}

func shieldResourceArn(runID string) string {
	return fmt.Sprintf("arn:aws:ec2:us-east-1:000000000000:eip/eipalloc-%s", runID)
}

func (g *shieldCliGroup) setupProtections(_ context.Context, _ *harness.TestContext) error {
	return nil
}
func (g *shieldCliGroup) teardownProtections(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("protection_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "shield", "delete-protection", "--protection-id", id) //nolint:errcheck
	}
	return nil
}

func (g *shieldCliGroup) DescribeSubscription(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "shield", "describe-subscription")
	return err
}

func (g *shieldCliGroup) CreateProtection(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "shield", "create-protection",
		"--name", name,
		"--resource-arn", shieldResourceArn(t.RunID),
	)
	if err != nil {
		return err
	}
	id, _ := out["ProtectionId"].(string)
	if id == "" {
		return fmt.Errorf("CreateProtection: missing ProtectionId")
	}
	t.Set("protection_id", id)
	return nil
}

func (g *shieldCliGroup) ListProtections(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "shield", "list-protections")
	return err
}

func (g *shieldCliGroup) DescribeProtection(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("protection_id")
	if id == "" {
		return fmt.Errorf("DescribeProtection: no protection_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"shield", "describe-protection",
		"--protection-id", id,
	)
	if err != nil {
		return err
	}
	prot, _ := out["Protection"].(map[string]any)
	if prot == nil || prot["Id"] != id {
		return fmt.Errorf("DescribeProtection: id mismatch")
	}
	return nil
}

func (g *shieldCliGroup) DeleteProtection(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("protection_id")
	if id == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "shield", "delete-protection",
		"--protection-id", id,
	)
}
