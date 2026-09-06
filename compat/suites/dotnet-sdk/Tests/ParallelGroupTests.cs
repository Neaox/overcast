using System.Text.Json;
using OvercastCompat.Harness;
using Xunit;

namespace OvercastCompat.Tests;

/// <summary>
/// The parallel-group path in <see cref="Runner"/>, and the shared state it
/// rests on. Ported from the go-sdk suite's harness tests, because the two
/// backends read the same <c>"parallel"</c> flag out of
/// <c>registry.generated.json</c> and must behave the same way.
/// </summary>
/// <remarks>
/// Nothing here talks to Overcast: each test's body is a delegate this file
/// supplies, so what is under test is the runner's scheduling and its result
/// stream, not any AWS call.
/// <para>The runner emits NDJSON on stdout, so these redirect
/// <see cref="Console.Out"/> — which is process-wide. The collection below
/// keeps them from running beside another collection that might write there.</para>
/// </remarks>
[Collection(ConsoleCapture.Name)]
public sealed class ParallelGroupTests
{
    private const string Suite = "dotnet-sdk";

    /// <summary>
    /// The flag does what it says: eight tests that each wait for all eight to
    /// have started can only finish if they overlap.
    /// </summary>
    /// <remarks>
    /// The wait is asynchronous rather than a <see cref="Barrier"/>, which is
    /// what the go-sdk suite's equivalent uses. A goroutine per test makes a
    /// blocking barrier work there; here the runner starts the tests on one
    /// thread and relies on each awaiting, so a test that blocked instead would
    /// stall the eight before the second had begun — and would report the
    /// runner as serial when it is not.
    /// </remarks>
    [Fact]
    public async Task AParallelGroupRunsItsTestsConcurrently()
    {
        using var slots = new ParallelSlots("8");
        var arrived = 0;
        var allStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var tests = Enumerable.Range(0, 8).Select(i => new TestCase(
            $"Probe{i:00}",
            async _ =>
            {
                if (Interlocked.Increment(ref arrived) == 8)
                {
                    allStarted.SetResult();
                }
                try
                {
                    // Bounded, not infinite: a serial runner would wait here
                    // forever, and a hung test says nothing until the CI job
                    // itself times out.
                    await allStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));
                }
                catch (TimeoutException)
                {
                    throw new InvalidOperationException(
                        "the group's tests did not overlap, so this ran serially");
                }
            })).ToList();

        var events = await CaptureAsync(() => Runner.RunGroupAsync(
            Suite, "http://127.0.0.1:1", "us-east-1",
            new TestGroup(Suite, "widgets", "widgets-gen-probe", tests, Parallel: true)));

        Assert.Equal(8, events.Count);
        Assert.All(events, ev => Assert.Equal("pass", ev.GetProperty("status").GetString()));
    }

    /// <summary>
    /// The half of the contract that is not about speed: results come out in
    /// declaration order whatever order the tests finished in, so the NDJSON a
    /// parallel group produces is the stream the serial path produced. The
    /// dashboard, the baseline and the flake detector all read it.
    /// </summary>
    [Fact]
    public async Task AParallelGroupEmitsItsResultsInRegistryOrder()
    {
        using var slots = new ParallelSlots("8");
        // Test i waits (8-i) ms, so completion order is the reverse of
        // declaration order; every third one fails.
        var tests = Enumerable.Range(0, 8).Select(i => new TestCase(
            $"Probe{i:00}",
            async _ =>
            {
                await Task.Delay(TimeSpan.FromMilliseconds(8 - i));
                if (i % 3 == 0)
                {
                    throw new InvalidOperationException("boom");
                }
            })).ToList();

        var events = await CaptureAsync(() => Runner.RunGroupAsync(
            Suite, "http://127.0.0.1:1", "us-east-1",
            new TestGroup(Suite, "widgets", "widgets-gen-probe", tests, Parallel: true)));

        Assert.Equal(
            tests.Select(test => test.Name).ToList(),
            events.Select(ev => ev.GetProperty("test").GetString() ?? "").ToList());
        Assert.Equal("fail", events[0].GetProperty("status").GetString());
        Assert.Equal("pass", events[1].GetProperty("status").GetString());
    }

    /// <summary>
    /// Both halves of the runner's condition are load-bearing. The concurrent
    /// path cannot express the dependency gate — it would have to decide what to
    /// skip from outcomes that have not happened yet — so a group declaring one
    /// runs serially even where the registry says parallel.
    /// </summary>
    [Fact]
    public async Task AParallelGroupWhoseTestsDependOnEachOtherRunsSerially()
    {
        using var slots = new ParallelSlots("8");
        var inFlight = 0;
        var peak = 0;
        var gate = new object();
        var tests = new List<TestCase>();
        for (var i = 0; i < 4; i++)
        {
            tests.Add(new TestCase(
                $"Probe{i:00}",
                async _ =>
                {
                    lock (gate)
                    {
                        inFlight++;
                        peak = Math.Max(peak, inFlight);
                    }
                    await Task.Delay(TimeSpan.FromMilliseconds(5));
                    lock (gate)
                    {
                        inFlight--;
                    }
                },
                Depends: i == 0 ? null : (IReadOnlyList<string>)new[] { "Probe00" }));
        }

        var events = await CaptureAsync(() => Runner.RunGroupAsync(
            Suite, "http://127.0.0.1:1", "us-east-1",
            new TestGroup(Suite, "widgets", "widgets-gen-probe", tests, Parallel: true)));

        Assert.Equal(1, peak);
        Assert.Equal(4, events.Count);
    }

    /// <summary>
    /// The bag a parallel group's tests share is created once. Get-then-Set
    /// would hand each racing caller a different bag, and the exports one test
    /// wrote would be invisible to the next.
    /// </summary>
    [Fact]
    public async Task LoadOrStoreCreatesOneValueUnderConcurrentCallers()
    {
        var context = new TestContext("http://127.0.0.1:1", "us-east-1", "run");
        using var start = new Barrier(16);
        var created = 0;

        var values = await Task.WhenAll(Enumerable.Range(0, 16).Select(_ => Task.Run(() =>
        {
            start.SignalAndWait(TimeSpan.FromSeconds(30));
            return context.LoadOrStore("bag", () =>
            {
                Interlocked.Increment(ref created);
                return new object();
            });
        })));

        Assert.Equal(1, created);
        Assert.All(values, value => Assert.Same(values[0], value));
        Assert.Same(values[0], context.Get<object>("bag"));
    }

    /// <summary>
    /// Runs the group with stdout captured, and returns the test_result events
    /// it emitted, in the order it emitted them.
    /// </summary>
    private static async Task<IReadOnlyList<JsonElement>> CaptureAsync(Func<Task> run)
    {
        var captured = new StringWriter();
        var saved = Console.Out;
        Console.SetOut(captured);
        try
        {
            await run();
        }
        finally
        {
            Console.SetOut(saved);
        }

        return captured.ToString()
            .Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
            .Select(line => JsonDocument.Parse(line).RootElement)
            .Where(ev => ev.GetProperty("event").GetString() == "test_result")
            .ToList();
    }

    /// <summary>
    /// Sets OVERCAST_COMPAT_PARALLEL_SLOTS for one test and restores it after.
    /// The runner reads it on every call, so a test that left it set would
    /// change how a later one schedules.
    /// </summary>
    private sealed class ParallelSlots : IDisposable
    {
        private const string Name = "OVERCAST_COMPAT_PARALLEL_SLOTS";
        private readonly string? _saved = Environment.GetEnvironmentVariable(Name);

        internal ParallelSlots(string value) => Environment.SetEnvironmentVariable(Name, value);

        public void Dispose() => Environment.SetEnvironmentVariable(Name, _saved);
    }
}

/// <summary>
/// The collection for tests that redirect <see cref="Console.Out"/>.
/// Disabling parallelization keeps them from running beside another collection
/// while the process-wide writer is swapped out.
/// </summary>
[CollectionDefinition(Name, DisableParallelization = true)]
public sealed class ConsoleCapture
{
    public const string Name = "console-capture";
}
