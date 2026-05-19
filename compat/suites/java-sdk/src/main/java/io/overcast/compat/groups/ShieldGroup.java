package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.shield.ShieldClient;
import software.amazon.awssdk.services.shield.model.*;

import java.util.Map;

/**
 * AWS Shield compatibility test group.
 *
 * <p>Groups: shield-protections.
 */
public final class ShieldGroup implements ServiceGroup {

    private final AwsClients clients;

    public ShieldGroup(AwsClients clients) {
        this.clients = clients;
    }

    private ShieldClient shield() { return clients.shield(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateProtection",  this::createProtection),
                Map.entry("DescribeProtection",this::describeProtection),
                Map.entry("DescribeSubscription",this::describeSubscription),
                Map.entry("ListProtections",   this::listProtections),
                Map.entry("DeleteProtection",  this::deleteProtection)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of("shield-protections", this::setupProtections);
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of("shield-protections",
                ctx -> deleteProtectionSilently(ctx.getString("shieldProtectionId")));
    }

    // ── shield-protections ────────────────────────────────────────────────────

    private void setupProtections(TestContext ctx) {
        ctx.set("shieldProtectionName", "compat-" + ctx.runId());
        // Placeholder resource ARN — the emulator should accept any valid ARN.
        ctx.set("shieldResourceArn",
                "arn:aws:ec2:us-east-1:000000000000:eip-allocation/eipalloc-" + ctx.runId());
    }

    private void createProtection(TestContext ctx) throws Exception {
        String name = ctx.getString("shieldProtectionName");
        String arn  = ctx.getString("shieldResourceArn");
        var resp = shield().createProtection(r -> r.name(name).resourceArn(arn));
        Assertions.assertNotBlank(resp.protectionId(), "CreateProtection: protectionId is blank");
        ctx.set("shieldProtectionId", resp.protectionId());
    }

    private void describeProtection(TestContext ctx) throws Exception {
        String id = ctx.getString("shieldProtectionId");
        var resp = shield().describeProtection(r -> r.protectionId(id));
        Assertions.assertEquals(id, resp.protection().id(), "DescribeProtection: id mismatch");
    }

    private void listProtections(TestContext ctx) throws Exception {
        var resp = shield().listProtections(r -> r.maxResults(100));
        Assertions.assertNotNull(resp.protections(), "ListProtections: protections is null");
    }

    private void describeSubscription(TestContext ctx) throws Exception {
        shield().describeSubscription();
    }

    private void deleteProtection(TestContext ctx) throws Exception {
        String id = ctx.getString("shieldProtectionId");
        shield().deleteProtection(r -> r.protectionId(id));
        ctx.set("shieldProtectionId", null);
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteProtectionSilently(String id) {
        if (id == null) return;
        try { shield().deleteProtection(r -> r.protectionId(id)); } catch (Exception ignored) {}
    }
}
