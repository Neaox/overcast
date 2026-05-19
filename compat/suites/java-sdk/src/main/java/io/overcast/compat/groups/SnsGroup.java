package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.sns.SnsClient;
import software.amazon.awssdk.services.sns.model.*;
import software.amazon.awssdk.services.sqs.SqsClient;
import software.amazon.awssdk.services.sqs.model.QueueAttributeName;

import java.util.Map;

/**
 * SNS compatibility test group.
 *
 * <p>Groups: sns-topics, sns-publish, sns-subscriptions.
 */
public final class SnsGroup implements ServiceGroup {

    private final AwsClients clients;

    public SnsGroup(AwsClients clients) {
        this.clients = clients;
    }

    private SnsClient sns() { return clients.sns(); }
    private SqsClient sqs() { return clients.sqs(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateTopic",               this::createTopic),
                Map.entry("ListTopics",                this::listTopics),
                Map.entry("GetTopicAttributes",        this::getTopicAttributes),
                Map.entry("SetTopicAttributes",        this::setTopicAttributes),
                Map.entry("DeleteTopic",               this::deleteTopic),
                Map.entry("Publish",                   this::publish),
                Map.entry("PublishWithAttributes",     this::publishWithAttributes),
                Map.entry("PublishBatch",              this::publishBatch),
                Map.entry("SubscribeSQS",              this::subscribeSqs),
                Map.entry("ListSubscriptionsByTopic",  this::listSubscriptionsByTopic),
                Map.entry("GetSubscriptionAttributes", this::getSubscriptionAttributes),
                Map.entry("PublishDeliveredToSQS",     this::publishDeliveredToSqs),
                Map.entry("SetSubscriptionAttributes", this::setSubscriptionAttributes),
                Map.entry("Unsubscribe",               this::unsubscribe)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("sns-topics",        this::setupTopics),
                Map.entry("sns-publish",       this::setupPublish),
                Map.entry("sns-subscriptions", this::setupSubscriptions)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("sns-topics",        ctx -> deleteTopicSilently(ctx.getString("snsTopicArn"))),
                Map.entry("sns-publish",       ctx -> deleteTopicSilently(ctx.getString("snsPubTopicArn"))),
                Map.entry("sns-subscriptions", this::teardownSubscriptions)
        );
    }

    // ── sns-topics ─────────────────────────────────────────────────────────────

    private void setupTopics(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-snstopic";
        var resp = sns().createTopic(r -> r.name(name));
        ctx.set("snsTopicArn", resp.topicArn());
        ctx.set("snsTopicName", name);
    }

    private void createTopic(TestContext ctx) {
        Assertions.assertNotBlank(ctx.getString("snsTopicArn"), "snsTopicArn");
    }

    private void listTopics(TestContext ctx) throws Exception {
        String arn = ctx.getString("snsTopicArn");
        var resp = sns().listTopics();
        boolean found = resp.topics().stream().anyMatch(t -> t.topicArn().equals(arn));
        Assertions.assertTrue(found, "ListTopics: topic not found (runId=" + ctx.runId() + ")");
    }

    private void getTopicAttributes(TestContext ctx) throws Exception {
        String arn = ctx.getString("snsTopicArn");
        var resp = sns().getTopicAttributes(r -> r.topicArn(arn));
        Assertions.assertTrue(resp.hasAttributes(), "GetTopicAttributes: no attributes returned");
        Assertions.assertNotBlank(resp.attributes().get("TopicArn"), "GetTopicAttributes: TopicArn");
    }

    private void setTopicAttributes(TestContext ctx) throws Exception {
        String arn = ctx.getString("snsTopicArn");
        sns().setTopicAttributes(r -> r.topicArn(arn)
                .attributeName("DisplayName")
                .attributeValue("Overcast Test Topic"));
        var resp = sns().getTopicAttributes(r -> r.topicArn(arn));
        Assertions.assertEquals("Overcast Test Topic", resp.attributes().get("DisplayName"),
                "SetTopicAttributes: DisplayName mismatch");
    }

    private void deleteTopic(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-snsdel";
        var resp = sns().createTopic(r -> r.name(name));
        sns().deleteTopic(r -> r.topicArn(resp.topicArn()));
    }

    // ── sns-publish ────────────────────────────────────────────────────────────

    private void setupPublish(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-snspub";
        var resp = sns().createTopic(r -> r.name(name));
        ctx.set("snsPubTopicArn", resp.topicArn());
    }

    private void publish(TestContext ctx) throws Exception {
        String arn = ctx.getString("snsPubTopicArn");
        var resp = sns().publish(r -> r.topicArn(arn).message("Hello SNS"));
        Assertions.assertNotBlank(resp.messageId(), "Publish: messageId");
    }

    private void publishWithAttributes(TestContext ctx) throws Exception {
        String arn = ctx.getString("snsPubTopicArn");
        var resp = sns().publish(r -> r.topicArn(arn)
                .message("Attributed message")
                .messageAttributes(Map.of("attr1",
                        MessageAttributeValue.builder()
                                .dataType("String")
                                .stringValue("value1")
                                .build())));
        Assertions.assertNotBlank(resp.messageId(), "PublishWithAttributes: messageId");
    }

    private void publishBatch(TestContext ctx) throws Exception {
        String arn = ctx.getString("snsPubTopicArn");
        var resp = sns().publishBatch(r -> r.topicArn(arn).publishBatchRequestEntries(
                PublishBatchRequestEntry.builder().id("m1").message("batch-msg-1").build(),
                PublishBatchRequestEntry.builder().id("m2").message("batch-msg-2").build()
        ));
        Assertions.assertEquals(2, resp.successful().size(), "PublishBatch: expected 2 successful");
    }

    // ── sns-subscriptions ─────────────────────────────────────────────────────

    private void setupSubscriptions(TestContext ctx) throws Exception {
        String topicName = ctx.runId() + "-snssub";
        String queueName = ctx.runId() + "-snssub-q";
        var topicResp = sns().createTopic(r -> r.name(topicName));
        var queueResp = sqs().createQueue(r -> r.queueName(queueName));
        var qAttrs = sqs().getQueueAttributes(r -> r
                .queueUrl(queueResp.queueUrl())
                .attributeNames(QueueAttributeName.QUEUE_ARN));
        String queueArn = qAttrs.attributesAsStrings().get("QueueArn");
        ctx.set("snsSubTopicArn", topicResp.topicArn());
        ctx.set("snsSubQueueUrl", queueResp.queueUrl());
        ctx.set("snsSubQueueArn", queueArn);
    }

    private void teardownSubscriptions(TestContext ctx) {
        deleteTopicSilently(ctx.getString("snsSubTopicArn"));
        deleteQueueSilently(ctx.getString("snsSubQueueUrl"));
    }

    private void subscribeSqs(TestContext ctx) throws Exception {
        String topicArn  = ctx.getString("snsSubTopicArn");
        String queueArn  = ctx.getString("snsSubQueueArn");
        var resp = sns().subscribe(r -> r.topicArn(topicArn).protocol("sqs").endpoint(queueArn));
        Assertions.assertNotBlank(resp.subscriptionArn(), "SubscribeSQS: subscriptionArn");
        ctx.set("snsSubArn", resp.subscriptionArn());
    }

    private void listSubscriptionsByTopic(TestContext ctx) throws Exception {
        String topicArn = ctx.getString("snsSubTopicArn");
        var resp = sns().listSubscriptionsByTopic(r -> r.topicArn(topicArn));
        Assertions.assertNotEmpty(resp.subscriptions(), "ListSubscriptionsByTopic: no subscriptions");
    }

    private void getSubscriptionAttributes(TestContext ctx) throws Exception {
        String subArn = ctx.getString("snsSubArn");
        if (subArn == null || subArn.equals("PendingConfirmation")) return;
        var resp = sns().getSubscriptionAttributes(r -> r.subscriptionArn(subArn));
        Assertions.assertTrue(resp.hasAttributes(), "GetSubscriptionAttributes: no attributes");
    }

    private void publishDeliveredToSqs(TestContext ctx) throws Exception {
        String topicArn  = ctx.getString("snsSubTopicArn");
        String queueUrl  = ctx.getString("snsSubQueueUrl");
        sns().publish(r -> r.topicArn(topicArn).message("deliver-test"));
        // Poll SQS briefly (no sleep — just try once; delivery is eventual).
        var recv = sqs().receiveMessage(r -> r.queueUrl(queueUrl)
                .maxNumberOfMessages(1).waitTimeSeconds(2));
        Assertions.assertNotEmpty(recv.messages(), "PublishDeliveredToSQS: no messages in queue after publish");
    }

    private void setSubscriptionAttributes(TestContext ctx) throws Exception {
        String subArn = ctx.getString("snsSubArn");
        if (subArn == null || subArn.equals("PendingConfirmation")) return;
        sns().setSubscriptionAttributes(r -> r.subscriptionArn(subArn)
                .attributeName("RawMessageDelivery")
                .attributeValue("true"));
    }

    private void unsubscribe(TestContext ctx) throws Exception {
        String subArn = ctx.getString("snsSubArn");
        if (subArn == null || subArn.equals("PendingConfirmation")) return;
        sns().unsubscribe(r -> r.subscriptionArn(subArn));
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteTopicSilently(String arn) {
        if (arn == null) return;
        try { sns().deleteTopic(r -> r.topicArn(arn)); } catch (Exception ignored) {}
    }

    private void deleteQueueSilently(String url) {
        if (url == null) return;
        try { sqs().deleteQueue(r -> r.queueUrl(url)); } catch (Exception ignored) {}
    }
}
