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
        return buildGroups(suite, root, impls, setups, teardowns, capabilities);
    }

    /**
     * Loads the registry and checks {@code impls} against it, without building
     * any groups. Useful on its own to assert a suite's registrations resolve.
     *
     * @throws IllegalStateException if any registration is unusable.
     */
    public static void validateImpls(Map<String, TestFn> impls, String suite) throws IOException {
        validateImpls(load(), impls, suite);
    }

    /**
     * Assembles groups from an already-loaded registry, without validating the
     * impl keys — {@link #validateImpls} is a separate step, as in the other
     * suite loaders. Package-private so tests can exercise resolution against a
     * fixture registry, including the paths validation would normally reject.
     */
    static List<TestGroup> buildGroups(
            String suite,
            RegistryRoot root,
            Map<String, TestFn> impls,
            Map<String, TestFn> setups,
            Map<String, TestFn> teardowns,
            Set<String> capabilities) {

        Set<String> ambiguous = ambiguousTestNames(root);

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

                // Look up by group-qualified key ("groupName:testName") first,
                // then fall back to the bare test name. The bare fallback is
                // refused for a name claimed by more than one group: it would
                // bind this group to another group's implementation and report
                // its result as ours. validateImpls rejects such a registration
                // outright; this is the second line of defence, so a mis-bind
                // cannot occur even if validation is bypassed.
                TestFn fn = impls.get(qualifiedKey(rg.name(), rt.name()));
                if (fn == null && !ambiguous.contains(rt.name())) fn = impls.get(rt.name());
                if (fn == null) {
                    // Sentinel wording is shared by every suite loader: the
                    // parity checker (cmd/compat --check-parity) classifies
                    // registry gaps by this exact phrasing, so it must not
                    // drift. See compat/AGENTS.md § Baseline & uniformity.
                    tests.add(new TestCase(
                            rt.name(),
                            NOOP,
                            op,
                            String.format("not yet implemented in %s test suite", suite),
                            rt.depends()));
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
     * The separator between a group name and a test name in a qualified impl
     * key. Every suite loader uses {@code ":"}; a key written with {@code "/"}
     * resolves to nothing and is rejected by {@link #validateImpls}.
     */
    public static final String KEY_SEPARATOR = ":";

    /** Builds the group-qualified impl key for a group/test pair. */
    public static String qualifiedKey(String group, String test) {
        return group + KEY_SEPARATOR + test;
    }

    /**
     * Test names that more than one registry group declares.
     *
     * <p>A bare-name implementation cannot serve these. {@code ListUsers}
     * belongs to both {@code iam-users} and {@code cognito-userpools}, so a
     * bare {@code ListUsers} impl binds whichever group happens to resolve it —
     * and the loser silently runs the other service's test and reports the
     * result as its own. Suites must register the group-qualified key for an
     * ambiguous name.
     */
    static Set<String> ambiguousTestNames(RegistryRoot root) {
        Set<String> ambiguous = new java.util.TreeSet<>();
        testNameOwners(root).forEach((name, groups) -> {
            if (groups.size() > 1) ambiguous.add(name);
        });
        return ambiguous;
    }

    /** Maps each registry test name to the sorted groups that declare it. */
    static Map<String, List<String>> testNameOwners(RegistryRoot root) {
        Map<String, Set<String>> owners = new java.util.TreeMap<>();
        for (RegistryGroup rg : root.groups()) {
            for (RegistryTest rt : rg.tests()) {
                owners.computeIfAbsent(rt.name(), k -> new java.util.TreeSet<>()).add(rg.name());
            }
        }
        Map<String, List<String>> result = new java.util.TreeMap<>();
        owners.forEach((name, groups) -> result.put(name, List.copyOf(groups)));
        return result;
    }

    /**
     * Rejects impl keys that cannot be bound to exactly one registry test.
     *
     * <p>This used to be a stderr warning nobody read, while the test the key
     * was meant to implement quietly fell back to another group's
     * implementation and reported a pass. Two registrations are refused:
     *
     * <ul>
     *   <li>a key matching no registry entry — a typo, a stale name, or the
     *       wrong separator (this suite used {@code "group/test"} until the
     *       separator was unified across suites on {@code "group:test"});</li>
     *   <li>a bare key for a test name that several groups declare, which
     *       cannot say which group it implements.</li>
     * </ul>
     *
     * @throws IllegalStateException if any registration is unusable.
     */
    static void validateImpls(RegistryRoot root, Map<String, TestFn> impls, String suite) {
        Map<String, List<String>> owners = testNameOwners(root);
        Set<String> registered = new HashSet<>();
        for (RegistryGroup rg : root.groups()) {
            for (RegistryTest rt : rg.tests()) {
                registered.add(rt.name());
                registered.add(qualifiedKey(rg.name(), rt.name()));
            }
        }

        List<String> problems = new ArrayList<>();
        for (String name : new java.util.TreeSet<>(impls.keySet())) {
            if (!registered.contains(name)) {
                String msg = String.format("impl \"%s\" matches no registry entry", name);
                if (name.contains("/")) {
                    msg += String.format(
                            " (group-qualified keys use \"%s\", not \"/\" — did you mean \"%s\"?)",
                            KEY_SEPARATOR, name.replaceFirst("/", KEY_SEPARATOR));
                }
                problems.add(msg);
            } else if (owners.getOrDefault(name, List.of()).size() > 1) {
                // Naming every candidate rather than guessing one: only the
                // author knows which group this implementation is for, and
                // binding it to the wrong one is the failure this prevents.
                List<String> candidates = new ArrayList<>();
                for (String group : owners.get(name)) {
                    candidates.add("\"" + qualifiedKey(group, name) + "\"");
                }
                problems.add(String.format(
                        "impl \"%s\" is ambiguous: groups %s all declare a test named \"%s\""
                                + " — qualify it with the group it implements, one of: %s",
                        name, owners.get(name), name, String.join(", ", candidates)));
            }
        }

        if (!problems.isEmpty()) {
            throw new IllegalStateException(String.format(
                    "[%s] %d unusable impl registration(s):%n  - %s",
                    suite, problems.size(), String.join(String.format("%n  - "), problems)));
        }
    }
}
