namespace OvercastCompat.Harness;

public delegate Task TestFn(TestContext context);
public delegate Task SetupFn(TestContext context);

public sealed record TestCase(
    string Name,
    TestFn Fn,
    string? Op = null,
    string? Skip = null,
    IReadOnlyList<string>? Depends = null)
{
    public IReadOnlyList<string> Depends { get; init; } = Depends ?? Array.Empty<string>();
}

public sealed record TestGroup(
    string Suite,
    string Service,
    string Name,
    IReadOnlyList<TestCase> Tests,
    SetupFn? Setup = null,
    SetupFn? Teardown = null);
