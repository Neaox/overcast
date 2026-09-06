package io.overcast.compat.scenario;

import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import software.amazon.awssdk.auth.credentials.AwsBasicCredentials;
import software.amazon.awssdk.auth.credentials.StaticCredentialsProvider;
import software.amazon.awssdk.http.urlconnection.UrlConnectionHttpClient;
import software.amazon.awssdk.regions.Region;
import software.amazon.awssdk.services.organizations.model.PolicyType;
import software.amazon.awssdk.services.sqs.SqsClient;
import software.amazon.awssdk.services.sqs.model.QueueAttributeName;

import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * The AWS SDK for Java v2 facts {@code cmd/compatgen}'s Java emitter derives
 * from the pinned model rather than from the SDK, measured on the wire.
 *
 * <p>{@code docs/plans/compat-coverage-modelgen.md} §3.2 lets a typed backend
 * skip the SDK lookup the Go emitter needs "wherever the SDK's nullability is
 * not derivable from the model", and says it is not derivable for Java. This
 * test is what makes that a measurement instead of a claim, and it is the reason
 * {@code emit_java_spell.go} carries no counterpart to the Go emitter's
 * zero-value refusal. If a future SDK ever changed either answer, the emitter
 * would start writing requests that quietly omit a member — a silent wrong
 * result in every generated group — and this fails first.
 *
 * <p>It talks to a loopback HTTP server from the JDK rather than to Overcast: the
 * point is what the SDK <em>serializes</em>, so the emulator would only add a
 * dependency and a port.
 */
class JavaSdkWireFactsTest {

    private HttpServer server;
    private SqsClient sqs;
    private final AtomicReference<String> body = new AtomicReference<>("");

    @BeforeEach
    void start() throws Exception {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/", exchange -> {
            body.set(new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
            byte[] out = "{}".getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().add("Content-Type", "application/x-amz-json-1.0");
            exchange.sendResponseHeaders(200, out.length);
            try (OutputStream os = exchange.getResponseBody()) {
                os.write(out);
            }
        });
        server.start();
        sqs = SqsClient.builder()
                .endpointOverride(URI.create("http://127.0.0.1:" + server.getAddress().getPort()))
                .region(Region.US_EAST_1)
                .credentialsProvider(StaticCredentialsProvider.create(AwsBasicCredentials.create("t", "t")))
                .httpClient(UrlConnectionHttpClient.create())
                .build();
    }

    @AfterEach
    void stop() {
        if (sqs != null) {
            sqs.close();
        }
        if (server != null) {
            server.stop(0);
        }
    }

    /**
     * A builder setter takes the value whatever the member's optionality, and a
     * boxed zero really is serialized — which is why the Java emitter needs no
     * SDK lookup and refuses no zero.
     *
     * <p>The Go emitter has to refuse the same scenario: smithy-go gives
     * {@code VisibilityTimeout} a plain {@code int32} and serializes it only
     * when it differs from the zero value, so a scenario asking for 0 there
     * silently asks for the queue's own timeout ({@code compat/model/README.md}
     * § Values). The two backends genuinely differ, and this is the measurement
     * that says so.
     */
    @Test
    void aBoxedZeroIsSent() {
        sqs.receiveMessage(r -> r.queueUrl("http://q/x")
                .visibilityTimeout(0)
                .waitTimeSeconds(0)
                .maxNumberOfMessages(1));
        assertTrue(body.get().contains("\"VisibilityTimeout\":0"), body.get());
        assertTrue(body.get().contains("\"WaitTimeSeconds\":0"), body.get());

        // And an unset member is still omitted, which is what makes null the
        // SDK's spelling of "unset" and 0 an ordinary value.
        body.set("");
        sqs.receiveMessage(r -> r.queueUrl("http://q/x").maxNumberOfMessages(1));
        assertTrue(body.get().contains("\"MaxNumberOfMessages\":1"), body.get());
        assertEquals(-1, body.get().indexOf("VisibilityTimeout"), body.get());
    }

    /**
     * An enum member accepts {@code fromValue} of the wire value the model
     * carries, and the wire value is what goes out — for a plain enum member, an
     * enum-keyed map and a list of enums alike. Those are the three shapes the
     * emitter spells through {@code javaSpeller.enumOf}.
     */
    @Test
    void enumsAreSentAsTheirWireValues() {
        sqs.getQueueAttributes(r -> r.queueUrl("http://q/x")
                .attributeNames(List.of(QueueAttributeName.fromValue("QueueArn"))));
        assertTrue(body.get().contains("\"AttributeNames\":[\"QueueArn\"]"), body.get());

        body.set("");
        sqs.setQueueAttributes(r -> r.queueUrl("http://q/x")
                .attributes(Map.of(QueueAttributeName.fromValue("VisibilityTimeout"), "30")));
        assertTrue(body.get().contains("\"VisibilityTimeout\":\"30\""), body.get());

        // A scalar enum member, off the wire: PolicyType is what
        // organizations-gen-policy/CreatePolicy sends.
        assertEquals("SERVICE_CONTROL_POLICY",
                PolicyType.fromValue("SERVICE_CONTROL_POLICY").toString());
    }

    /**
     * The hazard {@code fromValue} carries, stated rather than discovered: a
     * value the <em>pinned</em> SDK does not know becomes
     * {@code UNKNOWN_TO_SDK_VERSION}, which serializes as the literal string
     * {@code "null"}. That is loud — the request goes out with a value no
     * service accepts and the generated test fails — but it is not a compile
     * error, so the SDK pin in {@code pom.xml} has to stay at least as new as
     * the shape snapshot the emitter reads.
     */
    @Test
    void anEnumValueThePinnedSdkDoesNotKnowHasNoWireValue() {
        assertEquals(QueueAttributeName.UNKNOWN_TO_SDK_VERSION,
                QueueAttributeName.fromValue("NoSuchAttributeInThisSdk"));

        sqs.getQueueAttributes(r -> r.queueUrl("http://q/x")
                .attributeNames(List.of(QueueAttributeName.fromValue("NoSuchAttributeInThisSdk"))));
        assertTrue(body.get().contains("\"AttributeNames\":[\"null\"]"),
                "an unknown enum value reaches the wire as \"null\", not as itself: " + body.get());
    }
}
