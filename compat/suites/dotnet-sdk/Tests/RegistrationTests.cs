using OvercastCompat.Clients;
using OvercastCompat.Groups;
using OvercastCompat.Harness;
using OvercastCompat.Registry;
using Xunit;

namespace OvercastCompat.Tests;

/// <summary>
/// The suite's own registrations must resolve against the real registry.json.
/// </summary>
/// <remarks>
/// dotnet-sdk had no test project until #1697 added one - until then this
/// suite's only guard against a mis-binding was the abort at startup (see
/// compat/AGENTS.md § Implementation keys). This is the check every other
/// suite already has: an unresolvable key (a typo, or a stale separator), or a
/// bare key for a name several groups declare, which cannot say which group it
/// implements.
/// </remarks>
public sealed class RegistrationTests
{
    private const string Suite = "dotnet-sdk";

    [Fact]
    public void RegisteredImplsResolveAgainstRegistry()
    {
        var registry = RegistryLoader.Load(FindRegistryPath());
        // Throws InvalidOperationException naming every unusable key.
        RegistryLoader.ValidateImpls(registry, MergeAll(), Suite);
    }

    /// <summary>
    /// No two service group classes may register the same impl key. The merge
    /// is last-writer-wins, so a collision discards one implementation and the
    /// test it belonged to silently runs the other class's - the same
    /// mis-binding the resolution rules prevent for registry lookups, one step
    /// earlier.
    /// </summary>
    [Fact]
    public void RegisteredImplsHaveNoDuplicateKeys()
    {
        MergeAll(); // throws InvalidOperationException naming every duplicated key
    }

    /// <summary>
    /// Every impl-map key must be group-qualified ("group:test"), never a bare
    /// test name (#1700). A bare key that happens to be unambiguous today
    /// silently becomes ambiguous - and is refused at runtime - the moment a
    /// new group declares the same test name, so the rule is enforced here
    /// rather than left to be rediscovered by a future collision.
    /// </summary>
    [Fact]
    public void RegisteredImplKeysAreAllQualified()
    {
        var bare = MergeAll().Keys
            .Where(key => !key.Contains(':'))
            .OrderBy(key => key, StringComparer.Ordinal)
            .ToList();
        Assert.True(bare.Count == 0,
            $"bare (unqualified) impl keys found, want \"group:test\": {string.Join(", ", bare)}");
    }

    /// <summary>
    /// Flattens every service group's registrations the way Program.cs does,
    /// so all three tests see exactly the map a real run would build.
    /// </summary>
    private static Dictionary<string, TestFn> MergeAll()
    {
        var clients = new AwsClients("http://127.0.0.1:1", "us-east-1");
        var sources = ServiceGroups.All(clients).Select(group => (group.SourceName, group.Impls()));
        return RegistryLoader.MergeImpls(sources, Suite);
    }

    /// <summary>
    /// Walks up from the test host's own directory to find registry.json.
    /// </summary>
    /// <remarks>
    /// Unlike Program.cs's own resolution (relative to the process's working
    /// directory, or OVERCAST_REGISTRY_PATH), a VSTest host's working directory
    /// is its build output directory (e.g. Tests/bin/Release/net8.0/), several
    /// levels below compat/suites/ - a fixed "../registry.json" does not reach
    /// it and would silently depend on the build configuration's exact depth.
    /// </remarks>
    private static string FindRegistryPath()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir is not null)
        {
            var candidate = Path.Combine(dir.FullName, "registry.json");
            if (File.Exists(candidate)) return candidate;
            dir = dir.Parent;
        }
        throw new FileNotFoundException("registry.json not found walking up from " + AppContext.BaseDirectory);
    }
}
