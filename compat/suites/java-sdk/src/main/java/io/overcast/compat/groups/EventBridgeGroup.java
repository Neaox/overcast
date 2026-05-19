package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.eventbridge.EventBridgeClient;
import software.amazon.awssdk.services.eventbridge.model.*;

import java.util.List;
import java.util.Map;

/**
 * EventBridge compatibility test group.
 *
 * <p>Groups: eventbridge-buses, eventbridge-rules, eventbridge-events.
 */
public final class EventBridgeGroup implements ServiceGroup {

    private final AwsClients clients;

    public EventBridgeGroup(AwsClients clients) {
        this.clients = clients;
    }

    private EventBridgeClient eb() { return clients.eventBridge(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateEventBus",        this::createEventBus),
                Map.entry("DescribeEventBus",      this::describeEventBus),
                Map.entry("ListEventBuses",        this::listEventBuses),
                Map.entry("TagEventBus",           this::tagEventBus),
                Map.entry("ListEventBridgeTagsForResource",   this::listTagsForEventBus),
                Map.entry("DeleteEventBus",        this::deleteEventBus),
                Map.entry("PutRule",               this::putRule),
                Map.entry("DescribeRule",          this::describeRule),
                Map.entry("ListRules",             this::listRules),
                Map.entry("EnableRule",            this::enableRule),
                Map.entry("DisableRule",           this::disableRule),
                Map.entry("PutTargets",            this::putTargets),
                Map.entry("ListTargetsByRule",     this::listTargetsByRule),
                Map.entry("RemoveTargets",         this::removeTargets),
                Map.entry("DeleteRule",            this::deleteRule),
                Map.entry("PutEvents",             this::putEvents),
                Map.entry("PutEventsBatch",    this::putEventsCustomBus)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("eventbridge-buses",  this::setupBuses),
                Map.entry("eventbridge-rules",  this::setupRules),
                Map.entry("eventbridge-events", this::setupEventsGroup)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("eventbridge-buses",  ctx -> deleteBusSilently(ctx.getString("eventBusName"))),
                Map.entry("eventbridge-rules",  this::teardownRules),
                Map.entry("eventbridge-events", ctx -> deleteBusSilently(ctx.getString("eventsBusName")))
        );
    }

    // ── eventbridge-buses ─────────────────────────────────────────────────────

    private void setupBuses(TestContext ctx) {
        ctx.set("eventBusName", "compat-bus-" + ctx.runId());
    }

    private void createEventBus(TestContext ctx) throws Exception {
        String name = ctx.getString("eventBusName");
        var resp = eb().createEventBus(r -> r.name(name));
        Assertions.assertNotBlank(resp.eventBusArn(), "CreateEventBus: eventBusArn is blank");
    }

    private void describeEventBus(TestContext ctx) throws Exception {
        String name = ctx.getString("eventBusName");
        var resp = eb().describeEventBus(r -> r.name(name));
        Assertions.assertEquals(name, resp.name(), "DescribeEventBus: name mismatch");
    }

    private void listEventBuses(TestContext ctx) throws Exception {
        String name = ctx.getString("eventBusName");
        var resp = eb().listEventBuses(r -> r.limit(100));
        boolean found = resp.eventBuses().stream().anyMatch(b -> b.name().equals(name));
        Assertions.assertTrue(found, "ListEventBuses: created bus not found");
    }

    private void tagEventBus(TestContext ctx) throws Exception {
        var resp = eb().describeEventBus(r -> r.name(ctx.getString("eventBusName")));
        eb().tagResource(r -> r.resourceARN(resp.arn()).tags(
                Tag.builder().key("env").value("compat").build()));
    }

    private void listTagsForEventBus(TestContext ctx) throws Exception {
        var resp = eb().describeEventBus(r -> r.name(ctx.getString("eventBusName")));
        var tags = eb().listTagsForResource(r -> r.resourceARN(resp.arn()));
        boolean found = tags.tags().stream().anyMatch(t -> "env".equals(t.key()));
        Assertions.assertTrue(found, "ListTagsForEventBus: expected 'env' tag");
    }

    private void deleteEventBus(TestContext ctx) throws Exception {
        String name = ctx.getString("eventBusName");
        eb().deleteEventBus(r -> r.name(name));
        ctx.set("eventBusName", null);
    }

    // ── eventbridge-rules ─────────────────────────────────────────────────────

    private void setupRules(TestContext ctx) throws Exception {
        String bus  = "compat-rb-" + ctx.runId();
        String rule = "compat-rule-" + ctx.runId();
        eb().createEventBus(r -> r.name(bus));
        ctx.set("rulesBusName", bus);
        ctx.set("ruleName", rule);
    }

    private void teardownRules(TestContext ctx) {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        if (bus == null) return;
        try {
            eb().removeTargets(r -> r.rule(rule).eventBusName(bus).ids("t1"));
        } catch (Exception ignored) {}
        try {
            eb().deleteRule(r -> r.name(rule).eventBusName(bus));
        } catch (Exception ignored) {}
        deleteBusSilently(bus);
    }

    private void putRule(TestContext ctx) throws Exception {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        var resp = eb().putRule(r -> r
                .name(rule)
                .eventBusName(bus)
                .eventPattern("{\"source\":[\"compat.test\"]}")
                .state(RuleState.ENABLED));
        Assertions.assertNotBlank(resp.ruleArn(), "PutRule: ruleArn is blank");
    }

    private void describeRule(TestContext ctx) throws Exception {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        var resp = eb().describeRule(r -> r.name(rule).eventBusName(bus));
        Assertions.assertEquals(rule, resp.name(), "DescribeRule: name mismatch");
    }

    private void listRules(TestContext ctx) throws Exception {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        var resp = eb().listRules(r -> r.eventBusName(bus));
        boolean found = resp.rules().stream().anyMatch(r2 -> r2.name().equals(rule));
        Assertions.assertTrue(found, "ListRules: created rule not found");
    }

    private void enableRule(TestContext ctx) throws Exception {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        eb().enableRule(r -> r.name(rule).eventBusName(bus));
    }

    private void disableRule(TestContext ctx) throws Exception {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        eb().disableRule(r -> r.name(rule).eventBusName(bus));
        eb().enableRule(r -> r.name(rule).eventBusName(bus)); // re-enable for subsequent tests
    }

    private void putTargets(TestContext ctx) throws Exception {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        // Use a placeholder ARN — the emulator accepts any valid-looking ARN.
        eb().putTargets(r -> r
                .rule(rule).eventBusName(bus)
                .targets(Target.builder().id("t1").arn("arn:aws:sqs:us-east-1:000000000000:compat-dummy").build()));
    }

    private void listTargetsByRule(TestContext ctx) throws Exception {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        var resp = eb().listTargetsByRule(r -> r.rule(rule).eventBusName(bus));
        Assertions.assertNotEmpty(resp.targets(), "ListTargetsByRule: no targets found");
    }

    private void removeTargets(TestContext ctx) throws Exception {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        eb().removeTargets(r -> r.rule(rule).eventBusName(bus).ids("t1"));
    }

    private void deleteRule(TestContext ctx) throws Exception {
        String bus  = ctx.getString("rulesBusName");
        String rule = ctx.getString("ruleName");
        eb().deleteRule(r -> r.name(rule).eventBusName(bus));
    }

    // ── eventbridge-events ────────────────────────────────────────────────────

    private void setupEventsGroup(TestContext ctx) throws Exception {
        String bus = "compat-ev-" + ctx.runId();
        eb().createEventBus(r -> r.name(bus));
        ctx.set("eventsBusName", bus);
    }

    private void putEvents(TestContext ctx) throws Exception {
        var entry = PutEventsRequestEntry.builder()
                .eventBusName("default")
                .source("compat.test")
                .detailType("CompatTest")
                .detail("{\"key\":\"value\"}")
                .build();
        var resp = eb().putEvents(r -> r.entries(entry));
        Assertions.assertEquals(0, resp.failedEntryCount(), "PutEvents: some events failed");
    }

    private void putEventsCustomBus(TestContext ctx) throws Exception {
        String bus = ctx.getString("eventsBusName");
        var entry = PutEventsRequestEntry.builder()
                .eventBusName(bus)
                .source("compat.test")
                .detailType("CompatCustomBus")
                .detail("{\"bus\":\"custom\"}")
                .build();
        var resp = eb().putEvents(r -> r.entries(entry));
        Assertions.assertEquals(0, resp.failedEntryCount(), "PutEventsCustomBus: some events failed");
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteBusSilently(String name) {
        if (name == null) return;
        try { eb().deleteEventBus(r -> r.name(name)); } catch (Exception ignored) {}
    }
}
