package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	apigwv2 "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
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
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"apigateway-rest": g.setupRestApi,
			"apigateway-http": g.setupHttpApi,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"apigateway-rest": g.teardownRestApi,
			"apigateway-http": g.teardownHttpApi,
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
