using Amazon.SimpleSystemsManagement;
using Amazon.SimpleSystemsManagement.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class SsmGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["PutParameter"] = PutParameterAsync,
        ["GetParameter"] = GetParameterAsync,
        ["PutParameterOverwrite"] = PutParameterOverwriteAsync,
        ["GetParameterHistory"] = GetParameterHistoryAsync,
        ["PutMultipleParameters"] = PutMultipleParametersAsync,
        ["GetParameters"] = GetParametersAsync,
        ["DescribeParameters"] = DescribeParametersAsync,
        ["TagParameter"] = TagParameterAsync,
        ["ListSSMTagsForResource"] = ListSSMTagsForResourceAsync,
        ["DeleteParameters"] = DeleteParametersAsync,
        ["PutSecureStringParameter"] = PutSecureStringParameterAsync,
        ["GetSecureStringParameter"] = GetSecureStringParameterAsync,
        ["GetSecureStringWithoutDecryption"] = GetSecureStringWithoutDecryptionAsync,
        ["GetParametersByPath"] = GetParametersByPathAsync,
        ["GetParametersByPathRecursive"] = GetParametersByPathRecursiveAsync,
        ["GetParametersByPathPaginated"] = GetParametersByPathPaginatedAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["ssm-parameters"] = SetupParametersAsync,
        ["ssm-secure"] = SetupSecureAsync,
        ["ssm-path"] = SetupPathAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["ssm-parameters"] = TeardownParametersAsync,
        ["ssm-secure"] = TeardownSecureAsync,
        ["ssm-path"] = TeardownPathAsync,
    };

    // ---- helpers ----

    private async Task DeleteParamsIfExistsAsync(List<string> names)
    {
        try
        {
            await clients.SSM().DeleteParametersAsync(new DeleteParametersRequest { Names = names });
        }
        catch
        {
        }
    }

    private static string RequireSsmParam(TestContext context, string key)
    {
        return context.GetString(key) ?? throw new InvalidOperationException($"{key} not set");
    }

    // ---- setups ----

    private async Task SetupParametersAsync(TestContext context)
    {
        var name = $"{context.RunId}-ssm-param";
        await clients.SSM().PutParameterAsync(new PutParameterRequest
        {
            Name = name,
            Value = "v1",
            Type = ParameterType.String,
            Overwrite = false,
        });
        context.Set("SsmBaseParam", name);
    }

    private async Task SetupSecureAsync(TestContext context)
    {
        var name = $"{context.RunId}-ssm-secure";
        await clients.SSM().PutParameterAsync(new PutParameterRequest
        {
            Name = name,
            Value = "my-secret",
            Type = ParameterType.SecureString,
            Overwrite = false,
        });
        context.Set("SsmSecureParam", name);
    }

    private async Task SetupPathAsync(TestContext context)
    {
        var name1 = $"/{context.RunId}/ssm-path/a";
        var name2 = $"/{context.RunId}/ssm-path/b";
        var ssm = clients.SSM();
        await ssm.PutParameterAsync(new PutParameterRequest
        {
            Name = name1,
            Value = "value1",
            Type = ParameterType.String,
            Overwrite = false,
        });
        await ssm.PutParameterAsync(new PutParameterRequest
        {
            Name = name2,
            Value = "value2",
            Type = ParameterType.String,
            Overwrite = false,
        });
        context.Set("SsmPathParam1", name1);
        context.Set("SsmPathParam2", name2);
    }

    // ---- teardowns ----

    private async Task TeardownParametersAsync(TestContext context)
    {
        var names = new List<string>();
        var baseParam = context.GetString("SsmBaseParam");
        if (baseParam is not null) names.Add(baseParam);
        names.Add($"{context.RunId}-ssm-put");
        var new1 = context.GetString("SsmNewParam1");
        if (new1 is not null) names.Add(new1);
        var new2 = context.GetString("SsmNewParam2");
        if (new2 is not null) names.Add(new2);
        await DeleteParamsIfExistsAsync(names);
    }

    private async Task TeardownSecureAsync(TestContext context)
    {
        var names = new List<string>();
        var secureParam = context.GetString("SsmSecureParam");
        if (secureParam is not null) names.Add(secureParam);
        names.Add($"{context.RunId}-ssm-secure-put");
        await DeleteParamsIfExistsAsync(names);
    }

    private async Task TeardownPathAsync(TestContext context)
    {
        var names = new List<string>();
        var param1 = context.GetString("SsmPathParam1");
        if (param1 is not null) names.Add(param1);
        var param2 = context.GetString("SsmPathParam2");
        if (param2 is not null) names.Add(param2);
        await DeleteParamsIfExistsAsync(names);
    }

    // ---- ssm-parameters ----

    private async Task PutParameterAsync(TestContext context)
    {
        var name = $"{context.RunId}-ssm-put";
        await clients.SSM().PutParameterAsync(new PutParameterRequest
        {
            Name = name,
            Value = "put-value",
            Type = ParameterType.String,
            Overwrite = false,
        });
        var getResponse = await clients.SSM().GetParameterAsync(new GetParameterRequest
        {
            Name = name,
        });
        Assertions.NotNull(getResponse.Parameter, "PutParameter: Parameter");
        Assertions.Equal("put-value", getResponse.Parameter.Value,
            $"PutParameter: expected put-value but was {getResponse.Parameter.Value} (runId={context.RunId})");
    }

    private async Task GetParameterAsync(TestContext context)
    {
        var name = RequireSsmParam(context, "SsmBaseParam");
        var response = await clients.SSM().GetParameterAsync(new GetParameterRequest
        {
            Name = name,
        });
        Assertions.NotNull(response.Parameter, "GetParameter: Parameter");
        Assertions.Equal("v1", response.Parameter.Value,
            $"GetParameter: expected v1 but was {response.Parameter.Value} (runId={context.RunId})");
    }

    private async Task PutParameterOverwriteAsync(TestContext context)
    {
        var name = RequireSsmParam(context, "SsmBaseParam");
        await clients.SSM().PutParameterAsync(new PutParameterRequest
        {
            Name = name,
            Value = "v2",
            Type = ParameterType.String,
            Overwrite = true,
        });
        var getResponse = await clients.SSM().GetParameterAsync(new GetParameterRequest
        {
            Name = name,
        });
        Assertions.NotNull(getResponse.Parameter, "PutParameterOverwrite: Parameter");
        Assertions.Equal("v2", getResponse.Parameter.Value,
            $"PutParameterOverwrite: expected v2 but was {getResponse.Parameter.Value} (runId={context.RunId})");
    }

    private async Task GetParameterHistoryAsync(TestContext context)
    {
        var name = RequireSsmParam(context, "SsmBaseParam");
        var response = await clients.SSM().GetParameterHistoryAsync(new GetParameterHistoryRequest
        {
            Name = name,
        });
        Assertions.NotNull(response.Parameters, "GetParameterHistory: Parameters");
        Assertions.GreaterThanOrEqual(2, response.Parameters.Count,
            $"GetParameterHistory: expected >= 2 versions but was {response.Parameters.Count} (runId={context.RunId})");
    }

    private async Task PutMultipleParametersAsync(TestContext context)
    {
        var name1 = $"{context.RunId}-ssm-new1";
        var name2 = $"{context.RunId}-ssm-new2";
        var ssm = clients.SSM();
        await ssm.PutParameterAsync(new PutParameterRequest
        {
            Name = name1,
            Value = "new1",
            Type = ParameterType.String,
            Overwrite = false,
        });
        await ssm.PutParameterAsync(new PutParameterRequest
        {
            Name = name2,
            Value = "new2",
            Type = ParameterType.String,
            Overwrite = false,
        });
        context.Set("SsmNewParam1", name1);
        context.Set("SsmNewParam2", name2);

        var getResponse = await ssm.GetParametersAsync(new GetParametersRequest
        {
            Names = [name1, name2],
        });
        Assertions.GreaterThanOrEqual(2, getResponse.Parameters.Count,
            $"PutMultipleParameters: expected >= 2 params but was {getResponse.Parameters.Count} (runId={context.RunId})");
    }

    private async Task GetParametersAsync(TestContext context)
    {
        var name1 = RequireSsmParam(context, "SsmNewParam1");
        var name2 = RequireSsmParam(context, "SsmNewParam2");
        var response = await clients.SSM().GetParametersAsync(new GetParametersRequest
        {
            Names = [name1, name2],
        });
        Assertions.GreaterThanOrEqual(2, response.Parameters.Count,
            $"GetParameters: expected >= 2 params but was {response.Parameters.Count} (runId={context.RunId})");
    }

    private async Task DescribeParametersAsync(TestContext context)
    {
        var response = await clients.SSM().DescribeParametersAsync(new DescribeParametersRequest
        {
            ParameterFilters =
            [
                new ParameterStringFilter
                {
                    Key = "Name",
                    Option = "Contains",
                    Values = [context.RunId],
                },
            ],
        });
        Assertions.GreaterThanOrEqual(1, response.Parameters?.Count ?? 0,
            $"DescribeParameters: expected >= 1 param but was {response.Parameters?.Count ?? 0} (runId={context.RunId})");
    }

    private async Task TagParameterAsync(TestContext context)
    {
        var name = RequireSsmParam(context, "SsmBaseParam");
        await clients.SSM().AddTagsToResourceAsync(new AddTagsToResourceRequest
        {
            ResourceType = ResourceTypeForTagging.Parameter,
            ResourceId = name,
            Tags =
            [
                new Tag { Key = "project", Value = "overcast" },
            ],
        });

        var listResponse = await clients.SSM().ListTagsForResourceAsync(new ListTagsForResourceRequest
        {
            ResourceType = ResourceTypeForTagging.Parameter,
            ResourceId = name,
        });
        Assertions.True(
            (listResponse.TagList ?? []).Any(t => t.Key == "project" && t.Value == "overcast"),
            $"TagParameter: tag project=overcast not found (runId={context.RunId})");
    }

    private async Task ListSSMTagsForResourceAsync(TestContext context)
    {
        var name = RequireSsmParam(context, "SsmBaseParam");
        var response = await clients.SSM().ListTagsForResourceAsync(new ListTagsForResourceRequest
        {
            ResourceType = ResourceTypeForTagging.Parameter,
            ResourceId = name,
        });
        Assertions.True(
            (response.TagList ?? []).Any(t => t.Key == "project"),
            $"ListSSMTagsForResource: tag project not found (runId={context.RunId})");
    }

    private async Task DeleteParametersAsync(TestContext context)
    {
        var baseParam = RequireSsmParam(context, "SsmBaseParam");
        var new1 = RequireSsmParam(context, "SsmNewParam1");
        var new2 = RequireSsmParam(context, "SsmNewParam2");
        var putParam = $"{context.RunId}-ssm-put";
        var names = new List<string> { baseParam, new1, new2, putParam };

        var deleteResponse = await clients.SSM().DeleteParametersAsync(new DeleteParametersRequest
        {
            Names = names,
        });
        Assertions.GreaterThanOrEqual(3, deleteResponse.DeletedParameters.Count,
            $"DeleteParameters: expected >= 3 deleted but was {deleteResponse.DeletedParameters.Count} (runId={context.RunId})");

        var describeResponse = await clients.SSM().DescribeParametersAsync(new DescribeParametersRequest
        {
            ParameterFilters =
            [
                new ParameterStringFilter
                {
                    Key = "Name",
                    Option = "Contains",
                    Values = [context.RunId],
                },
            ],
        });
        Assertions.True(
            (describeResponse.Parameters ?? []).All(p => !names.Contains(p.Name)),
            $"DeleteParameters: params still present after deletion (runId={context.RunId})");
    }

    // ---- ssm-secure ----

    private async Task PutSecureStringParameterAsync(TestContext context)
    {
        var name = $"{context.RunId}-ssm-secure-put";
        var response = await clients.SSM().PutParameterAsync(new PutParameterRequest
        {
            Name = name,
            Value = "secure-value",
            Type = ParameterType.SecureString,
            Overwrite = false,
        });
        Assertions.GreaterThan(0, response.Version ?? 0,
            $"PutSecureStringParameter: expected Version > 0 but was {response.Version} (runId={context.RunId})");

        var getResponse = await clients.SSM().GetParameterAsync(new GetParameterRequest
        {
            Name = name,
            WithDecryption = true,
        });
        Assertions.NotNull(getResponse.Parameter, "PutSecureStringParameter: Parameter");
        Assertions.Equal("secure-value", getResponse.Parameter.Value,
            $"PutSecureStringParameter: expected secure-value but was {getResponse.Parameter.Value} (runId={context.RunId})");
    }

    private async Task GetSecureStringParameterAsync(TestContext context)
    {
        var name = RequireSsmParam(context, "SsmSecureParam");
        var response = await clients.SSM().GetParameterAsync(new GetParameterRequest
        {
            Name = name,
            WithDecryption = true,
        });
        Assertions.NotNull(response.Parameter, "GetSecureStringParameter: Parameter");
        Assertions.Equal(ParameterType.SecureString, response.Parameter.Type,
            $"GetSecureStringParameter: expected SecureString but was {response.Parameter.Type} (runId={context.RunId})");
        Assertions.Equal("my-secret", response.Parameter.Value,
            $"GetSecureStringParameter: expected my-secret but was {response.Parameter.Value} (runId={context.RunId})");
    }

    private async Task GetSecureStringWithoutDecryptionAsync(TestContext context)
    {
        var name = RequireSsmParam(context, "SsmSecureParam");
        var response = await clients.SSM().GetParameterAsync(new GetParameterRequest
        {
            Name = name,
            WithDecryption = false,
        });
        Assertions.NotNull(response.Parameter, "GetSecureStringWithoutDecryption: Parameter");
        Assertions.True(
            response.Parameter.Value != "my-secret",
            $"GetSecureStringWithoutDecryption: plaintext returned for encrypted param (runId={context.RunId})");
    }

    // ---- ssm-path ----

    private async Task GetParametersByPathAsync(TestContext context)
    {
        var path = $"/{context.RunId}/ssm-path";
        var response = await clients.SSM().GetParametersByPathAsync(new GetParametersByPathRequest
        {
            Path = path,
            Recursive = false,
        });
        Assertions.GreaterThanOrEqual(1, response.Parameters.Count,
            $"GetParametersByPath: expected >= 1 param but was {response.Parameters.Count} (runId={context.RunId})");
    }

    private async Task GetParametersByPathRecursiveAsync(TestContext context)
    {
        var path = $"/{context.RunId}";
        var response = await clients.SSM().GetParametersByPathAsync(new GetParametersByPathRequest
        {
            Path = path,
            Recursive = true,
        });
        Assertions.GreaterThanOrEqual(2, response.Parameters.Count,
            $"GetParametersByPathRecursive: expected >= 2 params but was {response.Parameters.Count} (runId={context.RunId})");
    }

    private async Task GetParametersByPathPaginatedAsync(TestContext context)
    {
        var path = $"/{context.RunId}/ssm-path";
        var page1 = await clients.SSM().GetParametersByPathAsync(new GetParametersByPathRequest
        {
            Path = path,
            Recursive = false,
            MaxResults = 1,
        });
        Assertions.Equal(1, page1.Parameters.Count,
            $"GetParametersByPathPaginated page1: expected 1 but was {page1.Parameters.Count} (runId={context.RunId})");
        Assertions.NotBlank(page1.NextToken,
            $"GetParametersByPathPaginated page1: missing NextToken (runId={context.RunId})");

        var page2 = await clients.SSM().GetParametersByPathAsync(new GetParametersByPathRequest
        {
            Path = path,
            Recursive = false,
            MaxResults = 1,
            NextToken = page1.NextToken,
        });
        Assertions.GreaterThanOrEqual(1, page2.Parameters.Count,
            $"GetParametersByPathPaginated page2: expected >= 1 param but was {page2.Parameters.Count} (runId={context.RunId})");
    }
}
