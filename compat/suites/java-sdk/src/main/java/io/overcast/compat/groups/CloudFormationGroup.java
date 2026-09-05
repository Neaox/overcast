package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.cloudformation.CloudFormationClient;
import software.amazon.awssdk.services.cloudformation.model.*;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * CloudFormation compatibility test group.
 *
 * <p>Groups: cloudformation-stacks.
 */
public final class CloudFormationGroup implements ServiceGroup {

    /** Minimal valid CloudFormation template that creates no real resources. */
    private static final String MINIMAL_TEMPLATE = """
            {
              "AWSTemplateFormatVersion": "2010-09-09",
              "Description": "Overcast compat test stack",
              "Resources": {
                "WaitHandle": {
                  "Type": "AWS::CloudFormation::WaitConditionHandle"
                }
              }
            }
            """;

    private final AwsClients clients;

    public CloudFormationGroup(AwsClients clients) {
        this.clients = clients;
    }

    private CloudFormationClient cfn() { return clients.cloudFormation(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("cloudformation-stacks:CreateStack",      this::createStack),
                Map.entry("cloudformation-stacks:DescribeStacks",   this::describeStacks),
                Map.entry("cloudformation-stacks:ListStacks",       this::listStacks),
                Map.entry("cloudformation-stacks:UpdateStack",      this::updateStack),
                Map.entry("cloudformation-stacks:ValidateTemplate", this::validateTemplate),
                Map.entry("cloudformation-stacks:DeleteStack",      this::deleteStack)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of("cloudformation-stacks", this::setupStacks);
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of("cloudformation-stacks", ctx -> deleteStackSilently(ctx.getString("cfnStackName")));
    }

    // ── cloudformation-stacks ─────────────────────────────────────────────────

    private void setupStacks(TestContext ctx) {
        ctx.set("cfnStackName", "compat-" + ctx.runId());
    }

    private void createStack(TestContext ctx) throws Exception {
        String name = ctx.getString("cfnStackName");
        cfn().createStack(r -> r.stackName(name).templateBody(MINIMAL_TEMPLATE));
        waitStackStatus(name, "CREATE_COMPLETE", "CREATE_FAILED");
        var stack = describeStack(name);
        Assertions.assertNotBlank(stack.stackId(), "CreateStack: stackId is blank");
    }

    private void describeStacks(TestContext ctx) throws Exception {
        String name = ctx.getString("cfnStackName");
        var stack = describeStack(name);
        Assertions.assertEquals(name, stack.stackName(), "DescribeStacks: stackName mismatch");
    }

    /**
     * Covers StackStatusFilter in both directions: a filter naming the stack's
     * status must include it, and one naming a status it cannot hold must
     * exclude it. Only the second catches an implementation that accepts the
     * parameter and ignores it.
     *
     * <p>DELETE_FAILED is a status the stack cannot reach here, since nothing
     * has tried to delete it yet.
     */
    private void listStacks(TestContext ctx) throws Exception {
        String name = ctx.getString("cfnStackName");

        var all = listStackNames();
        Assertions.assertTrue(all.contains(name),
                "ListStacks: " + name + " not in unfiltered listing " + all);

        var active = listStackNames(StackStatus.CREATE_COMPLETE, StackStatus.UPDATE_COMPLETE);
        Assertions.assertTrue(active.contains(name),
                "ListStacks: " + name + " not in CREATE_COMPLETE/UPDATE_COMPLETE listing " + active);

        var deleteFailed = listStackNames(StackStatus.DELETE_FAILED);
        Assertions.assertTrue(!deleteFailed.contains(name),
                "ListStacks: " + name + " returned by a DELETE_FAILED filter");
    }

    private void updateStack(TestContext ctx) throws Exception {
        String name = ctx.getString("cfnStackName");
        cfn().updateStack(r -> r
                .stackName(name)
                .templateBody(MINIMAL_TEMPLATE)
                .parameters(Parameter.builder()
                        .parameterKey("Unused").usePreviousValue(false).build())
                // Re-submit the same template — emulator should accept this.
                .usePreviousTemplate(true));
        waitStackStatus(name, "UPDATE_COMPLETE", "UPDATE_FAILED");
    }

    private void deleteStack(TestContext ctx) throws Exception {
        String name = ctx.getString("cfnStackName");
        cfn().deleteStack(r -> r.stackName(name));
        ctx.set("cfnStackName", null);
    }

    private void validateTemplate(TestContext ctx) throws Exception {
        cfn().validateTemplate(r -> r.templateBody(MINIMAL_TEMPLATE));
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private Stack describeStack(String name) {
        return cfn().describeStacks(r -> r.stackName(name)).stacks().get(0);
    }

    /**
     * Stack names from ListStacks, optionally filtered by status. Also checks
     * the invariant a status filter has to hold: every summary returned is in
     * one of the requested statuses.
     */
    private List<String> listStackNames(StackStatus... statuses) {
        var resp = cfn().listStacks(r -> r.stackStatusFilters(statuses));
        var wanted = List.of(statuses);
        var names = new ArrayList<String>();
        for (StackSummary s : resp.stackSummaries()) {
            if (!wanted.isEmpty() && !wanted.contains(s.stackStatus())) {
                throw new AssertionError("ListStacks: " + s.stackName() + " returned with status "
                        + s.stackStatusAsString() + ", not in filter " + wanted);
            }
            names.add(s.stackName());
        }
        return names;
    }

    private void waitStackStatus(String name, String... terminal) throws InterruptedException {
        for (int i = 0; i < 60; i++) {
            String status;
            try {
                status = describeStack(name).stackStatusAsString();
            } catch (Exception e) {
                return; // stack may have been deleted
            }
            for (String t : terminal) {
                if (t.equals(status)) return;
            }
            Thread.sleep(1_000);
        }
    }

    private void deleteStackSilently(String name) {
        if (name == null) return;
        try { cfn().deleteStack(r -> r.stackName(name)); } catch (Exception ignored) {}
    }
}
