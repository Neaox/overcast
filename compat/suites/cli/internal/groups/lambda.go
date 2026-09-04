package groups

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
)

// Lambda returns the Lambda service group.
func Lambda() ServiceGroup {
	g := &lambdaGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// lambda-crud — CreateFunction, GetFunction, ListFunctions and
			// DeleteFunction are also declared by appsync-functions, so they
			// must be group-qualified: a bare key cannot say which group it
			// implements and the loader refuses it.
			"lambda-crud:CreateFunction":              g.CreateFunction,
			"lambda-crud:GetFunction":                 g.GetFunction,
			"lambda-crud:ListFunctions":               g.ListFunctions,
			"lambda-crud:UpdateFunctionCode":          g.UpdateFunctionCode,
			"lambda-crud:UpdateFunctionConfiguration": g.UpdateFunctionConfiguration,
			"lambda-crud:DeleteFunction":              g.DeleteFunction,
			"lambda-policy:AddPermission":             g.AddPermission,
			"lambda-policy:GetPolicy":                 g.GetPolicy,
			"lambda-policy:RemovePermission":          g.RemovePermission,
			// lambda-invoke
			"lambda-invoke:InvokeDryRun": g.InvokeDryRun,
			"lambda-invoke:InvokeSync":   g.InvokeSync,
			"lambda-invoke:InvokeAsync":  g.InvokeAsync,
			// lambda-aliases
			"lambda-aliases:PublishVersion":         g.PublishVersion,
			"lambda-aliases:ListVersionsByFunction": g.ListVersionsByFunction,
			"lambda-aliases:CreateAlias":            g.CreateAlias,
			"lambda-aliases:GetAlias":               g.GetAlias,
			"lambda-aliases:ListAliases":            g.ListAliases,
			"lambda-aliases:UpdateAlias":            g.UpdateAlias,
			"lambda-aliases:DeleteAlias":            g.DeleteAlias,
			// lambda-invoke-stream
			"lambda-invoke-stream:InvokeWithResponseStream": nil, // invoke-with-response-stream is not available in the installed CLI version
			// lambda-invoke-error
			"lambda-invoke-error:InvokeWithError": g.InvokeWithError,
			// lambda-layers
			"lambda-layers:PublishLayerVersion": g.PublishLayerVersion,
			"lambda-layers:ListLayers":          g.ListLayers,
			"lambda-layers:DeleteLayerVersion":  g.DeleteLayerVersion,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"lambda-crud":          g.setupCRUD,
			"lambda-policy":        g.setupPolicy,
			"lambda-invoke":        g.setupInvoke,
			"lambda-aliases":       g.setupAliases,
			"lambda-invoke-stream": g.setupInvokeStream,
			"lambda-invoke-error":  g.setupInvokeError,
			"lambda-layers":        g.setupLayers,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"lambda-crud":          g.teardownFunction,
			"lambda-policy":        g.teardownFunction,
			"lambda-invoke":        g.teardownFunction,
			"lambda-aliases":       g.teardownFunction,
			"lambda-invoke-stream": g.teardownFunction,
			"lambda-invoke-error":  g.teardownFunction,
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
	return g.writeLambdaZip(fmt.Sprintf("lambda-%s.zip", t.RunID), code)
}

// lambdaErrorZipFile writes a Python handler that raises on every invocation;
// lambda-invoke-error proves the failure surfaces the way AWS reports it.
func (g *lambdaGroup) lambdaErrorZipFile(t *harness.TestContext) (string, error) {
	code := `def handler(event, context):
    raise RuntimeError("compat: intentional failure")
`
	return g.writeLambdaZip(fmt.Sprintf("lambda-err-%s.zip", t.RunID), code)
}

// writeLambdaZip zips code as index.py under filename in the temp dir and
// returns the file path.
func (g *lambdaGroup) writeLambdaZip(filename, code string) (string, error) {
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
	path := filepath.Join(os.TempDir(), filename)
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

func (g *lambdaGroup) setupPolicy(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-lmbd-policy", t.RunID)
	t.Set("fn_name", name)
	awscli.Run(t.Endpoint, t.Region, "lambda", "delete-function", "--function-name", name) //nolint:errcheck
	return g.CreateFunction(ctx, t)
}

func (g *lambdaGroup) AddPermission(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "lambda", "add-permission",
		"--function-name", g.currentFnName(t), "--statement-id", "allow-s3",
		"--action", "lambda:InvokeFunction", "--principal", "s3.amazonaws.com",
		"--source-account", "000000000000")
	if err != nil {
		return err
	}
	statement, _ := out["Statement"].(string)
	if !strings.Contains(statement, `"Sid":"allow-s3"`) {
		return fmt.Errorf("lambda AddPermission: statement not returned: %q", statement)
	}
	policyOut, err := awscli.RunOutput(t.Endpoint, t.Region, "lambda", "get-policy", "--function-name", g.currentFnName(t))
	if err != nil {
		return err
	}
	policy, _ := policyOut["Policy"].(string)
	if !strings.Contains(policy, `"Sid":"allow-s3"`) {
		return fmt.Errorf("lambda AddPermission: statement missing from GetPolicy: %v", policyOut)
	}
	return nil
}

func (g *lambdaGroup) GetPolicy(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "lambda", "get-policy", "--function-name", g.currentFnName(t))
	if err != nil {
		return err
	}
	policy, _ := out["Policy"].(string)
	if !strings.Contains(policy, `"Sid":"allow-s3"`) || out["RevisionId"] == "" {
		return fmt.Errorf("lambda GetPolicy: policy or revision missing: %v", out)
	}
	return nil
}

func (g *lambdaGroup) RemovePermission(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region, "lambda", "remove-permission", "--function-name", g.currentFnName(t), "--statement-id", "allow-s3"); err != nil {
		return err
	}
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "lambda", "get-policy", "--function-name", g.currentFnName(t))
	if err == nil || !strings.Contains(err.Error(), "ResourceNotFoundException") {
		return fmt.Errorf("lambda RemovePermission: expected ResourceNotFoundException after removal, got %v", err)
	}
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
	// LastUpdateStatus is what `aws lambda wait function-updated` polls, and
	// what the SDK, CDK and SAM waiters read after a deploy. A response that
	// omits it leaves every one of them polling until its attempt budget runs
	// out, so both the field and the waiter over it are checked here.
	switch status, _ := out["LastUpdateStatus"].(string); status {
	case "InProgress", "Successful":
	case "":
		return fmt.Errorf("lambda UpdateFunctionCode: missing LastUpdateStatus")
	default:
		return fmt.Errorf("lambda UpdateFunctionCode: LastUpdateStatus=%q, want InProgress or Successful", status)
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"lambda", "wait", "function-updated",
		"--function-name", g.currentFnName(t),
	); err != nil {
		return fmt.Errorf("lambda wait function-updated: %w", err)
	}
	settled, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "get-function-configuration",
		"--function-name", g.currentFnName(t),
	)
	if err != nil {
		return err
	}
	if got, _ := settled["LastUpdateStatus"].(string); got != "Successful" {
		return fmt.Errorf("lambda GetFunctionConfiguration after the waiter returned: LastUpdateStatus=%q, want Successful", got)
	}
	return nil
}

func (g *lambdaGroup) UpdateFunctionConfiguration(_ context.Context, t *harness.TestContext) error {
	// A real queue, so --dead-letter-config names a target that exists rather
	// than a plausible-looking string.
	queue := fmt.Sprintf("%s-lmbd-crud-dlq", t.RunID)
	created, err := awscli.RunOutput(t.Endpoint, t.Region, "sqs", "create-queue", "--queue-name", queue)
	if err != nil {
		return err
	}
	queueURL, _ := created["QueueUrl"].(string)
	defer awscli.Run(t.Endpoint, t.Region, "sqs", "delete-queue", "--queue-url", queueURL) //nolint:errcheck
	attrs, err := awscli.RunOutput(t.Endpoint, t.Region, "sqs", "get-queue-attributes",
		"--queue-url", queueURL, "--attribute-names", "QueueArn")
	if err != nil {
		return err
	}
	attributes, _ := attrs["Attributes"].(map[string]any)
	dlqARN, _ := attributes["QueueArn"].(string)
	if dlqARN == "" {
		return fmt.Errorf("lambda UpdateFunctionConfiguration: queue %q has no ARN", queue)
	}

	// DeadLetterConfig rides along because it is the member that used to answer
	// 501 here, which failed every `cdk deploy` of a function with a DLQ. Both
	// the response and a later read are checked: an update that answers 200 and
	// drops the property is the same bug wearing a better status code.
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "update-function-configuration",
		"--function-name", g.currentFnName(t),
		"--timeout", "30",
		"--memory-size", "256",
		"--dead-letter-config", "TargetArn="+dlqARN,
	)
	if err != nil {
		return err
	}
	if timeout, _ := out["Timeout"].(float64); timeout != 30 {
		return fmt.Errorf("lambda UpdateFunctionConfiguration: expected Timeout=30, got %v", out["Timeout"])
	}
	if got := deadLetterTargetARN(out); got != dlqARN {
		return fmt.Errorf("lambda UpdateFunctionConfiguration: expected DeadLetterConfig.TargetArn=%s, got %q", dlqARN, got)
	}
	fetched, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "get-function-configuration", "--function-name", g.currentFnName(t))
	if err != nil {
		return err
	}
	if got := deadLetterTargetARN(fetched); got != dlqARN {
		return fmt.Errorf("lambda UpdateFunctionConfiguration: expected the stored DeadLetterConfig.TargetArn=%s, got %q", dlqARN, got)
	}
	return nil
}

// deadLetterTargetARN reads DeadLetterConfig.TargetArn out of a decoded
// function-configuration document, returning "" when the block is absent.
func deadLetterTargetARN(cfg map[string]any) string {
	block, _ := cfg["DeadLetterConfig"].(map[string]any)
	arn, _ := block["TargetArn"].(string)
	return arn
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
	// Wait like every other invoke test: a function is Pending until its
	// runtime image is available, and invoking a Pending function is an
	// InvalidParameterValueException on the emulator (a 409 on real AWS —
	// AWS's own guidance is to use the function-active waiter). DryRun was
	// the only invoke without the wait, and as the group's first test it hit
	// the cold-pull window on every CI run (plan item R7).
	if err := g.waitFunctionActive(t); err != nil {
		return err
	}
	return awscli.Run(t.Endpoint, t.Region,
		"lambda", "invoke",
		"--function-name", g.currentFnName(t),
		"--invocation-type", "DryRun",
		"/dev/null",
	)
}

func (g *lambdaGroup) waitFunctionActive(t *harness.TestContext) error {
	// 60 x 500ms of sleep, plus ~1s of aws-cli startup per probe: enough to
	// sit out a cold runtime-image pull on a loaded CI runner. AWS's own
	// function-active-v2 waiter allows five minutes; the previous 20 x 200ms
	// budget could be outlived by any pull the runner had not cached.
	for i := 0; i < 60; i++ {
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
		// Failed is terminal — waiting out the rest of the budget just buries
		// the real error, so surface the state reason immediately.
		if state == "Failed" {
			reason, _ := cfg["StateReason"].(string)
			return fmt.Errorf("lambda: function %s entered Failed state: %s", g.currentFnName(t), reason)
		}
		time.Sleep(500 * time.Millisecond)
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

// ─── lambda-invoke-error ─────────────────────────────────────────────────────

func (g *lambdaGroup) setupInvokeError(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-lmbd-inverr", t.RunID)
	t.Set("fn_name", name)
	zipPath, err := g.lambdaErrorZipFile(t)
	if err != nil {
		return fmt.Errorf("lambda setupInvokeError: %w", err)
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

// InvokeWithError proves a handler failure is reported the way AWS reports
// it: `lambda invoke` itself succeeds with StatusCode 200, FunctionError names
// the failure class, and the payload file carries the error document.
func (g *lambdaGroup) InvokeWithError(_ context.Context, t *harness.TestContext) error {
	if err := g.waitFunctionActive(t); err != nil {
		return err
	}
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("invoke-err-%s.json", t.RunID))
	defer os.Remove(outFile)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"lambda", "invoke",
		"--function-name", g.currentFnName(t),
		"--payload", `{}`,
		outFile,
	)
	if err != nil {
		return err
	}
	if code, _ := out["StatusCode"].(float64); code != 200 {
		return fmt.Errorf("InvokeWithError: expected StatusCode 200, got %v", out["StatusCode"])
	}
	if fe, _ := out["FunctionError"].(string); fe != "Unhandled" {
		return fmt.Errorf("InvokeWithError: expected FunctionError=Unhandled for a raising handler, got %q in %v", fe, out)
	}
	body, err := os.ReadFile(outFile)
	if err != nil {
		return fmt.Errorf("InvokeWithError: read payload: %w", err)
	}
	if !strings.Contains(string(body), "errorMessage") {
		return fmt.Errorf("InvokeWithError: expected an error document in the payload, got %s", body)
	}
	return nil
}
