package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	apigwv2 "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

func APIGateway(c *clients.Clients) ServiceGroup {
	g := &apigwGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateRestApi": g.CreateRestApi,
			"GetRestApis":   g.GetRestApis,
			"DeleteRestApi": g.DeleteRestApi,
			"CreateApi":     g.CreateApi,
			"GetApis":       g.GetApis,
			"DeleteApi":     g.DeleteApi,
			// Group-qualified: CreateApiKey and GetUsagePlan are generic
			// enough that another service's group could claim the bare name.
			"apigateway-usage-plans:CreateUsagePlanWithLimits": g.CreateUsagePlanWithLimits,
			"apigateway-usage-plans:GetUsagePlan":              g.GetUsagePlan,
			"apigateway-usage-plans:CreateApiKey":              g.CreateAPIKey,
			"apigateway-usage-plans:CreateUsagePlanKey":        g.CreateUsagePlanKey,
			"apigateway-usage-plans:GetUsage":                  g.GetUsage,
			"apigateway-usage-plans:DeleteUsagePlan":           g.DeleteUsagePlan,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"apigateway-rest": g.setupRestApi,
			"apigateway-http": g.setupHttpApi,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"apigateway-rest":        g.teardownRestApi,
			"apigateway-http":        g.teardownHttpApi,
			"apigateway-usage-plans": g.teardownUsagePlan,
		},
	}
}

type apigwGroup struct{ c *clients.Clients }

func (g *apigwGroup) setupRestApi(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *apigwGroup) setupHttpApi(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *apigwGroup) teardownRestApi(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("apigw_rest_api_id"); id != "" {
		g.c.APIGateway().DeleteRestApi(ctx, &apigateway.DeleteRestApiInput{RestApiId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

func (g *apigwGroup) teardownHttpApi(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("apigw_http_api_id"); id != "" {
		g.c.APIGatewayV2().DeleteApi(ctx, &apigwv2.DeleteApiInput{ApiId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

func (g *apigwGroup) CreateRestApi(ctx context.Context, t *harness.TestContext) error {
	apiName := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.c.APIGateway().CreateRestApi(ctx, &apigateway.CreateRestApiInput{
		Name: aws.String(apiName),
	})
	if err != nil {
		return err
	}
	if resp.Id == nil {
		return fmt.Errorf("CreateRestApi: missing id")
	}
	t.Set("apigw_rest_api_id", *resp.Id)
	return nil
}

func (g *apigwGroup) GetRestApis(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.c.APIGateway().GetRestApis(ctx, &apigateway.GetRestApisInput{})
	if err != nil {
		return err
	}
	if resp.Items == nil {
		return fmt.Errorf("GetRestApis: missing items")
	}
	return nil
}

func (g *apigwGroup) DeleteRestApi(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("apigw_rest_api_id")
	if apiID == "" {
		return nil
	}
	_, err := g.c.APIGateway().DeleteRestApi(ctx, &apigateway.DeleteRestApiInput{
		RestApiId: aws.String(apiID),
	})
	return err
}

func (g *apigwGroup) CreateApi(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.c.APIGatewayV2().CreateApi(ctx, &apigwv2.CreateApiInput{
		Name:         aws.String(name),
		ProtocolType: apigwv2types.ProtocolTypeHttp,
	})
	if err != nil {
		return err
	}
	if resp.ApiId == nil {
		return fmt.Errorf("CreateApi: missing ApiId")
	}
	t.Set("apigw_http_api_id", *resp.ApiId)
	return nil
}

func (g *apigwGroup) GetApis(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.c.APIGatewayV2().GetApis(ctx, &apigwv2.GetApisInput{})
	if err != nil {
		return err
	}
	if resp.Items == nil {
		return fmt.Errorf("GetApis: missing Items")
	}
	return nil
}

func (g *apigwGroup) DeleteApi(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("apigw_http_api_id")
	if apiID == "" {
		return nil
	}
	_, err := g.c.APIGatewayV2().DeleteApi(ctx, &apigwv2.DeleteApiInput{
		ApiId: aws.String(apiID),
	})
	return err
}

// ---- apigateway-usage-plans ----------------------------------------------

func (g *apigwGroup) teardownUsagePlan(ctx context.Context, t *harness.TestContext) error {
	if planID := t.GetString("apigw_usage_plan_id"); planID != "" {
		g.c.APIGateway().DeleteUsagePlan(ctx, &apigateway.DeleteUsagePlanInput{UsagePlanId: aws.String(planID)}) //nolint:errcheck
	}
	if keyID := t.GetString("apigw_usage_key_id"); keyID != "" {
		g.c.APIGateway().DeleteApiKey(ctx, &apigateway.DeleteApiKeyInput{ApiKey: aws.String(keyID)}) //nolint:errcheck
	}
	return nil
}

func (g *apigwGroup) CreateUsagePlanWithLimits(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-%s-usage-plan", t.RunID)
	resp, err := g.c.APIGateway().CreateUsagePlan(ctx, &apigateway.CreateUsagePlanInput{
		Name:     aws.String(name),
		Throttle: &apigwtypes.ThrottleSettings{RateLimit: 10, BurstLimit: 20},
		Quota:    &apigwtypes.QuotaSettings{Limit: 1000, Period: apigwtypes.QuotaPeriodTypeDay},
	})
	if err != nil {
		return err
	}
	if resp.Id == nil {
		return fmt.Errorf("CreateUsagePlan: missing id")
	}
	if aws.ToString(resp.Name) != name {
		return fmt.Errorf("CreateUsagePlan: name %q != %q", aws.ToString(resp.Name), name)
	}
	t.Set("apigw_usage_plan_id", *resp.Id)
	return nil
}

func (g *apigwGroup) GetUsagePlan(ctx context.Context, t *harness.TestContext) error {
	planID := t.GetString("apigw_usage_plan_id")
	resp, err := g.c.APIGateway().GetUsagePlan(ctx, &apigateway.GetUsagePlanInput{
		UsagePlanId: aws.String(planID),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.Id) != planID {
		return fmt.Errorf("GetUsagePlan: wrong id %q", aws.ToString(resp.Id))
	}
	if resp.Throttle == nil || resp.Throttle.RateLimit != 10 {
		return fmt.Errorf("GetUsagePlan: rateLimit not round-tripped: %+v", resp.Throttle)
	}
	if resp.Throttle.BurstLimit != 20 {
		return fmt.Errorf("GetUsagePlan: burstLimit not round-tripped: %+v", resp.Throttle)
	}
	if resp.Quota == nil || resp.Quota.Limit != 1000 {
		return fmt.Errorf("GetUsagePlan: quota limit not round-tripped: %+v", resp.Quota)
	}
	if resp.Quota.Period != apigwtypes.QuotaPeriodTypeDay {
		return fmt.Errorf("GetUsagePlan: quota period %q != DAY", resp.Quota.Period)
	}
	return nil
}

func (g *apigwGroup) CreateAPIKey(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-%s-usage-key", t.RunID)
	resp, err := g.c.APIGateway().CreateApiKey(ctx, &apigateway.CreateApiKeyInput{
		Name:    aws.String(name),
		Enabled: true,
	})
	if err != nil {
		return err
	}
	if resp.Id == nil {
		return fmt.Errorf("CreateApiKey: missing id")
	}
	if aws.ToString(resp.Name) != name {
		return fmt.Errorf("CreateApiKey: name %q != %q", aws.ToString(resp.Name), name)
	}
	t.Set("apigw_usage_key_id", *resp.Id)
	return nil
}

func (g *apigwGroup) CreateUsagePlanKey(ctx context.Context, t *harness.TestContext) error {
	planID := t.GetString("apigw_usage_plan_id")
	keyID := t.GetString("apigw_usage_key_id")
	if _, err := g.c.APIGateway().CreateUsagePlanKey(ctx, &apigateway.CreateUsagePlanKeyInput{
		UsagePlanId: aws.String(planID),
		KeyId:       aws.String(keyID),
		KeyType:     aws.String("API_KEY"),
	}); err != nil {
		return err
	}
	listed, err := g.c.APIGateway().GetUsagePlanKeys(ctx, &apigateway.GetUsagePlanKeysInput{
		UsagePlanId: aws.String(planID),
	})
	if err != nil {
		return err
	}
	for _, k := range listed.Items {
		if aws.ToString(k.Id) == keyID {
			return nil
		}
	}
	return fmt.Errorf("GetUsagePlanKeys: attached key %q not listed", keyID)
}

func (g *apigwGroup) GetUsage(ctx context.Context, t *harness.TestContext) error {
	planID := t.GetString("apigw_usage_plan_id")
	keyID := t.GetString("apigw_usage_key_id")
	today := time.Now().UTC().Format("2006-01-02")
	resp, err := g.c.APIGateway().GetUsage(ctx, &apigateway.GetUsageInput{
		UsagePlanId: aws.String(planID),
		StartDate:   aws.String(today),
		EndDate:     aws.String(today),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.UsagePlanId) != planID {
		return fmt.Errorf("GetUsage: wrong usagePlanId %q", aws.ToString(resp.UsagePlanId))
	}
	if aws.ToString(resp.StartDate) != today {
		return fmt.Errorf("GetUsage: startDate %q not echoed", aws.ToString(resp.StartDate))
	}
	days, ok := resp.Items[keyID]
	if !ok {
		return fmt.Errorf("GetUsage: no daily log for key %q", keyID)
	}
	if len(days) != 1 {
		return fmt.Errorf("GetUsage: expected one day of data, got %d", len(days))
	}
	if len(days[0]) != 2 {
		return fmt.Errorf("GetUsage: expected a [used, remaining] pair, got %v", days[0])
	}
	return nil
}

func (g *apigwGroup) DeleteUsagePlan(ctx context.Context, t *harness.TestContext) error {
	planID := t.GetString("apigw_usage_plan_id")
	keyID := t.GetString("apigw_usage_key_id")
	// AWS refuses to delete a plan that still has keys attached.
	if _, err := g.c.APIGateway().DeleteUsagePlanKey(ctx, &apigateway.DeleteUsagePlanKeyInput{
		UsagePlanId: aws.String(planID),
		KeyId:       aws.String(keyID),
	}); err != nil {
		return err
	}
	if _, err := g.c.APIGateway().DeleteUsagePlan(ctx, &apigateway.DeleteUsagePlanInput{
		UsagePlanId: aws.String(planID),
	}); err != nil {
		return err
	}
	if _, err := g.c.APIGateway().GetUsagePlan(ctx, &apigateway.GetUsagePlanInput{
		UsagePlanId: aws.String(planID),
	}); err == nil {
		return fmt.Errorf("GetUsagePlan: deleted plan %q still readable", planID)
	}
	return nil
}
