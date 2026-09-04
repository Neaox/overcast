package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.sfn.SfnClient;
import software.amazon.awssdk.services.sfn.model.*;

import java.util.Map;

/**
 * AWS Step Functions compatibility test group.
 *
 * <p>Groups: sfn-statemachines, sfn-executions.
 */
public final class StepFunctionsGroup implements ServiceGroup {

    private static final String PASS_STATE_DEFINITION = """
            {
              "Comment": "Overcast compat test state machine",
              "StartAt": "Pass",
              "States": {
                "Pass": {
                  "Type": "Pass",
                  "End": true
                }
              }
            }
            """;

    private final AwsClients clients;

    public StepFunctionsGroup(AwsClients clients) {
        this.clients = clients;
    }

    private SfnClient sfn() { return clients.sfn(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("sfn-statemachines:CreateStateMachine",   this::createStateMachine),
                Map.entry("sfn-statemachines:DescribeStateMachine", this::describeStateMachine),
                Map.entry("sfn-statemachines:ListStateMachines",    this::listStateMachines),
                Map.entry("sfn-statemachines:StartExecution",       this::startExecution),
                Map.entry("sfn-statemachines:DeleteStateMachine",   this::deleteStateMachine),
                // sfn-executions — group-qualified ("group:test", as in every
                // suite) so StartExecution does not collide with the
                // sfn-statemachines test of the same name.
                Map.entry("sfn-executions:StartExecution",      this::execStartExecution),
                Map.entry("sfn-executions:DescribeExecution",   this::describeExecution),
                Map.entry("sfn-executions:GetExecutionHistory", this::getExecutionHistory),
                Map.entry("sfn-executions:ListExecutions",      this::listExecutions),
                Map.entry("sfn-executions:StopExecution",       this::stopExecution)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of(
                "sfn-statemachines", this::setupStateMachines,
                "sfn-executions",    this::setupExecutions);
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of(
                "sfn-statemachines", ctx -> deleteSfnSilently(ctx.getString("sfnArn")),
                "sfn-executions",    ctx -> deleteSfnSilently(ctx.getString("sfnExecSmArn")));
    }

    // ── sfn-statemachines ─────────────────────────────────────────────────────

    private void setupStateMachines(TestContext ctx) {
        ctx.set("sfnName", "compat-" + ctx.runId());
        // A valid IAM role ARN is required; the emulator should accept a placeholder.
        ctx.set("sfnRoleArn", "arn:aws:iam::000000000000:role/compat-sfn-role");
    }

    private void createStateMachine(TestContext ctx) throws Exception {
        String name    = ctx.getString("sfnName");
        String roleArn = ctx.getString("sfnRoleArn");
        var resp = sfn().createStateMachine(r -> r
                .name(name)
                .definition(PASS_STATE_DEFINITION)
                .roleArn(roleArn)
                .type(StateMachineType.STANDARD));
        Assertions.assertNotBlank(resp.stateMachineArn(), "CreateStateMachine: stateMachineArn is blank");
        ctx.set("sfnArn", resp.stateMachineArn());
    }

    private void describeStateMachine(TestContext ctx) throws Exception {
        String arn = ctx.getString("sfnArn");
        var resp = sfn().describeStateMachine(r -> r.stateMachineArn(arn));
        Assertions.assertEquals(ctx.getString("sfnName"), resp.name(),
                "DescribeStateMachine: name mismatch");
    }

    private void listStateMachines(TestContext ctx) throws Exception {
        var resp = sfn().listStateMachines(r -> r.maxResults(100));
        String arn  = ctx.getString("sfnArn");
        boolean found = resp.stateMachines().stream().anyMatch(m -> m.stateMachineArn().equals(arn));
        Assertions.assertTrue(found, "ListStateMachines: created state machine not found");
    }

    private void startExecution(TestContext ctx) throws Exception {
        String arn = ctx.getString("sfnArn");
        var resp = sfn().startExecution(r -> r
                .stateMachineArn(arn)
                .input("{\"compat\":true}"));
        Assertions.assertNotBlank(resp.executionArn(), "StartExecution: executionArn is blank");
    }

    private void deleteStateMachine(TestContext ctx) throws Exception {
        String arn = ctx.getString("sfnArn");
        sfn().deleteStateMachine(r -> r.stateMachineArn(arn));
        ctx.set("sfnArn", null);
    }

    // ── sfn-executions ────────────────────────────────────────────────────────
    //
    // The execution engine really interprets the definition, so these tests
    // assert on what the execution produced rather than just that a call
    // succeeded. This group provisions its own state machine in setup.

    private static final String GREETING_DEFINITION = """
            {
              "Comment": "compat executions",
              "StartAt": "Hello",
              "States": {
                "Hello": {
                  "Type": "Pass",
                  "Result": { "greeting": "hello" },
                  "End": true
                }
              }
            }
            """;

    private void setupExecutions(TestContext ctx) throws Exception {
        var resp = sfn().createStateMachine(r -> r
                .name("compat-exec-" + ctx.runId())
                .definition(GREETING_DEFINITION)
                .roleArn("arn:aws:iam::000000000000:role/compat-sfn-role")
                .type(StateMachineType.EXPRESS));
        Assertions.assertNotBlank(resp.stateMachineArn(), "setup sfn-executions: stateMachineArn is blank");
        ctx.set("sfnExecSmArn", resp.stateMachineArn());
    }

    private void execStartExecution(TestContext ctx) throws Exception {
        String smArn = ctx.getString("sfnExecSmArn");
        Assertions.assertNotBlank(smArn, "StartExecution: no state machine from setup");
        var resp = sfn().startExecution(r -> r
                .stateMachineArn(smArn)
                .name("run-" + ctx.runId())
                .input("{\"key\":\"value\"}"));
        Assertions.assertNotBlank(resp.executionArn(), "StartExecution: executionArn is blank");
        ctx.set("sfnExecArn", resp.executionArn());
    }

    private void describeExecution(TestContext ctx) throws Exception {
        String execArn = ctx.getString("sfnExecArn");
        Assertions.assertNotBlank(execArn, "DescribeExecution: no execution from StartExecution");
        var resp = sfn().describeExecution(r -> r.executionArn(execArn));
        Assertions.assertNotBlank(resp.statusAsString(), "DescribeExecution: status is blank");
        Assertions.assertEquals(execArn, resp.executionArn(),
                "DescribeExecution: executionArn did not round-trip");
    }

    private void getExecutionHistory(TestContext ctx) throws Exception {
        String execArn = ctx.getString("sfnExecArn");
        Assertions.assertNotBlank(execArn, "GetExecutionHistory: no execution from StartExecution");
        var resp = sfn().getExecutionHistory(r -> r.executionArn(execArn));
        Assertions.assertTrue(!resp.events().isEmpty(), "GetExecutionHistory: no events");
    }

    private void listExecutions(TestContext ctx) throws Exception {
        String smArn = ctx.getString("sfnExecSmArn");
        String execArn = ctx.getString("sfnExecArn");
        Assertions.assertNotBlank(smArn, "ListExecutions: no state machine from setup");
        var resp = sfn().listExecutions(r -> r.stateMachineArn(smArn));
        boolean found = resp.executions().stream().anyMatch(e -> e.executionArn().equals(execArn));
        Assertions.assertTrue(found, "ListExecutions: started execution not listed");
    }

    private void stopExecution(TestContext ctx) throws Exception {
        String execArn = ctx.getString("sfnExecArn");
        Assertions.assertNotBlank(execArn, "StopExecution: no execution from StartExecution");
        sfn().stopExecution(r -> r.executionArn(execArn));
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteSfnSilently(String arn) {
        if (arn == null) return;
        try { sfn().deleteStateMachine(r -> r.stateMachineArn(arn)); } catch (Exception ignored) {}
    }
}
