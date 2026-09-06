package io.overcast.compat.scenario;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.DynamicNode;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestFactory;
import software.amazon.awssdk.awscore.exception.AwsErrorDetails;
import software.amazon.awssdk.awscore.exception.AwsServiceException;
import software.amazon.awssdk.http.SdkHttpResponse;
import software.amazon.awssdk.services.apigateway.model.NotFoundException;
import software.amazon.awssdk.services.dynamodb.model.ResourceNotFoundException;
import software.amazon.awssdk.services.organizations.model.PolicyNotFoundException;
import software.amazon.awssdk.services.sqs.model.QueueDoesNotExistException;

import java.io.File;
import java.io.IOException;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.Supplier;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

/**
 * The shared error-matching conformance fixtures,
 * {@code compat/model/testdata/errors}.
 *
 * <p>Every backend reads the same documents and must agree about which clauses
 * they satisfy. Each suite writes this test once, against its own matcher, so a
 * rule only one backend implements fails somewhere rather than being discovered
 * when a generated group disagrees with itself across suites
 * ({@code compat/model/README.md} § Errors).
 *
 * <p>What this suite observes is the same as the two SDK interpreters': an
 * exception class, the code the unmarshaller resolved, and the response header.
 * What it cannot see is the AWS CLI's stderr banner, so those fixtures are
 * skipped by name and with a reason — a silently ignored fixture would look
 * exactly like a passing one.
 */
class ErrorFixturesTest {

    private static final ObjectMapper MAPPER = new ObjectMapper()
            .enable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES);

    /** The whole carrier vocabulary. A fixture naming anything else is a typo. */
    private static final Set<String> KNOWN_CARRIERS =
            Set.of("exceptionName", "bodyType", "bodyCode", "queryErrorHeader", "cliBanner");

    /**
     * What this suite can see. {@code bodyType} and {@code bodyCode} are
     * observed indirectly but faithfully: the Java SDK parses the body away
     * before the caller sees the error, and what survives is
     * {@code awsErrorDetails().errorCode()}, which is the code the protocol
     * unmarshaller read out of {@code __type} or the {@code code} member.
     */
    private static final Set<String> OBSERVED_CARRIERS =
            Set.of("exceptionName", "bodyType", "bodyCode", "queryErrorHeader");

    /**
     * Maps a fixture's {@code exceptionName} to the real SDK exception class of
     * that name. These are genuine generated classes rather than stand-ins, so
     * the fixture is answered by the same values a run against Overcast
     * produces.
     *
     * <p>A fixture naming an exception with no entry fails rather than skipping:
     * the list is short and adding to it is a line, while a quiet skip would hide
     * a carrier this suite claims to observe.
     */
    private static final Map<String, Supplier<AwsServiceException.Builder>> MODELED_ERRORS = Map.of(
            "PolicyNotFoundException", PolicyNotFoundException::builder,
            "ResourceNotFoundException", ResourceNotFoundException::builder,
            "NotFoundException", NotFoundException::builder);

    @JsonIgnoreProperties(ignoreUnknown = false)
    record Fixture(String id, String title, String why, List<String> carriers, Wire wire, List<Case> expect) {}

    @JsonIgnoreProperties(ignoreUnknown = false)
    record Wire(Integer status, String exceptionName, Map<String, String> headers,
                Map<String, Object> body, String stderr) {}

    @JsonIgnoreProperties(ignoreUnknown = false)
    record Case(String name, Spec error, boolean matches, String via) {}

    @JsonIgnoreProperties(ignoreUnknown = false)
    record Spec(String shape, String code) {}

    @TestFactory
    List<DynamicNode> sharedErrorFixtures() throws IOException {
        File dir = fixtureDir();
        if (dir == null) {
            // The Docker build copies this suite's sources without the model
            // directory, so the shared corpus is not reachable there. Reported
            // as a skip rather than a pass: a conformance set that quietly
            // asserted nothing would look exactly like one that held.
            return List.of(DynamicTest.dynamicTest("shared error fixtures", () -> Assumptions.abort(
                    "no compat/model/testdata/errors above the working directory;"
                            + " run `mvn -B test` from compat/suites/java-sdk to check the conformance set")));
        }
        File[] files = dir.listFiles((d, name) -> name.endsWith(".json"));
        assertNotNull(files, "no fixtures in " + dir);
        assertTrue(files.length > 0,
                "no fixtures in " + dir + ": the shared conformance set may not be skipped by deleting it");
        Arrays.sort(files);

        List<DynamicNode> nodes = new ArrayList<>();
        int checked = 0;
        for (File file : files) {
            Fixture f = MAPPER.readValue(file, Fixture.class);
            for (String carrier : f.carriers()) {
                assertTrue(KNOWN_CARRIERS.contains(carrier),
                        "unknown carrier \"" + carrier + "\" in " + f.id()
                                + "; the vocabulary is fixed by compat/model/README.md § Errors");
            }
            if (!observesAny(f.carriers())) {
                nodes.add(DynamicTest.dynamicTest(f.id() + " (skipped)", () -> {
                    // The java-sdk suite reads none of this fixture's surfaces:
                    // the AWS CLI's stderr banner never reaches an SDK caller.
                    assertEquals(List.of("cliBanner"), f.carriers(),
                            "the only unobservable carrier is the CLI banner");
                }));
                continue;
            }
            Throwable observed = asSdkError(f);
            for (Case c : f.expect()) {
                if (c.matches() && !OBSERVED_CARRIERS.contains(c.via())) {
                    continue; // matched through a carrier this suite does not observe
                }
                checked++;
                nodes.add(DynamicTest.dynamicTest(f.id() + " — " + c.name(), () -> {
                    ErrorSpec want = ErrorSpec.of(c.error().shape(), c.error().code());
                    assertEquals(c.matches(), Errors.matches(observed, want),
                            "codes observed: " + Errors.codes(observed));
                }));
            }
        }
        assertTrue(checked > 0, "every fixture was skipped: this suite is asserting nothing about error matching");
        return nodes;
    }

    /**
     * Renders a fixture the way this suite would have observed it: the SDK's
     * modeled exception where the wire names one, else the generic service
     * exception its unmarshaller mints, carrying the response the header
     * survives on.
     */
    private static Throwable asSdkError(Fixture f) {
        if (f.wire().stderr() != null) {
            // No HTTP exchange at all: the SDK failed before the wire. Nothing
            // states a code, which is what cli-no-parseable-code pins.
            return new RuntimeException(f.wire().stderr());
        }
        SdkHttpResponse.Builder http = SdkHttpResponse.builder()
                .statusCode(f.wire().status() == null ? 400 : f.wire().status());
        if (f.wire().headers() != null) {
            f.wire().headers().forEach(http::putHeader);
        }
        AwsErrorDetails details = AwsErrorDetails.builder()
                // The body's own code member, unsanitised: splitting a Smithy id
                // at "#" is the matcher's job, and pre-splitting it here would
                // test the fixture rather than the matcher.
                .errorCode(bodyCode(f.wire().body()))
                .errorMessage(bodyMessage(f.wire().body()))
                .sdkHttpResponse(http.build())
                .build();

        AwsServiceException.Builder builder;
        if (f.wire().exceptionName() != null) {
            Supplier<AwsServiceException.Builder> modeled = MODELED_ERRORS.get(f.wire().exceptionName());
            if (modeled == null) {
                fail("no SDK exception class for \"" + f.wire().exceptionName()
                        + "\"; add one to MODELED_ERRORS so the exceptionName carrier is really observed");
                return null;
            }
            builder = modeled.get();
        } else {
            builder = AwsServiceException.builder();
        }
        return builder.awsErrorDetails(details)
                .message(details.errorMessage())
                .statusCode(details.sdkHttpResponse().statusCode())
                .build();
    }

    private static String bodyCode(Map<String, Object> body) {
        if (body == null) {
            return null;
        }
        for (String key : List.of("__type", "Code", "code")) {
            if (body.get(key) instanceof String s) {
                return s;
            }
        }
        return null;
    }

    private static String bodyMessage(Map<String, Object> body) {
        return body != null && body.get("message") instanceof String s ? s : "";
    }

    /**
     * Whether this suite can see any surface the fixture states the code on. A
     * fixture that states none — {@code carriers: []} — is observed by everyone:
     * there is nothing to miss, and its expectations are all negative.
     */
    private static boolean observesAny(List<String> carriers) {
        if (carriers.isEmpty()) {
            return true;
        }
        return carriers.stream().anyMatch(OBSERVED_CARRIERS::contains);
    }

    /**
     * Pins the assumption the {@code exceptionName} surface rests on, and the
     * one place the AWS SDK for Java v2 diverges from it.
     *
     * <p>The SDK names a modeled exception class after the shape <b>plus
     * "Exception"</b>, so this surface states the shape's own spelling only
     * where the shape already ends that way. Stripping the suffix to recover the
     * other case is deliberately not done: it would make
     * {@code ResourceNotFoundException} also state {@code ResourceNotFound},
     * which {@code near-miss-longer-code.json} pins as a non-match. The modeled
     * shape reaches the matcher through {@code errorCode()} instead.
     */
    @Test
    void aModeledExceptionStatesItsClassNameAndNothingElse() {
        assertEquals("PolicyNotFoundException",
                Errors.modeledExceptionName(PolicyNotFoundException.builder().message("x").build()));
        assertEquals("QueueDoesNotExistException",
                Errors.modeledExceptionName(QueueDoesNotExistException.builder().message("x").build()),
                "the Java class for the shape QueueDoesNotExist carries an Exception suffix");
        assertFalse(Errors.codes(QueueDoesNotExistException.builder().message("x").build())
                        .contains("QueueDoesNotExist"),
                "the suffix must not be stripped: that would reinstate the near miss the fixtures exclude");
    }

    /**
     * The SDK's own catch-all is named after what it is rather than after a
     * modeled shape, and a clause naming it must never be satisfied by one.
     */
    @Test
    void theCatchAllExceptionIsNotAnExceptionName() {
        AwsServiceException generic = AwsServiceException.builder()
                .awsErrorDetails(AwsErrorDetails.builder().errorCode("SomethingElse").build())
                .message("x").build();
        assertEquals(null, Errors.modeledExceptionName(generic));
        assertFalse(Errors.matches(generic, ErrorSpec.of("AwsServiceException", "AwsServiceException")));
        assertTrue(Errors.matches(generic, ErrorSpec.of("SomethingElse", "SomethingElse")));
    }

    /**
     * Walks up from the working directory to the repository root, or returns
     * null when there is none above us.
     */
    private static File fixtureDir() {
        File dir = new File("").getAbsoluteFile();
        for (int i = 0; i < 8 && dir != null; i++) {
            File candidate = new File(dir, "compat/model/testdata/errors");
            if (candidate.isDirectory()) {
                return candidate;
            }
            dir = dir.getParentFile();
        }
        return null;
    }
}
