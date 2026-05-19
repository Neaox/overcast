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
        ["CreateFunction"] = CreateFunctionAsync,
        ["GetFunction"] = GetFunctionAsync,
        ["ListFunctions"] = ListFunctionsAsync,
        ["UpdateFunctionCode"] = UpdateFunctionCodeAsync,
        ["UpdateFunctionConfiguration"] = UpdateFunctionConfigurationAsync,
        ["DeleteFunction"] = DeleteFunctionAsync,
        ["InvokeDryRun"] = InvokeDryRunAsync,
        ["InvokeSync"] = InvokeSyncAsync,
        ["InvokeAsync"] = InvokeAsyncAsync,
        ["InvokeWithResponseStream"] = InvokeWithResponseStreamAsync,
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
        ["lambda-invoke"] = SetupInvokeAsync,
        ["lambda-invoke-stream"] = SetupInvokeStreamAsync,
        ["lambda-aliases"] = SetupAliasesAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["lambda-crud"] = TeardownCrudAsync,
        ["lambda-invoke"] = TeardownInvokeAsync,
        ["lambda-invoke-stream"] = TeardownInvokeStreamAsync,
        ["lambda-aliases"] = TeardownAliasesAsync,
        ["lambda-layers"] = TeardownLayersAsync,
    };

    private static MemoryStream DummyZip() => new(Encoding.UTF8.GetBytes("dummy-zip-content"));

    private async Task<CreateFunctionResponse> CreateFunc(string name)
    {
        return await clients.Lambda().CreateFunctionAsync(new CreateFunctionRequest
        {
            FunctionName = name,
            Runtime = Runtime.Nodejs18X,
            Handler = "index.handler",
            Role = "arn:aws:iam::000000000000:role/lambda-role",
            Code = new FunctionCode { ZipFile = DummyZip() },
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
        await clients.Lambda().UpdateFunctionCodeAsync(new UpdateFunctionCodeRequest
        {
            FunctionName = name,
            ZipFile = DummyZip(),
        });
    }

    private async Task UpdateFunctionConfigurationAsync(TestContext context)
    {
        var name = RequireFuncName(context, "LambdaFuncName");
        await clients.Lambda().UpdateFunctionConfigurationAsync(new UpdateFunctionConfigurationRequest
        {
            FunctionName = name,
            Timeout = 30,
            MemorySize = 256,
            Environment = new Amazon.Lambda.Model.Environment
            {
                Variables = new Dictionary<string, string> { ["KEY"] = "VAL" },
            },
        });
        var getResp = await clients.Lambda().GetFunctionAsync(new GetFunctionRequest { FunctionName = name });
        Assertions.Equal(30, getResp.Configuration.Timeout, "UpdateFunctionConfiguration: Timeout");
        Assertions.Equal(256, getResp.Configuration.MemorySize, "UpdateFunctionConfiguration: MemorySize");
        Assertions.NotNull(getResp.Configuration.Environment, "UpdateFunctionConfiguration: Environment");
        Assertions.True(getResp.Configuration.Environment.Variables.TryGetValue("KEY", out var val) && val == "VAL", "UpdateFunctionConfiguration: env var KEY");
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
        });
        Assertions.Equal(200, response.StatusCode, "InvokeSync: StatusCode");
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
            Content = new LayerVersionContentInput { ZipFile = DummyZip() },
            CompatibleRuntimes = new List<string> { "nodejs18.x" },
        });
        Assertions.NotBlank(response.LayerVersionArn, "PublishLayerVersion: LayerVersionArn");
        Assertions.GreaterThan(0, response.Version ?? 0, "PublishLayerVersion: Version");
        context.Set("LambdaLayerName", layerName);
        context.Set("LambdaLayerVersion", response.Version);
    }

    private async Task ListLayersAsync(TestContext context)
    {
        var layerName = context.GetString("LambdaLayerName") ?? throw new InvalidOperationException("LambdaLayerName not set");
        var response = await clients.Lambda().ListLayersAsync(new ListLayersRequest { CompatibleRuntime = "nodejs18.x" });
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
