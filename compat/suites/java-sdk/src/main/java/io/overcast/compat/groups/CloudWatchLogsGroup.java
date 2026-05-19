package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.cloudwatchlogs.CloudWatchLogsClient;
import software.amazon.awssdk.services.cloudwatchlogs.model.*;

import java.util.Map;

/**
 * CloudWatch Logs compatibility test group.
 *
 * <p>Groups: logs-groups, logs-events.
 */
public final class CloudWatchLogsGroup implements ServiceGroup {

    private final AwsClients clients;

    public CloudWatchLogsGroup(AwsClients clients) {
        this.clients = clients;
    }

    private CloudWatchLogsClient logs() { return clients.cloudWatchLogs(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateLogGroup",       this::createLogGroup),
                Map.entry("DescribeLogGroups",    this::describeLogGroups),
                Map.entry("CreateLogStream",      this::createLogStream),
                Map.entry("DescribeLogStreams",   this::describeLogStreams),
                Map.entry("TagLogGroup",          this::tagLogGroup),
                Map.entry("DeleteLogGroup",       this::deleteLogGroup),
                Map.entry("PutLogEvents",         this::putLogEvents),
                Map.entry("GetLogEvents",         this::getLogEvents),
                Map.entry("FilterLogEvents",      this::filterLogEvents),
                Map.entry("PutRetentionPolicy",   this::putRetentionPolicy),
                Map.entry("VerifyRetentionPolicy",this::verifyRetentionPolicy),
                Map.entry("DeleteRetentionPolicy",this::deleteRetentionPolicy),
                Map.entry("DeleteLogStream",      this::deleteLogStream)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("logs-groups", this::setupGroups),
                Map.entry("logs-events", this::setupEvents)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("logs-groups", ctx -> deleteGroupSilently(ctx.getString("logGroupName"))),
                Map.entry("logs-events", ctx -> deleteGroupSilently(ctx.getString("logsEventsGroup")))
        );
    }

    // ── logs-groups ───────────────────────────────────────────────────────────

    private void setupGroups(TestContext ctx) {
        ctx.set("logGroupName", "/compat/" + ctx.runId() + "/group");
    }

    private void createLogGroup(TestContext ctx) throws Exception {
        String name = ctx.getString("logGroupName");
        logs().createLogGroup(r -> r.logGroupName(name));
    }

    private void describeLogGroups(TestContext ctx) throws Exception {
        String name = ctx.getString("logGroupName");
        var resp = logs().describeLogGroups(r -> r.logGroupNamePrefix(name));
        boolean found = resp.logGroups().stream().anyMatch(g -> g.logGroupName().equals(name));
        Assertions.assertTrue(found, "DescribeLogGroups: created log group not found");
    }

    private void createLogStream(TestContext ctx) throws Exception {
        String grp = ctx.getString("logGroupName");
        if (grp == null) {
            grp = "/compat/" + ctx.runId() + "/stream";
            final String g = grp;
            logs().createLogGroup(r -> r.logGroupName(g));
            ctx.set("logGroupName", grp);
        }
        final String g = grp;
        String stream = "stream-" + ctx.runId();
        logs().createLogStream(r -> r.logGroupName(g).logStreamName(stream));
        ctx.set("logStreamName", stream);
    }

    private void describeLogStreams(TestContext ctx) throws Exception {
        String grp    = ctx.getString("logsEventsGroup");
        if (grp == null) grp = ctx.getString("logGroupName");
        String stream = ctx.getString("logsEventsStream");
        if (stream == null) stream = ctx.getString("logStreamName");
        Assertions.assertNotNull(grp, "DescribeLogStreams: no log group from setup");
        Assertions.assertNotNull(stream, "DescribeLogStreams: no log stream from setup");
        final String fg = grp;
        final String fs = stream;
        var resp = logs().describeLogStreams(r -> r.logGroupName(fg).logStreamNamePrefix(fs));
        boolean found = resp.logStreams().stream().anyMatch(s -> s.logStreamName().equals(fs));
        Assertions.assertTrue(found, "DescribeLogStreams: created log stream not found");
    }

    private void tagLogGroup(TestContext ctx) throws Exception {
        String name = ctx.getString("logGroupName");
        if (name == null) {
            name = "/compat/" + ctx.runId() + "/tag";
            final String n = name;
            logs().createLogGroup(r -> r.logGroupName(n));
            ctx.set("logGroupName", name);
        }
        final String n = name;
        logs().tagLogGroup(r -> r.logGroupName(n).tags(Map.of("env", "compat")));
    }

    private void deleteLogGroup(TestContext ctx) throws Exception {
        String name = ctx.getString("logGroupName");
        logs().deleteLogGroup(r -> r.logGroupName(name));
        ctx.set("logGroupName", null);
    }

    // ── logs-events ───────────────────────────────────────────────────────────

    private void setupEvents(TestContext ctx) throws Exception {
        String grp    = "/compat/" + ctx.runId() + "/events";
        String stream = "stream-events";
        logs().createLogGroup(r -> r.logGroupName(grp));
        logs().createLogStream(r -> r.logGroupName(grp).logStreamName(stream));
        ctx.set("logsEventsGroup",  grp);
        ctx.set("logsEventsStream", stream);
    }

    private void putLogEvents(TestContext ctx) throws Exception {
        String grp    = ctx.getString("logsEventsGroup");
        String stream = ctx.getString("logsEventsStream");
        var event = InputLogEvent.builder()
                .timestamp(System.currentTimeMillis())
                .message("compat test event")
                .build();
        var resp = logs().putLogEvents(r -> r
                .logGroupName(grp)
                .logStreamName(stream)
                .logEvents(event));
        Assertions.assertNotNull(resp, "PutLogEvents: response is null");
    }

    private void getLogEvents(TestContext ctx) throws Exception {
        String grp    = ctx.getString("logsEventsGroup");
        String stream = ctx.getString("logsEventsStream");
        var resp = logs().getLogEvents(r -> r
                .logGroupName(grp).logStreamName(stream).limit(10));
        Assertions.assertNotNull(resp.events(), "GetLogEvents: events is null");
    }

    private void filterLogEvents(TestContext ctx) throws Exception {
        String grp = ctx.getString("logsEventsGroup");
        var resp = logs().filterLogEvents(r -> r
                .logGroupName(grp).filterPattern("compat"));
        Assertions.assertNotNull(resp.events(), "FilterLogEvents: events is null");
    }

    private void putRetentionPolicy(TestContext ctx) throws Exception {
        String grp = ctx.getString("logGroupName");
        if (grp == null) {
            grp = "/compat/" + ctx.runId() + "/retention";
            final String g = grp;
            logs().createLogGroup(r -> r.logGroupName(g));
            ctx.set("logGroupName", grp);
        }
        final String g = grp;
        logs().putRetentionPolicy(r -> r.logGroupName(g).retentionInDays(7));
    }

    private void deleteRetentionPolicy(TestContext ctx) throws Exception {
        String grp = ctx.getString("logGroupName");
        if (grp == null) {
            grp = "/compat/" + ctx.runId() + "/retention";
            final String g = grp;
            logs().createLogGroup(r -> r.logGroupName(g));
            ctx.set("logGroupName", grp);
        }
        final String g = grp;
        logs().deleteRetentionPolicy(r -> r.logGroupName(g));
    }

    private void verifyRetentionPolicy(TestContext ctx) throws Exception {
        String grp = ctx.getString("logGroupName");
        var resp = logs().describeLogGroups(r -> r.logGroupNamePrefix(grp));
        var match = resp.logGroups().stream()
                .filter(lg -> lg.logGroupName().equals(grp))
                .findFirst();
        Assertions.assertTrue(match.isPresent(), "VerifyRetentionPolicy: log group not found");
        Assertions.assertTrue(match.get().retentionInDays() != null && match.get().retentionInDays() == 7,
                "VerifyRetentionPolicy: expected retentionInDays=7");
    }

    private void deleteLogStream(TestContext ctx) throws Exception {
        String grp = ctx.getString("logsEventsGroup");
        String stream = ctx.getString("logsEventsStream");
        if (grp == null || stream == null) {
            grp = "/compat/" + ctx.runId() + "/delstream";
            stream = "stream-del";
            final String g = grp;
            final String s = stream;
            logs().createLogGroup(r -> r.logGroupName(g));
            logs().createLogStream(r -> r.logGroupName(g).logStreamName(s));
            ctx.set("logsEventsGroup", grp);
            ctx.set("logsEventsStream", stream);
        }
        final String g = grp;
        final String s = stream;
        logs().deleteLogStream(r -> r.logGroupName(g).logStreamName(s));
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteGroupSilently(String name) {
        if (name == null) return;
        try { logs().deleteLogGroup(r -> r.logGroupName(name)); } catch (Exception ignored) {}
    }
}
