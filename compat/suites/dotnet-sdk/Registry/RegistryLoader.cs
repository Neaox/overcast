using OvercastCompat.Harness;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace OvercastCompat.Registry;

/// <summary>
/// Resolves a generated group's test to an implementation.
/// </summary>
/// <remarks>
/// A generated group is not implemented by a registered impl the way a
/// hand-written one is: it is executed by an interpreter that reads the group's
/// <see cref="RegistryLoader.RegistryGroup.Scenario"/> IR. This is the extension
/// point that interpreter plugs into - the last step of the loader's resolution
/// order, after the group-qualified and bare impl keys and before the
/// not-implemented sentinel. Returns null when the backend cannot execute the
/// test.
/// <para>The suite's implementation is the generated groups under Groups/ that
/// cmd/compatgen writes, collected by ScenarioGroups.All and registered in
/// Program.cs. A generated group scoped to this suite that the backend does not
/// resolve still reports a failure rather than a skip.</para>
/// </remarks>
internal delegate TestFn? ScenarioBackend(RegistryLoader.RegistryGroup group, RegistryLoader.RegistryTest test);

public static class RegistryLoader
{
    private static readonly TestFn Noop = _ => Task.CompletedTask;

    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
    };

    /// <summary>Sibling of registry.json holding the generated groups.</summary>
    internal const string GeneratedRegistryFileName = "registry.generated.json";

    /// <summary>The only version the generated registry may declare.</summary>
    internal const int GeneratedRegistryVersion = 1;

    public static IReadOnlyList<TestGroup> BuildGroups(
        string suite,
        IReadOnlyDictionary<string, TestFn> impls,
        IReadOnlyDictionary<string, SetupFn> setups,
        IReadOnlyDictionary<string, SetupFn> teardowns,
        ISet<string> capabilities) =>
        BuildGroups(suite, impls, setups, teardowns, capabilities, backend: null);

    /// <summary>
    /// As <see cref="BuildGroups(string, IReadOnlyDictionary{string, TestFn}, IReadOnlyDictionary{string, SetupFn}, IReadOnlyDictionary{string, SetupFn}, ISet{string})"/>,
    /// with a <see cref="ScenarioBackend"/> for the generated groups this suite
    /// can execute. <paramref name="backend"/> may be null - none exists yet.
    /// </summary>
    internal static IReadOnlyList<TestGroup> BuildGroups(
        string suite,
        IReadOnlyDictionary<string, TestFn> impls,
        IReadOnlyDictionary<string, SetupFn> setups,
        IReadOnlyDictionary<string, SetupFn> teardowns,
        ISet<string> capabilities,
        ScenarioBackend? backend)
    {
        var registry = Load();
        ValidateImpls(registry, impls, suite);
        return BuildGroups(suite, registry, impls, setups, teardowns, capabilities, backend);
    }

    /// <summary>
    /// Assembles groups from an already-loaded registry, without validating the
    /// impl keys - <see cref="ValidateImpls"/> is a separate step, as in the
    /// other suite loaders.
    /// </summary>
    internal static IReadOnlyList<TestGroup> BuildGroups(
        string suite,
        RegistryRoot registry,
        IReadOnlyDictionary<string, TestFn> impls,
        IReadOnlyDictionary<string, SetupFn> setups,
        IReadOnlyDictionary<string, SetupFn> teardowns,
        ISet<string> capabilities,
        ScenarioBackend? backend)
    {
        var ambiguous = AmbiguousTestNames(registry);

        return registry.Groups
            // A group scoped to specific suites ("suites" in the registry) is
            // out of scope for every other suite: no tests, no skips, no
            // results. The rule is general and covers both halves of the
            // registry. On a generated group the list names the backends that
            // can execute it (always present - enforced in LoadGenerated); on a
            // hand-written group it is reserved for cdk-lifecycle, which is why
            // applying this to every group stopped this suite reporting that
            // group's 35 skips and re-seeded its baseline shard (#1737).
            .Where(group => group.InScopeFor(suite))
            .Select(group => new TestGroup(
                suite,
                group.Service,
                group.Name,
                TopoSort(group.Tests).Select(test => BuildTestCase(group, test, suite, impls, capabilities, ambiguous, backend)).ToList(),
                setups.TryGetValue(group.Name, out var setup) ? setup : null,
                teardowns.TryGetValue(group.Name, out var teardown) ? teardown : null,
                group.Parallel))
            .ToList();
    }

    private static TestCase BuildTestCase(
        RegistryGroup group,
        RegistryTest test,
        string suite,
        IReadOnlyDictionary<string, TestFn> impls,
        ISet<string> capabilities,
        ISet<string> ambiguous,
        ScenarioBackend? backend)
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
            // A generated group is executed by an interpreter reading its
            // scenario IR rather than by a registered impl, so the backend is
            // the last resolution step before the sentinel.
            implementation = backend?.Invoke(group, test);
        }

        if (implementation is not null)
        {
            return new TestCase(test.Name, implementation, test.Op, null, test.Depends);
        }

        return group.Generated
            ? MissingScenarioBackend(group, test, suite)
            : new TestCase(test.Name, Noop, test.Op, $"not yet implemented in {suite} test suite", test.Depends);
    }

    /// <summary>
    /// The interim result for a generated test this suite cannot execute.
    /// </summary>
    /// <remarks>
    /// A generated group's "suites" list is derived from backend availability by
    /// cmd/compatgen, so a suite named in it that has no scenario backend is a
    /// generator or loader bug - and it has to be loud. Reporting it as the
    /// not-implemented sentinel would file it as ordinary parity debt, and
    /// reporting it as "na" would call it a permanent, accepted divergence; both
    /// read as "nothing to see here". A candidate group gates nothing
    /// (cmd/compat, #1367), so this cannot red a build until the group is
    /// promoted to "gated" - at which point it is a real regression and should.
    /// <para>Failing is how a test body reports, so the body throws. It carries
    /// no Depends: the runner cascade-skips a test whose dependency failed, and
    /// a skip is exactly what this result must never be.</para>
    /// </remarks>
    private static TestCase MissingScenarioBackend(RegistryGroup group, RegistryTest test, string suite)
    {
        var message = $"generated group \"{group.Name}\" is scoped to {suite} but {suite} has no scenario backend";
        return new TestCase(test.Name, _ => throw new InvalidOperationException(message), test.Op, null, Array.Empty<string>());
    }

    private static RegistryRoot Load()
    {
        var path = Environment.GetEnvironmentVariable("OVERCAST_REGISTRY_PATH");
        if (string.IsNullOrWhiteSpace(path))
        {
            path = Path.Combine("..", "registry.json");
        }

        return Load(path);
    }

    /// <summary>
    /// Reads the registry at <paramref name="path"/> and concatenates the
    /// generated groups from its registry.generated.json sibling.
    /// </summary>
    /// <remarks>
    /// Hand-written groups come first, generated groups after, both in file
    /// order - the loader sorts neither.
    /// </remarks>
    internal static RegistryRoot Load(string path)
    {
        RegistryRoot? registry;
        using (var stream = File.OpenRead(path))
        {
            registry = JsonSerializer.Deserialize<RegistryRoot>(stream, JsonOptions);
        }

        if (registry is null)
        {
            throw new InvalidOperationException($"failed to deserialize registry at {path}");
        }

        var directory = Path.GetDirectoryName(Path.GetFullPath(path)) ?? ".";
        var generated = LoadGenerated(Path.Combine(directory, GeneratedRegistryFileName), registry);
        if (generated.Count == 0)
        {
            return registry;
        }

        return registry with { Groups = [.. registry.Groups, .. generated] };
    }

    /// <summary>
    /// Reads the generated registry, or returns nothing when it is absent.
    /// </summary>
    /// <remarks>
    /// Absence is not an error: suite images, CI artifacts and branches cut
    /// before the file existed must all keep working, and "the file is not
    /// there" has to produce the same groups as "the file is there and empty".
    /// Everything else about a file that is there is an error, exactly as a
    /// malformed registry.json is - a bad generated file must never be silently
    /// dropped.
    /// </remarks>
    /// <exception cref="InvalidDataException">If the file is unusable.</exception>
    private static IReadOnlyList<RegistryGroup> LoadGenerated(string path, RegistryRoot handWritten)
    {
        if (!File.Exists(path))
        {
            return [];
        }

        GeneratedRegistryRoot? generated;
        try
        {
            using var stream = File.OpenRead(path);
            generated = JsonSerializer.Deserialize<GeneratedRegistryRoot>(stream, JsonOptions);
        }
        catch (JsonException ex)
        {
            throw new InvalidDataException($"parse generated registry {path}: {ex.Message}", ex);
        }

        if (generated is null)
        {
            throw new InvalidDataException($"failed to deserialize generated registry at {path}");
        }
        if (generated.Version != GeneratedRegistryVersion)
        {
            throw new InvalidDataException(
                $"generated registry {path} has version {generated.Version?.ToString() ?? "none"}, "
                + $"want {GeneratedRegistryVersion}");
        }

        var handWrittenNames = handWritten.Groups
            .Select(group => group.Name)
            .ToHashSet(StringComparer.Ordinal);

        foreach (var group in generated.Groups)
        {
            // The two registries are concatenated and every gate file
            // (baseline, flaky, parity-debt) keys on suite/group/test with no
            // notion of which file a group came from, so a reused name merges
            // two different groups rather than conflicting. cmd/compat lints
            // this; the loader is the second line of defence.
            if (handWrittenNames.Contains(group.Name))
            {
                throw new InvalidDataException(
                    $"generated group \"{group.Name}\" in {path} collides with a hand-written group of the same name");
            }
            if (!group.Generated)
            {
                throw new InvalidDataException(
                    $"generated group \"{group.Name}\" in {path} does not set \"generated\": true");
            }
            if (string.IsNullOrEmpty(group.State))
            {
                throw new InvalidDataException($"generated group \"{group.Name}\" in {path} has no \"state\"");
            }
            if (group.Suites.Count == 0)
            {
                throw new InvalidDataException($"generated group \"{group.Name}\" in {path} has no \"suites\"");
            }
        }

        return generated.Groups;
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
    /// Flattens the per-service impl maps into the single map the loader
    /// resolves against, refusing any key that two sources both register.
    /// </summary>
    /// <remarks>
    /// The merge used to be a plain <c>impls[entry.Key] = entry.Value</c> - last
    /// writer wins, and silently. Two group classes both registering
    /// "lambda-crud:CreateFunction" left one implementation unreachable with
    /// nothing said about it, and the run reported a result for whichever one
    /// survived. <see cref="ValidateImpls"/> cannot catch this: by the time it
    /// sees the flattened map the discarded implementation is already gone, and
    /// the surviving key resolves perfectly well.
    /// <para>Sources are (label, impls) in registration order; the label is the
    /// group class, so a collision can name both sides.</para>
    /// </remarks>
    /// <exception cref="InvalidOperationException">If any key is registered more than once.</exception>
    public static Dictionary<string, TestFn> MergeImpls(
        IEnumerable<(string Name, IReadOnlyDictionary<string, TestFn> Impls)> sources,
        string suite)
    {
        var merged = new Dictionary<string, TestFn>(StringComparer.Ordinal);
        var owner = new Dictionary<string, string>(StringComparer.Ordinal); // key -> first registrant

        var problems = new List<string>();
        foreach (var (name, impls) in sources)
        {
            foreach (var entry in impls)
            {
                if (owner.TryGetValue(entry.Key, out var first))
                {
                    problems.Add(DuplicateProblem(entry.Key, first, name));
                    continue;
                }
                owner[entry.Key] = name;
                merged[entry.Key] = entry.Value;
            }
        }

        if (problems.Count == 0) return merged;
        // Sorted so the message is the same however the source maps iterate.
        // Every problem starts with the key, which is what a reader scans for.
        problems.Sort(StringComparer.Ordinal);
        throw new InvalidOperationException(
            $"[{suite}] {problems.Count} duplicate impl registration(s):{Environment.NewLine}  - "
            + string.Join($"{Environment.NewLine}  - ", problems));
    }

    /// <summary>
    /// One collision. The two sources are the same when a single group class
    /// registers the key twice.
    /// </summary>
    private static string DuplicateProblem(string key, string first, string second)
    {
        var where = first == second
            ? $"is registered twice by \"{first}\""
            : $"is registered by both \"{first}\" and \"{second}\"";
        return $"impl \"{key}\" {where} - one of the two would be silently discarded; "
            + "remove or re-key one";
    }

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

    /// <summary>The generated sibling. Version is checked; the rest is shared.</summary>
    internal sealed record GeneratedRegistryRoot
    {
        [JsonPropertyName("version")]
        public int? Version { get; init; }

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

        /// <summary>Suites the group is in scope for; empty means all of them.</summary>
        [JsonPropertyName("suites")]
        public IReadOnlyList<string> Suites { get; init; } = [];

        /// <summary>True for a group read from registry.generated.json.</summary>
        [JsonPropertyName("generated")]
        public bool Generated { get; init; }

        /// <summary>
        /// Whether the group's tests may run concurrently with one another.
        /// </summary>
        /// <remarks>
        /// Only a generated probe group carries it: a probe has no setup, no
        /// teardown and no exports, so nothing orders its tests. A loader that
        /// ignored the flag would still be correct - it would run the group in
        /// order - which is what makes it safe to read from the registry rather
        /// than from the scenario file.
        /// </remarks>
        [JsonPropertyName("parallel")]
        public bool Parallel { get; init; }

        /// <summary>"candidate" or "gated" - generated groups only.</summary>
        [JsonPropertyName("state")]
        public string? State { get; init; }

        /// <summary>
        /// Path to the scenario IR the group was generated from, for a
        /// <see cref="ScenarioBackend"/> to interpret. Generated groups only.
        /// </summary>
        [JsonPropertyName("scenario")]
        public string? Scenario { get; init; }

        /// <summary>
        /// Whether <paramref name="suite"/> may run this group.
        /// </summary>
        /// <remarks>
        /// A group that names its suites is out of scope for every other suite -
        /// not in debt: it is not loaded at all there, so it emits no tests, no
        /// skips and no results. On a generated group the list is derived from
        /// backend availability by cmd/compatgen.
        /// </remarks>
        public bool InScopeFor(string suite) =>
            Suites.Count == 0 || Suites.Contains(suite, StringComparer.Ordinal);
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
