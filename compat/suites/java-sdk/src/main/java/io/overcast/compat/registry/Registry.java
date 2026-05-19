package io.overcast.compat.registry;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.overcast.compat.harness.TestCase;
import io.overcast.compat.harness.TestFn;
import io.overcast.compat.harness.TestGroup;

import java.io.File;
import java.io.IOException;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Loads the shared {@code registry.json} and converts it into a list of
 * {@link TestGroup}s, auto-skipping any test that has no implementation.
 *
 * <p>The registry is the single source of truth for which groups and tests
 * must appear in every suite's output. By reading it at runtime, the Java
 * suite automatically picks up new tests added to the registry and emits
 * {@code skip} for them until an implementation is added here — keeping the
 * dashboard matrix consistent across all suites.
 *
 * <p>Registry path resolution (first match wins):
 * <ol>
 *   <li>{@code OVERCAST_REGISTRY_PATH} env var</li>
 *   <li>{@code ../registry.json} relative to the JVM working directory
 *       (i.e. {@code compat/suites/java-sdk/} → {@code compat/suites/})</li>
 * </ol>
 */
public final class Registry {

    private static final ObjectMapper MAPPER = new ObjectMapper();
    private static final TestFn NOOP = ctx -> {};

    // ── Jackson model ─────────────────────────────────────────────────────────

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record RegistryRoot(List<RegistryGroup> groups) {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record RegistryGroup(String service, String name, List<RegistryTest> tests) {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record RegistryTest(
            String name,
            String op,          // null = absent (use name), "" = suppressed
            String skip,        // non-null → emit as skipped with this reason
            List<String> requires, // capability requirements, e.g. ["docker"]
            List<String> depends   // tests in the same group that must pass first
    ) {
        public RegistryTest {
            if (requires == null) requires = List.of();
            if (depends == null) depends = List.of();
        }
    }

    // ── Public API ────────────────────────────────────────────────────────────

    /**
     * Loads the registry, cross-references it with {@code impls}, and returns
     * a fully built list of {@link TestGroup}s.
     *
     * @param suite        Suite name written into every event, e.g. {@code "java-sdk"}.
     * @param impls        Map of test name → {@link TestFn} implementation.
     * @param setups       Map of group name → setup {@link TestFn}.
     * @param teardowns    Map of group name → teardown {@link TestFn}.
     * @param capabilities Set of capability strings this runner supports (e.g. {@code "docker"}).
     */
    public static List<TestGroup> buildGroups(
            String suite,
            Map<String, TestFn> impls,
            Map<String, TestFn> setups,
            Map<String, TestFn> teardowns,
            Set<String> capabilities) throws IOException {

        RegistryRoot root = load();
        validateImpls(root, impls, suite);

        List<TestGroup> groups = new ArrayList<>();

        for (RegistryGroup rg : root.groups()) {
            List<TestCase> tests = new ArrayList<>();

            for (RegistryTest rt : topoSort(rg.tests())) {
                String op   = rt.op();      // null or overridden op
                String skip = rt.skip();

                if (skip != null && !skip.isEmpty()) {
                    tests.add(new TestCase(rt.name(), NOOP, op, skip, rt.depends()));
                    continue;
                }

                // Capability gate — auto-skip if a required capability is absent.
                boolean capsMissing = rt.requires().stream()
                        .anyMatch(cap -> !capabilities.contains(cap));
                if (capsMissing) {
                    String reason = "requires: " + String.join(", ", rt.requires());
                    tests.add(new TestCase(rt.name(), NOOP, op, reason, rt.depends()));
                    continue;
                }

                TestFn fn = impls.get(rg.name() + "/" + rt.name());
                if (fn == null) fn = impls.get(rt.name());
                if (fn == null) {
                    tests.add(new TestCase(rt.name(), NOOP, op, "not implemented", rt.depends()));
                } else {
                    tests.add(new TestCase(rt.name(), fn, op, null, rt.depends()));
                }
            }

            groups.add(new TestGroup(
                    suite,
                    rg.service(),
                    rg.name(),
                    List.copyOf(tests),
                    setups.get(rg.name()),
                    teardowns.get(rg.name())));
        }

        return List.copyOf(groups);
    }

    // ── Internal ──────────────────────────────────────────────────────────────

    /**
     * Topologically sorts tests within a group so that every test runs after
     * its {@code depends} have run.  Tests with no deps (or identical depth)
     * retain their original registry order.
     */
    static List<RegistryTest> topoSort(List<RegistryTest> tests) {
        // Index by name, preserving insertion order.
        LinkedHashMap<String, RegistryTest> byName = new LinkedHashMap<>();
        for (RegistryTest t : tests) byName.put(t.name(), t);

        List<RegistryTest> sorted = new ArrayList<>(tests.size());
        Set<String> visited = new HashSet<>();
        Set<String> visiting = new HashSet<>(); // cycle detection

        for (RegistryTest t : tests) {
            visit(t.name(), byName, visited, visiting, sorted);
        }
        return sorted;
    }

    private static void visit(
            String name,
            LinkedHashMap<String, RegistryTest> byName,
            Set<String> visited,
            Set<String> visiting,
            List<RegistryTest> sorted) {
        if (visited.contains(name)) return;
        if (visiting.contains(name)) {
            // Cycle — break it silently; the test will just run in whatever order.
            return;
        }
        RegistryTest t = byName.get(name);
        if (t == null) return; // unknown dep — ignore

        visiting.add(name);
        for (String dep : t.depends()) {
            visit(dep, byName, visited, visiting, sorted);
        }
        visiting.remove(name);
        visited.add(name);
        sorted.add(t);
    }

    static RegistryRoot load() throws IOException {
        String envPath = System.getenv("OVERCAST_REGISTRY_PATH");
        File file = envPath != null && !envPath.isEmpty()
                ? new File(envPath)
                : new File("../registry.json");

        if (!file.exists()) {
            throw new IOException("registry not found at: " + file.getAbsolutePath()
                    + " — set OVERCAST_REGISTRY_PATH to override");
        }
        return MAPPER.readValue(file, RegistryRoot.class);
    }

    /**
     * Warns on stderr if any key in {@code impls} is not present in the
     * registry — those implementations are orphaned and will never run.
     */
    static void validateImpls(RegistryRoot root, Map<String, TestFn> impls, String suite) {
        Set<String> registered = new java.util.HashSet<>();
        for (RegistryGroup rg : root.groups()) {
            for (RegistryTest rt : rg.tests()) {
                registered.add(rt.name());
                registered.add(rg.name() + "/" + rt.name());
            }
        }
        for (String name : impls.keySet()) {
            if (!registered.contains(name)) {
                System.err.printf("[%s] WARNING: impl %s is not in registry.json — it will never run%n",
                        suite, name);
            }
        }
    }
}
