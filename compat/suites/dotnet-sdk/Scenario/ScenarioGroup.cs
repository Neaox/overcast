using OvercastCompat.Harness;

namespace OvercastCompat.Scenario;

/// <summary>
/// Running a generated group (compat/model/README.md § The scenario file).
/// </summary>
/// <remarks>
/// A group is setup → tests → teardown. Setup runs every step in order and a
/// failure reports every test in the group as skip with
/// "setup failed: &lt;the six fields&gt;" — which the harness Runner does for
/// us, from the exception thrown here. Teardown runs afterwards <b>even when
/// setup failed</b>, with every step wrapped individually: a setup that failed
/// on its third step has already created what its first two made, and no test
/// will run to remove it.
/// <para>The emitted file declares one of these per group and hangs its setup,
/// tests and teardown off it, so the group name and the scenario file —
/// failure-message fields 1 and 6 — are written once.</para>
/// </remarks>
internal sealed class ScenarioGroup(string name, string file)
{
    /// <summary>
    /// Where the group's context bag lives on the harness TestContext. The
    /// harness creates one TestContext per group run and hands the same one to
    /// setup, every test and teardown, so the bag has exactly the lifetime the
    /// IR gives a group's context.
    /// </summary>
    private const string BagKey = "scenario_context";

    /// <summary>The registry group name (sqs-gen-queue).</summary>
    public string Name { get; } = name;

    /// <summary>
    /// The scenario file this group was generated from, repository-relative:
    /// failure-message field 6's first half.
    /// </summary>
    public string File { get; } = file;

    /// <summary>Runs one generated test: the primary call, then every clause in order.</summary>
    public async Task RunTestAsync(TestContext context, string name, ScenarioTest test)
    {
        var execution = NewExecution(context, name);
        var wanted = ErrorCodeClause(test.Assert);

        var (observed, sdkError) = await execution.CallRawAsync(test.Call, "call");
        if (wanted is not null)
        {
            // A test carrying an errorCode clause expects its primary call to
            // fail; the generator refuses such a test any clause that would
            // read the primary response, so every other clause makes a call of
            // its own.
            if (sdkError is null)
            {
                throw execution.Fail(observed, "call", "errorCode", "", Failures.AcceptedCodes(wanted), "<no error>");
            }
            if (!Errors.Matches(sdkError, wanted))
            {
                throw execution.Fail(observed, "call", "errorCode", "", Failures.AcceptedCodes(wanted), Failures.Quote(sdkError.Message));
            }
        }
        else if (sdkError is not null)
        {
            throw execution.FailedCall(observed, "call", sdkError);
        }
        else
        {
            execution.ApplyExports(test.Call, observed, "call");
        }

        for (var i = 0; i < test.Assert.Length; i++)
        {
            var clause = test.Assert[i];
            if (clause.Kind == ClauseKind.ErrorCode)
            {
                continue; // already checked against the primary call
            }
            await execution.AssertAsync(clause, observed, $"assert[{i}]");
        }
    }

    /// <summary>
    /// Runs a group's setup steps in order, stopping at the first failure.
    /// </summary>
    /// <remarks>
    /// The failure is thrown to the harness, which reports every test in the
    /// group as skip with "setup failed: &lt;message&gt;" and still runs
    /// teardown. An empty list is a no-op, not a missing phase: a probe group
    /// has nothing to set up and still registers the hook, so "a probe creates
    /// nothing" is visible in the emitted source rather than being a convention
    /// to remember.
    /// </remarks>
    public async Task RunSetupAsync(TestContext context, params ScenarioCall[] calls)
    {
        var execution = NewExecution(context, "setup");
        for (var i = 0; i < calls.Length; i++)
        {
            await execution.InvokeAsync(calls[i], $"setup[{i}]");
        }
    }

    /// <summary>
    /// Runs a group's teardown steps, each wrapped individually: an error, or
    /// an unresolvable ref, skips that step and the rest still run.
    /// </summary>
    /// <remarks>
    /// Each skip is logged to stderr and none of them fails the group, which is
    /// this suite's existing teardown convention and compat/AGENTS.md's
    /// "teardown must not throw". Throwing instead would report a teardown
    /// failure on every clean run of a lifecycle group: the delete test has
    /// already removed the resource the teardown step names, so a "not found"
    /// there is the expected outcome, not a leak. Proof that nothing leaked is
    /// the orphan sweep — a {runId} search after the run — not the teardown's
    /// own exit status.
    /// </remarks>
    public async Task RunTeardownAsync(TestContext context, params ScenarioCall[] calls)
    {
        var execution = NewExecution(context, "teardown");
        for (var i = 0; i < calls.Length; i++)
        {
            var step = $"teardown[{i}]";
            try
            {
                await execution.InvokeAsync(calls[i], step);
            }
            catch (Exception ex)
            {
                context.Log($"{Name}: skipped {step}: {ex.Message}");
            }
        }
    }

    private Execution NewExecution(TestContext context, string test) =>
        new(this, context, BagFor(context), test);

    /// <summary>
    /// The group's context bag, created on first use. The create-if-absent is
    /// atomic because a parallel group's tests share one TestContext and reach
    /// this concurrently.
    /// </summary>
    /// <remarks>
    /// Something else under this key cannot happen — the key is private to this
    /// class and nothing else writes it — and if it ever did, handing back a
    /// fresh bag would be the worst answer available: the exports one step
    /// wrote would be invisible to the next, and the step would fail on a
    /// missing $ref rather than on the thing that is actually wrong.
    /// </remarks>
    private static ContextBag BagFor(TestContext context) =>
        context.LoadOrStore(BagKey, static () => new ContextBag()) as ContextBag
            ?? throw new InvalidOperationException(
                $"the scenario context key \"{BagKey}\" holds something other than a ContextBag");

    /// <summary>
    /// The test's errorCode clause, if it has one. Its presence means the
    /// primary call is expected to fail.
    /// </summary>
    private static ErrorSpec? ErrorCodeClause(Clause[] clauses)
    {
        foreach (var clause in clauses)
        {
            if (clause.Kind == ClauseKind.ErrorCode)
            {
                return clause.Error;
            }
        }
        return null;
    }
}
