using System.Text.Json;
using Amazon.EventBridge.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

/// <summary>
/// EventBridge groups.
///
/// eventbridge-patterns — TestEventPattern, the stateless pattern matcher. It
/// creates nothing, so the group has no setup or teardown: both tests post the
/// same event and differ only in the pattern they match it against.
/// </summary>
public sealed class EventBridgeGroup(AwsClients clients) : IServiceGroup
{
    private const string EventSource = "compat.eventbridge-patterns";
    private const string EventDetailType = "order.created";

    /// <summary>Matches the shared event on both source and detail-type.</summary>
    private const string MatchingPattern =
        """{"source":["compat.eventbridge-patterns"],"detail-type":["order.created"]}""";

    /// <summary>A source the shared event does not carry.</summary>
    private const string NonMatchingPattern =
        """{"source":["compat.eventbridge-patterns.other"]}""";

    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["TestEventPattern"] = TestEventPatternAsync,
        ["TestEventPatternNoMatch"] = TestEventPatternNoMatchAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal);

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal);

    // ── eventbridge-patterns ──

    /// <summary>
    /// The event both tests match against, serialised as the JSON string the
    /// API takes. The run id rides in <c>id</c> so a failure names the run that
    /// produced it.
    /// </summary>
    private static string SharedEvent(TestContext context) => JsonSerializer.Serialize(
        new Dictionary<string, object?>(StringComparer.Ordinal)
        {
            ["id"] = context.RunId,
            ["detail-type"] = EventDetailType,
            ["source"] = EventSource,
            ["account"] = "000000000000",
            ["time"] = "2026-01-01T00:00:00Z",
            ["region"] = context.Region,
            ["resources"] = Array.Empty<string>(),
            ["detail"] = new Dictionary<string, object?>(StringComparer.Ordinal) { ["orderId"] = "1" },
        });

    private async Task TestEventPatternAsync(TestContext context)
    {
        var response = await clients.EventBridge().TestEventPatternAsync(new TestEventPatternRequest
        {
            EventPattern = MatchingPattern,
            Event = SharedEvent(context),
        });
        // Checked for presence as well as value: an omitted Result deserialises
        // to false, which is the same value a genuine non-match reports.
        Assertions.NotNull(response.Result, "TestEventPattern: Result");
        Assertions.True(response.Result == true,
            $"TestEventPattern: expected Result=true for pattern {MatchingPattern}, got {response.Result} (runId={context.RunId})");
    }

    private async Task TestEventPatternNoMatchAsync(TestContext context)
    {
        var response = await clients.EventBridge().TestEventPatternAsync(new TestEventPatternRequest
        {
            EventPattern = NonMatchingPattern,
            Event = SharedEvent(context),
        });
        Assertions.NotNull(response.Result, "TestEventPatternNoMatch: Result");
        Assertions.True(response.Result == false,
            $"TestEventPatternNoMatch: expected Result=false for pattern {NonMatchingPattern}, got {response.Result} (runId={context.RunId})");
    }
}
