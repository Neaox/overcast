using System.Text.Json;

namespace OvercastCompat.Harness;

public static class Runner
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNamingPolicy = null,
        DefaultIgnoreCondition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull,
    };

    public static async Task RunSuiteAsync(string suite, string endpoint, string region, IReadOnlyList<TestGroup> groups)
    {
        var started = DateTimeOffset.UtcNow;
        Emit(new
        {
            @event = "run_start",
            suite,
            started_at = started.ToString("O"),
            endpoint,
            version = "1",
            total_tests = groups.Sum(group => group.Tests.Count),
        });

        var slots = 8;
        if (int.TryParse(Environment.GetEnvironmentVariable("OVERCAST_COMPAT_PARALLEL_SLOTS"), out var configured) && configured > 0)
        {
            slots = configured;
        }

        var semaphore = new SemaphoreSlim(slots, slots);
        var tasks = groups.Select(async group =>
        {
            await semaphore.WaitAsync();
            try
            {
                return await RunGroupAsync(suite, endpoint, region, group);
            }
            finally
            {
                semaphore.Release();
            }
        });

        var results = await Task.WhenAll(tasks);
        Emit(new
        {
            @event = "run_end",
            suite,
            passed = results.Sum(result => result.Passed),
            failed = results.Sum(result => result.Failed),
            skipped = results.Sum(result => result.Skipped),
            unimplemented = results.Sum(result => result.Unimplemented),
            duration_ms = (long)(DateTimeOffset.UtcNow - started).TotalMilliseconds,
        });
    }

    private static async Task<GroupResult> RunGroupAsync(string suite, string endpoint, string region, TestGroup group)
    {
        var context = new TestContext(endpoint, region, Environment.GetEnvironmentVariable("OVERCAST_COMPAT_RUN_ID") ?? "local");
        var result = new GroupResult();

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

                await RunTeardownAsync(group, context);
                return result;
            }
        }

        var blocked = new HashSet<string>(StringComparer.Ordinal);
        foreach (var test in group.Tests)
        {
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

            var started = DateTimeOffset.UtcNow;
            try
            {
                await test.Fn(context);
                EmitTestResult(suite, group, test.Name, "pass", (long)(DateTimeOffset.UtcNow - started).TotalMilliseconds, null);
                result.Passed++;
            }
            catch (Exception ex)
            {
                var status = IsUnimplemented(ex) ? "unimplemented" : "fail";
                EmitTestResult(suite, group, test.Name, status, (long)(DateTimeOffset.UtcNow - started).TotalMilliseconds, ex.Message);
                if (status == "unimplemented")
                {
                    result.Unimplemented++;
                }
                else
                {
                    result.Failed++;
                }
                blocked.Add(test.Name);
            }
        }

        await RunTeardownAsync(group, context);
        return result;
    }

    private static async Task RunTeardownAsync(TestGroup group, TestContext context)
    {
        if (group.Teardown is null)
        {
            return;
        }

        try
        {
            await group.Teardown(context);
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine($"[dotnet-sdk] teardown failed for {group.Name}: {ex.Message}");
        }
    }

    public static bool IsUnimplemented(Exception exception)
    {
        var message = exception.ToString();
        return message.Contains("501", StringComparison.OrdinalIgnoreCase)
            || message.Contains("NotImplemented", StringComparison.OrdinalIgnoreCase)
            || message.Contains("UnknownOperationException", StringComparison.OrdinalIgnoreCase)
            || message.Contains("Unknown action", StringComparison.OrdinalIgnoreCase)
            || message.Contains("not implemented", StringComparison.OrdinalIgnoreCase);
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

    private sealed class GroupResult
    {
        public int Passed { get; set; }
        public int Failed { get; set; }
        public int Skipped { get; set; }
        public int Unimplemented { get; set; }
    }
}
