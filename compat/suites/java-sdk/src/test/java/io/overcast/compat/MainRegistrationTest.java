package io.overcast.compat;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.groups.ServiceGroup;
import io.overcast.compat.harness.TestFn;
import io.overcast.compat.registry.Registry;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.TreeSet;

import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * The suite's own registrations must resolve against the real registry.json.
 *
 * <p>This is the check that catches a mis-binding before a run reports one: an
 * unresolvable key (a typo, or the {@code "group/test"} form this suite used
 * before the separator was unified), or a bare key for a name that several
 * groups declare and which therefore binds an arbitrary one of them.
 *
 * <p>The registry is read from {@code ../registry.json}, which resolves both
 * when Maven runs from {@code compat/suites/java-sdk/} and in the Docker build,
 * whose working directory is {@code /build} with the registry at {@code /}.
 */
class MainRegistrationTest {

    @Test
    void registeredImplsResolveAgainstRegistry() throws Exception {
        // Throws IllegalStateException naming every unusable key.
        Registry.validateImpls(mergeAll(), "java-sdk");
    }

    /**
     * No two service group classes may register the same impl key. The merge is
     * last-writer-wins, so a collision discards one implementation and the test
     * it belonged to silently runs the other class's — the same mis-binding the
     * resolution rules prevent for registry lookups, one step earlier.
     */
    @Test
    void registeredImplsHaveNoDuplicateKeys() {
        mergeAll(); // throws IllegalStateException naming every duplicated key
    }

    /**
     * Every impl-map key must be group-qualified ({@code "group:test"}), never
     * a bare test name (#1700). A bare key that happens to be unambiguous today
     * silently becomes ambiguous — and is refused at runtime — the moment a new
     * group declares the same test name, so the rule is enforced here rather
     * than left to be rediscovered by a future collision.
     */
    @Test
    void registeredImplKeysAreAllQualified() {
        TreeSet<String> bare = new TreeSet<>();
        for (String key : mergeAll().keySet()) {
            if (!key.contains(Registry.KEY_SEPARATOR)) bare.add(key);
        }
        assertTrue(bare.isEmpty(), "bare (unqualified) impl keys found, want \"group:test\": " + bare);
    }

    /**
     * Flattens every service group's registrations the way {@code Main} does,
     * so both tests see exactly the map a real run would build.
     */
    private static Map<String, TestFn> mergeAll() {
        AwsClients clients = new AwsClients("http://127.0.0.1:1", "us-east-1");

        List<Registry.ImplSource> sources = new ArrayList<>();
        for (ServiceGroup sg : Main.serviceGroups(clients)) {
            sources.add(new Registry.ImplSource(sg.sourceName(), sg.impls()));
        }
        return Registry.mergeImpls(sources, "java-sdk");
    }
}
