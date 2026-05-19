using System.Text.Json;
using System.Text.Json.Serialization;

namespace OvercastCompat.Harness;

/// <summary>
/// Interactive mode runner for the .NET compat suite.
/// When OVERCAST_COMPAT_INTERACTIVE=1 is set, the suite enters a long-lived
/// stdin/stdout NDJSON protocol. See compat/PROTOCOL.md for the full spec.
/// </summary>
public static class InteractiveRunner
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    private static readonly JsonSerializerOptions ReadOptions = new()
    {
        PropertyNameCaseInsensitive = true,
    };

    public static async Task RunAsync(string suite, string endpoint, string region, IReadOnlyList<TestGroup> allGroups)
    {
        Emit(new { @event = "building", suite, message = "Loading registry and building test groups..." });

        var totalTests = allGroups.Sum(g => g.Tests.Count);
        Emit(new { @event = "ready", suite, total_tests = totalTests });

        var slots = 8;
        if (int.TryParse(Environment.GetEnvironmentVariable("OVERCAST_COMPAT_PARALLEL_SLOTS"), out var configured) && configured > 0)
        {
            slots = configured;
        }

        var semaphore = new SemaphoreSlim(slots, slots);
        var cancellationFlags = new System.Collections.Concurrent.ConcurrentDictionary<string, CancellationTokenSource>();
        var runningTest = new System.Collections.Concurrent.ConcurrentDictionary<string, string>();

        // Build lookup map: group name → TestGroup
        var groupMap = allGroups.ToDictionary(g => g.Name, g => g, StringComparer.Ordinal);

        using var cts = new CancellationTokenSource();

        // Read stdin in a loop, dispatch commands.
        await foreach (var line in ReadLinesAsync(cts.Token))
        {
            var trimmed = line.Trim();
            if (string.IsNullOrEmpty(trimmed)) continue;

            JsonDocument doc;
            try
            {
                doc = JsonDocument.Parse(trimmed);
            }
            catch
            {
                Console.Error.WriteLine($"[dotnet-sdk] invalid JSON on stdin: {trimmed}");
                continue;
            }

            if (!doc.RootElement.TryGetProperty("command", out var commandProp))
            {
                Console.Error.WriteLine($"[dotnet-sdk] missing 'command' field: {trimmed}");
                continue;
            }

            var command = commandProp.GetString();
            switch (command)
            {
                case "run":
                    HandleRun(trimmed, suite, endpoint, region, groupMap, semaphore, cancellationFlags, runningTest);
                    break;

                case "ping":
                    runningTest.TryGetValue(suite, out var pt);
                    Emit(new { @event = "pong", suite, running_test = pt });
                    break;

                case "cancel":
                    HandleCancel(doc, cancellationFlags);
                    break;

                case "shutdown":
                    // Cancel all in-flight work and exit.
                    foreach (var flag in cancellationFlags.Values)
                    {
                        flag.Cancel();
                    }
                    return;

                default:
                    Console.Error.WriteLine($"[dotnet-sdk] unknown command: {command}");
                    break;
            }
        }
    }

    private static void HandleRun(
        string json,
        string suite,
        string endpoint,
        string region,
        Dictionary<string, TestGroup> groupMap,
        SemaphoreSlim semaphore,
        System.Collections.Concurrent.ConcurrentDictionary<string, CancellationTokenSource> cancellationFlags,
        System.Collections.Concurrent.ConcurrentDictionary<string, string> runningTest)
    {
        RunCommand? cmd;
        try
        {
            cmd = JsonSerializer.Deserialize<RunCommand>(json, ReadOptions);
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine($"[dotnet-sdk] failed to parse run command: {ex.Message}");
            return;
        }

        if (cmd?.BatchId == null) return;

        // Resolve requested groups/tests.
        // A null or empty Tests list means "run all groups" (the run-all command).
        var groupsToRun = new List<TestGroup>();
        if (cmd.Tests == null || cmd.Tests.Count == 0)
        {
            groupsToRun.AddRange(groupMap.Values.OrderBy(g => g.Name));
        }
        else
        {
            foreach (var testRef in cmd.Tests)
            {
                if (!groupMap.TryGetValue(testRef.Group, out var group))
                {
                    Console.Error.WriteLine($"[dotnet-sdk] unknown group in run command: {testRef.Group}");
                    continue;
                }

                if (testRef.Tests is { Count: > 0 })
                {
                    var requested = new HashSet<string>(testRef.Tests, StringComparer.Ordinal);
                    var filtered = group.Tests.Where(t => requested.Contains(t.Name)).ToList();
                    groupsToRun.Add(group with { Tests = filtered });
                }
                else
                {
                    groupsToRun.Add(group);
                }
            }
        }

        var batchId = cmd.BatchId;

        // Fire off the batch asynchronously so stdin reading continues.
        _ = Task.Run(async () =>
        {
            var batchStart = DateTimeOffset.UtcNow;
            var tasks = groupsToRun.Select(async group =>
            {
                await semaphore.WaitAsync();
                try
                {
                    return await RunGroupInteractiveAsync(suite, endpoint, region, group, batchId, cancellationFlags, runningTest);
                }
                finally
                {
                    semaphore.Release();
                }
            });

            var results = await Task.WhenAll(tasks);

            Emit(new
            {
                @event = "batch_complete",
                suite,
                batch_id = batchId,
                passed = results.Sum(r => r.Passed),
                failed = results.Sum(r => r.Failed),
                skipped = results.Sum(r => r.Skipped),
                unimplemented = results.Sum(r => r.Unimplemented),
                cancelled = results.Sum(r => r.Cancelled),
                duration_ms = (long)(DateTimeOffset.UtcNow - batchStart).TotalMilliseconds,
            });
        });
    }

    private static void HandleCancel(
        JsonDocument doc,
        System.Collections.Concurrent.ConcurrentDictionary<string, CancellationTokenSource> cancellationFlags)
    {
        string? group = null;
        string? test = null;

        if (doc.RootElement.TryGetProperty("group", out var groupProp))
            group = groupProp.GetString();
        if (doc.RootElement.TryGetProperty("test", out var testProp))
            test = testProp.GetString();

        if (group != null && test != null)
        {
            var key = $"{group}:{test}";
            if (cancellationFlags.TryGetValue(key, out var flag))
            {
                flag.Cancel();
            }
        }
        else
        {
            foreach (var flag in cancellationFlags.Values)
            {
                flag.Cancel();
            }
        }
    }

    private static async Task<InteractiveGroupResult> RunGroupInteractiveAsync(
        string suite,
        string endpoint,
        string region,
        TestGroup group,
        string batchId,
        System.Collections.Concurrent.ConcurrentDictionary<string, CancellationTokenSource> cancellationFlags,
        System.Collections.Concurrent.ConcurrentDictionary<string, string> runningTest)
    {
        var context = new TestContext(endpoint, region, Environment.GetEnvironmentVariable("OVERCAST_COMPAT_RUN_ID") ?? "local");
        var result = new InteractiveGroupResult();

        // Register cancellation tokens for each test.
        foreach (var test in group.Tests)
        {
            var key = $"{group.Name}:{test.Name}";
            cancellationFlags.TryAdd(key, new CancellationTokenSource());
        }

        try
        {
            // Setup phase
            if (group.Setup is not null)
            {
                try
                {
                    await group.Setup(context);
                }
                catch (Exception ex)
                {
                    var reason = $"setup failed: {ex.Message}";
                    foreach (var test in group.Tests)
                    {
                        EmitTestResult(suite, group, test.Name, "skip", 0, reason);
                        result.Skipped++;
                    }

                    return result;
                }
            }

            var blocked = new HashSet<string>(StringComparer.Ordinal);
            foreach (var test in group.Tests)
            {
                var key = $"{group.Name}:{test.Name}";
                cancellationFlags.TryGetValue(key, out var cts);

                // Check cancellation before running.
                if (cts?.IsCancellationRequested == true)
                {
                    Emit(new { @event = "cancelled", suite, batch_id = batchId, group = group.Name, test = test.Name, reason = "user" });
                    result.Cancelled++;
                    blocked.Add(test.Name);
                    continue;
                }

                if (!string.IsNullOrWhiteSpace(test.Skip))
                {
                    EmitTestResult(suite, group, test.Name, "skip", 0, test.Skip);
                    result.Skipped++;
                    blocked.Add(test.Name);
                    continue;
                }

                var failedDeps = test.Depends.Where(blocked.Contains).ToList();
                if (failedDeps.Count > 0)
                {
                    EmitTestResult(suite, group, test.Name, "skip", 0, $"dependency failed: {string.Join(", ", failedDeps)}");
                    result.Skipped++;
                    blocked.Add(test.Name);
                    continue;
                }

                Emit(new { @event = "test_start", suite, service = group.Service, group = group.Name, test = test.Name });

                runningTest[suite] = $"{group.Name}:{test.Name}";

                var started = DateTimeOffset.UtcNow;
                try
                {
                    await test.Fn(context);
                    var ms = (long)(DateTimeOffset.UtcNow - started).TotalMilliseconds;

                    if (cts?.IsCancellationRequested == true)
                    {
                        Emit(new { @event = "cancelled", suite, batch_id = batchId, group = group.Name, test = test.Name, reason = "user" });
                        result.Cancelled++;
                        blocked.Add(test.Name);
                    }
                    else
                    {
                        EmitTestResult(suite, group, test.Name, "pass", ms, null);
                        result.Passed++;
                    }
                }
                catch (Exception ex)
                {
                    var ms = (long)(DateTimeOffset.UtcNow - started).TotalMilliseconds;

                    if (cts?.IsCancellationRequested == true)
                    {
                        Emit(new { @event = "cancelled", suite, batch_id = batchId, group = group.Name, test = test.Name, reason = "user" });
                        result.Cancelled++;
                    }
                    else
                    {
                        var status = Runner.IsUnimplemented(ex) ? "unimplemented" : "fail";
                        EmitTestResult(suite, group, test.Name, status, ms, ex.Message);
                        if (status == "unimplemented")
                            result.Unimplemented++;
                        else
                            result.Failed++;
                    }
                    blocked.Add(test.Name);
                }
            }

            runningTest.TryRemove(suite, out _);
        }
        finally
        {
            // Always run teardown and clean up cancellation flags.
            if (group.Teardown is not null)
            {
                try
                {
                    await group.Teardown(context);
                }
                catch (Exception ex)
                {
                    Console.Error.WriteLine($"[dotnet-sdk] teardown failed for {group.Name}: {ex.Message}");
                }
            }

            foreach (var test in group.Tests)
            {
                cancellationFlags.TryRemove($"{group.Name}:{test.Name}", out _);
            }
        }

        return result;
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private static async IAsyncEnumerable<string> ReadLinesAsync([System.Runtime.CompilerServices.EnumeratorCancellation] CancellationToken ct = default)
    {
        using var reader = new StreamReader(Console.OpenStandardInput());
        while (!ct.IsCancellationRequested)
        {
            string? line;
            try
            {
                line = await reader.ReadLineAsync(ct).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                yield break;
            }
            if (line == null) yield break; // stdin closed
            yield return line;
        }
    }

    private static void EmitTestResult(string suite, TestGroup group, string test, string status, long durationMs, string? error)
    {
        Emit(new
        {
            @event = "test_result",
            suite,
            service = group.Service,
            group = group.Name,
            test,
            status,
            duration_ms = durationMs,
            error,
        });
    }

    private static readonly object EmitLock = new();

    private static void Emit(object value)
    {
        lock (EmitLock)
        {
            Console.Out.WriteLine(JsonSerializer.Serialize(value, JsonOptions));
            Console.Out.Flush();
        }
    }

    // ── Types ─────────────────────────────────────────────────────────────────

    private sealed class InteractiveGroupResult
    {
        public int Passed { get; set; }
        public int Failed { get; set; }
        public int Skipped { get; set; }
        public int Unimplemented { get; set; }
        public int Cancelled { get; set; }
    }

    private sealed class RunCommand
    {
        [JsonPropertyName("command")]
        public string? Command { get; set; }

        [JsonPropertyName("batch_id")]
        public string? BatchId { get; set; }

        [JsonPropertyName("tests")]
        public List<TestRef>? Tests { get; set; }
    }

    private sealed class TestRef
    {
        [JsonPropertyName("group")]
        public string Group { get; set; } = "";

        [JsonPropertyName("tests")]
        public List<string>? Tests { get; set; }
    }
}
