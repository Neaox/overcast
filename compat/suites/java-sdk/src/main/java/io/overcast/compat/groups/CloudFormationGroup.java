package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.cloudformation.CloudFormationClient;
import software.amazon.awssdk.services.cloudformation.model.*;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * CloudFormation compatibility test group.
 *
 * <p>Groups: cloudformation-stacks, cloudformation-eks-refs.
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
                Map.entry("cloudformation-stacks:DeleteStack",      this::deleteStack),

                Map.entry("cloudformation-eks-refs:CreateStackWithEksRefs",            this::createStackWithEksRefs),
                Map.entry("cloudformation-eks-refs:DescribeStackResourcesPhysicalIds", this::describeStackResourcesPhysicalIds),
                Map.entry("cloudformation-eks-refs:GetAttClusterArn",                  this::getAttClusterArn),
                Map.entry("cloudformation-eks-refs:RefNodegroupIsClusterSlashName",    this::refNodegroupIsClusterSlashName),
                Map.entry("cloudformation-eks-refs:RefAddonIsClusterPipeAddon",        this::refAddonIsClusterPipeAddon),
                Map.entry("cloudformation-eks-refs:DeleteStackWithEksRefs",            this::deleteStackWithEksRefs)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of(
                "cloudformation-stacks", this::setupStacks,
                "cloudformation-eks-refs", this::setupEksRefs);
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of(
                "cloudformation-stacks", ctx -> deleteStackSilently(ctx.getString("cfnStackName")),
                // Deleting the stack cascades to the cluster, the nodegroup and
                // the add-on: CloudFormation owns every resource the group
                // creates, and deleting a stack deletes the resources it owns.
                "cloudformation-eks-refs", ctx -> deleteStackSilently(ctx.getString("cfnEksStackName")));
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

    // ── cloudformation-eks-refs ───────────────────────────────────────────────

    /**
     * CloudFormation documents Ref and Fn::GetAtt separately for every resource
     * type, and the EKS family disagrees with itself in ways only a deployed
     * stack reveals: Ref on a Cluster is its name and the ARN is a distinct Arn
     * attribute, Ref on a Nodegroup is {@code <cluster>/<nodegroup>}, and Ref on
     * an Addon is {@code <cluster>|<addon>} — a pipe, not a slash.
     *
     * <p>Overcast returned the cluster ARN from Ref (#1690) and separated the
     * add-on with "/" (#1692). Both were wire-observable and neither was caught
     * here, so this group asserts the values through CloudFormation alone —
     * stack outputs and DescribeStackResources — which is what a template
     * author actually sees.
     *
     * <p>The Nodegroup and the Addon are wired to their cluster purely by
     * {@code {"Ref": "Cluster"}} — no literal cluster name and no DependsOn.
     * That is the point: CloudFormation must hand CreateNodegroup and
     * CreateAddon the cluster <em>name</em>, so a Ref that resolves to the ARN
     * fails the stack outright.
     *
     * @see <a href="https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-cluster.html#aws-resource-eks-cluster-return-values">AWS::EKS::Cluster return values</a>
     * @see <a href="https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-addon.html#aws-resource-eks-addon-return-values">AWS::EKS::Addon return values</a>
     */
    private static final String EKS_REFS_TEMPLATE = """
            {
              "AWSTemplateFormatVersion": "2010-09-09",
              "Resources": {
                "Cluster": {
                  "Type": "AWS::EKS::Cluster",
                  "Properties": {
                    "Name": "%s",
                    "RoleArn": "arn:aws:iam::000000000000:role/compat-eks-refs-cluster",
                    "ResourcesVpcConfig": {"SubnetIds": ["subnet-1"]}
                  }
                },
                "Nodegroup": {
                  "Type": "AWS::EKS::Nodegroup",
                  "Properties": {
                    "ClusterName": {"Ref": "Cluster"},
                    "NodegroupName": "%s",
                    "NodeRole": "arn:aws:iam::000000000000:role/compat-eks-refs-node",
                    "Subnets": ["subnet-1"]
                  }
                },
                "Addon": {
                  "Type": "AWS::EKS::Addon",
                  "Properties": {
                    "ClusterName": {"Ref": "Cluster"},
                    "AddonName": "%s"
                  }
                }
              },
              "Outputs": {
                "ClusterRef":   {"Value": {"Ref": "Cluster"}},
                "ClusterArn":   {"Value": {"Fn::GetAtt": ["Cluster", "Arn"]}},
                "NodegroupRef": {"Value": {"Ref": "Nodegroup"}},
                "AddonRef":     {"Value": {"Ref": "Addon"}}
              }
            }
            """;

    /**
     * vpc-cni is one of the add-ons DescribeAddonVersions publishes. AWS refuses
     * an AddonName it does not know, so the name is not arbitrary.
     */
    private static final String EKS_REFS_ADDON_NAME = "vpc-cni";

    private void setupEksRefs(TestContext ctx) {
        ctx.set("cfnEksStackName", "compat-eks-refs-" + ctx.runId());
        ctx.set("cfnEksClusterName", "compat-eks-refs-cluster-" + ctx.runId());
        ctx.set("cfnEksNodegroupName", "compat-eks-refs-ng-" + ctx.runId());
    }

    private void createStackWithEksRefs(TestContext ctx) throws Exception {
        String name = ctx.getString("cfnEksStackName");
        String template = EKS_REFS_TEMPLATE.formatted(
                ctx.getString("cfnEksClusterName"),
                ctx.getString("cfnEksNodegroupName"),
                EKS_REFS_ADDON_NAME);
        var resp = cfn().createStack(r -> r.stackName(name).templateBody(template));
        Assertions.assertNotBlank(resp.stackId(), "CreateStack: stackId is blank");
        ctx.set("cfnEksStackId", resp.stackId());

        // Outputs and physical resource IDs only exist once the stack settles.
        waitStackStatus(name, "CREATE_COMPLETE", "CREATE_FAILED", "ROLLBACK_COMPLETE");
        var stack = describeStack(name);
        Assertions.assertEquals("CREATE_COMPLETE", stack.stackStatusAsString(),
                "CreateStack: " + name + " reason: " + stack.stackStatusReason());
    }

    /**
     * Asserts the physical ID CloudFormation recorded for each resource. The
     * physical ID is what Ref resolves to, so this is the same contract the
     * output assertions below check, seen through the API an operator reaches
     * for when a stack misbehaves.
     */
    private void describeStackResourcesPhysicalIds(TestContext ctx) throws Exception {
        String name = ctx.getString("cfnEksStackName");
        var physicalIds = new HashMap<String, String>();
        for (StackResource r : cfn().describeStackResources(b -> b.stackName(name)).stackResources()) {
            physicalIds.put(r.logicalResourceId(), r.physicalResourceId());
        }
        var want = eksRefsExpectedIds(ctx);
        for (String logicalId : List.of("Cluster", "Nodegroup", "Addon")) {
            Assertions.assertEquals(want.get(logicalId), physicalIds.get(logicalId),
                    "DescribeStackResources: " + logicalId + " PhysicalResourceId");
        }
    }

    /**
     * Covers the divergence AWS documents for AWS::EKS::Cluster: Ref is the
     * cluster name, and the ARN is reachable only as the Arn attribute.
     * Asserting both together is what makes the pair meaningful — either value
     * alone looks plausible on its own.
     */
    private void getAttClusterArn(TestContext ctx) throws Exception {
        var outputs = eksRefsOutputs(ctx);
        String cluster = ctx.getString("cfnEksClusterName");
        Assertions.assertEquals(cluster, outputs.get("ClusterRef"),
                "Ref on AWS::EKS::Cluster must be the cluster name");
        String account = accountFromStackId(ctx.getString("cfnEksStackId"));
        String want = "arn:aws:eks:" + ctx.region() + ":" + account + ":cluster/" + cluster;
        Assertions.assertEquals(want, outputs.get("ClusterArn"), "Fn::GetAtt [Cluster, Arn]");
    }

    private void refNodegroupIsClusterSlashName(TestContext ctx) throws Exception {
        var outputs = eksRefsOutputs(ctx);
        Assertions.assertEquals(eksRefsExpectedIds(ctx).get("Nodegroup"), outputs.get("NodegroupRef"),
                "Ref on AWS::EKS::Nodegroup");
    }

    private void refAddonIsClusterPipeAddon(TestContext ctx) throws Exception {
        var outputs = eksRefsOutputs(ctx);
        Assertions.assertEquals(eksRefsExpectedIds(ctx).get("Addon"), outputs.get("AddonRef"),
                "Ref on AWS::EKS::Addon is pipe-separated, not a slash");
    }

    /**
     * Deletes the group's own stack. A deleted stack is readable by stack ID and
     * gone by name — AWS documents that asymmetry — so the stack ID is what
     * proves the delete finished rather than merely started.
     */
    private void deleteStackWithEksRefs(TestContext ctx) throws Exception {
        String name = ctx.getString("cfnEksStackName");
        String stackId = ctx.getString("cfnEksStackId");
        Assertions.assertNotBlank(stackId, "DeleteStack: no stack from CreateStackWithEksRefs");
        cfn().deleteStack(r -> r.stackName(name));
        waitStackStatus(name, "DELETE_COMPLETE", "DELETE_FAILED");
        var stack = describeStack(stackId);
        Assertions.assertEquals("DELETE_COMPLETE", stack.stackStatusAsString(),
                "DeleteStack: " + name + " reason: " + stack.stackStatusReason());
    }

    /**
     * The AWS-documented Ref value — and so the physical resource ID — of each
     * resource in the template.
     */
    private Map<String, String> eksRefsExpectedIds(TestContext ctx) {
        String cluster = ctx.getString("cfnEksClusterName");
        return Map.of(
                "Cluster", cluster,
                "Nodegroup", cluster + "/" + ctx.getString("cfnEksNodegroupName"),
                "Addon", cluster + "|" + EKS_REFS_ADDON_NAME);
    }

    /**
     * Stack outputs, read afresh for each test rather than stashed, so every
     * assertion is made against a value the server just produced rather than one
     * an earlier test cached.
     */
    private Map<String, String> eksRefsOutputs(TestContext ctx) {
        var outputs = new HashMap<String, String>();
        for (Output o : describeStack(ctx.getString("cfnEksStackName")).outputs()) {
            outputs.put(o.outputKey(), o.outputValue());
        }
        return outputs;
    }

    /**
     * The account ID out of a stack ARN
     * ("arn:aws:cloudformation:&lt;region&gt;:&lt;account&gt;:stack/&lt;name&gt;/&lt;uuid&gt;"),
     * so the expected EKS ARN can be built without reaching for a second AWS
     * client.
     */
    private static String accountFromStackId(String stackId) {
        String[] parts = stackId.split(":");
        if (parts.length < 6 || parts[4].isEmpty()) {
            throw new AssertionError("stack ID " + stackId
                    + " is not a stack ARN, cannot read the account ID from it");
        }
        return parts[4];
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
