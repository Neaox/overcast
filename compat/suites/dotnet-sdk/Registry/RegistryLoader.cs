using OvercastCompat.Harness;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace OvercastCompat.Registry;

public static class RegistryLoader
{
    private static readonly TestFn Noop = _ => Task.CompletedTask;

    public static IReadOnlyList<TestGroup> BuildGroups(
        string suite,
        IReadOnlyDictionary<string, TestFn> impls,
        IReadOnlyDictionary<string, SetupFn> setups,
        IReadOnlyDictionary<string, SetupFn> teardowns,
        ISet<string> capabilities)
    {
        var registry = Load();
        ValidateImpls(registry, impls, suite);
        var ambiguous = AmbiguousTestNames(registry);

        return registry.Groups
            .Select(group => new TestGroup(
                suite,
                group.Service,
                group.Name,
                TopoSort(group.Tests).Select(test => BuildTestCase(group, test, suite, impls, capabilities, ambiguous)).ToList(),
                setups.TryGetValue(group.Name, out var setup) ? setup : null,
                teardowns.TryGetValue(group.Name, out var teardown) ? teardown : null))
            .ToList();
    }

    private static TestCase BuildTestCase(
        RegistryGroup group,
        RegistryTest test,
        string suite,
        IReadOnlyDictionary<string, TestFn> impls,
        ISet<string> capabilities,
        ISet<string> ambiguous)
    {
        if (!string.IsNullOrWhiteSpace(test.Skip))
        {
            return new TestCase(test.Name, Noop, test.Op, test.Skip, test.Depends);
        }

        if (test.Requires.Count > 0 && test.Requires.Any(required => !capabilities.Contains(required)))
        {
            return new TestCase(test.Name, Noop, test.Op, $"requires {string.Join(", ", test.Requires)} (not available in this environment)", test.Depends);
        }

        // Look up by group-qualified key first, then fall back to the bare test
        // name. The bare fallback is refused for a name claimed by more than one
        // group: it would bind this group to another group's implementation and
        // report its result as ours. ValidateImpls rejects such a registration
        // outright; this is the second line of defence, so a mis-bind cannot
        // occur even if validation is bypassed.
        var qualified = $"{group.Name}:{test.Name}";
        var bareUsable = !ambiguous.Contains(test.Name);
        if (!impls.TryGetValue(qualified, out var implementation)
            && !(bareUsable && impls.TryGetValue(test.Name, out implementation)))
        {
            return new TestCase(test.Name, Noop, test.Op, $"not yet implemented in {suite} test suite", test.Depends);
        }

        return new TestCase(test.Name, implementation, test.Op, null, test.Depends);
    }

    private static RegistryRoot Load()
    {
        var path = Environment.GetEnvironmentVariable("OVERCAST_REGISTRY_PATH");
        if (string.IsNullOrWhiteSpace(path))
        {
            path = Path.Combine("..", "registry.json");
        }

        using var stream = File.OpenRead(path);
        var registry = JsonSerializer.Deserialize<RegistryRoot>(stream, new JsonSerializerOptions
        {
            PropertyNameCaseInsensitive = true,
        });

        return registry ?? throw new InvalidOperationException($"failed to deserialize registry at {path}");
    }

    private static IReadOnlyList<RegistryTest> TopoSort(IReadOnlyList<RegistryTest> tests)
    {
        var byName = tests.ToDictionary(test => test.Name, StringComparer.Ordinal);
        var visited = new HashSet<string>(StringComparer.Ordinal);
        var visiting = new HashSet<string>(StringComparer.Ordinal);
        var sorted = new List<RegistryTest>(tests.Count);

        foreach (var test in tests)
        {
            Visit(test.Name);
        }

        return sorted;

        void Visit(string name)
        {
            if (visited.Contains(name) || visiting.Contains(name) || !byName.TryGetValue(name, out var current))
            {
                return;
            }

            visiting.Add(name);
            foreach (var dependency in current.Depends)
            {
                Visit(dependency);
            }
            visiting.Remove(name);
            visited.Add(name);
            sorted.Add(current);
        }
    }

    /// <summary>Maps each registry test name to the sorted groups that declare it.</summary>
    internal static SortedDictionary<string, List<string>> TestNameOwners(RegistryRoot registry)
    {
        var owners = new SortedDictionary<string, List<string>>(StringComparer.Ordinal);
        foreach (var group in registry.Groups)
        {
            foreach (var test in group.Tests)
            {
                if (!owners.TryGetValue(test.Name, out var groups))
                {
                    owners[test.Name] = groups = new List<string>();
                }
                if (!groups.Contains(group.Name)) groups.Add(group.Name);
            }
        }
        foreach (var groups in owners.Values) groups.Sort(StringComparer.Ordinal);
        return owners;
    }

    /// <summary>Test names that more than one registry group declares.</summary>
    /// <remarks>
    /// A bare-name implementation cannot serve these. <c>ListUsers</c> belongs to
    /// both <c>iam-users</c> and <c>cognito-userpools</c>, so a bare
    /// <c>ListUsers</c> impl binds whichever group happens to resolve it - and
    /// the loser silently runs the other service's test and reports the result
    /// as its own. Suites must register the group-qualified key for these.
    /// </remarks>
    internal static ISet<string> AmbiguousTestNames(RegistryRoot registry) =>
        TestNameOwners(registry)
            .Where(entry => entry.Value.Count > 1)
            .Select(entry => entry.Key)
            .ToHashSet(StringComparer.Ordinal);

    /// <summary>
    /// Rejects impl keys that cannot be bound to exactly one registry test.
    /// </summary>
    /// <remarks>
    /// This used to be a stderr warning nobody read, while the test the key was
    /// meant to implement quietly fell back to another group's implementation
    /// and reported a pass. Two registrations are refused: a key matching no
    /// registry entry (a typo, a stale name, or the wrong separator - every
    /// suite uses "group:test"), and a bare key for a name several groups
    /// declare, which cannot say which group it implements.
    /// </remarks>
    /// <exception cref="InvalidOperationException">If any registration is unusable.</exception>
    internal static void ValidateImpls(RegistryRoot registry, IReadOnlyDictionary<string, TestFn> impls, string suite)
    {
        var owners = TestNameOwners(registry);
        var names = registry.Groups
            .SelectMany(group => group.Tests.SelectMany(test => new[] { test.Name, $"{group.Name}:{test.Name}" }))
            .ToHashSet(StringComparer.Ordinal);

        var problems = new List<string>();
        foreach (var name in impls.Keys.OrderBy(k => k, StringComparer.Ordinal))
        {
            if (!names.Contains(name))
            {
                var message = $"impl \"{name}\" matches no registry entry";
                if (name.Contains('/'))
                {
                    // The Java suite used "group/test" until the separator was
                    // unified; a key copied from it resolves to nothing here.
                    var index = name.IndexOf('/');
                    var suggestion = name[..index] + ":" + name[(index + 1)..];
                    message += $" (group-qualified keys use \":\", not \"/\" - did you mean \"{suggestion}\"?)";
                }
                problems.Add(message);
            }
            else if (owners.TryGetValue(name, out var claimedBy) && claimedBy.Count > 1)
            {
                // Naming every candidate rather than guessing one: only the author
                // knows which group this implementation is for, and binding it to
                // the wrong one is the failure this check exists to prevent.
                var candidates = string.Join(", ", claimedBy.Select(group => $"\"{group}:{name}\""));
                problems.Add(
                    $"impl \"{name}\" is ambiguous: groups [{string.Join(", ", claimedBy)}] all declare "
                    + $"a test named \"{name}\" - qualify it with the group it implements, one of: {candidates}");
            }
        }

        if (problems.Count == 0) return;
        throw new InvalidOperationException(
            $"[{suite}] {problems.Count} unusable impl registration(s):{Environment.NewLine}  - "
            + string.Join($"{Environment.NewLine}  - ", problems));
    }

    internal sealed record RegistryRoot
    {
        [JsonPropertyName("groups")]
        public IReadOnlyList<RegistryGroup> Groups { get; init; } = [];
    }

    internal sealed record RegistryGroup
    {
        [JsonPropertyName("service")]
        public string Service { get; init; } = "";

        [JsonPropertyName("name")]
        public string Name { get; init; } = "";

        [JsonPropertyName("tests")]
        public IReadOnlyList<RegistryTest> Tests { get; init; } = [];
    }

    internal sealed record RegistryTest
    {
        [JsonPropertyName("name")]
        public string Name { get; init; } = "";

        [JsonPropertyName("op")]
        public string? Op { get; init; }

        [JsonPropertyName("skip")]
        public string? Skip { get; init; }

        [JsonPropertyName("requires")]
        public IReadOnlyList<string> Requires { get; init; } = Array.Empty<string>();

        [JsonPropertyName("depends")]
        public IReadOnlyList<string> Depends { get; init; } = Array.Empty<string>();
    }
}
