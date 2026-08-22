using OvercastCompat.Clients;
using OvercastCompat.Groups;
using OvercastCompat.Harness;
using OvercastCompat.Registry;

const string suite = "dotnet-sdk";

var endpoint = EnvOr("OVERCAST_ENDPOINT", "http://localhost:4566");
var region = EnvOr("OVERCAST_DEFAULT_REGION", "us-east-1");
var skipDocker = Environment.GetEnvironmentVariable("OVERCAST_COMPAT_SKIP_DOCKER") == "1";

var clients = new AwsClients(endpoint, region);
var serviceGroups = new IServiceGroup[]
{
    new S3Group(clients),
    new SqsGroup(clients),
    new DynamoDbGroup(clients),
    new SnsGroup(clients),
    new LambdaGroup(clients),
    new StsGroup(clients),
    new KmsGroup(clients),
    new SecretsManagerGroup(clients),
    new SsmGroup(clients),
    new IamGroup(clients),
    new EventBridgeGroup(clients),
};

var setups = new Dictionary<string, SetupFn>(StringComparer.Ordinal);
var teardowns = new Dictionary<string, SetupFn>(StringComparer.Ordinal);

foreach (var group in serviceGroups)
{
    foreach (var entry in group.Setups())
    {
        setups[entry.Key] = entry.Value;
    }
    foreach (var entry in group.Teardowns())
    {
        teardowns[entry.Key] = entry.Value;
    }
}

// The impls go through MergeImpls rather than a plain assignment: a key two
// group classes both register would otherwise lose one implementation with
// nothing said about it.
Dictionary<string, TestFn> impls;
try
{
    impls = RegistryLoader.MergeImpls(
        serviceGroups.Select(group => (group.SourceName, group.Impls())), suite);
}
catch (InvalidOperationException ex)
{
    // Duplicate registrations - see RegistryLoader.MergeImpls. Aborting is the
    // point: the discarded implementation never runs, and its test reports the
    // surviving group's result under its own name.
    Console.Error.WriteLine(ex.Message);
    Environment.Exit(1);
    return;
}

var capabilities = new HashSet<string>(StringComparer.Ordinal);
if (!skipDocker)
{
    capabilities.Add("docker");
}

IReadOnlyList<TestGroup> allGroups;
try
{
    allGroups = RegistryLoader.BuildGroups(suite, impls, setups, teardowns, capabilities);
}
catch (InvalidOperationException ex)
{
    // Unusable impl registrations - see RegistryLoader.ValidateImpls. Aborting
    // is the point: binding a test to another group's implementation would
    // report a result for a test that never ran.
    Console.Error.WriteLine(ex.Message);
    Environment.Exit(1);
    return;
}
catch (Exception ex)
{
    Console.Error.WriteLine($"[dotnet-sdk] failed to load registry: {ex.Message}");
    Environment.Exit(1);
    return;
}

var filterServices = SplitFilter(Environment.GetEnvironmentVariable("OVERCAST_COMPAT_SERVICE"));
var filterGroups = SplitFilter(Environment.GetEnvironmentVariable("OVERCAST_COMPAT_GROUPS"));
var filterTests = SplitFilter(Environment.GetEnvironmentVariable("OVERCAST_COMPAT_TESTS"));
var filterTestPairs = SplitFilter(Environment.GetEnvironmentVariable("OVERCAST_COMPAT_TEST_PAIRS"));

var groups = allGroups;
if (filterServices.Count > 0)
{
    groups = groups.Where(group => filterServices.Contains(group.Service)).ToList();
}
if (filterGroups.Count > 0)
{
    groups = groups.Where(group => filterGroups.Contains(group.Name)).ToList();
}
if (filterTests.Count > 0)
{
    groups = groups
        .Select(group => group with { Tests = group.Tests.Where(test => filterTests.Contains(test.Name)).ToList() })
        .Where(group => group.Tests.Count > 0)
        .ToList();
}
if (filterTestPairs.Count > 0)
{
    groups = allGroups
        .Select(group => group with { Tests = group.Tests.Where(test => filterTestPairs.Contains($"{group.Name}:{test.Name}")).ToList() })
        .Where(group => group.Tests.Count > 0)
        .ToList();
}

if (Environment.GetEnvironmentVariable("OVERCAST_COMPAT_INTERACTIVE") == "1")
{
    await InteractiveRunner.RunAsync(suite, endpoint, region, allGroups);
}
else
{
    await Runner.RunSuiteAsync(suite, endpoint, region, groups);
}

static string EnvOr(string name, string defaultValue)
{
    var value = Environment.GetEnvironmentVariable(name);
    return string.IsNullOrWhiteSpace(value) ? defaultValue : value;
}

static HashSet<string> SplitFilter(string? value)
{
    if (string.IsNullOrWhiteSpace(value))
    {
        return [];
    }

    return value
        .Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
        .Where(entry => !string.IsNullOrWhiteSpace(entry))
        .ToHashSet(StringComparer.Ordinal);
}
