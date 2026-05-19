using Amazon.SecurityToken.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class StsGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["GetCallerIdentity"] = GetCallerIdentityAsync,
        ["GetSessionToken"] = GetSessionTokenAsync,
        ["GetFederationToken"] = GetFederationTokenAsync,
        ["AssumeRole"] = AssumeRoleAsync,
        ["AssumeRoleWithWebIdentity"] = AssumeRoleWithWebIdentityAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal);

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal);

    private async Task GetCallerIdentityAsync(TestContext context)
    {
        var response = await clients.STS().GetCallerIdentityAsync(new GetCallerIdentityRequest());
        Assertions.NotBlank(response.Account, "GetCallerIdentity: account");
        Assertions.NotBlank(response.Arn, "GetCallerIdentity: arn");
        Assertions.NotBlank(response.UserId, "GetCallerIdentity: userId");
    }

    private async Task GetSessionTokenAsync(TestContext context)
    {
        var response = await clients.STS().GetSessionTokenAsync(new GetSessionTokenRequest());
        Assertions.NotNull(response.Credentials, "GetSessionToken: credentials");
        Assertions.NotBlank(response.Credentials.AccessKeyId, "GetSessionToken: accessKeyId");
    }

    private async Task GetFederationTokenAsync(TestContext context)
    {
        var response = await clients.STS().GetFederationTokenAsync(new GetFederationTokenRequest
        {
            Name = "compat-user",
        });
        Assertions.NotNull(response.Credentials, "GetFederationToken: credentials");
        Assertions.NotBlank(response.Credentials.AccessKeyId, "GetFederationToken: accessKeyId");
    }

    private async Task AssumeRoleAsync(TestContext context)
    {
        var response = await clients.STS().AssumeRoleAsync(new AssumeRoleRequest
        {
            RoleArn = "arn:aws:iam::000000000000:role/compat-role",
            RoleSessionName = "dotnet-sdk-compat",
        });
        Assertions.NotNull(response.Credentials, "AssumeRole: credentials");
        Assertions.NotBlank(response.Credentials.AccessKeyId, "AssumeRole: accessKeyId");
    }

    private async Task AssumeRoleWithWebIdentityAsync(TestContext context)
    {
        var response = await clients.STS().AssumeRoleWithWebIdentityAsync(new AssumeRoleWithWebIdentityRequest
        {
            RoleArn = "arn:aws:iam::000000000000:role/compat-role",
            RoleSessionName = "dotnet-sdk-compat",
            WebIdentityToken = "fake-web-identity-token",
        });
        Assertions.NotNull(response.Credentials, "AssumeRoleWithWebIdentity: credentials");
        Assertions.NotBlank(response.Credentials.AccessKeyId, "AssumeRoleWithWebIdentity: accessKeyId");
    }
}
