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
        ["secretsmanager-crud:TagResource"] = TagResourceAsync,
        ["secretsmanager-crud:UntagResource"] = UntagResourceAsync,
        ["GetRandomPassword"] = GetRandomPasswordAsync,
        ["BatchGetSecretValue"] = BatchGetSecretValueAsync,
        ["ListSecrets"] = ListSecretsAsync,
        ["DeleteSecret"] = DeleteSecretAsync,
        ["RotateSecretWithoutLambda"] = RotateSecretWithoutLambdaAsync,
        ["RotateSecret"] = RotateSecretAsync,
        ["PutSecretValuePending"] = PutSecretValuePendingAsync,
        ["GetSecretValueByStage"] = GetSecretValueByStageAsync,
        ["UpdateSecretVersionStage"] = UpdateSecretVersionStageAsync,
        ["CancelRotateSecret"] = CancelRotateSecretAsync,
        ["ValidateResourcePolicy"] = ValidateResourcePolicyAsync,
        ["PutResourcePolicy"] = PutResourcePolicyAsync,
        ["GetResourcePolicy"] = GetResourcePolicyAsync,
        ["DeleteResourcePolicy"] = DeleteResourcePolicyAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["secretsmanager-crud"] = SetupCrudAsync,
        ["secretsmanager-rotate"] = SetupRotateAsync,
        ["secretsmanager-policy"] = SetupPolicyAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["secretsmanager-crud"] = context => ForceDeleteSecretAsync(context.GetString("SmSecretName")),
        ["secretsmanager-rotate"] = context => ForceDeleteSecretAsync(context.GetString("SmRotateSecretName")),
        ["secretsmanager-policy"] = context => ForceDeleteSecretAsync(context.GetString("SmPolicySecretName")),
    };

    /// <summary>
    /// AWS wants a ClientRequestToken of 32-64 characters, and the token becomes
    /// the version ID, so a fixed UUID keeps the assertions readable.
    /// </summary>
    private const string PendingToken = "0f9c1d2e-3a4b-4c5d-8e6f-7a8b9c0d1e2f";

    /// <summary>A minimal, well-formed secret resource policy.</summary>
    private const string CompatResourcePolicy =
        """{"Version":"2012-10-17","Statement":[{"Sid":"OvercastCompatRead","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"secretsmanager:GetSecretValue","Resource":"*"}]}""";

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

    /// <summary>
    /// A placeholder rotation function. This group always sets
    /// RotateImmediately=false, so it is never invoked: driving the four-step
    /// protocol needs a real rotation Lambda and belongs with the
    /// Docker-dependent Lambda groups.
    /// </summary>
    private static string RotationLambdaArn(TestContext context) =>
        $"arn:aws:lambda:{context.Region}:000000000000:function:oc-rotate-fn";

    private async Task RotateSecretWithoutLambdaAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmRotateSecretName");
        try
        {
            await clients.SecretsManager().RotateSecretAsync(new RotateSecretRequest
            {
                SecretId = secretName,
                RotationRules = new RotationRulesType { AutomaticallyAfterDays = 30 },
            });
        }
        catch (AmazonSecretsManagerException e)
        {
            Assertions.Equal("InvalidRequestException", e.ErrorCode,
                $"RotateSecretWithoutLambda: expected InvalidRequestException but was {e.ErrorCode} (runId={context.RunId})");
            return;
        }

        throw new InvalidOperationException(
            $"RotateSecretWithoutLambda: expected InvalidRequestException, call succeeded (runId={context.RunId})");
    }

    private async Task RotateSecretAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmRotateSecretName");
        var lambdaArn = RotationLambdaArn(context);
        var response = await clients.SecretsManager().RotateSecretAsync(new RotateSecretRequest
        {
            SecretId = secretName,
            RotationLambdaARN = lambdaArn,
            RotationRules = new RotationRulesType { AutomaticallyAfterDays = 30 },
            RotateImmediately = false,
        });
        Assertions.NotBlank(response.ARN, "RotateSecret: ARN");

        var desc = await clients.SecretsManager().DescribeSecretAsync(new DescribeSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.True(desc.RotationEnabled ?? false,
            $"RotateSecret: RotationEnabled not set (runId={context.RunId})");
        Assertions.Equal(lambdaArn, desc.RotationLambdaARN,
            $"RotateSecret: RotationLambdaARN expected {lambdaArn} but was {desc.RotationLambdaARN} (runId={context.RunId})");
        Assertions.NotNull(desc.NextRotationDate, "RotateSecret: NextRotationDate");
    }

    private async Task PutSecretValuePendingAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmRotateSecretName");
        var response = await clients.SecretsManager().PutSecretValueAsync(new PutSecretValueRequest
        {
            SecretId = secretName,
            SecretString = "pending-value",
            ClientRequestToken = PendingToken,
            VersionStages = ["AWSPENDING"],
        });
        Assertions.Equal(PendingToken, response.VersionId,
            $"PutSecretValuePending: VersionId should be the ClientRequestToken but was {response.VersionId} (runId={context.RunId})");
        Assertions.True(response.VersionStages.Contains("AWSPENDING"),
            $"PutSecretValuePending: AWSPENDING missing from VersionStages (runId={context.RunId})");

        // Staging a pending version must not change AWSCURRENT.
        var current = await clients.SecretsManager().GetSecretValueAsync(new GetSecretValueRequest
        {
            SecretId = secretName,
        });
        Assertions.Equal("rotate-me", current.SecretString,
            $"PutSecretValuePending: AWSCURRENT changed to {current.SecretString} (runId={context.RunId})");
    }

    private async Task GetSecretValueByStageAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmRotateSecretName");
        var response = await clients.SecretsManager().GetSecretValueAsync(new GetSecretValueRequest
        {
            SecretId = secretName,
            VersionStage = "AWSPENDING",
        });
        Assertions.Equal("pending-value", response.SecretString,
            $"GetSecretValueByStage: expected pending-value but was {response.SecretString} (runId={context.RunId})");
    }

    private async Task UpdateSecretVersionStageAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmRotateSecretName");
        var desc = await clients.SecretsManager().DescribeSecretAsync(new DescribeSecretRequest
        {
            SecretId = secretName,
        });
        var current = desc.VersionIdsToStages
            .FirstOrDefault(kv => kv.Value.Contains("AWSCURRENT")).Key;
        Assertions.NotBlank(current, "UpdateSecretVersionStage: AWSCURRENT version");

        // This is what a rotation function's finishSecret step does.
        await clients.SecretsManager().UpdateSecretVersionStageAsync(new UpdateSecretVersionStageRequest
        {
            SecretId = secretName,
            VersionStage = "AWSCURRENT",
            MoveToVersionId = PendingToken,
            RemoveFromVersionId = current,
        });

        var after = await clients.SecretsManager().GetSecretValueAsync(new GetSecretValueRequest
        {
            SecretId = secretName,
        });
        Assertions.Equal("pending-value", after.SecretString,
            $"UpdateSecretVersionStage: AWSCURRENT is {after.SecretString} (runId={context.RunId})");
        Assertions.Equal(PendingToken, after.VersionId,
            $"UpdateSecretVersionStage: AWSCURRENT version is {after.VersionId} (runId={context.RunId})");

        var post = await clients.SecretsManager().DescribeSecretAsync(new DescribeSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.True(post.VersionIdsToStages[current].Contains("AWSPREVIOUS"),
            $"UpdateSecretVersionStage: displaced version did not become AWSPREVIOUS (runId={context.RunId})");
    }

    private async Task CancelRotateSecretAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmRotateSecretName");
        var response = await clients.SecretsManager().CancelRotateSecretAsync(new CancelRotateSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.NotBlank(response.ARN, "CancelRotateSecret: ARN");

        var desc = await clients.SecretsManager().DescribeSecretAsync(new DescribeSecretRequest
        {
            SecretId = secretName,
        });
        Assertions.False(desc.RotationEnabled ?? false,
            $"CancelRotateSecret: rotation still enabled after cancel (runId={context.RunId})");
        // AWS keeps the function configured so rotation can be turned back on.
        Assertions.Equal(RotationLambdaArn(context), desc.RotationLambdaARN,
            $"CancelRotateSecret: rotation function should stay configured but was {desc.RotationLambdaARN} (runId={context.RunId})");
    }

    // ---- secretsmanager-policy ----
    //
    // Overcast stores and validates a secret's resource policy but does not
    // evaluate it, so these assert the round-trip and the validation verdict,
    // never an access decision.

    private async Task SetupPolicyAsync(TestContext context)
    {
        var secretName = $"{context.RunId}-sm-policy";
        await clients.SecretsManager().CreateSecretAsync(new CreateSecretRequest
        {
            Name = secretName,
            SecretString = "policy-value",
        });
        context.Set("SmPolicySecretName", secretName);
    }

    private async Task ValidateResourcePolicyAsync(TestContext context)
    {
        var ok = await clients.SecretsManager().ValidateResourcePolicyAsync(new ValidateResourcePolicyRequest
        {
            ResourcePolicy = CompatResourcePolicy,
        });
        Assertions.True(ok.PolicyValidationPassed ?? false,
            $"ValidateResourcePolicy: valid policy rejected (runId={context.RunId})");

        var bad = await clients.SecretsManager().ValidateResourcePolicyAsync(new ValidateResourcePolicyRequest
        {
            ResourcePolicy = """{"Version":"2012-10-17"}""",
        });
        Assertions.False(bad.PolicyValidationPassed ?? false,
            $"ValidateResourcePolicy: policy with no Statement should not pass (runId={context.RunId})");
        Assertions.GreaterThanOrEqual(1, bad.ValidationErrors.Count,
            $"ValidateResourcePolicy: no ValidationErrors for a failing policy (runId={context.RunId})");
    }

    private async Task PutResourcePolicyAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmPolicySecretName");
        var response = await clients.SecretsManager().PutResourcePolicyAsync(new PutResourcePolicyRequest
        {
            SecretId = secretName,
            ResourcePolicy = CompatResourcePolicy,
        });
        Assertions.NotBlank(response.ARN, "PutResourcePolicy: ARN");
        Assertions.Equal(secretName, response.Name,
            $"PutResourcePolicy: expected {secretName} but was {response.Name} (runId={context.RunId})");
    }

    private async Task GetResourcePolicyAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmPolicySecretName");
        var response = await clients.SecretsManager().GetResourcePolicyAsync(new GetResourcePolicyRequest
        {
            SecretId = secretName,
        });
        Assertions.NotBlank(response.ResourcePolicy, "GetResourcePolicy: ResourcePolicy");
        Assertions.True(response.ResourcePolicy.Contains("OvercastCompatRead", StringComparison.Ordinal),
            $"GetResourcePolicy: policy does not carry the Sid that was stored (runId={context.RunId})");
    }

    private async Task DeleteResourcePolicyAsync(TestContext context)
    {
        var secretName = RequireSecretName(context, "SmPolicySecretName");
        await clients.SecretsManager().DeleteResourcePolicyAsync(new DeleteResourcePolicyRequest
        {
            SecretId = secretName,
        });
        var response = await clients.SecretsManager().GetResourcePolicyAsync(new GetResourcePolicyRequest
        {
            SecretId = secretName,
        });
        Assertions.True(string.IsNullOrEmpty(response.ResourcePolicy),
            $"DeleteResourcePolicy: policy still present (runId={context.RunId})");
    }
}
