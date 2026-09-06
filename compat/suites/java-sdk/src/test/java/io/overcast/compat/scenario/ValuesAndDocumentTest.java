package io.overcast.compat.scenario;

import org.junit.jupiter.api.Test;
import software.amazon.awssdk.services.sqs.model.CreateQueueRequest;
import software.amazon.awssdk.services.sqs.model.GetQueueAttributesResponse;
import software.amazon.awssdk.services.sqs.model.ListQueuesResponse;
import software.amazon.awssdk.services.sqs.model.Message;
import software.amazon.awssdk.services.sqs.model.QueueAttributeName;
import software.amazon.awssdk.services.sqs.model.ReceiveMessageResponse;
import software.amazon.awssdk.services.sqs.model.SendMessageBatchRequestEntry;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * The value grammar, the document conversion and the path resolver — the three
 * pieces every assertion is built on.
 */
class ValuesAndDocumentTest {

    private static Binder binder(String... contextPairs) {
        ContextBag bag = new ContextBag();
        for (int i = 0; i < contextPairs.length; i += 2) {
            bag.set(contextPairs[i], contextPairs[i + 1]);
        }
        return new Binder("oc-test", "sqs-gen-queue", bag);
    }

    // ── Values ───────────────────────────────────────────────────────────────

    @Test
    void nameIsRunIdGroupSuffix() {
        assertEquals("oc-test-sqs-gen-queue-q", binder().string("QueueName", Values.name("q")));
    }

    @Test
    void concatAndIndex() {
        Binder b = binder("dlq.arn", "arn:aws:sqs:x");
        assertEquals("{\"a\":\"arn:aws:sqs:x\"}",
                b.string("Attributes", Values.concat("{\"a\":\"", Values.ref("dlq.arn"), "\"}")));
        assertEquals("second",
                b.string("M", Values.index(Values.list("first", "second"), 1)));

        // Out of range and a non-list are errors for the step, not a null.
        assertThrows(ValueException.class,
                () -> b.string("M", Values.index(Values.list("only"), 3)));
        assertThrows(ValueException.class,
                () -> b.string("M", Values.index("not a list", 0)));
        // A concat part that is not a string is refused rather than coerced.
        assertThrows(ValueException.class, () -> b.string("M", Values.concat(1)));
    }

    @Test
    void anUnresolvableRefNamesThePath() {
        ValueException e = assertThrows(ValueException.class,
                () -> binder().string("QueueUrl", Values.ref("queue.url")));
        assertEquals("queue.url", e.contextPath());
        assertTrue(e.getMessage().contains("queue.url"), e.getMessage());
    }

    @Test
    void litIsNeverInterpreted() {
        Object v = binder().eval(Values.lit(Values.map("$ref", "not an expression")));
        assertEquals("{\"$ref\":\"not an expression\"}", Json.canonical(v));
    }

    /**
     * Every accessor the emitter can write, over the whole scalar set. A
     * mismatch throws rather than coercing: "30" is not 30 anywhere else in the
     * IR.
     */
    @Test
    void binderConvertsEveryScalarTheEmitterCanSpell() {
        Binder b = binder();
        assertEquals("s", b.string("M", "s"));
        assertEquals(Boolean.TRUE, b.bool("M", true));
        assertEquals(Byte.valueOf((byte) 7), b.byteValue("M", 7));
        assertEquals(Short.valueOf((short) 7), b.shortValue("M", 7));
        assertEquals(Integer.valueOf(30), b.integer("M", 30));
        assertEquals(Long.valueOf(30L), b.longValue("M", 30));
        assertEquals(Float.valueOf(1.5f), b.floatValue("M", 1.5));
        assertEquals(Double.valueOf(1.5), b.doubleValue("M", 1.5));
        // A boxed zero is a value like any other — the Java SDK sends it.
        assertEquals(Integer.valueOf(0), b.integer("M", 0));

        assertThrows(ValueException.class, () -> b.integer("M", "30"));
        assertThrows(ValueException.class, () -> b.string("M", 30));
        assertThrows(ValueException.class, () -> b.integer("M", 1.5));
        assertThrows(ValueException.class, () -> b.byteValue("M", 300));
        assertThrows(ValueException.class, () -> b.bool("M", "true"));
    }

    // ── Documents ────────────────────────────────────────────────────────────

    /**
     * The document's keys are the modeled member names, taken from the SDK's own
     * {@code sdkFields()} rather than from a Java accessor name this package
     * would have to un-mangle.
     */
    @Test
    void documentKeysAreModeledMemberNames() {
        Object doc = Doc.of(ReceiveMessageResponse.builder()
                .messages(Message.builder().messageId("m-1").body("hello").build())
                .build());
        assertEquals("{\"Messages\":[{\"Body\":\"hello\",\"MessageId\":\"m-1\"}]}", Json.canonical(doc));
    }

    /** An unset member is absence, not null — and so is an omitted list. */
    @Test
    void unsetMembersAreAbsent() {
        Object doc = Doc.of(ListQueuesResponse.builder().build());
        assertEquals("{}", Json.canonical(doc));
        assertFalse(Paths.resolve(doc, "$.QueueUrls").ok());
        assertFalse(Paths.resolve(doc, "$.NextToken").ok());
    }

    /** An enum-keyed map reads back under the wire spelling of its keys. */
    @Test
    void enumKeyedMapsUseTheWireSpelling() {
        Object doc = Doc.of(GetQueueAttributesResponse.builder()
                .attributes(Map.of(QueueAttributeName.VISIBILITY_TIMEOUT, "30"))
                .build());
        assertEquals("{\"Attributes\":{\"VisibilityTimeout\":\"30\"}}", Json.canonical(doc));
    }

    /**
     * Every number becomes a double on the way in and renders without a
     * fractional part on the way out, so an {@code equals} on an integer member
     * compares and prints the same here as in the three interpreters.
     */
    @Test
    void numbersAreJsonNumbers() {
        Object doc = Doc.of(SendMessageBatchRequestEntry.builder()
                .id("m-1").messageBody("x").delaySeconds(0).build());
        assertEquals("{\"DelaySeconds\":0,\"Id\":\"m-1\",\"MessageBody\":\"x\"}", Json.canonical(doc));
        assertTrue(Json.equal(Paths.resolve(doc, "$.DelaySeconds").value(), 0.0));
        assertFalse(Json.equal(Paths.resolve(doc, "$.DelaySeconds").value(), "0"));
    }

    /** A request is a document too — that is failure-message field 3. */
    @Test
    void aRequestRendersAsTheParamsThatWereSent() {
        Object doc = Doc.of(CreateQueueRequest.builder()
                .queueName("oc-test-q")
                .attributes(Map.of(QueueAttributeName.VISIBILITY_TIMEOUT, "30"))
                .build());
        assertEquals("{\"Attributes\":{\"VisibilityTimeout\":\"30\"},\"QueueName\":\"oc-test-q\"}",
                Json.canonical(doc));
    }

    // ── Paths ────────────────────────────────────────────────────────────────

    @Test
    void pathsResolveMembersAndIndices() {
        Object doc = Doc.of(ReceiveMessageResponse.builder()
                .messages(Message.builder().messageId("m-1").receiptHandle("r-1").build())
                .build());
        assertEquals("r-1", Paths.resolve(doc, "$.Messages[0].ReceiptHandle").value());
        assertFalse(Paths.resolve(doc, "$.Messages[1]").ok());
        assertFalse(Paths.resolve(doc, "$.Messages[0].Body").ok());
        assertEquals(doc, Paths.resolve(doc, "$").value());
    }

    @Test
    void aMalformedPathIsAnErrorNotAnAbsence() {
        Object doc = Doc.of(ListQueuesResponse.builder().build());
        for (String path : List.of("QueueUrls", "$.", "$.QueueUrls[", "$.QueueUrls[x]", "$/QueueUrls")) {
            assertThrows(ValueException.class, () -> Paths.resolve(doc, path), path);
        }
    }

    // ── Canonical JSON ───────────────────────────────────────────────────────

    @Test
    void canonicalJsonSortsKeysAndKeepsTypesApart() {
        assertEquals("{\"a\":1,\"b\":true,\"z\":null}",
                Json.canonical(Values.map("z", null, "b", true, "a", 1)));
        assertFalse(Json.equal(1, "1"));
        assertFalse(Json.equal(true, 1));
        assertTrue(Json.equal(List.of(1, 2), List.of(1.0, 2.0)));
        assertEquals("\"a\\\"b\\nc\"", Json.quote("a\"b\nc"));
        // No HTML escaping — a policy document is full of angle brackets and
        // ampersands, and the interpreters print them raw.
        assertEquals("\"<a&b>\"", Json.quote("<a&b>"));
        assertNull(Paths.resolve(Values.map(), "$.nope").value());
    }
}
