package groups

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

const _lambdaRoleARN = "arn:aws:iam::000000000000:role/lambda-role"
const _handlerJS = `exports.handler = async (e) => ({statusCode:200,body:JSON.stringify({ok:true,event:e})});`

func Lambda(c *clients.Clients) ServiceGroup {
	g := &lambdaGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateFunction":              g.CreateFunction,
			"GetFunction":                 g.GetFunction,
			"ListFunctions":               g.ListFunctions,
			"UpdateFunctionCode":          g.UpdateFunctionCode,
			"UpdateFunctionConfiguration": g.UpdateFunctionConfiguration,
			"DeleteFunction":              g.DeleteFunction,
			"InvokeSync":                  g.InvokeSync,
			"InvokeAsync":                 g.InvokeAsync,
			"InvokeDryRun":                g.InvokeDryRun,
			"PublishVersion":              g.PublishVersion,
			"ListVersionsByFunction":      g.ListVersionsByFunction,
			"CreateAlias":                 g.CreateAlias,
			"GetAlias":                    g.GetAlias,
			"ListAliases":                 g.ListAliases,
			"UpdateAlias":                 g.UpdateAlias,
			"DeleteAlias":                 g.DeleteAlias,
			"InvokeWithResponseStream":    g.InvokeWithResponseStream,
			"PublishLayerVersion":         g.PublishLayerVersion,
			"ListLayers":                  g.ListLayers,
			"DeleteLayerVersion":          g.DeleteLayerVersion,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"lambda-crud":          g.setupCrud,
			"lambda-invoke":        g.setupInvoke,
			"lambda-aliases":       g.setupAliases,
			"lambda-invoke-stream": g.setupInvokeStream,
			"lambda-layers":        func(_ context.Context, _ *harness.TestContext) error { return nil },
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"lambda-crud":          g.teardownCrud,
			"lambda-invoke":        g.teardownInvoke,
			"lambda-aliases":       g.teardownAliases,
			"lambda-invoke-stream": g.teardownInvokeStream,
			"lambda-layers":        g.teardownLayers,
		},
	}
}

type lambdaGroup struct{ c *clients.Clients }

func (g *lambdaGroup) client() *lambda.Client { return g.c.Lambda() }

func makeZip(filename, content string) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(filename)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(content)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (g *lambdaGroup) waitActive(ctx context.Context, name string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := g.client().GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(name)})
		if err == nil {
			state := resp.Configuration.State
			if state == lambdatypes.StatePending {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("lambda %q did not become active", name)
}

func (g *lambdaGroup) createFunc(ctx context.Context, name string) error {
	zipBytes, err := makeZip("index.js", _handlerJS)
	if err != nil {
		return err
	}
	_, err = g.client().CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.RuntimeNodejs20x,
		Handler:      aws.String("index.handler"),
		Role:         aws.String(_lambdaRoleARN),
		Code:         &lambdatypes.FunctionCode{ZipFile: zipBytes},
		Timeout:      aws.Int32(30),
	})
	if err != nil {
		return err
	}
	return g.waitActive(ctx, name)
}

func (g *lambdaGroup) deleteFunc(ctx context.Context, name string) {
	g.client().DeleteFunction(ctx, &lambda.DeleteFunctionInput{FunctionName: aws.String(name)})                                    //nolint:errcheck
	g.c.CloudWatchLogs().DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{LogGroupName: aws.String("/aws/lambda/" + name)}) //nolint:errcheck
}

// ── lambda-crud ───────────────────────────────────────────────────────────────

func (g *lambdaGroup) setupCrud(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-fn", t.RunID)
	if err := g.createFunc(ctx, name); err != nil {
		return err
	}
	t.Set("lambda_fn_name", name)
	return nil
}

func (g *lambdaGroup) teardownCrud(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("lambda_fn_name"); name != "" {
		g.deleteFunc(ctx, name)
	}
	return nil
}

func (g *lambdaGroup) CreateFunction(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-fncreate", t.RunID)
	if err := g.createFunc(ctx, name); err != nil {
		return err
	}
	defer g.deleteFunc(ctx, name)
	resp, err := g.client().GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(name)})
	if err != nil {
		return err
	}
	if aws.ToString(resp.Configuration.CodeSha256) == "" {
		return fmt.Errorf("CreateFunction: missing CodeSha256")
	}
	return nil
}

func (g *lambdaGroup) GetFunction(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_fn_name")
	resp, err := g.client().GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(name)})
	if err != nil {
		return err
	}
	if aws.ToString(resp.Configuration.FunctionName) != name {
		return fmt.Errorf("GetFunction: name mismatch")
	}
	return nil
}

func (g *lambdaGroup) ListFunctions(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_fn_name")
	resp, err := g.client().ListFunctions(ctx, &lambda.ListFunctionsInput{})
	if err != nil {
		return err
	}
	for _, fn := range resp.Functions {
		if aws.ToString(fn.FunctionName) == name {
			return nil
		}
	}
	return fmt.Errorf("ListFunctions: %q not found", name)
}

func (g *lambdaGroup) UpdateFunctionCode(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_fn_name")
	zipBytes, err := makeZip("index.js", _handlerJS+"// updated\n")
	if err != nil {
		return err
	}
	resp, err := g.client().UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(name),
		ZipFile:      zipBytes,
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.CodeSha256) == "" {
		return fmt.Errorf("UpdateFunctionCode: missing CodeSha256")
	}
	return nil
}

func (g *lambdaGroup) UpdateFunctionConfiguration(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_fn_name")
	_, err := g.client().UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String(name),
		Timeout:      aws.Int32(10),
	})
	return err
}

func (g *lambdaGroup) DeleteFunction(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-fndel", t.RunID)
	if err := g.createFunc(ctx, name); err != nil {
		return err
	}
	_, err := g.client().DeleteFunction(ctx, &lambda.DeleteFunctionInput{FunctionName: aws.String(name)})
	return err
}

// ── lambda-invoke ─────────────────────────────────────────────────────────────

func (g *lambdaGroup) setupInvoke(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-fninvoke", t.RunID)
	if err := g.createFunc(ctx, name); err != nil {
		return err
	}
	t.Set("lambda_invoke_fn", name)
	return nil
}

func (g *lambdaGroup) teardownInvoke(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("lambda_invoke_fn"); name != "" {
		g.deleteFunc(ctx, name)
	}
	return nil
}

func (g *lambdaGroup) InvokeSync(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_invoke_fn")
	resp, err := g.client().Invoke(ctx, &lambda.InvokeInput{
		FunctionName: aws.String(name),
		Payload:      []byte(`{"hello":"world"}`),
	})
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("InvokeSync: expected 200, got %d", resp.StatusCode)
	}
	return nil
}

func (g *lambdaGroup) InvokeAsync(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_invoke_fn")
	_, err := g.client().Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(name),
		InvocationType: lambdatypes.InvocationTypeEvent,
		Payload:        []byte(`{}`),
	})
	return err
}

func (g *lambdaGroup) InvokeWithPayload(ctx context.Context, t *harness.TestContext) error {
	return g.InvokeSync(ctx, t)
}

func (g *lambdaGroup) InvokeDryRun(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_invoke_fn")
	resp, err := g.client().Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(name),
		InvocationType: lambdatypes.InvocationTypeDryRun,
		Payload:        []byte(`{}`),
	})
	if err != nil {
		return err
	}
	if resp.StatusCode != 204 {
		return fmt.Errorf("InvokeDryRun: expected 204, got %d", resp.StatusCode)
	}
	return nil
}

func (g *lambdaGroup) InvokeWithError(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_invoke_fn")
	resp, err := g.client().Invoke(ctx, &lambda.InvokeInput{
		FunctionName: aws.String(name),
		Payload:      []byte(`{}`),
	})
	if err != nil {
		return err
	}
	_ = resp
	return nil
}

// ── lambda-invoke-stream ──────────────────────────────────────────────────────

func (g *lambdaGroup) setupInvokeStream(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-fnstream", t.RunID)
	if err := g.createFunc(ctx, name); err != nil {
		return err
	}
	t.Set("lambda_stream_fn", name)
	return nil
}

func (g *lambdaGroup) teardownInvokeStream(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("lambda_stream_fn"); name != "" {
		g.deleteFunc(ctx, name)
	}
	return nil
}

func (g *lambdaGroup) InvokeWithResponseStream(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_stream_fn")
	resp, err := g.client().InvokeWithResponseStream(ctx, &lambda.InvokeWithResponseStreamInput{
		FunctionName: aws.String(name),
		Payload:      []byte(`{}`),
	})
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("InvokeWithResponseStream: expected 200, got %d", resp.StatusCode)
	}
	stream := resp.GetStream()
	defer stream.Close()
	for range stream.Events() {
	}
	return stream.Err()
}

// ── lambda-aliases ────────────────────────────────────────────────────────────

func (g *lambdaGroup) setupAliases(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-fnalias", t.RunID)
	if err := g.createFunc(ctx, name); err != nil {
		return err
	}
	// Publish a version
	pResp, err := g.client().PublishVersion(ctx, &lambda.PublishVersionInput{
		FunctionName: aws.String(name),
	})
	if err != nil {
		return err
	}
	t.Set("lambda_alias_fn", name)
	t.Set("lambda_alias_ver", aws.ToString(pResp.Version))
	return nil
}

func (g *lambdaGroup) teardownAliases(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_alias_fn")
	if name == "" {
		return nil
	}
	if alias := t.GetString("lambda_alias_name"); alias != "" {
		g.client().DeleteAlias(ctx, &lambda.DeleteAliasInput{ //nolint:errcheck
			FunctionName: aws.String(name), Name: aws.String(alias),
		})
	}
	g.deleteFunc(ctx, name)
	return nil
}

func (g *lambdaGroup) PublishVersion(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_alias_fn")
	resp, err := g.client().PublishVersion(ctx, &lambda.PublishVersionInput{
		FunctionName: aws.String(name),
	})
	if err != nil {
		return err
	}
	t.Set("lambda_published_ver", aws.ToString(resp.Version))
	return nil
}

func (g *lambdaGroup) ListVersionsByFunction(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_alias_fn")
	publishedVer := t.GetString("lambda_published_ver")
	resp, err := g.client().ListVersionsByFunction(ctx, &lambda.ListVersionsByFunctionInput{
		FunctionName: aws.String(name),
	})
	if err != nil {
		return err
	}
	for _, v := range resp.Versions {
		if aws.ToString(v.Version) == publishedVer {
			return nil
		}
	}
	return fmt.Errorf("ListVersionsByFunction: version %q not found", publishedVer)
}

func (g *lambdaGroup) CreateAlias(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_alias_fn")
	ver := t.GetString("lambda_alias_ver")
	resp, err := g.client().CreateAlias(ctx, &lambda.CreateAliasInput{
		FunctionName:    aws.String(name),
		Name:            aws.String("live"),
		FunctionVersion: aws.String(ver),
	})
	if err != nil {
		return err
	}
	t.Set("lambda_alias_name", aws.ToString(resp.Name))
	return nil
}

func (g *lambdaGroup) GetAlias(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_alias_fn")
	resp, err := g.client().GetAlias(ctx, &lambda.GetAliasInput{
		FunctionName: aws.String(name), Name: aws.String("live"),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.Name) != "live" {
		return fmt.Errorf("GetAlias: name mismatch")
	}
	return nil
}

func (g *lambdaGroup) ListAliases(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_alias_fn")
	resp, err := g.client().ListAliases(ctx, &lambda.ListAliasesInput{FunctionName: aws.String(name)})
	if err != nil {
		return err
	}
	if len(resp.Aliases) == 0 {
		return fmt.Errorf("ListAliases: no aliases")
	}
	return nil
}

func (g *lambdaGroup) UpdateAlias(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_alias_fn")
	ver := t.GetString("lambda_alias_ver")
	_, err := g.client().UpdateAlias(ctx, &lambda.UpdateAliasInput{
		FunctionName:    aws.String(name),
		Name:            aws.String("live"),
		FunctionVersion: aws.String(ver),
		Description:     aws.String("updated"),
	})
	return err
}

func (g *lambdaGroup) DeleteAlias(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_alias_fn")
	_, err := g.client().DeleteAlias(ctx, &lambda.DeleteAliasInput{
		FunctionName: aws.String(name), Name: aws.String("live"),
	})
	if err != nil {
		return err
	}
	t.Set("lambda_alias_name", "")
	return nil
}

// ── lambda-layers ─────────────────────────────────────────────────────────────

func (g *lambdaGroup) teardownLayers(ctx context.Context, t *harness.TestContext) error {
	layerName := t.GetString("lambda_layer_name")
	ver64, _ := t.Get("lambda_layer_ver")
	if layerName == "" {
		return nil
	}
	ver, _ := ver64.(int64)
	if ver > 0 {
		g.client().DeleteLayerVersion(ctx, &lambda.DeleteLayerVersionInput{ //nolint:errcheck
			LayerName:     aws.String(layerName),
			VersionNumber: aws.Int64(ver),
		})
	}
	return nil
}

func (g *lambdaGroup) PublishLayerVersion(ctx context.Context, t *harness.TestContext) error {
	zipBytes, err := makeZip("lib/helper.js", "exports.hello = () => 'hello';\n")
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s-layer", t.RunID)
	resp, err := g.client().PublishLayerVersion(ctx, &lambda.PublishLayerVersionInput{
		LayerName:          aws.String(name),
		CompatibleRuntimes: []lambdatypes.Runtime{lambdatypes.RuntimeNodejs20x},
		Content:            &lambdatypes.LayerVersionContentInput{ZipFile: zipBytes},
	})
	if err != nil {
		return err
	}
	t.Set("lambda_layer_name", name)
	t.Set("lambda_layer_ver", resp.Version)
	return nil
}

func (g *lambdaGroup) GetLayerVersion(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_layer_name")
	ver64, _ := t.Get("lambda_layer_ver")
	ver, _ := ver64.(int64)
	resp, err := g.client().GetLayerVersion(ctx, &lambda.GetLayerVersionInput{
		LayerName: aws.String(name), VersionNumber: aws.Int64(ver),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.LayerArn) == "" {
		return fmt.Errorf("GetLayerVersion: empty LayerArn")
	}
	return nil
}

func (g *lambdaGroup) ListLayers(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_layer_name")
	resp, err := g.client().ListLayers(ctx, &lambda.ListLayersInput{})
	if err != nil {
		return err
	}
	for _, l := range resp.Layers {
		if aws.ToString(l.LayerName) == name {
			return nil
		}
	}
	return fmt.Errorf("ListLayers: %q not found", name)
}

func (g *lambdaGroup) DeleteLayerVersion(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("lambda_layer_name")
	ver64, _ := t.Get("lambda_layer_ver")
	ver, _ := ver64.(int64)
	_, err := g.client().DeleteLayerVersion(ctx, &lambda.DeleteLayerVersionInput{
		LayerName: aws.String(name), VersionNumber: aws.Int64(ver),
	})
	if err != nil {
		return err
	}
	t.Set("lambda_layer_ver", int64(0))
	return nil
}
