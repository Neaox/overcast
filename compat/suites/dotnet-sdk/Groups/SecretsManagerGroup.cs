using Amazon.SecretsManager;
using Amazon.SecretsManager.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class SecretsManagerGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["CreateSecret"] = CreateSecretAsync,
        ["GetSecretValue"] = GetSecretValueAsync,
        ["DescribeSecret"] = DescribeSecretAsync,
        ["PutSecretValue"] = PutSecretValueAsync,
        ["ListSecretVersionIds"] = ListSecretVersionIdsAsync,
        ["UpdateSecret"] = UpdateSecretAsync,
        ["TagResource"] = TagResourceAsync,
        ["UntagResource"] = UntagResourceAsync,
        ["GetRandomPassword"] = GetRandomPasswordAsync,
        ["BatchGetSecretValue"] = BatchGetSecretValueAsync,
        ["ListSecrets"] = ListSecretsAsync,
        ["DeleteSecret"] = DeleteSecretAsync,
        ["RotateSecret"] = RotateSecretAsync,
        ["CancelRotateSecret"] = CancelRotateSecretAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["secretsmanager-crud"] = SetupCrudAsync,
        ["secretsmanager-rotate"] = SetupRotateAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["secretsmanager-crud"] = context => ForceDeleteSecretAsync(context.GetString("SmSecretName")),
        ["secretsmanager-rotate"] = context => ForceDeleteSecretAsync(context.GetString("SmRotateSecretName")),
    };

    // ---- helpers ----

    private async Task ForceDeleteSecretAsync(string? secretName)
    {
        if (string.IsNullOrWhiteSpace(secretName))
        {
            return;
        }

        try
        {
            await clients.SecretsManager().DeleteSecretAsync(new DeleteSecretRequest
            {
                SecretId = secretName,
                ForceDeleteWithoutRecovery = true,
            });
        }
        catch
        {
        }
    }

    private static string RequireSecretName(TestContext context, string key)
    {
        return context.GetString(key) ?? throw new InvalidOperationException($"{key} not set");
    }

    // ---- setups ----

    private async Task SetupCrudAsync(TestContext context)
    {
        var secretName = $"{context.RunId}-sm-crud";
        var response = await clients.SecretsManager().CreateSecretAsync(new CreateSecretRequest
        {
            Name = secretName,
            SecretString = "initial-value",
        });
        context.Set("SmSecretName", secretName);
        context.Set("SmSecretArn", response.ARN);
    }

    private async Task SetupRotateAsync(TestContext context)
    {
        var secretName = $"{context.RunId}-sm-rotate";
        await clients.SecretsManager().CreateSecretAsync(new CreateSecretRequest
        {
            Name = secretName,
            SecretString = "rotate-me",
        });
        context.Set("SmRotateSecretName", secretName);
    }

    // ---- secretsmanager-crud ----

    private async Task CreateSecretAsync(TestContext context)
    {
        var secretName = $"{context.RunId}-sm-create";
        await clients.SecretsManager().CreateSecretAsync(new CreateSecretRequest
        {
            Name = secretName,
            SecretString = "test-secret",
        });
        try
        {
            var listResponse = await clients.SecretsManager().ListSecretsAsync(new ListSecretsRequest());
            Assertions.True(
                listResponse.SecretList.Any(s => s.Name == secretName),
                $"CreateSecret: secret {secretName} not found in ListSecrets (runId={context.RunId})");
        }
        finally
        {
            await ForceDeleteSecretAsync(secretName);
        }
    }

    private async Task GetSecretValueAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmSecretName");
        var response = await clients.SecretsManager().GetSecretValueAsync(new GetSecretValueRequest
        {
            SecretId = secretName,
        });
        Assertions.NotBlank(response.SecretString, "GetSecretValue: SecretString");
        Assertions.Equal("initial-value", response.SecretString,
            $"GetSecretValue: expected initial-value but was {response.SecretString} (runId={context.RunId})");
    }

    private async Task DescribeSecretAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmSecretName");
        var response = await clients.SecretsManager().DescribeSecretAsync(new DescribeSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.NotBlank(response.Name, "DescribeSecret: Name");
        Assertions.Equal(secretName, response.Name,
            $"DescribeSecret: expected {secretName} but was {response.Name} (runId={context.RunId})");
    }

    private async Task PutSecretValueAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmSecretName");
        await clients.SecretsManager().PutSecretValueAsync(new PutSecretValueRequest
        {
            SecretId = secretName,
            SecretString = "updated-value",
        });

        var getResponse = await clients.SecretsManager().GetSecretValueAsync(new GetSecretValueRequest
        {
            SecretId = secretName,
        });
        Assertions.NotBlank(getResponse.SecretString, "PutSecretValue: SecretString");
        Assertions.Equal("updated-value", getResponse.SecretString,
            $"PutSecretValue: expected updated-value but was {getResponse.SecretString} (runId={context.RunId})");
    }

    private async Task ListSecretVersionIdsAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmSecretName");
        var response = await clients.SecretsManager().ListSecretVersionIdsAsync(new ListSecretVersionIdsRequest
        {
            SecretId = secretName,
        });
        Assertions.NotNull(response.Versions, "ListSecretVersionIds: Versions");
        Assertions.GreaterThanOrEqual(2, response.Versions.Count,
            $"ListSecretVersionIds: expected >= 2 versions but was {response.Versions.Count} (runId={context.RunId})");
    }

    private async Task UpdateSecretAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmSecretName");
        await clients.SecretsManager().UpdateSecretAsync(new UpdateSecretRequest
        {
            SecretId = secretName,
            Description = "compat-description",
        });

        var desc = await clients.SecretsManager().DescribeSecretAsync(new DescribeSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.Equal("compat-description", desc.Description,
            $"UpdateSecret: expected compat-description but was {desc.Description} (runId={context.RunId})");
    }

    private async Task TagResourceAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmSecretName");
        await clients.SecretsManager().TagResourceAsync(new TagResourceRequest
        {
            SecretId = secretName,
            Tags =
            [
                new Tag { Key = "project", Value = "overcast" },
            ],
        });

        var desc = await clients.SecretsManager().DescribeSecretAsync(new DescribeSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.NotNull(desc.Tags, "TagResource: Tags");
        Assertions.True(
            desc.Tags.Any(t => t.Key == "project" && t.Value == "overcast"),
            $"TagResource: tag project=overcast not found (runId={context.RunId})");
    }

    private async Task UntagResourceAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmSecretName");
        await clients.SecretsManager().UntagResourceAsync(new UntagResourceRequest
        {
            SecretId = secretName,
            TagKeys = ["project"],
        });

        var desc = await clients.SecretsManager().DescribeSecretAsync(new DescribeSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.False(
            (desc.Tags ?? []).Any(t => t.Key == "project"),
            $"UntagResource: project tag still present after untag (runId={context.RunId})");
    }

    private async Task GetRandomPasswordAsync(TestContext context)
    {
        var response = await clients.SecretsManager().GetRandomPasswordAsync(new GetRandomPasswordRequest
        {
            PasswordLength = 20,
        });
        Assertions.NotBlank(response.RandomPassword, "GetRandomPassword: RandomPassword");
        Assertions.Equal(20, response.RandomPassword.Length,
            $"GetRandomPassword: expected length 20 but was {response.RandomPassword.Length} (runId={context.RunId})");
    }

    private async Task BatchGetSecretValueAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmSecretName");
        var response = await clients.SecretsManager().BatchGetSecretValueAsync(new BatchGetSecretValueRequest
        {
            Filters =
            [
                new Filter
                {
                    Key = FilterNameStringType.Name,
                    Values = [secretName],
                },
            ],
        });
        Assertions.NotNull(response.SecretValues, "BatchGetSecretValue: SecretValues");
        Assertions.True(
            response.SecretValues.Any(s => s.Name == secretName),
            $"BatchGetSecretValue: secret {secretName} not found in batch results (runId={context.RunId})");
    }

    private async Task ListSecretsAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmSecretName");
        var response = await clients.SecretsManager().ListSecretsAsync(new ListSecretsRequest());
        Assertions.True(
            response.SecretList.Any(s => s.Name == secretName),
            $"ListSecrets: secret {secretName} not found (runId={context.RunId})");
    }

    private async Task DeleteSecretAsync(TestContext context)
    {
        var secretName = $"{context.RunId}-sm-del";
        await clients.SecretsManager().CreateSecretAsync(new CreateSecretRequest
        {
            Name = secretName,
            SecretString = "to-be-deleted",
        });
        await clients.SecretsManager().DeleteSecretAsync(new DeleteSecretRequest
        {
            SecretId = secretName,
            ForceDeleteWithoutRecovery = true,
        });
        var listResponse = await clients.SecretsManager().ListSecretsAsync(new ListSecretsRequest());
        Assertions.False(
            listResponse.SecretList.Any(s => s.Name == secretName),
            $"DeleteSecret: secret {secretName} still present after deletion (runId={context.RunId})");
    }

    // ---- secretsmanager-rotate ----

    private async Task RotateSecretAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmRotateSecretName");
        var response = await clients.SecretsManager().RotateSecretAsync(new RotateSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.NotBlank(response.ARN, "RotateSecret: ARN");
        Assertions.NotBlank(response.Name, "RotateSecret: Name");
    }

    private async Task CancelRotateSecretAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmRotateSecretName");
        var response = await clients.SecretsManager().CancelRotateSecretAsync(new CancelRotateSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.NotBlank(response.ARN, "CancelRotateSecret: ARN");
        Assertions.NotBlank(response.Name, "CancelRotateSecret: Name");
    }
}
