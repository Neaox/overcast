using OvercastCompat.Clients;
using OvercastCompat.Groups;
using OvercastCompat.Harness;
using OvercastCompat.Registry;
using Xunit;

namespace OvercastCompat.Tests;

/// <summary>
/// The generated groups' registrations, against the real registry.
/// </summary>
/// <remarks>
/// A generated group is resolved through the loader's
/// <see cref="ScenarioBackend"/> hook rather than through the impl map, so
/// RegistrationTests — which validates the hand-written half — never sees these
/// keys. This is the same check for the other half: every generated test the
/// registry scopes to this suite must resolve to something, because a generated
/// group scoped here that the backend cannot resolve is reported as a hard
/// failure rather than a skip (RegistryLoader.MissingScenarioBackend), and
/// finding that out during a compat run is late.
/// </remarks>
public sealed class ScenarioRegistrationTests
{
    private const string Suite = "dotnet-sdk";

    [Fact]
    public void EveryGeneratedTestScopedToThisSuiteResolves()
    {
        var registry = RegistryLoader.Load(FindRegistryPath());
        var impls = ScenarioImpls();

        var unresolved = new List<string>();
        foreach (var group in registry.Groups.Where(group => group.Generated && group.InScopeFor(Suite)))
        {
            foreach (var test in group.Tests)
            {
                var key = $"{group.Name}:{test.Name}";
                if (!impls.ContainsKey(key))
                {
                    unresolved.Add(key);
                }
            }
        }

        Assert.True(unresolved.Count == 0,
            "generated tests scoped to dotnet-sdk with no emitted implementation "
            + "(run `make generate-compat-model`): " + string.Join(", ", unresolved));
    }

    /// <summary>
    /// The reverse: an emitted implementation for a test the registry does not
    /// carry is dead code, and the likeliest cause is a generated file left
    /// behind by a stale generation run.
    /// </summary>
    [Fact]
    public void EveryEmittedImplementationHasARegistryTest()
    {
        var registry = RegistryLoader.Load(FindRegistryPath());
        var known = registry.Groups
            .SelectMany(group => group.Tests.Select(test => $"{group.Name}:{test.Name}"))
            .ToHashSet(StringComparer.Ordinal);

        var orphans = ScenarioImpls().Keys.Where(key => !known.Contains(key)).OrderBy(key => key, StringComparer.Ordinal).ToList();
        Assert.True(orphans.Count == 0,
            "emitted implementations matching no registry entry: " + string.Join(", ", orphans));
    }

    /// <summary>
    /// Generated keys are always group-qualified. A bare key would resolve
    /// through the loader's bare-name fallback and could bind one group's test
    /// to another group's implementation.
    /// </summary>
    [Fact]
    public void EveryEmittedKeyIsQualified()
    {
        var bare = ScenarioImpls().Keys
            .Where(key => !key.Contains(':', StringComparison.Ordinal))
            .OrderBy(key => key, StringComparer.Ordinal)
            .ToList();
        Assert.True(bare.Count == 0, "bare (unqualified) generated impl keys: " + string.Join(", ", bare));
    }

    /// <summary>
    /// Every generated group registers both hooks, even a probe group whose two
    /// lists are empty: an empty phase is a no-op, not a missing one.
    /// </summary>
    [Fact]
    public void EveryGeneratedGroupRegistersBothHooks()
    {
        var clients = new AwsClients("http://127.0.0.1:1", "us-east-1");
        var groups = ScenarioGroups.All(clients);
        var setups = groups.SelectMany(group => group.Setups().Keys).ToHashSet(StringComparer.Ordinal);
        var teardowns = groups.SelectMany(group => group.Teardowns().Keys).ToHashSet(StringComparer.Ordinal);

        var registry = RegistryLoader.Load(FindRegistryPath());
        foreach (var group in registry.Groups.Where(group => group.Generated && group.InScopeFor(Suite)))
        {
            Assert.True(setups.Contains(group.Name), $"{group.Name} registers no setup hook");
            Assert.True(teardowns.Contains(group.Name), $"{group.Name} registers no teardown hook");
        }
    }

    /// <summary>Flattens the generated groups' registrations the way Program.cs does.</summary>
    private static Dictionary<string, TestFn> ScenarioImpls()
    {
        var clients = new AwsClients("http://127.0.0.1:1", "us-east-1");
        var sources = ScenarioGroups.All(clients).Select(group => (group.SourceName, group.Impls()));
        return RegistryLoader.MergeImpls(sources, Suite);
    }

    /// <summary>
    /// Walks up from the test host's own directory to find registry.json — the
    /// same rule RegistrationTests uses, and for the same reason: a VSTest
    /// host's working directory is its build output directory.
    /// </summary>
    private static string FindRegistryPath()
    {
        var directory = new DirectoryInfo(AppContext.BaseDirectory);
        while (directory is not null)
        {
            var candidate = Path.Combine(directory.FullName, "registry.json");
            if (File.Exists(candidate))
            {
                return candidate;
            }
            directory = directory.Parent;
        }
        throw new FileNotFoundException("registry.json not found walking up from " + AppContext.BaseDirectory);
    }
}
