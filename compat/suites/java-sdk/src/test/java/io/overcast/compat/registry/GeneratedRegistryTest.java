package io.overcast.compat.registry;

import io.overcast.compat.harness.TestCase;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import io.overcast.compat.harness.TestGroup;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Pins how the loader reads {@code registry.generated.json}.
 *
 * <p>Nothing here asserts what the checked-in generated registry contains —
 * only that an empty one behaves exactly as an absent one. The file is
 * generator output and is about to stop being empty; the invariant is what
 * must hold, not the moment.
 */
class GeneratedRegistryTest {

    private static final TestFn NOOP = ctx -> {};

    private static final String HAND_WRITTEN = """
            {
              "version": 1,
              "groups": [
                {"service": "s3", "name": "s3-crud", "tests": [{"name": "CreateBucket"}]}
              ]
            }
            """;

    /** Writes registry.json into {@code dir} and returns its path. */
    private static Path handWritten(Path dir) throws IOException {
        return Files.writeString(dir.resolve("registry.json"), HAND_WRITTEN);
    }

    private static Path generated(Path dir, String json) throws IOException {
        return Files.writeString(dir.resolve(Registry.GENERATED_REGISTRY_FILENAME), json);
    }

    private static List<TestGroup> build(Path registry, String suite) throws IOException {
        return build(registry, suite, null);
    }

    private static List<TestGroup> build(Path registry, String suite, ScenarioBackend backend)
            throws IOException {
        return Registry.buildGroups(suite, Registry.load(registry.toFile()),
                Map.of(), Map.of(), Map.of(), Set.of(), backend);
    }

    private static List<String> groupNames(List<TestGroup> groups) {
        return groups.stream().map(TestGroup::name).toList();
    }

    private static TestCase only(List<TestGroup> groups, String group) {
        for (TestGroup g : groups) {
            if (g.name().equals(group)) {
                assertEquals(1, g.tests().size(), "group " + group + " test count");
                return g.tests().get(0);
            }
        }
        throw new AssertionError("no group " + group + " in " + groupNames(groups));
    }

    // ── Absence and emptiness are the same thing ──────────────────────────────

    @Test
    void missingGeneratedFileIsANoOp(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        assertEquals(List.of("s3-crud"), groupNames(build(registry, "java-sdk")));
    }

    @Test
    void emptyGeneratedFileIsANoOp(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        generated(dir, "{\"version\":1,\"groups\":[]}");
        assertEquals(List.of("s3-crud"), groupNames(build(registry, "java-sdk")));
    }

    // ── Concatenation and scoping ─────────────────────────────────────────────

    @Test
    void generatedGroupsAreConcatenatedAfterTheHandWrittenOnes(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        generated(dir, """
                {
                  "version": 1,
                  "groups": [
                    {"service": "sqs", "name": "gen-sqs-a", "generated": true,
                     "state": "candidate", "scenario": "scenarios/sqs-a.json",
                     "suites": ["java-sdk"], "tests": [{"name": "SendMessage"}]},
                    {"service": "sqs", "name": "gen-sqs-b", "generated": true,
                     "state": "gated", "scenario": "scenarios/sqs-b.json",
                     "suites": ["java-sdk"], "tests": [{"name": "ReceiveMessage"}]}
                  ]
                }
                """);

        // File order, hand-written first — the loader sorts neither half.
        assertEquals(List.of("s3-crud", "gen-sqs-a", "gen-sqs-b"),
                groupNames(build(registry, "java-sdk")));
    }

    @Test
    void generatedGroupScopedToAnotherSuiteIsNotLoaded(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        generated(dir, """
                {
                  "version": 1,
                  "groups": [
                    {"service": "sqs", "name": "gen-sqs", "generated": true,
                     "state": "candidate", "scenario": "scenarios/sqs.json",
                     "suites": ["python-sdk"], "tests": [{"name": "SendMessage"}]}
                  ]
                }
                """);

        // Out of scope, not in debt: no tests, no skips, no results.
        assertEquals(List.of("s3-crud"), groupNames(build(registry, "java-sdk")));
    }

    // ── The interim rule: loud, never a skip ──────────────────────────────────

    @Test
    void generatedGroupWithNoBackendFails(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        generated(dir, inScopeGroup());

        TestCase test = only(build(registry, "java-sdk"), "gen-sqs");

        assertNull(test.skip(), "a missing backend must not report as a skip");
        assertTrue(test.depends().isEmpty(),
                "depends would cascade-skip the rest of the group; every test must fail");

        AssertionError e = assertThrows(AssertionError.class,
                () -> test.fn().run(new TestContext("http://127.0.0.1:1", "us-east-1", "run")));
        assertEquals("generated group \"gen-sqs\" is scoped to java-sdk"
                + " but java-sdk has no scenario backend", e.getMessage());
    }

    /** The sentinel skip stays exactly as it was for hand-written groups. */
    @Test
    void handWrittenGroupWithNoImplStillSkips(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        generated(dir, inScopeGroup());

        assertEquals("not yet implemented in java-sdk test suite",
                only(build(registry, "java-sdk"), "s3-crud").skip());
    }

    @Test
    void scenarioBackendResolvesAGeneratedTest(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        generated(dir, inScopeGroup());

        ScenarioBackend backend = (group, test) ->
                "scenarios/sqs.json".equals(group.scenario()) ? NOOP : null;

        TestCase test = only(build(registry, "java-sdk", backend), "gen-sqs");
        assertNull(test.skip());
        assertEquals(NOOP, test.fn(), "the backend's implementation should be bound");
    }

    // ── A bad generated file is an error, never a silent drop ─────────────────

    @Test
    void collidingGroupNameIsALoadError(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        generated(dir, """
                {
                  "version": 1,
                  "groups": [
                    {"service": "s3", "name": "s3-crud", "generated": true,
                     "state": "candidate", "suites": ["java-sdk"],
                     "tests": [{"name": "CreateBucket"}]}
                  ]
                }
                """);

        IOException e = assertThrows(IOException.class, () -> Registry.load(registry.toFile()));
        assertTrue(e.getMessage().contains("s3-crud"), e.getMessage());
    }

    @Test
    void unparsableGeneratedFileIsALoadError(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        generated(dir, "{\"version\": 1, \"groups\": [");
        assertThrows(IOException.class, () -> Registry.load(registry.toFile()));
    }

    @Test
    void wrongVersionIsALoadError(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        generated(dir, "{\"version\":2,\"groups\":[]}");

        IOException e = assertThrows(IOException.class, () -> Registry.load(registry.toFile()));
        assertTrue(e.getMessage().contains("version"), e.getMessage());
    }

    @Test
    void groupMissingGeneratedStateOrSuitesIsALoadError(@TempDir Path dir) throws Exception {
        Path registry = handWritten(dir);
        for (String group : List.of(
                "{\"service\":\"sqs\",\"name\":\"gen-sqs\",\"state\":\"candidate\","
                        + "\"suites\":[\"java-sdk\"],\"tests\":[{\"name\":\"SendMessage\"}]}",
                "{\"service\":\"sqs\",\"name\":\"gen-sqs\",\"generated\":true,"
                        + "\"suites\":[\"java-sdk\"],\"tests\":[{\"name\":\"SendMessage\"}]}",
                "{\"service\":\"sqs\",\"name\":\"gen-sqs\",\"generated\":true,"
                        + "\"state\":\"candidate\",\"tests\":[{\"name\":\"SendMessage\"}]}")) {
            generated(dir, "{\"version\":1,\"groups\":[" + group + "]}");
            IOException e = assertThrows(IOException.class,
                    () -> Registry.load(registry.toFile()), group);
            assertTrue(e.getMessage().contains("gen-sqs"), e.getMessage());
        }
    }

    /** One generated group this suite is named in, with one test. */
    private static String inScopeGroup() {
        return """
                {
                  "version": 1,
                  "groups": [
                    {"service": "sqs", "name": "gen-sqs", "generated": true,
                     "state": "candidate", "scenario": "scenarios/sqs.json",
                     "suites": ["java-sdk"], "tests": [{"name": "SendMessage"}]}
                  ]
                }
                """;
    }
}
