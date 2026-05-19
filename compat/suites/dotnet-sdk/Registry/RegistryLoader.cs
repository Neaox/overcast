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

        return registry.Groups
            .Select(group => new TestGroup(
                suite,
                group.Service,
                group.Name,
                TopoSort(group.Tests).Select(test => BuildTestCase(group, test, suite, impls, capabilities)).ToList(),
                setups.TryGetValue(group.Name, out var setup) ? setup : null,
                teardowns.TryGetValue(group.Name, out var teardown) ? teardown : null))
            .ToList();
    }

    private static TestCase BuildTestCase(
        RegistryGroup group,
        RegistryTest test,
        string suite,
        IReadOnlyDictionary<string, TestFn> impls,
        ISet<string> capabilities)
    {
        if (!string.IsNullOrWhiteSpace(test.Skip))
        {
            return new TestCase(test.Name, Noop, test.Op, test.Skip, test.Depends);
        }

        if (test.Requires.Count > 0 && test.Requires.Any(required => !capabilities.Contains(required)))
        {
            return new TestCase(test.Name, Noop, test.Op, $"requires {string.Join(", ", test.Requires)} (not available in this environment)", test.Depends);
        }

        var qualified = $"{group.Name}:{test.Name}";
        if (!impls.TryGetValue(qualified, out var implementation) && !impls.TryGetValue(test.Name, out implementation))
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

    private static void ValidateImpls(RegistryRoot registry, IReadOnlyDictionary<string, TestFn> impls, string suite)
    {
        var names = registry.Groups
            .SelectMany(group => group.Tests.SelectMany(test => new[] { test.Name, $"{group.Name}:{test.Name}" }))
            .ToHashSet(StringComparer.Ordinal);

        foreach (var name in impls.Keys.Where(name => !names.Contains(name)))
        {
            Console.Error.WriteLine($"[{suite}] WARNING: impl {name} is not in registry.json and will never run");
        }
    }

    private sealed record RegistryRoot
    {
        [JsonPropertyName("groups")]
        public IReadOnlyList<RegistryGroup> Groups { get; init; } = [];
    }

    private sealed record RegistryGroup
    {
        [JsonPropertyName("service")]
        public string Service { get; init; } = "";

        [JsonPropertyName("name")]
        public string Name { get; init; } = "";

        [JsonPropertyName("tests")]
        public IReadOnlyList<RegistryTest> Tests { get; init; } = [];
    }

    private sealed record RegistryTest
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
