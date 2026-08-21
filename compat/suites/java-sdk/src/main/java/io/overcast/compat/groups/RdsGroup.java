package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.rds.RdsClient;
import software.amazon.awssdk.services.rds.model.*;

import java.util.Map;

/**
 * RDS compatibility test group.
 *
 * <p>Groups: rds-instances, rds-clusters, rds-subnet-groups, rds-parameter-groups.
 */
public final class RdsGroup implements ServiceGroup {

    private final AwsClients clients;

    public RdsGroup(AwsClients clients) {
        this.clients = clients;
    }

    private RdsClient rds() { return clients.rds(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("DescribeDBEngineVersions", this::describeDbEngineVersions),
                Map.entry("CreateDBInstance",   this::createDbInstance),
                Map.entry("DescribeDBInstances",this::describeDbInstances),
                Map.entry("StopDBInstance",     this::stopDbInstance),
                Map.entry("StartDBInstance",    this::startDbInstance),
                Map.entry("ModifyDBInstance",   this::modifyDbInstance),
                Map.entry("DeleteDBInstance",   this::deleteDbInstance),
                Map.entry("CreateDBSubnetGroup",       this::createDbSubnetGroup),
                Map.entry("DescribeDBSubnetGroups",    this::describeDbSubnetGroups),
                Map.entry("DeleteDBSubnetGroup",       this::deleteDbSubnetGroup),
                Map.entry("CreateDBParameterGroup",    this::createDbParameterGroup),
                Map.entry("DescribeDBParameterGroups", this::describeDbParameterGroups),
                Map.entry("DeleteDBParameterGroup",    this::deleteDbParameterGroup),
                Map.entry("CreateDBCluster",    this::createDbCluster),
                Map.entry("DescribeDBClusters", this::describeDbClusters),
                Map.entry("ModifyDBCluster",    this::modifyDbCluster),
                Map.entry("StopDBCluster",      this::stopDbCluster),
                Map.entry("StartDBCluster",     this::startDbCluster),
                Map.entry("DeleteDBCluster",    this::deleteDbCluster)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of(
                "rds-instances",        this::setupInstances,
                "rds-subnet-groups",    this::setupNoop,
                "rds-parameter-groups", this::setupNoop,
                "rds-clusters",         this::setupNoop
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of(
                "rds-instances",        ctx -> deleteDbSilently(ctx.getString("rdsInstanceId")),
                "rds-subnet-groups",    this::teardownSubnetGroups,
                "rds-parameter-groups", this::teardownParameterGroups,
                "rds-clusters",         this::teardownClusters
        );
    }

    // ── rds-instances ─────────────────────────────────────────────────────────

    private void setupInstances(TestContext ctx) {
        ctx.set("rdsInstanceId", "compat-" + ctx.runId());
    }

    private void setupNoop(TestContext ctx) {}

    private void describeDbEngineVersions(TestContext ctx) throws Exception {
        rds().describeDBEngineVersions(r -> r.engine("mysql"));
    }

    private void createDbInstance(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsInstanceId");
        var resp = rds().createDBInstance(r -> r
                .dbInstanceIdentifier(id)
                .dbInstanceClass("db.t3.micro")
                .engine("mysql")
                .masterUsername("admin")
                .masterUserPassword("Passw0rd!")
                .allocatedStorage(20));
        Assertions.assertNotBlank(resp.dbInstance().dbInstanceIdentifier(),
                "CreateDBInstance: dbInstanceIdentifier is blank");
    }

    private void describeDbInstances(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsInstanceId");
        var resp = rds().describeDBInstances(r -> r.dbInstanceIdentifier(id));
        Assertions.assertNotEmpty(resp.dbInstances(), "DescribeDBInstances: no instances");
        Assertions.assertEquals(id, resp.dbInstances().get(0).dbInstanceIdentifier(),
                "DescribeDBInstances: id mismatch");
    }

    private void modifyDbInstance(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsInstanceId");
        rds().modifyDBInstance(r -> r
                .dbInstanceIdentifier(id)
                .backupRetentionPeriod(1)
                .applyImmediately(true));
    }

    private void deleteDbInstance(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsInstanceId");
        rds().deleteDBInstance(r -> r
                .dbInstanceIdentifier(id)
                .skipFinalSnapshot(true));
        ctx.set("rdsInstanceId", null);
    }

    private void stopDbInstance(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsInstanceId");
        Assertions.assertNotBlank(id, "StopDBInstance: no id from setup");
        waitForStatus(id, "available");
        var resp = rds().stopDBInstance(r -> r.dbInstanceIdentifier(id));
        Assertions.assertNotNull(resp.dbInstance(), "StopDBInstance: missing dbInstance");
    }

    private void startDbInstance(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsInstanceId");
        Assertions.assertNotBlank(id, "StartDBInstance: no id from setup");
        waitForStatus(id, "stopped");
        var resp = rds().startDBInstance(r -> r.dbInstanceIdentifier(id));
        Assertions.assertNotNull(resp.dbInstance(), "StartDBInstance: missing dbInstance");
    }

    /**
     * How long a DB instance is given to reach a target status, and how often it is asked.
     * Shared with the go, node, python and cli suites, which wait for the same thing against
     * the same emulator.
     *
     * <p>A wall-clock budget rather than a count of attempts, because those two stop meaning
     * the same thing the moment the machine is busy. This suite had the tightest budget of the
     * five — 20 attempts at 500ms, ten seconds — for a real database container's first boot,
     * data directory initialisation included. The CLI suite already carried 120 seconds for
     * this reason; this is that number, made common.
     */
    private static final long RDS_WAIT_BUDGET_MS = 120_000L;
    private static final long RDS_POLL_INTERVAL_MS = 1_000L;

    /**
     * Polls DescribeDBInstances until the instance reaches the expected status, and throws once
     * the budget is spent.
     *
     * <p>It used to return quietly instead, which is how a ten-second budget presented as a
     * server fault: StopDBInstance ran anyway and failed with "must be available to stop", so
     * the report named the operation that came after the wait rather than the wait that had not
     * finished. A wait that gives up has to say so.
     */
    private void waitForStatus(String id, String expected) throws Exception {
        long deadline = System.nanoTime() + RDS_WAIT_BUDGET_MS * 1_000_000L;
        String last = "(none reported)";
        while (true) {
            var resp = rds().describeDBInstances(r -> r.dbInstanceIdentifier(id));
            if (!resp.dbInstances().isEmpty()) {
                String status = resp.dbInstances().get(0).dbInstanceStatus();
                if (status != null) {
                    last = status;
                    if (expected.equals(status)) {
                        return;
                    }
                    // A failed instance will never reach any target, so spending the rest of
                    // the budget on it only delays the report.
                    if ("failed".equals(status)) {
                        throw new AssertionError("DB instance " + id
                                + " went to \"failed\" while waiting for \"" + expected + "\"");
                    }
                }
            }
            if (System.nanoTime() >= deadline) {
                throw new AssertionError("DB instance " + id + " did not reach \"" + expected
                        + "\" within " + RDS_WAIT_BUDGET_MS + "ms (last status \"" + last + "\")");
            }
            Thread.sleep(RDS_POLL_INTERVAL_MS);
        }
    }

    // ── rds-subnet-groups ─────────────────────────────────────────────────────

    private void teardownSubnetGroups(TestContext ctx) {
        String name = ctx.getString("rdsSubnetGroup");
        if (name != null)
            try { rds().deleteDBSubnetGroup(r -> r.dbSubnetGroupName(name)); } catch (Exception ignored) {}
    }

    private void createDbSubnetGroup(TestContext ctx) throws Exception {
        String name = "compat-" + ctx.runId();
        var resp = rds().createDBSubnetGroup(r -> r
                .dbSubnetGroupName(name)
                .dbSubnetGroupDescription("compat test subnet group")
                .subnetIds("subnet-00000000", "subnet-00000001"));
        Assertions.assertNotBlank(resp.dbSubnetGroup().dbSubnetGroupName(),
                "CreateDBSubnetGroup: missing name");
        ctx.set("rdsSubnetGroup", name);
    }

    private void describeDbSubnetGroups(TestContext ctx) throws Exception {
        String name = ctx.getString("rdsSubnetGroup");
        Assertions.assertNotBlank(name, "DescribeDBSubnetGroups: no group from create");
        var resp = rds().describeDBSubnetGroups(r -> r.dbSubnetGroupName(name));
        Assertions.assertNotEmpty(resp.dbSubnetGroups(), "DescribeDBSubnetGroups: empty");
    }

    private void deleteDbSubnetGroup(TestContext ctx) throws Exception {
        String name = ctx.getString("rdsSubnetGroup");
        if (name == null) return;
        rds().deleteDBSubnetGroup(r -> r.dbSubnetGroupName(name));
        ctx.set("rdsSubnetGroup", null);
    }

    // ── rds-parameter-groups ──────────────────────────────────────────────────

    private void teardownParameterGroups(TestContext ctx) {
        String name = ctx.getString("rdsParamGroup");
        if (name != null)
            try { rds().deleteDBParameterGroup(r -> r.dbParameterGroupName(name)); } catch (Exception ignored) {}
    }

    private void createDbParameterGroup(TestContext ctx) throws Exception {
        String name = "compat-pg-" + ctx.runId();
        var resp = rds().createDBParameterGroup(r -> r
                .dbParameterGroupName(name)
                .dbParameterGroupFamily("aurora-mysql8.0")
                .description("compat test parameter group"));
        Assertions.assertNotBlank(resp.dbParameterGroup().dbParameterGroupName(),
                "CreateDBParameterGroup: missing name");
        Assertions.assertEquals("aurora-mysql8.0", resp.dbParameterGroup().dbParameterGroupFamily(),
                "CreateDBParameterGroup: family mismatch");
        ctx.set("rdsParamGroup", name);
    }

    private void describeDbParameterGroups(TestContext ctx) throws Exception {
        String name = ctx.getString("rdsParamGroup");
        Assertions.assertNotBlank(name, "DescribeDBParameterGroups: no group from create");
        var resp = rds().describeDBParameterGroups(r -> r.dbParameterGroupName(name));
        Assertions.assertNotEmpty(resp.dbParameterGroups(), "DescribeDBParameterGroups: empty");
    }

    private void deleteDbParameterGroup(TestContext ctx) throws Exception {
        String name = ctx.getString("rdsParamGroup");
        if (name == null) return;
        rds().deleteDBParameterGroup(r -> r.dbParameterGroupName(name));
        ctx.set("rdsParamGroup", null);
    }

    // ── rds-clusters ──────────────────────────────────────────────────────────
    //
    // Aurora DB clusters. A cluster is a logical record in Overcast — member
    // instances are what start engine containers — so the waits here are for the
    // emulator's own status transitions rather than for a database to boot. They
    // still have to be waited for: StopDBCluster refuses a cluster that is not
    // "available" and StartDBCluster refuses one that is not "stopped", here and
    // on AWS, so going straight from create to stop asks for
    // InvalidDBClusterStateFault.
    //
    // The poll interval is tighter than the instance one for the same reason:
    // there is no container boot to wait on, only a scheduled transition, so a
    // coarser interval would spend seconds observing something that had already
    // happened.
    private static final long RDS_CLUSTER_POLL_INTERVAL_MS = 500L;

    private static final String RDS_CLUSTER_ENGINE = "aurora-mysql";
    private static final String RDS_CLUSTER_MASTER_USERNAME = "admin";
    private static final String RDS_CLUSTER_MASTER_PASSWORD = "Passw0rd!";

    /**
     * The retention period ModifyDBCluster sets. Non-zero so it is distinguishable from the
     * create-time default, and echoed back by DescribeDBClusters so the change can be observed
     * rather than assumed.
     */
    private static final int RDS_CLUSTER_BACKUP_RETENTION = 7;

    private void teardownClusters(TestContext ctx) {
        String id = ctx.getString("rdsClusterId");
        if (id == null) return;
        try {
            rds().deleteDBCluster(r -> r.dbClusterIdentifier(id).skipFinalSnapshot(true));
        } catch (Exception ignored) {}
    }

    /**
     * Returns this group's cluster, or throws naming what came back instead. Every assertion
     * below reads through it, so a describe that comes back empty reports as a missing cluster
     * rather than an IndexOutOfBoundsException.
     */
    private DBCluster describeCluster(String id) {
        var resp = rds().describeDBClusters(r -> r.dbClusterIdentifier(id));
        if (resp.dbClusters().isEmpty()) {
            throw new AssertionError("DescribeDBClusters: no cluster returned for " + id);
        }
        return resp.dbClusters().get(0);
    }

    /** Polls DescribeDBClusters until the cluster reports the expected status. */
    private void waitForClusterStatus(String id, String expected) throws Exception {
        long deadline = System.nanoTime() + RDS_WAIT_BUDGET_MS * 1_000_000L;
        String last = "(none reported)";
        while (true) {
            String status = describeCluster(id).status();
            if (status != null) {
                last = status;
                if (expected.equals(status)) {
                    return;
                }
            }
            if (System.nanoTime() >= deadline) {
                throw new AssertionError("DB cluster " + id + " did not reach \"" + expected
                        + "\" within " + RDS_WAIT_BUDGET_MS + "ms (last status \"" + last + "\")");
            }
            Thread.sleep(RDS_CLUSTER_POLL_INTERVAL_MS);
        }
    }

    /**
     * Polls the unfiltered DescribeDBClusters until this run's cluster is no longer listed.
     * Deletion is asynchronous — the call marks the cluster "deleting" and the record goes
     * shortly after — so absence is what has to be waited for. The unfiltered form rather than
     * a lookup by identifier, so the check does not depend on which not-found shape the SDK
     * surfaces.
     */
    private void waitForClusterGone(String id) throws Exception {
        long deadline = System.nanoTime() + RDS_WAIT_BUDGET_MS * 1_000_000L;
        while (true) {
            boolean found = rds().describeDBClusters().dbClusters().stream()
                    .anyMatch(c -> id.equals(c.dbClusterIdentifier()));
            if (!found) {
                return;
            }
            if (System.nanoTime() >= deadline) {
                throw new AssertionError("DB cluster " + id + " still listed "
                        + RDS_WAIT_BUDGET_MS + "ms after DeleteDBCluster");
            }
            Thread.sleep(RDS_CLUSTER_POLL_INTERVAL_MS);
        }
    }

    private void createDbCluster(TestContext ctx) throws Exception {
        // Named apart from the instance group's resource so the two never collide when both
        // run against the same emulator.
        String id = "compat-cluster-" + ctx.runId();
        // Recorded before the call, not after it: a create that fails partway still leaves
        // something for teardown to remove.
        ctx.set("rdsClusterId", id);
        var resp = rds().createDBCluster(r -> r
                .dbClusterIdentifier(id)
                .engine(RDS_CLUSTER_ENGINE)
                .masterUsername(RDS_CLUSTER_MASTER_USERNAME)
                .masterUserPassword(RDS_CLUSTER_MASTER_PASSWORD));
        Assertions.assertEquals(id, resp.dbCluster().dbClusterIdentifier(),
                "CreateDBCluster: dbClusterIdentifier mismatch");
        Assertions.assertNotBlank(resp.dbCluster().dbClusterArn(),
                "CreateDBCluster: dbClusterArn is blank");
    }

    private void describeDbClusters(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsClusterId");
        Assertions.assertNotBlank(id, "DescribeDBClusters: no cluster from CreateDBCluster");
        var cluster = describeCluster(id);
        Assertions.assertEquals(id, cluster.dbClusterIdentifier(),
                "DescribeDBClusters: dbClusterIdentifier mismatch");
        Assertions.assertEquals(RDS_CLUSTER_ENGINE, cluster.engine(),
                "DescribeDBClusters: engine mismatch");
        Assertions.assertNotBlank(cluster.status(), "DescribeDBClusters: status is blank");
        // The writer and reader endpoints are what makes a cluster a cluster, and nothing
        // outside the emulator's own Go tests looked at them until this group existed. Both
        // must be present and they must differ — a reader endpoint collapsed onto the writer
        // reads as success everywhere else.
        // Both stay distinct only while the cluster has no writer member: since #1076 a host
        // caller that cannot resolve the names is given the loopback address for each,
        // deliberately. This group creates no members, so it never reaches that case — one
        // that adds an instance would.
        Assertions.assertNotBlank(cluster.endpoint(), "DescribeDBClusters: endpoint is blank");
        Assertions.assertNotBlank(cluster.readerEndpoint(),
                "DescribeDBClusters: readerEndpoint is blank");
        Assertions.assertNotEquals(cluster.endpoint(), cluster.readerEndpoint(),
                "DescribeDBClusters: endpoint and readerEndpoint are the same name");
    }

    private void modifyDbCluster(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsClusterId");
        Assertions.assertNotBlank(id, "ModifyDBCluster: no cluster from CreateDBCluster");
        var resp = rds().modifyDBCluster(r -> r
                .dbClusterIdentifier(id)
                .backupRetentionPeriod(RDS_CLUSTER_BACKUP_RETENTION));
        Assertions.assertEquals(RDS_CLUSTER_BACKUP_RETENTION,
                resp.dbCluster().backupRetentionPeriod(),
                "ModifyDBCluster: backupRetentionPeriod not applied in the response");
        // Read it back rather than trusting the modify response to have echoed a value it
        // never stored.
        Assertions.assertEquals(RDS_CLUSTER_BACKUP_RETENTION,
                describeCluster(id).backupRetentionPeriod(),
                "ModifyDBCluster: backupRetentionPeriod not persisted");
    }

    private void stopDbCluster(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsClusterId");
        Assertions.assertNotBlank(id, "StopDBCluster: no cluster from CreateDBCluster");
        // StopDBCluster requires an available cluster, here and on AWS.
        waitForClusterStatus(id, "available");
        var resp = rds().stopDBCluster(r -> r.dbClusterIdentifier(id));
        Assertions.assertNotBlank(resp.dbCluster().status(), "StopDBCluster: status is blank");
        // The stop is this test's mutation, so this test is where it is confirmed to have
        // landed — not StartDBCluster's precondition wait.
        waitForClusterStatus(id, "stopped");
    }

    private void startDbCluster(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsClusterId");
        Assertions.assertNotBlank(id, "StartDBCluster: no cluster from CreateDBCluster");
        waitForClusterStatus(id, "stopped");
        var resp = rds().startDBCluster(r -> r.dbClusterIdentifier(id));
        Assertions.assertNotBlank(resp.dbCluster().status(), "StartDBCluster: status is blank");
        waitForClusterStatus(id, "available");
    }

    private void deleteDbCluster(TestContext ctx) throws Exception {
        String id = ctx.getString("rdsClusterId");
        Assertions.assertNotBlank(id, "DeleteDBCluster: no cluster from CreateDBCluster");
        rds().deleteDBCluster(r -> r.dbClusterIdentifier(id).skipFinalSnapshot(true));
        waitForClusterGone(id);
        ctx.set("rdsClusterId", null);
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteDbSilently(String id) {
        if (id == null) return;
        try {
            rds().deleteDBInstance(r -> r.dbInstanceIdentifier(id).skipFinalSnapshot(true));
        } catch (Exception ignored) {}
    }
}
