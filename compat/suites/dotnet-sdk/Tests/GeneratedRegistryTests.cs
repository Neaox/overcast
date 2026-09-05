using OvercastCompat.Harness;
using OvercastCompat.Registry;
using Xunit;

namespace OvercastCompat.Tests;

/// <summary>
/// Pins how the loader reads registry.generated.json.
/// </summary>
/// <remarks>
/// Nothing here asserts what the checked-in generated registry contains - only
/// that an empty one behaves exactly as an absent one. The file is generator
/// output and is about to stop being empty; the invariant is what must hold,
/// not the moment.
/// </remarks>
public sealed class GeneratedRegistryTests : IDisposable
{
    private const string Suite = "dotnet-sdk";

    private static readonly TestFn Noop = _ => Task.CompletedTask;

    private const string HandWritten = """
        {
          "version": 1,
          "groups": [
            {"service": "s3", "name": "s3-crud", "tests": [{"name": "CreateBucket"}]}
          ]
        }
        """;

    /// <summary>One generated group this suite is named in, with one test.</summary>
    private const string InScopeGroup = """
        {
          "version": 1,
          "groups": [
            {"service": "sqs", "name": "gen-sqs", "generated": true,
             "state": "candidate", "scenario": "scenarios/sqs.json",
             "suites": ["dotnet-sdk"], "tests": [{"name": "SendMessage"}]}
          ]
        }
        """;

    private readonly string _directory =
        Directory.CreateDirectory(Path.Combine(Path.GetTempPath(), $"oc-compat-{Guid.NewGuid():n}")).FullName;

    public void Dispose() => Directory.Delete(_directory, recursive: true);

    private string HandWrittenRegistry()
    {
        var path = Path.Combine(_directory, "registry.json");
        File.WriteAllText(path, HandWritten);
        return path;
    }

    private void Generated(string json) =>
        File.WriteAllText(Path.Combine(_directory, RegistryLoader.GeneratedRegistryFileName), json);

    private static IReadOnlyList<TestGroup> Build(string registry, ScenarioBackend? backend = null) =>
        RegistryLoader.BuildGroups(
            Suite,
            RegistryLoader.Load(registry),
            new Dictionary<string, TestFn>(StringComparer.Ordinal),
            new Dictionary<string, SetupFn>(StringComparer.Ordinal),
            new Dictionary<string, SetupFn>(StringComparer.Ordinal),
            new HashSet<string>(StringComparer.Ordinal),
            backend);

    private static string[] GroupNames(IReadOnlyList<TestGroup> groups) =>
        groups.Select(group => group.Name).ToArray();

    private static TestCase Only(IReadOnlyList<TestGroup> groups, string name)
    {
        var group = Assert.Single(groups, candidate => candidate.Name == name);
        return Assert.Single(group.Tests);
    }

    // -- Absence and emptiness are the same thing -----------------------------

    [Fact]
    public void MissingGeneratedFileIsANoOp()
    {
        Assert.Equal(["s3-crud"], GroupNames(Build(HandWrittenRegistry())));
    }

    [Fact]
    public void EmptyGeneratedFileIsANoOp()
    {
        var registry = HandWrittenRegistry();
        Generated("""{"version":1,"groups":[]}""");
        Assert.Equal(["s3-crud"], GroupNames(Build(registry)));
    }

    // -- Concatenation and scoping --------------------------------------------

    [Fact]
    public void GeneratedGroupsAreConcatenatedAfterTheHandWrittenOnes()
    {
        var registry = HandWrittenRegistry();
        Generated("""
            {
              "version": 1,
              "groups": [
                {"service": "sqs", "name": "gen-sqs-a", "generated": true,
                 "state": "candidate", "scenario": "scenarios/sqs-a.json",
                 "suites": ["dotnet-sdk"], "tests": [{"name": "SendMessage"}]},
                {"service": "sqs", "name": "gen-sqs-b", "generated": true,
                 "state": "gated", "scenario": "scenarios/sqs-b.json",
                 "suites": ["dotnet-sdk"], "tests": [{"name": "ReceiveMessage"}]}
              ]
            }
            """);

        // File order, hand-written first - the loader sorts neither half.
        Assert.Equal(["s3-crud", "gen-sqs-a", "gen-sqs-b"], GroupNames(Build(registry)));
    }

    [Fact]
    public void GeneratedGroupScopedToAnotherSuiteIsNotLoaded()
    {
        var registry = HandWrittenRegistry();
        Generated("""
            {
              "version": 1,
              "groups": [
                {"service": "sqs", "name": "gen-sqs", "generated": true,
                 "state": "candidate", "scenario": "scenarios/sqs.json",
                 "suites": ["python-sdk"], "tests": [{"name": "SendMessage"}]}
              ]
            }
            """);

        // Out of scope, not in debt: no tests, no skips, no results.
        Assert.Equal(["s3-crud"], GroupNames(Build(registry)));
    }

    // -- The interim rule: loud, never a skip ---------------------------------

    [Fact]
    public async Task GeneratedGroupWithNoBackendFails()
    {
        var registry = HandWrittenRegistry();
        Generated(InScopeGroup);

        var test = Only(Build(registry), "gen-sqs");

        Assert.Null(test.Skip);
        Assert.Empty(test.Depends);

        var error = await Assert.ThrowsAsync<InvalidOperationException>(
            () => test.Fn(new TestContext("http://127.0.0.1:1", "us-east-1", "run")));
        Assert.Equal(
            "generated group \"gen-sqs\" is scoped to dotnet-sdk but dotnet-sdk has no scenario backend",
            error.Message);

        // The runner classifies a thrown error as "unimplemented" when it looks
        // like a 501; this message must not, or the fail would be reported as
        // the very gap it exists to distinguish itself from.
        Assert.False(Runner.IsUnimplemented(error));
    }

    /// <summary>The sentinel skip stays exactly as it was for hand-written groups.</summary>
    [Fact]
    public void HandWrittenGroupWithNoImplStillSkips()
    {
        var registry = HandWrittenRegistry();
        Generated(InScopeGroup);

        Assert.Equal("not yet implemented in dotnet-sdk test suite", Only(Build(registry), "s3-crud").Skip);
    }

    [Fact]
    public void ScenarioBackendResolvesAGeneratedTest()
    {
        var registry = HandWrittenRegistry();
        Generated(InScopeGroup);

        var test = Only(
            Build(registry, (group, _) => group.Scenario == "scenarios/sqs.json" ? Noop : null),
            "gen-sqs");

        Assert.Null(test.Skip);
        Assert.Same(Noop, test.Fn);
    }

    // -- A bad generated file is an error, never a silent drop ----------------

    [Fact]
    public void CollidingGroupNameIsALoadError()
    {
        var registry = HandWrittenRegistry();
        Generated("""
            {
              "version": 1,
              "groups": [
                {"service": "s3", "name": "s3-crud", "generated": true,
                 "state": "candidate", "suites": ["dotnet-sdk"],
                 "tests": [{"name": "CreateBucket"}]}
              ]
            }
            """);

        var error = Assert.Throws<InvalidDataException>(() => RegistryLoader.Load(registry));
        Assert.Contains("s3-crud", error.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void UnparsableGeneratedFileIsALoadError()
    {
        var registry = HandWrittenRegistry();
        Generated("""{"version": 1, "groups": [""");
        Assert.Throws<InvalidDataException>(() => RegistryLoader.Load(registry));
    }

    [Fact]
    public void WrongVersionIsALoadError()
    {
        var registry = HandWrittenRegistry();
        Generated("""{"version":2,"groups":[]}""");

        var error = Assert.Throws<InvalidDataException>(() => RegistryLoader.Load(registry));
        Assert.Contains("version", error.Message, StringComparison.Ordinal);
    }

    [Theory]
    // No "generated": true.
    [InlineData("""
        {"service":"sqs","name":"gen-sqs","state":"candidate",
         "suites":["dotnet-sdk"],"tests":[{"name":"SendMessage"}]}
        """)]
    // No "state".
    [InlineData("""
        {"service":"sqs","name":"gen-sqs","generated":true,
         "suites":["dotnet-sdk"],"tests":[{"name":"SendMessage"}]}
        """)]
    // No "suites".
    [InlineData("""
        {"service":"sqs","name":"gen-sqs","generated":true,
         "state":"candidate","tests":[{"name":"SendMessage"}]}
        """)]
    public void GroupMissingGeneratedStateOrSuitesIsALoadError(string group)
    {
        var registry = HandWrittenRegistry();
        Generated($$"""{"version":1,"groups":[{{group}}]}""");

        var error = Assert.Throws<InvalidDataException>(() => RegistryLoader.Load(registry));
        Assert.Contains("gen-sqs", error.Message, StringComparison.Ordinal);
    }
}
