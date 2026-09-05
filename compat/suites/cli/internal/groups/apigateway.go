package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
)

// APIGateway returns the API Gateway service group (REST v1 + HTTP v2).
func APIGateway() ServiceGroup {
	g := &apigwCliGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"apigateway-rest:CreateRestApi": g.CreateRestApi,
			"apigateway-rest:GetRestApis":   g.GetRestApis,
			"apigateway-rest:DeleteRestApi": g.DeleteRestApi,
			"apigateway-http:CreateApi":     g.CreateApi,
			"apigateway-http:GetApis":       g.GetApis,
			"apigateway-http:DeleteApi":     g.DeleteApi,
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
			"apigateway-rest": g.setupRest,
			"apigateway-http": g.setupHttp,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"apigateway-rest":        g.teardownRest,
			"apigateway-http":        g.teardownHttp,
			"apigateway-usage-plans": g.teardownUsagePlan,
		},
	}
}

type apigwCliGroup struct{}

func (g *apigwCliGroup) setupRest(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *apigwCliGroup) teardownRest(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("rest_api_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "apigateway", "delete-rest-api", "--rest-api-id", id) //nolint:errcheck
	}
	return nil
}

func (g *apigwCliGroup) setupHttp(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *apigwCliGroup) teardownHttp(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("http_api_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "apigatewayv2", "delete-api", "--api-id", id) //nolint:errcheck
	}
	return nil
}

func (g *apigwCliGroup) CreateRestApi(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "create-rest-api",
		"--name", name,
	)
	if err != nil {
		return err
	}
	id, _ := out["id"].(string)
	if id == "" {
		return fmt.Errorf("CreateRestApi: missing id")
	}
	t.Set("rest_api_id", id)
	return nil
}

func (g *apigwCliGroup) GetRestApis(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "get-rest-apis")
	return err
}

func (g *apigwCliGroup) DeleteRestApi(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-del-%s", t.RunID)
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "create-rest-api", "--name", name)
	id, _ := out["id"].(string)
	if id == "" {
		return fmt.Errorf("DeleteRestApi: could not create REST API to delete")
	}
	return awscli.Run(t.Endpoint, t.Region, "apigateway", "delete-rest-api", "--rest-api-id", id)
}

func (g *apigwCliGroup) CreateApi(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "apigatewayv2", "create-api",
		"--name", name,
		"--protocol-type", "HTTP",
	)
	if err != nil {
		return err
	}
	id, _ := out["ApiId"].(string)
	if id == "" {
		return fmt.Errorf("CreateApi: missing ApiId")
	}
	t.Set("http_api_id", id)
	return nil
}

func (g *apigwCliGroup) GetApis(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "apigatewayv2", "get-apis")
	return err
}

func (g *apigwCliGroup) DeleteApi(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-del-%s", t.RunID)
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "apigatewayv2", "create-api",
		"--name", name, "--protocol-type", "HTTP",
	)
	id, _ := out["ApiId"].(string)
	if id == "" {
		return fmt.Errorf("DeleteApi: could not create API to delete")
	}
	return awscli.Run(t.Endpoint, t.Region, "apigatewayv2", "delete-api", "--api-id", id)
}

// ---- apigateway-usage-plans ----------------------------------------------

func (g *apigwCliGroup) teardownUsagePlan(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("usage_plan_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "apigateway", "delete-usage-plan", "--usage-plan-id", id) //nolint:errcheck
	}
	if id := t.GetString("usage_key_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "apigateway", "delete-api-key", "--api-key", id) //nolint:errcheck
	}
	return nil
}

func (g *apigwCliGroup) CreateUsagePlanWithLimits(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-%s-usage-plan", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "create-usage-plan",
		"--name", name,
		"--throttle", "rateLimit=10,burstLimit=20",
		"--quota", "limit=1000,period=DAY",
	)
	if err != nil {
		return err
	}
	id, _ := out["id"].(string)
	if id == "" {
		return fmt.Errorf("CreateUsagePlan: missing id")
	}
	if got, _ := out["name"].(string); got != name {
		return fmt.Errorf("CreateUsagePlan: name %q != %q", got, name)
	}
	t.Set("usage_plan_id", id)
	return nil
}

func (g *apigwCliGroup) GetUsagePlan(_ context.Context, t *harness.TestContext) error {
	planID := t.GetString("usage_plan_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "get-usage-plan",
		"--usage-plan-id", planID,
	)
	if err != nil {
		return err
	}
	if got, _ := out["id"].(string); got != planID {
		return fmt.Errorf("GetUsagePlan: wrong id %q", got)
	}
	throttle, _ := out["throttle"].(map[string]any)
	if throttle == nil {
		return fmt.Errorf("GetUsagePlan: throttle not round-tripped")
	}
	if rate, _ := throttle["rateLimit"].(float64); rate != 10 {
		return fmt.Errorf("GetUsagePlan: rateLimit %v != 10", throttle["rateLimit"])
	}
	if burst, _ := throttle["burstLimit"].(float64); burst != 20 {
		return fmt.Errorf("GetUsagePlan: burstLimit %v != 20", throttle["burstLimit"])
	}
	quota, _ := out["quota"].(map[string]any)
	if quota == nil {
		return fmt.Errorf("GetUsagePlan: quota not round-tripped")
	}
	if limit, _ := quota["limit"].(float64); limit != 1000 {
		return fmt.Errorf("GetUsagePlan: quota limit %v != 1000", quota["limit"])
	}
	if period, _ := quota["period"].(string); period != "DAY" {
		return fmt.Errorf("GetUsagePlan: quota period %q != DAY", period)
	}
	return nil
}

func (g *apigwCliGroup) CreateAPIKey(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-%s-usage-key", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "create-api-key",
		"--name", name, "--enabled",
	)
	if err != nil {
		return err
	}
	id, _ := out["id"].(string)
	if id == "" {
		return fmt.Errorf("CreateApiKey: missing id")
	}
	if got, _ := out["name"].(string); got != name {
		return fmt.Errorf("CreateApiKey: name %q != %q", got, name)
	}
	t.Set("usage_key_id", id)
	return nil
}

func (g *apigwCliGroup) CreateUsagePlanKey(_ context.Context, t *harness.TestContext) error {
	planID := t.GetString("usage_plan_id")
	keyID := t.GetString("usage_key_id")
	if _, err := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "create-usage-plan-key",
		"--usage-plan-id", planID, "--key-id", keyID, "--key-type", "API_KEY",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "get-usage-plan-keys",
		"--usage-plan-id", planID,
	)
	if err != nil {
		return err
	}
	items, _ := out["items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if id, _ := item["id"].(string); id == keyID {
			return nil
		}
	}
	return fmt.Errorf("GetUsagePlanKeys: attached key %q not listed", keyID)
}

func (g *apigwCliGroup) GetUsage(_ context.Context, t *harness.TestContext) error {
	planID := t.GetString("usage_plan_id")
	keyID := t.GetString("usage_key_id")
	today := time.Now().UTC().Format("2006-01-02")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "get-usage",
		"--usage-plan-id", planID, "--start-date", today, "--end-date", today,
	)
	if err != nil {
		return err
	}
	if got, _ := out["usagePlanId"].(string); got != planID {
		return fmt.Errorf("GetUsage: wrong usagePlanId %q", got)
	}
	if got, _ := out["startDate"].(string); got != today {
		return fmt.Errorf("GetUsage: startDate %q not echoed", got)
	}
	values, _ := out["items"].(map[string]any)
	if values == nil {
		return fmt.Errorf("GetUsage: missing daily log map")
	}
	days, _ := values[keyID].([]any)
	if days == nil {
		return fmt.Errorf("GetUsage: no daily log for key %q", keyID)
	}
	if len(days) != 1 {
		return fmt.Errorf("GetUsage: expected one day of data, got %d", len(days))
	}
	pair, _ := days[0].([]any)
	if len(pair) != 2 {
		return fmt.Errorf("GetUsage: expected a [used, remaining] pair, got %v", days[0])
	}
	return nil
}

func (g *apigwCliGroup) DeleteUsagePlan(_ context.Context, t *harness.TestContext) error {
	planID := t.GetString("usage_plan_id")
	keyID := t.GetString("usage_key_id")
	// AWS refuses to delete a plan that still has keys attached.
	if err := awscli.Run(t.Endpoint, t.Region, "apigateway", "delete-usage-plan-key",
		"--usage-plan-id", planID, "--key-id", keyID,
	); err != nil {
		return err
	}
	if err := awscli.Run(t.Endpoint, t.Region, "apigateway", "delete-usage-plan",
		"--usage-plan-id", planID,
	); err != nil {
		return err
	}
	if _, err := awscli.RunOutput(t.Endpoint, t.Region, "apigateway", "get-usage-plan",
		"--usage-plan-id", planID,
	); err == nil {
		return fmt.Errorf("GetUsagePlan: deleted plan %q still readable", planID)
	}
	return nil
}
