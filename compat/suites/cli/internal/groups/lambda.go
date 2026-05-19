package groups

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// Lambda returns the Lambda service group.
func Lambda() ServiceGroup {
	g := &lambdaGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// lambda-crud — also register under group-qualified keys so these
			// impls are not overridden by the appsync group which shares the
			// same bare test names (CreateFunction, GetFunction, etc.).
			"CreateFunction":                          g.CreateFunction,
			"GetFunction":                             g.GetFunction,
			"ListFunctions":                           g.ListFunctions,
			"UpdateFunctionCode":                      g.UpdateFunctionCode,
			"UpdateFunctionConfiguration":             g.UpdateFunctionConfiguration,
			"DeleteFunction":                          g.DeleteFunction,
			"lambda-crud:CreateFunction":              g.CreateFunction,
			"lambda-crud:GetFunction":                 g.GetFunction,
			"lambda-crud:ListFunctions":               g.ListFunctions,
			"lambda-crud:UpdateFunctionCode":          g.UpdateFunctionCode,
			"lambda-crud:UpdateFunctionConfiguration": g.UpdateFunctionConfiguration,
			"lambda-crud:DeleteFunction":              g.DeleteFunction,
			// lambda-invoke
			"InvokeDryRun": g.InvokeDryRun,
			"InvokeSync":   g.InvokeSync,
			"InvokeAsync":  g.InvokeAsync,
			// lambda-aliases
			"PublishVersion":         g.PublishVersion,
			"ListVersionsByFunction": g.ListVersionsByFunction,
			"CreateAlias":            g.CreateAlias,
			"GetAlias":               g.GetAlias,
			"ListAliases":            g.ListAliases,
			"UpdateAlias":            g.UpdateAlias,
			"DeleteAlias":            g.DeleteAlias,
			// lambda-invoke-stream
			"InvokeWithResponseStream": nil, // invoke-with-response-stream is not available in the installed CLI version
			// lambda-layers
			"PublishLayerVersion": g.PublishLayerVersion,
			"ListLayers":          g.ListLayers,
			"DeleteLayerVersion":  g.DeleteLayerVersion,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"lambda-crud":          g.setupCRUD,
			"lambda-invoke":        g.setupInvoke,
			"lambda-aliases":       g.setupAliases,
			"lambda-invoke-stream": g.setupInvokeStream,
			"lambda-layers":        g.setupLayers,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"lambda-crud":          g.teardownFunction,
			"lambda-invoke":        g.teardownFunction,
			"lambda-aliases":       g.teardownFunction,
			"lambda-invoke-stream": g.teardownFunction,
			"lambda-layers":        g.teardownLayers,
		},
	}
}

type lambdaGroup struct{}

func (g *lambdaGroup) fnName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-lambda", t.RunID)
}

// currentFnName returns the function name set by the group's setup,
// falling back to the default fnName for legacy compatibility.
func (g *lambdaGroup) currentFnName(t *harness.TestContext) string {
	if n := t.GetString("fn_name"); n != "" {
		return n
	}
	return g.fnName(t)
}

func (g *lambdaGroup) layerName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-layer", t.RunID)
}

// lambdaZipFile writes minimal Python handler to a temp zip and returns the file path.
func (g *lambdaGroup) lambdaZipFile(t *harness.TestContext) (string, error) {
	code := `import json
def handler(event, context):
    return {"statusCode": 200, "body": json.dumps({"ok": True})}
`
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("index.py")
	if err != nil {
		return "", err
	}
	if _, err := f.Write([]byte(code)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("lambda-%s.zip", t.RunID))
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		return "", err
	}
	return path, nil
}

// fakeRoleARN returns a plausible IAM role ARN suitable for emulator use.
func (g *lambdaGroup) fakeRoleARN(t *harness.TestContext) string {
	return fmt.Sprintf("arn:aws:iam::000000000000:role/oc-lambda-role-%s", t.RunID)
}

// ─── lambda-crud ─────────────────────────────────────────────────────────────

// setupCRUD pre-deletes any leftover function from previous runs so that
// the CreateFunction test operates on a clean slate.
func (g *lambdaGroup) setupCRUD(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-lmbd-crud", t.RunID)
	t.Set("fn_name", name)
	// Best-effort delete of leftover function — error ignored.
	awscli.Run(t.Endpoint, t.Region, "lambda", "delete-function", "--function-name", name) //nolint:errcheck
	return nil
}

func (g *lambdaGroup) CreateFunction(_ context.Context, t *harness.TestContext) error {
	zipPath, err := g.lambdaZipFile(t)
	if err != nil {
		return fmt.Errorf("lambda CreateFunction: build zip: %w", err)
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "create-function",
		"--function-name", g.currentFnName(t),
		"--runtime", "python3.12",
		"--role", g.fakeRoleARN(t),
		"--handler", "index.handler",
		"--zip-file", fmt.Sprintf("fileb://%s", zipPath),
	)
	if err != nil {
		return err
	}
	if name, _ := out["FunctionName"].(string); name != g.currentFnName(t) {
		return fmt.Errorf("lambda CreateFunction: expected FunctionName=%q, got %q", g.currentFnName(t), name)
	}
	return nil
}

func (g *lambdaGroup) GetFunction(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "get-function",
		"--function-name", g.currentFnName(t),
	)
	if err != nil {
		return err
	}
	cfg, _ := out["Configuration"].(map[string]any)
	if cfg == nil {
		return fmt.Errorf("lambda GetFunction: missing Configuration")
	}
	return nil
}

func (g *lambdaGroup) ListFunctions(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "lambda", "list-functions")
	if err != nil {
		return err
	}
	functions, _ := out["Functions"].([]any)
	want := g.currentFnName(t)
	for _, raw := range functions {
		if m, ok := raw.(map[string]any); ok && m["FunctionName"] == want {
			return nil
		}
	}
	return fmt.Errorf("lambda ListFunctions: function %q not found", want)
}

func (g *lambdaGroup) UpdateFunctionCode(_ context.Context, t *harness.TestContext) error {
	zipPath, err := g.lambdaZipFile(t)
	if err != nil {
		return fmt.Errorf("lambda UpdateFunctionCode: build zip: %w", err)
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "update-function-code",
		"--function-name", g.currentFnName(t),
		"--zip-file", fmt.Sprintf("fileb://%s", zipPath),
	)
	if err != nil {
		return err
	}
	if arn, _ := out["FunctionArn"].(string); arn == "" {
		return fmt.Errorf("lambda UpdateFunctionCode: missing FunctionArn")
	}
	if sha, _ := out["CodeSha256"].(string); sha == "" {
		return fmt.Errorf("lambda UpdateFunctionCode: missing CodeSha256")
	}
	return nil
}

func (g *lambdaGroup) UpdateFunctionConfiguration(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "update-function-configuration",
		"--function-name", g.currentFnName(t),
		"--timeout", "30",
		"--memory-size", "256",
	)
	if err != nil {
		return err
	}
	if timeout, _ := out["Timeout"].(float64); timeout != 30 {
		return fmt.Errorf("lambda UpdateFunctionConfiguration: expected Timeout=30, got %v", out["Timeout"])
	}
	return nil
}

func (g *lambdaGroup) DeleteFunction(_ context.Context, t *harness.TestContext) error {
	name := g.currentFnName(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"lambda", "delete-function",
		"--function-name", name,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "lambda", "list-functions")
	if err != nil {
		return fmt.Errorf("lambda DeleteFunction: list-functions failed: %w", err)
	}
	functions, _ := out["Functions"].([]any)
	for _, raw := range functions {
		if m, ok := raw.(map[string]any); ok && m["FunctionName"] == name {
			return fmt.Errorf("lambda DeleteFunction: function %q still present after delete", name)
		}
	}
	return nil
}

func (g *lambdaGroup) teardownFunction(_ context.Context, t *harness.TestContext) error {
	name := g.currentFnName(t)
	awscli.Run(t.Endpoint, t.Region, "lambda", "delete-function", "--function-name", name)                //nolint:errcheck
	awscli.Run(t.Endpoint, t.Region, "logs", "delete-log-group", "--log-group-name", "/aws/lambda/"+name) //nolint:errcheck
	// Note: we intentionally do not remove the temp zip file here because
	// multiple groups share the same zip path and run concurrently — removing
	// it in one teardown causes "file not found" in another group's tests.
	return nil
}

// ─── lambda-invoke ───────────────────────────────────────────────────────────

func (g *lambdaGroup) setupInvoke(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-lmbd-inv", t.RunID)
	t.Set("fn_name", name)
	zipPath, err := g.lambdaZipFile(t)
	if err != nil {
		return fmt.Errorf("lambda setupInvoke: %w", err)
	}
	_, err = awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "create-function",
		"--function-name", name,
		"--runtime", "python3.12",
		"--role", g.fakeRoleARN(t),
		"--handler", "index.handler",
		"--zip-file", fmt.Sprintf("fileb://%s", zipPath),
		"--timeout", "30",
	)
	return err
}

func (g *lambdaGroup) InvokeDryRun(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"lambda", "invoke",
		"--function-name", g.currentFnName(t),
		"--invocation-type", "DryRun",
		"/dev/null",
	)
}

func (g *lambdaGroup) waitFunctionActive(t *harness.TestContext) error {
	for i := 0; i < 20; i++ {
		out, err := awscli.RunOutput(t.Endpoint, t.Region,
			"lambda", "get-function",
			"--function-name", g.currentFnName(t),
		)
		if err != nil {
			return err
		}
		cfg, _ := out["Configuration"].(map[string]any)
		state, _ := cfg["State"].(string)
		if state == "Active" {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("lambda: function %s did not become Active", g.currentFnName(t))
}

func (g *lambdaGroup) InvokeSync(_ context.Context, t *harness.TestContext) error {
	if err := g.waitFunctionActive(t); err != nil {
		return err
	}
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("invoke-%s.json", t.RunID))
	defer os.Remove(outFile)
	return awscli.Run(t.Endpoint, t.Region,
		"lambda", "invoke",
		"--function-name", g.currentFnName(t),
		"--payload", `{"key":"value"}`,
		outFile,
	)
}

func (g *lambdaGroup) InvokeAsync(_ context.Context, t *harness.TestContext) error {
	if err := g.waitFunctionActive(t); err != nil {
		return err
	}
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("invoke-async-%s.json", t.RunID))
	defer os.Remove(outFile)
	return awscli.Run(t.Endpoint, t.Region,
		"lambda", "invoke",
		"--function-name", g.currentFnName(t),
		"--invocation-type", "Event",
		"--payload", `{}`,
		outFile,
	)
}

// ─── lambda-invoke-stream ────────────────────────────────────────────────────

func (g *lambdaGroup) setupInvokeStream(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-lmbd-stream", t.RunID)
	t.Set("fn_name", name)
	zipPath, err := g.lambdaZipFile(t)
	if err != nil {
		return fmt.Errorf("lambda setupInvokeStream: %w", err)
	}
	_, err = awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "create-function",
		"--function-name", name,
		"--runtime", "python3.12",
		"--role", g.fakeRoleARN(t),
		"--handler", "index.handler",
		"--zip-file", fmt.Sprintf("fileb://%s", zipPath),
		"--timeout", "30",
	)
	return err
}

func (g *lambdaGroup) InvokeWithResponseStream(_ context.Context, t *harness.TestContext) error {
	if err := g.waitFunctionActive(t); err != nil {
		return err
	}
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("invoke-stream-%s.json", t.RunID))
	defer os.Remove(outFile)
	return awscli.Run(t.Endpoint, t.Region,
		"lambda", "invoke-with-response-stream",
		"--function-name", g.currentFnName(t),
		"--payload", `{}`,
		outFile,
	)
}

// ─── lambda-aliases ──────────────────────────────────────────────────────────

func (g *lambdaGroup) setupAliases(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-lmbd-ali", t.RunID)
	t.Set("fn_name", name)
	zipPath, err := g.lambdaZipFile(t)
	if err != nil {
		return fmt.Errorf("lambda setupAliases: %w", err)
	}
	_, err = awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "create-function",
		"--function-name", name,
		"--runtime", "python3.12",
		"--role", g.fakeRoleARN(t),
		"--handler", "index.handler",
		"--zip-file", fmt.Sprintf("fileb://%s", zipPath),
	)
	return err
}

func (g *lambdaGroup) PublishVersion(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "publish-version",
		"--function-name", g.currentFnName(t),
	)
	if err != nil {
		return err
	}
	ver, _ := out["Version"].(string)
	if ver == "" {
		return fmt.Errorf("lambda PublishVersion: missing Version")
	}
	t.Set("fn_version", ver)
	return nil
}

func (g *lambdaGroup) ListVersionsByFunction(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "list-versions-by-function",
		"--function-name", g.currentFnName(t),
	)
	if err != nil {
		return err
	}
	versions, _ := out["Versions"].([]any)
	if len(versions) == 0 {
		return fmt.Errorf("lambda ListVersionsByFunction: no versions returned")
	}
	return nil
}

func (g *lambdaGroup) CreateAlias(_ context.Context, t *harness.TestContext) error {
	ver := t.GetString("fn_version")
	if ver == "" {
		ver = "1"
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "create-alias",
		"--function-name", g.currentFnName(t),
		"--name", "live",
		"--function-version", ver,
	)
	if err != nil {
		return err
	}
	if name, _ := out["Name"].(string); name != "live" {
		return fmt.Errorf("lambda CreateAlias: expected Name=live, got %q", name)
	}
	return nil
}

func (g *lambdaGroup) GetAlias(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "get-alias",
		"--function-name", g.currentFnName(t),
		"--name", "live",
	)
	if err != nil {
		return err
	}
	if out["Name"] != "live" {
		return fmt.Errorf("lambda GetAlias: expected Name=live, got %v", out["Name"])
	}
	return nil
}

func (g *lambdaGroup) ListAliases(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "list-aliases",
		"--function-name", g.currentFnName(t),
	)
	if err != nil {
		return err
	}
	aliases, _ := out["Aliases"].([]any)
	if len(aliases) == 0 {
		return fmt.Errorf("lambda ListAliases: no aliases returned")
	}
	return nil
}

func (g *lambdaGroup) UpdateAlias(_ context.Context, t *harness.TestContext) error {
	ver := t.GetString("fn_version")
	if ver == "" {
		ver = "1"
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"lambda", "update-alias",
		"--function-name", g.currentFnName(t),
		"--name", "live",
		"--function-version", ver,
		"--description", "updated by CLI test",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "get-alias",
		"--function-name", g.currentFnName(t),
		"--name", "live",
	)
	if err != nil {
		return fmt.Errorf("lambda UpdateAlias: get-alias failed: %w", err)
	}
	if out["Description"] != "updated by CLI test" {
		return fmt.Errorf("lambda UpdateAlias: expected description 'updated by CLI test', got %v", out["Description"])
	}
	return nil
}

func (g *lambdaGroup) DeleteAlias(_ context.Context, t *harness.TestContext) error {
	name := g.currentFnName(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"lambda", "delete-alias",
		"--function-name", name,
		"--name", "live",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "list-aliases",
		"--function-name", name,
	)
	if err != nil {
		return fmt.Errorf("lambda DeleteAlias: list-aliases failed: %w", err)
	}
	aliases, _ := out["Aliases"].([]any)
	for _, raw := range aliases {
		if m, ok := raw.(map[string]any); ok && m["Name"] == "live" {
			return fmt.Errorf("lambda DeleteAlias: alias 'live' still present after delete")
		}
	}
	return nil
}

// ─── lambda-layers ───────────────────────────────────────────────────────────

func (g *lambdaGroup) setupLayers(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *lambdaGroup) PublishLayerVersion(_ context.Context, t *harness.TestContext) error {
	// Build a minimal zip.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("requirements.txt")
	f.Write([]byte("# placeholder\n")) //nolint:errcheck
	w.Close()

	path := filepath.Join(os.TempDir(), fmt.Sprintf("layer-%s.zip", t.RunID))
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		return fmt.Errorf("lambda PublishLayerVersion: write zip: %w", err)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "publish-layer-version",
		"--layer-name", g.layerName(t),
		"--zip-file", fmt.Sprintf("fileb://%s", path),
		"--compatible-runtimes", "python3.12",
	)
	if err != nil {
		return err
	}
	ver, _ := out["Version"].(float64)
	t.Set("layer_version", fmt.Sprintf("%.0f", ver))
	return nil
}

func (g *lambdaGroup) ListLayers(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "lambda", "list-layers")
	if err != nil {
		return err
	}
	layers, _ := out["Layers"].([]any)
	if len(layers) == 0 {
		return fmt.Errorf("lambda ListLayers: expected at least 1 layer")
	}
	return nil
}

func (g *lambdaGroup) DeleteLayerVersion(_ context.Context, t *harness.TestContext) error {
	ver := t.GetString("layer_version")
	if ver == "" {
		ver = "1"
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"lambda", "delete-layer-version",
		"--layer-name", g.layerName(t),
		"--version-number", ver,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "list-layer-versions",
		"--layer-name", g.layerName(t),
	)
	if err != nil {
		return fmt.Errorf("lambda DeleteLayerVersion: list-layer-versions failed: %w", err)
	}
	versions, _ := out["LayerVersions"].([]any)
	for _, raw := range versions {
		if m, ok := raw.(map[string]any); ok {
			if fmt.Sprintf("%v", m["Version"]) == ver {
				return fmt.Errorf("lambda DeleteLayerVersion: version %s still present", ver)
			}
		}
	}
	return nil
}

func (g *lambdaGroup) teardownLayers(_ context.Context, t *harness.TestContext) error {
	ver := t.GetString("layer_version")
	if ver == "" {
		ver = "1"
	}
	awscli.Run(t.Endpoint, t.Region, //nolint:errcheck
		"lambda", "delete-layer-version",
		"--layer-name", g.layerName(t),
		"--version-number", ver,
	)
	os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("layer-%s.zip", t.RunID)))
	return nil
}
