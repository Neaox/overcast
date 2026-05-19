package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.wafv2.Wafv2Client;
import software.amazon.awssdk.services.wafv2.model.*;

import java.util.Map;

/**
 * WAF v2 compatibility test group.
 *
 * <p>Groups: waf-webacls.
 */
public final class WafGroup implements ServiceGroup {

    private final AwsClients clients;

    public WafGroup(AwsClients clients) {
        this.clients = clients;
    }

    private Wafv2Client waf() { return clients.wafv2(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateWebACL",  this::createWebAcl),
                Map.entry("GetWebACL",     this::getWebAcl),
                Map.entry("ListWebACLs",   this::listWebAcls),
                Map.entry("DeleteWebACL",  this::deleteWebAcl)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of("waf-webacls", this::setupWebAcls);
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of("waf-webacls", this::teardownWebAcls);
    }

    // ── waf-webacls ───────────────────────────────────────────────────────────

    private void setupWebAcls(TestContext ctx) {
        ctx.set("wafAclName", "compat-" + ctx.runId());
    }

    private void teardownWebAcls(TestContext ctx) {
        String id       = ctx.getString("wafAclId");
        String lockToken = ctx.getString("wafAclToken");
        if (id == null) return;
        try { waf().deleteWebACL(r -> r.name(ctx.getString("wafAclName")).id(id)
                .scope(Scope.REGIONAL).lockToken(lockToken)); } catch (Exception ignored) {}
    }

    private void createWebAcl(TestContext ctx) throws Exception {
        String name = ctx.getString("wafAclName");
        var resp = waf().createWebACL(r -> r
                .name(name)
                .scope(Scope.REGIONAL)
                .defaultAction(DefaultAction.builder().allow(AllowAction.builder().build()).build())
                .visibilityConfig(VisibilityConfig.builder()
                        .sampledRequestsEnabled(true)
                        .cloudWatchMetricsEnabled(false)
                        .metricName(name)
                        .build()));
        Assertions.assertNotBlank(resp.summary().id(), "CreateWebACL: id is blank");
        ctx.set("wafAclId",    resp.summary().id());
        ctx.set("wafAclToken", resp.summary().lockToken());
    }

    private void getWebAcl(TestContext ctx) throws Exception {
        String id   = ctx.getString("wafAclId");
        String name = ctx.getString("wafAclName");
        var resp = waf().getWebACL(r -> r.id(id).name(name).scope(Scope.REGIONAL));
        Assertions.assertEquals(id, resp.webACL().id(), "GetWebACL: id mismatch");
        ctx.set("wafAclToken", resp.lockToken());
    }

    private void listWebAcls(TestContext ctx) throws Exception {
        var resp = waf().listWebACLs(r -> r.scope(Scope.REGIONAL).limit(100));
        String id = ctx.getString("wafAclId");
        boolean found = resp.webACLs().stream().anyMatch(a -> a.id().equals(id));
        Assertions.assertTrue(found, "ListWebACLs: created web ACL not found");
    }

    private void deleteWebAcl(TestContext ctx) throws Exception {
        String id    = ctx.getString("wafAclId");
        String name  = ctx.getString("wafAclName");
        String saved = ctx.getString("wafAclToken");
        final String token;
        if (saved == null) {
            var resp = waf().getWebACL(r -> r.id(id).name(name).scope(Scope.REGIONAL));
            token = resp.lockToken();
        } else {
            token = saved;
        }
        waf().deleteWebACL(r -> r.id(id).name(name).scope(Scope.REGIONAL).lockToken(token));
        ctx.set("wafAclId",    null);
        ctx.set("wafAclToken", null);
    }
}
