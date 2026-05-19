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
 * <p>Groups: sfn-statemachines.
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
                Map.entry("CreateStateMachine",  this::createStateMachine),
                Map.entry("DescribeStateMachine",this::describeStateMachine),
                Map.entry("ListStateMachines",   this::listStateMachines),
                Map.entry("StartExecution",      this::startExecution),
                Map.entry("DeleteStateMachine",  this::deleteStateMachine)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of("sfn-statemachines", this::setupStateMachines);
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of("sfn-statemachines",
                ctx -> deleteSfnSilently(ctx.getString("sfnArn")));
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

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteSfnSilently(String arn) {
        if (arn == null) return;
        try { sfn().deleteStateMachine(r -> r.stateMachineArn(arn)); } catch (Exception ignored) {}
    }
}
