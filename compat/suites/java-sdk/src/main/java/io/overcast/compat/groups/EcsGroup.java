package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.ecs.EcsClient;
import software.amazon.awssdk.services.ecs.model.*;

import java.util.Map;

/**
 * ECS compatibility test group.
 *
 * <p>Groups: ecs-clusters, ecs-tasks, ecs-services.
 */
public final class EcsGroup implements ServiceGroup {

    private final AwsClients clients;

    public EcsGroup(AwsClients clients) {
        this.clients = clients;
    }

    private EcsClient ecs() { return clients.ecs(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateCluster",      this::createCluster),
                Map.entry("DescribeClusters",   this::describeClusters),
                Map.entry("ListClusters",       this::listClusters),
                Map.entry("RegisterTaskDefinition",  this::registerTaskDef),
                Map.entry("ListTaskDefinitions",      this::listTaskDefinitions),
                Map.entry("DeregisterTaskDefinition", this::deregisterTaskDef),
                Map.entry("DeleteCluster",      this::deleteCluster),
                Map.entry("RunTask",            this::runTask),
                Map.entry("DescribeTasks",      this::describeTasks),
                Map.entry("ListTasks",          this::listTasks),
                Map.entry("StopTask",           this::stopTask),
                Map.entry("CreateService",      this::createService),
                Map.entry("DescribeServices",   this::describeServices),
                Map.entry("ListServices",       this::listServices),
                Map.entry("UpdateService",      this::updateService),
                Map.entry("DeleteService",      this::deleteService)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of(
                "ecs-clusters",  this::setupClusters,
                "ecs-tasks",     this::setupTasks,
                "ecs-services",  this::setupServices
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of(
                "ecs-clusters",  this::teardownClusters,
                "ecs-tasks",     this::teardownTasks,
                "ecs-services",  this::teardownServices
        );
    }

    // ── ecs-clusters ──────────────────────────────────────────────────────────

    private void setupClusters(TestContext ctx) {
        ctx.set("ecsCluster", "compat-" + ctx.runId());
    }

    private void teardownClusters(TestContext ctx) {
        String cluster = ctx.getString("ecsCluster");
        String taskDef = ctx.getString("ecsTaskDefArn");
        if (taskDef != null)
            try { ecs().deregisterTaskDefinition(r -> r.taskDefinition(taskDef)); } catch (Exception ignored) {}
        if (cluster != null)
            try { ecs().deleteCluster(r -> r.cluster(cluster)); } catch (Exception ignored) {}
    }

    private void createCluster(TestContext ctx) throws Exception {
        String name = ctx.getString("ecsCluster");
        var resp = ecs().createCluster(r -> r.clusterName(name));
        Assertions.assertNotBlank(resp.cluster().clusterArn(), "CreateCluster: clusterArn is blank");
    }

    private void describeClusters(TestContext ctx) throws Exception {
        String name = ctx.getString("ecsCluster");
        var resp = ecs().describeClusters(r -> r.clusters(name));
        Assertions.assertNotEmpty(resp.clusters(), "DescribeClusters: no clusters returned");
        Assertions.assertEquals(name, resp.clusters().get(0).clusterName(),
                "DescribeClusters: clusterName mismatch");
    }

    private void listClusters(TestContext ctx) throws Exception {
        var resp = ecs().listClusters(r -> r.maxResults(100));
        Assertions.assertNotNull(resp.clusterArns(), "ListClusters: clusterArns is null");
    }

    private void registerTaskDef(TestContext ctx) throws Exception {
        var resp = ecs().registerTaskDefinition(r -> r
                .family("compat-" + ctx.runId())
                .networkMode(NetworkMode.BRIDGE)
                .containerDefinitions(ContainerDefinition.builder()
                        .name("app")
                        .image("public.ecr.aws/amazonlinux/amazonlinux:latest")
                        .cpu(256)
                        .memory(512)
                        .essential(true)
                        .build()));
        Assertions.assertNotBlank(resp.taskDefinition().taskDefinitionArn(),
                "RegisterTaskDef: taskDefinitionArn is blank");
        ctx.set("ecsTaskDefArn", resp.taskDefinition().taskDefinitionArn());
    }

    private void listTaskDefinitions(TestContext ctx) throws Exception {
        var resp = ecs().listTaskDefinitions(r -> r.maxResults(100));
        Assertions.assertNotNull(resp.taskDefinitionArns(), "ListTaskDefinitions: taskDefinitionArns is null");
    }

    private void deregisterTaskDef(TestContext ctx) throws Exception {
        String arn = ctx.getString("ecsTaskDefArn");
        ecs().deregisterTaskDefinition(r -> r.taskDefinition(arn));
        ctx.set("ecsTaskDefArn", null);
    }

    private void deleteCluster(TestContext ctx) throws Exception {
        String name = ctx.getString("ecsCluster");
        ecs().deleteCluster(r -> r.cluster(name));
        ctx.set("ecsCluster", null);
    }

    // ── ecs-tasks ──────────────────────────────────────────────────────────

    private void setupTasks(TestContext ctx) throws Exception {
        String cluster = "compat-task-" + ctx.runId();
        ecs().createCluster(r -> r.clusterName(cluster));
        ctx.set("taskCluster", cluster);

        var resp = ecs().registerTaskDefinition(r -> r
                .family("compat-task-" + ctx.runId())
                .networkMode(NetworkMode.AWSVPC)
                .requiresCompatibilities(Compatibility.FARGATE)
                .cpu("256")
                .memory("512")
                .containerDefinitions(ContainerDefinition.builder()
                        .name("app")
                        .image("public.ecr.aws/nginx/nginx:latest")
                        .essential(true)
                        .build()));
        ctx.set("taskTaskDefArn", resp.taskDefinition().taskDefinitionArn());
    }

    private void teardownTasks(TestContext ctx) {
        String taskArn = ctx.getString("taskArn");
        String cluster = ctx.getString("taskCluster");
        if (taskArn != null && cluster != null)
            try { ecs().stopTask(r -> r.cluster(cluster).task(taskArn).reason("teardown")); } catch (Exception ignored) {}
        String taskDef = ctx.getString("taskTaskDefArn");
        if (taskDef != null)
            try { ecs().deregisterTaskDefinition(r -> r.taskDefinition(taskDef)); } catch (Exception ignored) {}
        if (cluster != null)
            try { ecs().deleteCluster(r -> r.cluster(cluster)); } catch (Exception ignored) {}
    }

    private void runTask(TestContext ctx) throws Exception {
        String cluster = ctx.getString("taskCluster");
        String taskDef = ctx.getString("taskTaskDefArn");
        Assertions.assertNotBlank(cluster, "RunTask: missing cluster from setup");
        Assertions.assertNotBlank(taskDef, "RunTask: missing taskDef from setup");
        var resp = ecs().runTask(r -> r
                .cluster(cluster)
                .taskDefinition(taskDef)
                .launchType(LaunchType.FARGATE)
                .networkConfiguration(n -> n.awsvpcConfiguration(a -> a
                        .subnets("subnet-00000000")
                        .assignPublicIp(AssignPublicIp.DISABLED))));
        Assertions.assertNotEmpty(resp.tasks(), "RunTask: no tasks returned");
        ctx.set("taskArn", resp.tasks().get(0).taskArn());
    }

    private void describeTasks(TestContext ctx) throws Exception {
        String cluster = ctx.getString("taskCluster");
        String taskArn = ctx.getString("taskArn");
        Assertions.assertNotBlank(taskArn, "DescribeTasks: no task from RunTask");
        var resp = ecs().describeTasks(r -> r.cluster(cluster).tasks(taskArn));
        Assertions.assertNotEmpty(resp.tasks(), "DescribeTasks: no tasks returned");
    }

    private void listTasks(TestContext ctx) throws Exception {
        String cluster = ctx.getString("taskCluster");
        var resp = ecs().listTasks(r -> r.cluster(cluster));
        Assertions.assertNotEmpty(resp.taskArns(), "ListTasks: empty taskArns");
    }

    private void stopTask(TestContext ctx) throws Exception {
        String cluster = ctx.getString("taskCluster");
        String taskArn = ctx.getString("taskArn");
        Assertions.assertNotBlank(taskArn, "StopTask: no task from RunTask");
        var resp = ecs().stopTask(r -> r.cluster(cluster).task(taskArn).reason("compat test cleanup"));
        Assertions.assertEquals("STOPPED", resp.task().desiredStatus(), "StopTask: expected STOPPED");
    }

    // ── ecs-services ───────────────────────────────────────────────────────

    private void setupServices(TestContext ctx) throws Exception {
        String cluster = "compat-svc-" + ctx.runId();
        ecs().createCluster(r -> r.clusterName(cluster));
        ctx.set("svcCluster", cluster);

        var resp = ecs().registerTaskDefinition(r -> r
                .family("compat-svc-" + ctx.runId())
                .networkMode(NetworkMode.AWSVPC)
                .requiresCompatibilities(Compatibility.FARGATE)
                .cpu("256")
                .memory("512")
                .containerDefinitions(ContainerDefinition.builder()
                        .name("app")
                        .image("public.ecr.aws/nginx/nginx:latest")
                        .essential(true)
                        .build()));
        ctx.set("svcTaskDefArn", resp.taskDefinition().taskDefinitionArn());
    }

    private void teardownServices(TestContext ctx) {
        String svcName = ctx.getString("svcName");
        String cluster = ctx.getString("svcCluster");
        if (svcName != null && cluster != null) {
            try { ecs().updateService(r -> r.cluster(cluster).service(svcName).desiredCount(0)); } catch (Exception ignored) {}
            try { ecs().deleteService(r -> r.cluster(cluster).service(svcName)); } catch (Exception ignored) {}
        }
        String taskDef = ctx.getString("svcTaskDefArn");
        if (taskDef != null)
            try { ecs().deregisterTaskDefinition(r -> r.taskDefinition(taskDef)); } catch (Exception ignored) {}
        if (cluster != null)
            try { ecs().deleteCluster(r -> r.cluster(cluster)); } catch (Exception ignored) {}
    }

    private void createService(TestContext ctx) throws Exception {
        String cluster = ctx.getString("svcCluster");
        String taskDef = ctx.getString("svcTaskDefArn");
        Assertions.assertNotBlank(cluster, "CreateService: missing cluster from setup");
        Assertions.assertNotBlank(taskDef, "CreateService: missing taskDef from setup");
        String svcName = "compat-svc-" + ctx.runId();
        var resp = ecs().createService(r -> r
                .cluster(cluster)
                .serviceName(svcName)
                .taskDefinition(taskDef)
                .desiredCount(1)
                .launchType(LaunchType.FARGATE)
                .networkConfiguration(n -> n.awsvpcConfiguration(a -> a
                        .subnets("subnet-00000000")
                        .assignPublicIp(AssignPublicIp.DISABLED))));
        Assertions.assertNotBlank(resp.service().serviceArn(), "CreateService: missing serviceArn");
        ctx.set("svcName", svcName);
    }

    private void describeServices(TestContext ctx) throws Exception {
        String cluster = ctx.getString("svcCluster");
        String svcName = ctx.getString("svcName");
        Assertions.assertNotBlank(svcName, "DescribeServices: no service from CreateService");
        var resp = ecs().describeServices(r -> r.cluster(cluster).services(svcName));
        Assertions.assertNotEmpty(resp.services(), "DescribeServices: no services returned");
    }

    private void listServices(TestContext ctx) throws Exception {
        String cluster = ctx.getString("svcCluster");
        var resp = ecs().listServices(r -> r.cluster(cluster));
        Assertions.assertNotEmpty(resp.serviceArns(), "ListServices: empty serviceArns");
    }

    private void updateService(TestContext ctx) throws Exception {
        String cluster = ctx.getString("svcCluster");
        String svcName = ctx.getString("svcName");
        Assertions.assertNotBlank(svcName, "UpdateService: no service from CreateService");
        var resp = ecs().updateService(r -> r.cluster(cluster).service(svcName).desiredCount(2));
        Assertions.assertNotNull(resp.service(), "UpdateService: missing service in response");
    }

    private void deleteService(TestContext ctx) throws Exception {
        String cluster = ctx.getString("svcCluster");
        String svcName = ctx.getString("svcName");
        if (svcName == null) return;
        try { ecs().updateService(r -> r.cluster(cluster).service(svcName).desiredCount(0)); } catch (Exception ignored) {}
        ecs().deleteService(r -> r.cluster(cluster).service(svcName));
    }
}
