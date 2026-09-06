package io.overcast.compat;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.groups.ScenariosGen;
import io.overcast.compat.groups.ServiceGroup;
import io.overcast.compat.harness.TestFn;
import io.overcast.compat.harness.TestGroup;
import io.overcast.compat.registry.Registry;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TreeSet;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * The generated groups resolve against the real
 * {@code registry.generated.json}, through the {@code ScenarioBackend} hook.
 *
 * <p>This is the check that catches the failure the loader's interim rule exists
 * to make loud: a group the generator scoped to {@code java-sdk} that this suite
 * cannot execute reports as a hard failure naming the group, not as a skip. It
 * runs on {@code mvn test} — and so inside the Docker build every compat run
 * performs — without starting a run or touching an emulator.
 */
class GeneratedGroupsRegistrationTest {

    private static final String SUITE = "java-sdk";

    private static List<ServiceGroup> generated() {
        return ScenariosGen.all(new AwsClients("http://127.0.0.1:1", "us-east-1"));
    }

    /**
     * Every generated group the registry scopes to this suite resolves to a real
     * implementation, and nothing left over is registered for a group the
     * registry does not have.
     */
    @Test
    void everyGeneratedTestScopedToThisSuiteResolves() throws Exception {
        List<ServiceGroup> generated = generated();
        Map<String, TestFn> impls = Main.generatedImpls(generated);

        Map<String, TestFn> setups = new LinkedHashMap<>();
        Map<String, TestFn> teardowns = new LinkedHashMap<>();
        for (ServiceGroup sg : generated) {
            setups.putAll(sg.setups());
            teardowns.putAll(sg.teardowns());
        }

        List<TestGroup> groups = Registry.buildGroups(SUITE, Map.of(), setups, teardowns, Set.of(),
                (group, test) -> impls.get(Registry.qualifiedKey(group.name(), test.name())));

        Set<String> resolved = new TreeSet<>();
        List<String> unresolved = new ArrayList<>();
        for (TestGroup g : groups) {
            // A group this emitter registered anything for is one of ours; a
            // hand-written group with no impl here is MainRegistrationTest's.
            //
            // Deliberately not a name test. "-gen-" names a group generated
            // from a recipe, but an authored scenario ported from a
            // hand-written group keeps that group's own name (and, while it
            // soaks, that name plus "-shadow"), so a substring check would skip
            // exactly the groups a migration is most at risk of dropping. See
            // compat/model/README.md's "Authored scenarios" section.
            boolean mine = g.tests().stream()
                    .anyMatch(tc -> impls.containsKey(Registry.qualifiedKey(g.name(), tc.name())));
            if (!mine) {
                continue;
            }
            assertNotNull(g.setup(), "generated group " + g.name() + " has no setup hook");
            assertNotNull(g.teardown(), "generated group " + g.name() + " has no teardown hook");
            for (var tc : g.tests()) {
                String key = Registry.qualifiedKey(g.name(), tc.name());
                if (impls.containsKey(key)) {
                    resolved.add(key);
                } else {
                    unresolved.add(key);
                }
                assertNull(tc.skip(), "generated test " + key + " was not resolved by the backend");
            }
        }

        assertTrue(unresolved.isEmpty(),
                "generated tests with no implementation: " + unresolved);
        assertTrue(resolved.size() >= impls.size(),
                "the emitter registered implementations the registry does not scope to this suite: "
                        + new TreeSet<>(impls.keySet()).stream().filter(k -> !resolved.contains(k)).toList());
    }

    /** Every generated impl key is group-qualified, exactly as a hand-written one must be. */
    @Test
    void generatedImplKeysAreAllQualified() {
        TreeSet<String> bare = new TreeSet<>();
        for (String key : Main.generatedImpls(generated()).keySet()) {
            if (!key.contains(Registry.KEY_SEPARATOR)) {
                bare.add(key);
            }
        }
        assertTrue(bare.isEmpty(), "bare (unqualified) generated impl keys: " + bare);
    }

    /**
     * A generated class registers each key once, and no two of them collide.
     * {@code Registry.validateImpls} never sees these — the backend resolves
     * them by group and test — so the merge is the only guard they get.
     */
    @Test
    void generatedImplsHaveNoDuplicateKeys() {
        Main.generatedImpls(generated()); // throws IllegalStateException naming every duplicate
    }

    /**
     * The suite's own registrations and the generated ones stay disjoint: a
     * generated group's name always carries {@code -gen-}, which no hand-written
     * group does, and the loader would otherwise merge two different groups.
     */
    @Test
    void generatedAndHandWrittenRegistrationsAreDisjoint() {
        Set<String> generatedKeys = Main.generatedImpls(generated()).keySet();
        AwsClients clients = new AwsClients("http://127.0.0.1:1", "us-east-1");
        List<Registry.ImplSource> sources = new ArrayList<>();
        for (ServiceGroup sg : Main.serviceGroups(clients)) {
            sources.add(new Registry.ImplSource(sg.sourceName(), sg.impls()));
        }
        Set<String> handWritten = Registry.mergeImpls(sources, SUITE).keySet();

        TreeSet<String> shared = new TreeSet<>(generatedKeys);
        shared.retainAll(handWritten);
        assertEquals(Set.of(), shared, "an impl key registered by both halves of the suite");
    }
}
