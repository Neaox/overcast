using System.IO.Compression;
using System.Text;
using Amazon.Lambda;
using Amazon.Lambda.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class LambdaGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["lambda-crud:CreateFunction"] = CreateFunctionAsync,
        ["lambda-crud:GetFunction"] = GetFunctionAsync,
        ["lambda-crud:ListFunctions"] = ListFunctionsAsync,
        ["UpdateFunctionCode"] = UpdateFunctionCodeAsync,
        ["UpdateFunctionConfiguration"] = UpdateFunctionConfigurationAsync,
        ["lambda-crud:DeleteFunction"] = DeleteFunctionAsync,
        ["lambda-policy:AddPermission"] = AddPermissionAsync,
        ["lambda-policy:GetPolicy"] = GetPolicyAsync,
        ["lambda-policy:RemovePermission"] = RemovePermissionAsync,
        ["InvokeDryRun"] = InvokeDryRunAsync,
        ["InvokeSync"] = InvokeSyncAsync,
        ["InvokeAsync"] = InvokeAsyncAsync,
        ["InvokeWithResponseStream"] = InvokeWithResponseStreamAsync,
        ["InvokeWithError"] = InvokeWithErrorAsync,
        ["PublishVersion"] = PublishVersionAsync,
        ["ListVersionsByFunction"] = ListVersionsByFunctionAsync,
        ["CreateAlias"] = CreateAliasAsync,
        ["GetAlias"] = GetAliasAsync,
        ["ListAliases"] = ListAliasesAsync,
        ["UpdateAlias"] = UpdateAliasAsync,
        ["DeleteAlias"] = DeleteAliasAsync,
        ["PublishLayerVersion"] = PublishLayerVersionAsync,
        ["ListLayers"] = ListLayersAsync,
        ["DeleteLayerVersion"] = DeleteLayerVersionAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["lambda-crud"] = SetupCrudAsync,
        ["lambda-policy"] = SetupPolicyAsync,
        ["lambda-invoke"] = SetupInvokeAsync,
        ["lambda-invoke-stream"] = SetupInvokeStreamAsync,
        ["lambda-invoke-error"] = SetupInvokeErrorAsync,
        ["lambda-aliases"] = SetupAliasesAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["lambda-crud"] = TeardownCrudAsync,
        ["lambda-policy"] = TeardownPolicyAsync,
        ["lambda-invoke"] = TeardownInvokeAsync,
        ["lambda-invoke-stream"] = TeardownInvokeStreamAsync,
        ["lambda-invoke-error"] = TeardownInvokeErrorAsync,
        ["lambda-aliases"] = TeardownAliasesAsync,
        ["lambda-layers"] = TeardownLayersAsync,
    };

    /// <summary>
    /// Source of the default <c>index.handler</c>: a Node handler that answers
    /// every event with a 200 and echoes it back. Same shape as the go-sdk and
    /// python-sdk suites use.
    /// </summary>
    private const string HandlerJs =
        "exports.handler = async (e) => ({statusCode:200,body:JSON.stringify({ok:true,event:e})});\n";

    /// <summary>
    /// A real zip archive holding a single file. Every bundle this group
    /// uploads goes through here so the runtime can actually unpack and load
    /// it: a stand-in byte string is accepted by CreateFunction, but then the
    /// Invoke tests exercise "the runtime could not open the bundle" rather
    /// than a handler round-trip, and pass for the wrong reason (AWS answers
    /// a synchronous Invoke with HTTP 200 either way).
    /// </summary>
    private static MemoryStream MakeZip(string fileName, string source)
    {
        var buffer = new MemoryStream();
        using (var archive = new ZipArchive(buffer, ZipArchiveMode.Create, leaveOpen: true))
        {
            using var entry = archive.CreateEntry(fileName).Open();
            var bytes = Encoding.UTF8.GetBytes(source);
            entry.Write(bytes, 0, bytes.Length);
        }

        buffer.Position = 0;
        return buffer;
    }

    /// <summary>The default code bundle: <see cref="HandlerJs"/> as <c>index.js</c>.</summary>
    private static MemoryStream HandlerZip() => MakeZip("index.js", HandlerJs);

    /// <summary>
    /// A bundle whose handler throws on every invocation, for lambda-invoke-error:
    /// that test asserts on the payload the handler's own exception produces.
    /// </summary>
    private static MemoryStream ThrowingHandlerZip() => MakeZip("index.js",
        "exports.handler = async () => { throw new Error(\"compat: intentional failure\"); };\n");

    private async Task<CreateFunctionResponse> CreateFunc(string name, MemoryStream? code = null)
    {
        return await clients.Lambda().CreateFunctionAsync(new CreateFunctionRequest
        {
            FunctionName = name,
            Runtime = Runtime.Nodejs20X,
            Handler = "index.handler",
            Role = "arn:aws:iam::000000000000:role/lambda-role",
            Code = new FunctionCode { ZipFile = code ?? HandlerZip() },
        });
    }

    private async Task PollActiveAsync(string name)
    {
        for (var i = 0; i < 10; i++)
        {
            var resp = await clients.Lambda().GetFunctionAsync(new GetFunctionRequest { FunctionName = name });
            if (resp.Configuration.State == State.Active) break;
            await Task.Delay(500);
        }
    }

    private static string RequireFuncName(TestContext context, string key)
    {
        return context.GetString(key) ?? throw new InvalidOperationException($"{key} not set");
    }

    // ── lambda-crud ──

    private async Task SetupCrudAsync(TestContext context)
    {
        var name = $"{context.RunId}-lcrud";
        await CreateFunc(name);
        context.Set("LambdaFuncName", name);
    }

    private async Task CreateFunctionAsync(TestContext context)
    {
        var name = $"{context.RunId}-lcreate";
        await CreateFunc(name);
        try
        {
            var list = await clients.Lambda().ListFunctionsAsync(new ListFunctionsRequest());
            Assertions.True(list.Functions.Any(f => f.FunctionName == name), $"CreateFunction: {name} not found in ListFunctions (runId={context.RunId})");
        }
        finally
        {
            try { await clients.Lambda().DeleteFunctionAsync(new DeleteFunctionRequest { FunctionName = name }); } catch { }
        }
    }

    private async Task GetFunctionAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaFuncName");
        var response = await clients.Lambda().GetFunctionAsync(new GetFunctionRequest { FunctionName = name });
        Assertions.NotBlank(response.Configuration.FunctionArn, "GetFunction: FunctionArn");
    }

    private async Task ListFunctionsAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaFuncName");
        var response = await clients.Lambda().ListFunctionsAsync(new ListFunctionsRequest());
        Assertions.True(response.Functions.Any(f => f.FunctionName == name), $"ListFunctions: {name} not found (runId={context.RunId})");
    }

    private async Task UpdateFunctionCodeAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaFuncName");
        // A bundle that differs from the one CreateFunction uploaded, so the
        // update is not a no-op and the returned digest describes new bytes.
        var response = await clients.Lambda().UpdateFunctionCodeAsync(new UpdateFunctionCodeRequest
        {
            FunctionName = name,
            ZipFile = MakeZip("index.js", HandlerJs + "// updated\n"),
        });
        Assertions.NotBlank(response.CodeSha256, "UpdateFunctionCode: CodeSha256");
    }

    private async Task UpdateFunctionConfigurationAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaFuncName");

        // A real queue, so DeadLetterConfig names a target that exists rather
        // than a plausible-looking string.
        var queueName = $"{context.RunId}-lcrud-dlq";
        var queue = await clients.SQS().CreateQueueAsync(new Amazon.SQS.Model.CreateQueueRequest { QueueName = queueName });
        try
        {
            var qAttrs = await clients.SQS().GetQueueAttributesAsync(new Amazon.SQS.Model.GetQueueAttributesRequest
            {
                QueueUrl = queue.QueueUrl,
                AttributeNames = ["QueueArn"],
            });
            var dlqArn = qAttrs.Attributes["QueueArn"];

            // DeadLetterConfig rides along because it is the member that used to
            // answer 501 here, which failed every `cdk deploy` of a function with
            // a DLQ. Both the response and a later read are checked: an update
            // that answers 200 and drops the property is the same bug wearing a
            // better status code.
            var updated = await clients.Lambda().UpdateFunctionConfigurationAsync(new UpdateFunctionConfigurationRequest
            {
                FunctionName = name,
                Timeout = 30,
                MemorySize = 256,
                Environment = new Amazon.Lambda.Model.Environment
                {
                    Variables = new Dictionary<string, string> { ["KEY"] = "VAL" },
                },
                DeadLetterConfig = new DeadLetterConfig { TargetArn = dlqArn },
            });
            Assertions.NotNull(updated.DeadLetterConfig, "UpdateFunctionConfiguration: response DeadLetterConfig");
            Assertions.Equal(dlqArn, updated.DeadLetterConfig.TargetArn, "UpdateFunctionConfiguration: response DeadLetterConfig.TargetArn");

            var getResp = await clients.Lambda().GetFunctionAsync(new GetFunctionRequest { FunctionName = name });
            Assertions.Equal(30, getResp.Configuration.Timeout, "UpdateFunctionConfiguration: Timeout");
            Assertions.Equal(256, getResp.Configuration.MemorySize, "UpdateFunctionConfiguration: MemorySize");
            Assertions.NotNull(getResp.Configuration.Environment, "UpdateFunctionConfiguration: Environment");
            Assertions.True(getResp.Configuration.Environment.Variables.TryGetValue("KEY", out var val) && val == "VAL", "UpdateFunctionConfiguration: env var KEY");
            Assertions.NotNull(getResp.Configuration.DeadLetterConfig, "UpdateFunctionConfiguration: stored DeadLetterConfig");
            Assertions.Equal(dlqArn, getResp.Configuration.DeadLetterConfig.TargetArn, "UpdateFunctionConfiguration: stored DeadLetterConfig.TargetArn");
        }
        finally
        {
            try
            {
                await clients.SQS().DeleteQueueAsync(new Amazon.SQS.Model.DeleteQueueRequest { QueueUrl = queue.QueueUrl });
            }
            catch (Amazon.SQS.AmazonSQSException)
            {
                // Teardown is best effort.
            }
        }
    }

    private async Task DeleteFunctionAsync(TestContext context)
    {
        var name = $"{context.RunId}-ldel";
        await CreateFunc(name);
        await clients.Lambda().DeleteFunctionAsync(new DeleteFunctionRequest { FunctionName = name });
        var list = await clients.Lambda().ListFunctionsAsync(new ListFunctionsRequest());
        Assertions.False(list.Functions.Any(f => f.FunctionName == name), $"DeleteFunction: {name} still present (runId={context.RunId})");
    }

    private async Task TeardownCrudAsync(TestContext context)
    {
        var name = context.GetString("LambdaFuncName");
        if (!string.IsNullOrWhiteSpace(name))
        {
            try { await clients.Lambda().DeleteFunctionAsync(new DeleteFunctionRequest { FunctionName = name }); } catch { }
        }
    }

    // ── lambda-policy ──────────────────────────────────────────────────────

    private async Task SetupPolicyAsync(TestContext context)
    {
        var name = $"{context.RunId}-fn-policy";
        await CreateFunc(name);
        context.Set("LambdaPolicyFuncName", name);
    }

    private async Task AddPermissionAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaPolicyFuncName");
        var response = await clients.Lambda().AddPermissionAsync(new AddPermissionRequest
        {
            FunctionName = name,
            StatementId = "allow-s3",
            Action = "lambda:InvokeFunction",
            Principal = "s3.amazonaws.com",
            SourceAccount = "000000000000",
        });
        Assertions.True(response.Statement?.Contains("\"Sid\":\"allow-s3\"", StringComparison.Ordinal) == true,
            "AddPermission: statement missing allow-s3 SID");
        var policy = await clients.Lambda().GetPolicyAsync(new GetPolicyRequest { FunctionName = name });
        Assertions.True(policy.Policy?.Contains("\"Sid\":\"allow-s3\"", StringComparison.Ordinal) == true,
            "AddPermission: statement missing from GetPolicy");
    }

    private async Task GetPolicyAsync(TestContext context)
    {
        var response = await clients.Lambda().GetPolicyAsync(new GetPolicyRequest
        {
            FunctionName = RequireFuncName(context, "LambdaPolicyFuncName"),
        });
        Assertions.True(response.Policy?.Contains("\"Sid\":\"allow-s3\"", StringComparison.Ordinal) == true,
            "GetPolicy: allow-s3 statement missing");
        Assertions.NotBlank(response.RevisionId, "GetPolicy: RevisionId");
    }

    private async Task RemovePermissionAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaPolicyFuncName");
        await clients.Lambda().RemovePermissionAsync(new RemovePermissionRequest
        {
            FunctionName = name,
            StatementId = "allow-s3",
        });
        try
        {
            await clients.Lambda().GetPolicyAsync(new GetPolicyRequest { FunctionName = name });
            throw new InvalidOperationException("RemovePermission: policy still exists");
        }
        catch (ResourceNotFoundException)
        {
            // Expected after the final statement is removed.
        }
    }

    private async Task TeardownPolicyAsync(TestContext context)
    {
        var name = context.GetString("LambdaPolicyFuncName");
        if (!string.IsNullOrWhiteSpace(name))
        {
            try { await clients.Lambda().DeleteFunctionAsync(new DeleteFunctionRequest { FunctionName = name }); } catch { }
        }
    }

    // ── lambda-invoke ──

    private async Task SetupInvokeAsync(TestContext context)
    {
        var name = $"{context.RunId}-linvoke";
        await CreateFunc(name);
        await PollActiveAsync(name);
        context.Set("LambdaInvokeFuncName", name);
    }

    private async Task InvokeDryRunAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaInvokeFuncName");
        var response = await clients.Lambda().InvokeAsync(new InvokeRequest
        {
            FunctionName = name,
            InvocationType = InvocationType.DryRun,
        });
        Assertions.Equal(204, response.StatusCode, "InvokeDryRun: StatusCode");
    }

    private async Task InvokeSyncAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaInvokeFuncName");
        var response = await clients.Lambda().InvokeAsync(new InvokeRequest
        {
            FunctionName = name,
            InvocationType = InvocationType.RequestResponse,
            Payload = "{\"hello\":\"world\"}",
        });
        Assertions.Equal(200, response.StatusCode, "InvokeSync: StatusCode");
        // HTTP 200 alone does not prove the handler ran: a bundle the runtime
        // cannot load also answers 200, with X-Amz-Function-Error set. The
        // handler's own payload is the evidence of a round-trip.
        Assertions.True(string.IsNullOrEmpty(response.FunctionError),
            $"InvokeSync: unexpected FunctionError={response.FunctionError} (runId={context.RunId})");
        var body = response.Payload is null ? "" : Encoding.UTF8.GetString(response.Payload.ToArray());
        Assertions.True(body.Contains("\"statusCode\":200", StringComparison.Ordinal),
            $"InvokeSync: expected handler payload, got <{body}> (runId={context.RunId})");
    }

    private async Task InvokeAsyncAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaInvokeFuncName");
        var response = await clients.Lambda().InvokeAsync(new InvokeRequest
        {
            FunctionName = name,
            InvocationType = InvocationType.Event,
        });
        Assertions.Equal(202, response.StatusCode, "InvokeAsync: StatusCode");
    }

    private async Task TeardownInvokeAsync(TestContext context)
    {
        var name = context.GetString("LambdaInvokeFuncName");
        if (!string.IsNullOrWhiteSpace(name))
        {
            try { await clients.Lambda().DeleteFunctionAsync(new DeleteFunctionRequest { FunctionName = name }); } catch { }
        }
    }

    // ── lambda-invoke-stream ──

    private async Task SetupInvokeStreamAsync(TestContext context)
    {
        var name = $"{context.RunId}-lstream";
        await CreateFunc(name);
        await PollActiveAsync(name);
        context.Set("LambdaStreamFuncName", name);
    }

    private async Task InvokeWithResponseStreamAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaStreamFuncName");
        var response = await clients.Lambda().InvokeWithResponseStreamAsync(new InvokeWithResponseStreamRequest
        {
            FunctionName = name,
        });
        Assertions.NotNull(response, "InvokeWithResponseStream: response");
    }

    private async Task TeardownInvokeStreamAsync(TestContext context)
    {
        var name = context.GetString("LambdaStreamFuncName");
        if (!string.IsNullOrWhiteSpace(name))
        {
            try { await clients.Lambda().DeleteFunctionAsync(new DeleteFunctionRequest { FunctionName = name }); } catch { }
        }
    }

    // ── lambda-invoke-error ──
    //
    // A handler that throws unconditionally. AWS still answers the synchronous
    // Invoke with HTTP 200 — the failure is signalled by the
    // X-Amz-Function-Error header, surfaced as FunctionError on the response,
    // not as a thrown SDK exception.

    private async Task SetupInvokeErrorAsync(TestContext context)
    {
        var name = $"{context.RunId}-linverr";
        await CreateFunc(name, ThrowingHandlerZip());
        await PollActiveAsync(name);
        context.Set("LambdaInvokeErrFuncName", name);
    }

    private async Task InvokeWithErrorAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaInvokeErrFuncName");
        var response = await clients.Lambda().InvokeAsync(new InvokeRequest
        {
            FunctionName = name,
            InvocationType = InvocationType.RequestResponse,
            Payload = "{}",
        });
        Assertions.Equal(200, response.StatusCode, "InvokeWithError: StatusCode");
        Assertions.Equal("Unhandled", response.FunctionError, "InvokeWithError: FunctionError for a throwing handler");
        var body = response.Payload is null ? "" : Encoding.UTF8.GetString(response.Payload.ToArray());
        Assertions.True(body.Contains("errorMessage", StringComparison.Ordinal),
            $"InvokeWithError: expected payload to contain errorMessage, got <{body}> (runId={context.RunId})");
    }

    private async Task TeardownInvokeErrorAsync(TestContext context)
    {
        var name = context.GetString("LambdaInvokeErrFuncName");
        if (!string.IsNullOrWhiteSpace(name))
        {
            try { await clients.Lambda().DeleteFunctionAsync(new DeleteFunctionRequest { FunctionName = name }); } catch { }
        }
    }

    // ── lambda-aliases ──

    private async Task SetupAliasesAsync(TestContext context)
    {
        var name = $"{context.RunId}-lalias";
        await CreateFunc(name);
        context.Set("LambdaAliasFuncName", name);
    }

    private async Task PublishVersionAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaAliasFuncName");
        var response = await clients.Lambda().PublishVersionAsync(new PublishVersionRequest { FunctionName = name });
        Assertions.NotBlank(response.Version, "PublishVersion: Version");
        context.Set("LambdaVersion", response.Version);
    }

    private async Task ListVersionsByFunctionAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaAliasFuncName");
        var version = context.GetString("LambdaVersion") ?? throw new InvalidOperationException("LambdaVersion not set");
        var response = await clients.Lambda().ListVersionsByFunctionAsync(new ListVersionsByFunctionRequest { FunctionName = name });
        Assertions.True(response.Versions.Any(v => v.Version == version), $"ListVersionsByFunction: version {version} not found (runId={context.RunId})");
    }

    private async Task CreateAliasAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaAliasFuncName");
        var version = context.GetString("LambdaVersion") ?? throw new InvalidOperationException("LambdaVersion not set");
        await clients.Lambda().CreateAliasAsync(new CreateAliasRequest
        {
            FunctionName = name,
            Name = "live",
            FunctionVersion = version,
        });
        var alias = await clients.Lambda().GetAliasAsync(new GetAliasRequest { FunctionName = name, Name = "live" });
        Assertions.NotBlank(alias.AliasArn, "CreateAlias: AliasArn");
    }

    private async Task GetAliasAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaAliasFuncName");
        var response = await clients.Lambda().GetAliasAsync(new GetAliasRequest { FunctionName = name, Name = "live" });
        Assertions.NotBlank(response.AliasArn, "GetAlias: AliasArn");
        Assertions.Equal("live", response.Name, "GetAlias: Name");
    }

    private async Task ListAliasesAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaAliasFuncName");
        var response = await clients.Lambda().ListAliasesAsync(new ListAliasesRequest { FunctionName = name });
        Assertions.True(response.Aliases.Any(a => a.Name == "live"), $"ListAliases: alias 'live' not found (runId={context.RunId})");
    }

    private async Task UpdateAliasAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaAliasFuncName");
        await clients.Lambda().UpdateAliasAsync(new UpdateAliasRequest
        {
            FunctionName = name,
            Name = "live",
            Description = "production alias",
        });
        var alias = await clients.Lambda().GetAliasAsync(new GetAliasRequest { FunctionName = name, Name = "live" });
        Assertions.Equal("production alias", alias.Description, "UpdateAlias: Description");
    }

    private async Task DeleteAliasAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaAliasFuncName");
        await clients.Lambda().DeleteAliasAsync(new DeleteAliasRequest { FunctionName = name, Name = "live" });
        var list = await clients.Lambda().ListAliasesAsync(new ListAliasesRequest { FunctionName = name });
        Assertions.False(list.Aliases.Any(a => a.Name == "live"), $"DeleteAlias: alias 'live' still present (runId={context.RunId})");
    }

    private async Task TeardownAliasesAsync(TestContext context)
    {
        var name = context.GetString("LambdaAliasFuncName");
        if (!string.IsNullOrWhiteSpace(name))
        {
            try { await clients.Lambda().DeleteFunctionAsync(new DeleteFunctionRequest { FunctionName = name }); } catch { }
        }
    }

    // ── lambda-layers ──

    private async Task PublishLayerVersionAsync(TestContext context)
    {
        var layerName = $"{context.RunId}-llayer";
        var response = await clients.Lambda().PublishLayerVersionAsync(new PublishLayerVersionRequest
        {
            LayerName = layerName,
            Content = new LayerVersionContentInput { ZipFile = MakeZip("lib/helper.js", "exports.hello = () => 'hello';\n") },
            CompatibleRuntimes = new List<string> { "nodejs20.x" },
        });
        Assertions.NotBlank(response.LayerVersionArn, "PublishLayerVersion: LayerVersionArn");
        Assertions.GreaterThan(0, response.Version ?? 0, "PublishLayerVersion: Version");
        context.Set("LambdaLayerName", layerName);
        context.Set("LambdaLayerVersion", response.Version);
    }

    private async Task ListLayersAsync(TestContext context)
    {
        var layerName = context.GetString("LambdaLayerName") ?? throw new InvalidOperationException("LambdaLayerName not set");
        var response = await clients.Lambda().ListLayersAsync(new ListLayersRequest { CompatibleRuntime = "nodejs20.x" });
        Assertions.True(response.Layers.Any(l => l.LayerName == layerName), $"ListLayers: layer {layerName} not found (runId={context.RunId})");
    }

    private async Task DeleteLayerVersionAsync(TestContext context)
    {
        var layerName = context.GetString("LambdaLayerName") ?? throw new InvalidOperationException("LambdaLayerName not set");
        var version = context.Get<long>("LambdaLayerVersion");
        await clients.Lambda().DeleteLayerVersionAsync(new DeleteLayerVersionRequest
        {
            LayerName = layerName,
            VersionNumber = version,
        });
        context.Set("LambdaLayerName", null);
    }

    private async Task TeardownLayersAsync(TestContext context)
    {
        var layerName = context.GetString("LambdaLayerName");
        if (string.IsNullOrWhiteSpace(layerName)) return;
        var version = context.Get<long>("LambdaLayerVersion");
        try { await clients.Lambda().DeleteLayerVersionAsync(new DeleteLayerVersionRequest { LayerName = layerName, VersionNumber = version }); } catch { }
    }
}
