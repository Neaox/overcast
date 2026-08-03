package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.dynamodb.DynamoDbClient;
import software.amazon.awssdk.services.dynamodb.model.AttributeDefinition;
import software.amazon.awssdk.services.dynamodb.model.AttributeValue;
import software.amazon.awssdk.services.dynamodb.model.BillingMode;
import software.amazon.awssdk.services.dynamodb.model.KeySchemaElement;
import software.amazon.awssdk.services.dynamodb.model.KeyType;
import software.amazon.awssdk.services.dynamodb.model.ScalarAttributeType;
import software.amazon.awssdk.services.dynamodb.model.StreamSpecification;
import software.amazon.awssdk.services.dynamodb.model.StreamViewType;
import software.amazon.awssdk.services.pipes.PipesClient;
import software.amazon.awssdk.services.pipes.model.PipeState;
import software.amazon.awssdk.services.pipes.model.RequestedPipeState;
import software.amazon.awssdk.services.pipes.model.ValidationException;
import software.amazon.awssdk.services.sqs.SqsClient;
import software.amazon.awssdk.services.sqs.model.Message;
import software.amazon.awssdk.services.sqs.model.QueueAttributeName;

import java.util.Map;

/**
 * EventBridge Pipes compatibility test group.
 *
 * <p>Groups: pipes-wiring — pipe CRUD, the loud rejection of a source/target
 * combination the emulator cannot run, and an end-to-end delivery from a
 * DynamoDB stream source to an SQS target.
 *
 * <p>The group provisions its own table and queue (#388), and the delivery
 * assertion matches on this group's own record rather than "the first message
 * received", so a shared resource can never make it pass by accident.
 */
public final class PipesGroup implements ServiceGroup {

    /**
     * RoleArn is a required CreatePipe member. Overcast does not evaluate it —
     * it is not a security boundary — but AWS rejects the call without it.
     */
    private static final String ROLE_ARN = "arn:aws:iam::000000000000:role/oc-compat-pipe";

    private final AwsClients clients;

    public PipesGroup(AwsClients clients) {
        this.clients = clients;
    }

    private PipesClient pipes() { return clients.pipes(); }
    private DynamoDbClient ddb() { return clients.dynamoDb(); }
    private SqsClient sqs() { return clients.sqs(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreatePipe",   this::createPipe),
                Map.entry("DescribePipe", this::describePipe),
                Map.entry("ListPipes",    this::listPipes),
                Map.entry("CreatePipeRejectsUnsupportedTarget", this::createPipeRejectsUnsupportedTarget),
                Map.entry("PipeDeliversToTarget", this::pipeDeliversToTarget),
                Map.entry("UpdatePipe",   this::updatePipe),
                Map.entry("DeletePipe",   this::deletePipe)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of("pipes-wiring", this::setupWiring);
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of("pipes-wiring", this::teardownWiring);
    }

    // ── pipes-wiring ──────────────────────────────────────────────────────────

    private void setupWiring(TestContext ctx) throws Exception {
        String table = "oc-" + ctx.runId() + "-pipe-src";
        String queue = "oc-" + ctx.runId() + "-pipe-dst";
        ctx.set("pipeName", "oc-" + ctx.runId() + "-pipe");
        ctx.set("pipeTable", table);

        ddb().createTable(r -> r
                .tableName(table)
                .attributeDefinitions(AttributeDefinition.builder()
                        .attributeName("id").attributeType(ScalarAttributeType.S).build())
                .keySchema(KeySchemaElement.builder()
                        .attributeName("id").keyType(KeyType.HASH).build())
                .billingMode(BillingMode.PAY_PER_REQUEST)
                .streamSpecification(StreamSpecification.builder()
                        .streamEnabled(true)
                        .streamViewType(StreamViewType.NEW_AND_OLD_IMAGES)
                        .build()));
        var described = ddb().describeTable(r -> r.tableName(table));
        Assertions.assertNotBlank(described.table().latestStreamArn(),
                "pipes setup: table has no LatestStreamArn");
        ctx.set("pipeSourceArn", described.table().latestStreamArn());

        var created = sqs().createQueue(r -> r.queueName(queue));
        ctx.set("pipeQueueUrl", created.queueUrl());
        var attrs = sqs().getQueueAttributes(r -> r
                .queueUrl(created.queueUrl())
                .attributeNames(QueueAttributeName.QUEUE_ARN));
        String arn = attrs.attributes().get(QueueAttributeName.QUEUE_ARN);
        Assertions.assertNotBlank(arn, "pipes setup: missing QueueArn");
        ctx.set("pipeTargetArn", arn);
    }

    private void teardownWiring(TestContext ctx) {
        try {
            pipes().deletePipe(r -> r.name(ctx.getString("pipeName")));
        } catch (Exception ignored) {
            // teardown is best-effort
        }
        try {
            sqs().deleteQueue(r -> r.queueUrl(ctx.getString("pipeQueueUrl")));
        } catch (Exception ignored) {
            // teardown is best-effort
        }
        try {
            ddb().deleteTable(r -> r.tableName(ctx.getString("pipeTable")));
        } catch (Exception ignored) {
            // teardown is best-effort
        }
    }

    private void createPipe(TestContext ctx) throws Exception {
        var resp = pipes().createPipe(r -> r
                .name(ctx.getString("pipeName"))
                .source(ctx.getString("pipeSourceArn"))
                .target(ctx.getString("pipeTargetArn"))
                .roleArn(ROLE_ARN));
        Assertions.assertNotBlank(resp.arn(), "CreatePipe: arn is blank");
        Assertions.assertEquals(ctx.getString("pipeName"), resp.name(), "CreatePipe: name mismatch");
        Assertions.assertEquals(RequestedPipeState.RUNNING.toString(),
                resp.desiredStateAsString(), "CreatePipe: DesiredState");
    }

    private void describePipe(TestContext ctx) throws Exception {
        var resp = pipes().describePipe(r -> r.name(ctx.getString("pipeName")));
        Assertions.assertEquals(ctx.getString("pipeSourceArn"), resp.source(),
                "DescribePipe: Source did not round-trip");
        Assertions.assertEquals(ctx.getString("pipeTargetArn"), resp.target(),
                "DescribePipe: Target did not round-trip");
        Assertions.assertEquals(ROLE_ARN, resp.roleArn(), "DescribePipe: RoleArn did not round-trip");
    }

    private void listPipes(TestContext ctx) throws Exception {
        var resp = pipes().listPipes(r -> r.namePrefix("oc-" + ctx.runId()));
        boolean found = resp.pipes().stream()
                .anyMatch(p -> ctx.getString("pipeName").equals(p.name()));
        if (!found) {
            throw new AssertionError("ListPipes: " + ctx.getString("pipeName") + " not found");
        }
    }

    /**
     * A well-formed ARN naming a service the emulator cannot deliver to must
     * fail the call rather than being stored and silently ignored.
     */
    private void createPipeRejectsUnsupportedTarget(TestContext ctx) throws Exception {
        String name = ctx.getString("pipeName") + "-bad";
        try {
            pipes().createPipe(r -> r
                    .name(name)
                    .source(ctx.getString("pipeSourceArn"))
                    .target("arn:aws:logs:us-east-1:000000000000:log-group:/aws/pipes/compat")
                    .roleArn(ROLE_ARN));
        } catch (ValidationException expected) {
            return;
        } catch (Exception other) {
            if (other.toString().contains("ValidationException")) {
                return;
            }
            throw new AssertionError("CreatePipe: " + other + ", want ValidationException");
        }
        try {
            pipes().deletePipe(r -> r.name(name));
        } catch (Exception ignored) {
            // cleanup is best-effort
        }
        throw new AssertionError("CreatePipe accepted a target the emulator cannot deliver to");
    }

    /** Writes to the source table and waits for this group's own record. */
    private void pipeDeliversToTarget(TestContext ctx) throws Exception {
        String marker = "oc-" + ctx.runId() + "-delivery";

        // The pipe reaches RUNNING asynchronously.
        for (int attempt = 0; attempt < 20; attempt++) {
            var described = pipes().describePipe(r -> r.name(ctx.getString("pipeName")));
            if (described.currentState() == PipeState.RUNNING) {
                break;
            }
            Thread.sleep(100);
        }

        ddb().putItem(r -> r
                .tableName(ctx.getString("pipeTable"))
                .item(Map.of("id", AttributeValue.builder().s(marker).build())));

        String url = ctx.getString("pipeQueueUrl");
        for (int attempt = 0; attempt < 20; attempt++) {
            var resp = sqs().receiveMessage(r -> r
                    .queueUrl(url).maxNumberOfMessages(10).waitTimeSeconds(1));
            for (Message message : resp.messages()) {
                try {
                    sqs().deleteMessage(r -> r.queueUrl(url).receiptHandle(message.receiptHandle()));
                } catch (Exception ignored) {
                    // best-effort consume
                }
                if (message.body() == null || !message.body().contains(marker)) {
                    continue;
                }
                if (!message.body().contains("\"aws:dynamodb\"")) {
                    throw new AssertionError(
                            "PipeDeliversToTarget: delivered record is not a DynamoDB record: " + message.body());
                }
                return;
            }
            Thread.sleep(100);
        }
        throw new AssertionError("PipeDeliversToTarget: no delivery reached the target queue");
    }

    private void updatePipe(TestContext ctx) throws Exception {
        var resp = pipes().updatePipe(r -> r
                .name(ctx.getString("pipeName"))
                .roleArn(ROLE_ARN)
                .desiredState(RequestedPipeState.STOPPED)
                .description("compat"));
        Assertions.assertEquals(RequestedPipeState.STOPPED.toString(),
                resp.desiredStateAsString(), "UpdatePipe: DesiredState");
    }

    private void deletePipe(TestContext ctx) throws Exception {
        var resp = pipes().deletePipe(r -> r.name(ctx.getString("pipeName")));
        Assertions.assertEquals(PipeState.DELETING.toString(),
                resp.currentStateAsString(), "DeletePipe: CurrentState");
    }
}
