package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.efs.EfsClient;
import software.amazon.awssdk.services.efs.model.MountTargetDescription;

import java.util.List;
import java.util.Map;

/**
 * Amazon EFS compatibility test group.
 *
 * <p>Groups: efs-mount-targets.
 */
public final class EfsGroup implements ServiceGroup {

    private final AwsClients clients;

    public EfsGroup(AwsClients clients) {
        this.clients = clients;
    }

    private EfsClient efs() { return clients.efs(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("efs-mount-targets:CreateMountTarget",                 this::createMountTarget),
                Map.entry("efs-mount-targets:DescribeMountTargets",              this::describeMountTargets),
                Map.entry("efs-mount-targets:DescribeMountTargetSecurityGroups", this::describeMountTargetSecurityGroups),
                Map.entry("efs-mount-targets:ModifyMountTargetSecurityGroups",   this::modifyMountTargetSecurityGroups),
                Map.entry("efs-mount-targets:DeleteMountTarget",                 this::deleteMountTarget)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of("efs-mount-targets", this::setupMountTargets);
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of("efs-mount-targets", this::teardownMountTargets);
    }

    // ── efs-mount-targets ─────────────────────────────────────────────────────

    /**
     * Builds an AWS-shaped subnet ID that is still unique per run.
     *
     * <p>A file system accepts one mount target per availability zone, and the
     * emulator derives the zone from the subnet ID — so it must vary per run.
     * The shape matters too: the SDKs validate SubnetId's length before the
     * request leaves the client, and "subnet-&lt;runId&gt;" is short enough to
     * be rejected. Real long-form IDs are "subnet-" plus 17 hex characters, so
     * keep the run ID's own hex digits and pad the rest.
     */
    private static String subnetId(TestContext ctx) {
        StringBuilder hex = new StringBuilder();
        for (char c : ctx.runId().toCharArray()) {
            if ((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
                hex.append(c);
            }
        }
        return "subnet-" + (hex + "00000000000000000").substring(0, 17);
    }

    /**
     * Creates the file system the group's mount targets attach to. Mount
     * targets have no meaning without one, so this is setup rather than a test.
     */
    private void setupMountTargets(TestContext ctx) {
        var resp = efs().createFileSystem(
                r -> r.creationToken("oc-" + ctx.runId() + "-efs-mount-targets"));
        Assertions.assertNotBlank(resp.fileSystemId(), "setup: CreateFileSystem fileSystemId");
        ctx.set("efsFileSystemId", resp.fileSystemId());
    }

    /**
     * Reverse creation order: DeleteFileSystem answers FileSystemInUse while
     * any mount target remains.
     */
    private void teardownMountTargets(TestContext ctx) {
        String mountTargetId = ctx.getString("efsMountTargetId");
        if (mountTargetId != null && !mountTargetId.isBlank()) {
            try {
                efs().deleteMountTarget(r -> r.mountTargetId(mountTargetId));
            } catch (Exception ignored) {
                // Teardown must not throw.
            }
        }
        String fileSystemId = ctx.getString("efsFileSystemId");
        if (fileSystemId != null && !fileSystemId.isBlank()) {
            try {
                efs().deleteFileSystem(r -> r.fileSystemId(fileSystemId));
            } catch (Exception ignored) {
                // Teardown must not throw.
            }
        }
    }

    private void createMountTarget(TestContext ctx) throws Exception {
        String fileSystemId = ctx.getString("efsFileSystemId");
        Assertions.assertNotBlank(fileSystemId, "CreateMountTarget: file system from setup");
        var resp = efs().createMountTarget(r -> r
                .fileSystemId(fileSystemId)
                .subnetId(subnetId(ctx))
                .securityGroups("sg-compat-a"));
        Assertions.assertNotBlank(resp.mountTargetId(), "CreateMountTarget: mountTargetId");
        Assertions.assertEquals(fileSystemId, resp.fileSystemId(),
                "CreateMountTarget: fileSystemId mismatch");
        Assertions.assertNotNull(resp.lifeCycleState(), "CreateMountTarget: lifeCycleState");
        ctx.set("efsMountTargetId", resp.mountTargetId());
    }

    private void describeMountTargets(TestContext ctx) throws Exception {
        String fileSystemId = ctx.getString("efsFileSystemId");
        String mountTargetId = ctx.getString("efsMountTargetId");
        Assertions.assertNotBlank(mountTargetId,
                "DescribeMountTargets: mount target from CreateMountTarget");
        var resp = efs().describeMountTargets(r -> r.fileSystemId(fileSystemId));
        Assertions.assertNotEmpty(resp.mountTargets(), "DescribeMountTargets: mountTargets");
        MountTargetDescription match = resp.mountTargets().stream()
                .filter(mt -> mountTargetId.equals(mt.mountTargetId()))
                .findFirst()
                .orElse(null);
        Assertions.assertNotNull(match,
                "DescribeMountTargets: mount target " + mountTargetId + " not in the response");
        Assertions.assertNotBlank(match.ipAddress(),
                "DescribeMountTargets: mount target " + mountTargetId + " ipAddress");
    }

    private void describeMountTargetSecurityGroups(TestContext ctx) throws Exception {
        String mountTargetId = ctx.getString("efsMountTargetId");
        Assertions.assertNotBlank(mountTargetId,
                "DescribeMountTargetSecurityGroups: mount target from CreateMountTarget");
        var resp = efs().describeMountTargetSecurityGroups(r -> r.mountTargetId(mountTargetId));
        Assertions.assertEquals(List.of("sg-compat-a"), resp.securityGroups(),
                "DescribeMountTargetSecurityGroups: expected the group set at create");
    }

    private void modifyMountTargetSecurityGroups(TestContext ctx) throws Exception {
        String mountTargetId = ctx.getString("efsMountTargetId");
        Assertions.assertNotBlank(mountTargetId,
                "ModifyMountTargetSecurityGroups: mount target from CreateMountTarget");
        efs().modifyMountTargetSecurityGroups(r -> r
                .mountTargetId(mountTargetId)
                .securityGroups("sg-compat-b", "sg-compat-c"));
        // The mutation is what this test is about, so assert it here rather
        // than leaving it for another test to notice.
        var resp = efs().describeMountTargetSecurityGroups(r -> r.mountTargetId(mountTargetId));
        Assertions.assertEquals(List.of("sg-compat-b", "sg-compat-c"), resp.securityGroups(),
                "ModifyMountTargetSecurityGroups: security groups were not updated");
    }

    private void deleteMountTarget(TestContext ctx) throws Exception {
        String fileSystemId = ctx.getString("efsFileSystemId");
        String mountTargetId = ctx.getString("efsMountTargetId");
        Assertions.assertNotBlank(mountTargetId,
                "DeleteMountTarget: mount target from CreateMountTarget");
        efs().deleteMountTarget(r -> r.mountTargetId(mountTargetId));
        ctx.set("efsMountTargetId", "");

        var resp = efs().describeMountTargets(r -> r.fileSystemId(fileSystemId));
        boolean stillListed = resp.mountTargets().stream()
                .anyMatch(mt -> mountTargetId.equals(mt.mountTargetId()));
        Assertions.assertFalse(stillListed,
                "DeleteMountTarget: mount target " + mountTargetId + " still listed after delete");
    }
}
