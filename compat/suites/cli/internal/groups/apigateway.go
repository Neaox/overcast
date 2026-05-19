package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// APIGateway returns the API Gateway service group (REST v1 + HTTP v2).
func APIGateway() ServiceGroup {
	g := &apigwCliGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateRestApi": g.CreateRestApi,
			"GetRestApis":   g.GetRestApis,
			"DeleteRestApi": g.DeleteRestApi,
			"CreateApi":     g.CreateApi,
			"GetApis":       g.GetApis,
			"DeleteApi":     g.DeleteApi,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"apigateway-rest": g.setupRest,
			"apigateway-http": g.setupHttp,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"apigateway-rest": g.teardownRest,
			"apigateway-http": g.teardownHttp,
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
