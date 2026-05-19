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
 * <p>Groups: rds-instances.
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
                Map.entry("DeleteDBParameterGroup",    this::deleteDbParameterGroup)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of(
                "rds-instances",        this::setupInstances,
                "rds-subnet-groups",    this::setupNoop,
                "rds-parameter-groups", this::setupNoop
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of(
                "rds-instances",        ctx -> deleteDbSilently(ctx.getString("rdsInstanceId")),
                "rds-subnet-groups",    this::teardownSubnetGroups,
                "rds-parameter-groups", this::teardownParameterGroups
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

    /** Polls DescribeDBInstances until the instance reaches the expected status (max 10 s). */
    private void waitForStatus(String id, String expected) throws Exception {
        for (int i = 0; i < 20; i++) {
            var resp = rds().describeDBInstances(r -> r.dbInstanceIdentifier(id));
            if (!resp.dbInstances().isEmpty()
                    && expected.equals(resp.dbInstances().get(0).dbInstanceStatus())) {
                return;
            }
            Thread.sleep(500);
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
                .dbParameterGroupFamily("mysql8.0")
                .description("compat test parameter group"));
        Assertions.assertNotBlank(resp.dbParameterGroup().dbParameterGroupName(),
                "CreateDBParameterGroup: missing name");
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

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteDbSilently(String id) {
        if (id == null) return;
        try {
            rds().deleteDBInstance(r -> r.dbInstanceIdentifier(id).skipFinalSnapshot(true));
        } catch (Exception ignored) {}
    }
}
