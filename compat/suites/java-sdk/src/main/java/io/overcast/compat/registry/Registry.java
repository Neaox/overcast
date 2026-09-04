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
 *
 * <p>{@code registry.generated.json} — the machine-written half that
 * {@code cmd/compatgen} owns — is read from the same directory and its groups
 * are concatenated after the hand-written ones. A missing file is an empty
 * registry, not an error: suite images, CI artifacts and branches cut before
 * the file existed all have to keep working. See
 * {@code docs/plans/compat-coverage-modelgen.md} § 3.6.
 */
public final class Registry {

    private static final ObjectMapper MAPPER = new ObjectMapper();
    private static final TestFn NOOP = ctx -> {};

    /** Sibling of {@code registry.json} holding the generated groups. */
    static final String GENERATED_REGISTRY_FILENAME = "registry.generated.json";

    /** The only {@code version} the generated registry may declare. */
    static final int GENERATED_REGISTRY_VERSION = 1;

    // ── Jackson model ─────────────────────────────────────────────────────────

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record RegistryRoot(List<RegistryGroup> groups) {}

    /** The generated sibling. {@code version} is checked; the rest is shared. */
    @JsonIgnoreProperties(ignoreUnknown = true)
    public record GeneratedRegistryRoot(Integer version, List<RegistryGroup> groups) {
        public GeneratedRegistryRoot {
            if (groups == null) groups = List.of();
        }
    }

    /**
     * One registry group.
     *
     * @param service  AWS service identifier, e.g. {@code "s3"}.
     * @param name     Group name, e.g. {@code "s3-crud"}.
     * @param tests    The group's tests, in registry order.
     * @param suites   Suites the group is in scope for; empty means all of them.
     * @param generated True for a group read from {@code registry.generated.json}.
     * @param state    {@code candidate} or {@code gated} — generated groups only.
     * @param scenario Path to the scenario IR the group was generated from, for
     *                 a scenario backend to interpret. Generated groups only.
     */
    @JsonIgnoreProperties(ignoreUnknown = true)
    public record RegistryGroup(
            String service,
            String name,
            List<RegistryTest> tests,
            List<String> suites,
            boolean generated,
            String state,
            String scenario) {

        public RegistryGroup {
            if (tests == null) tests = List.of();
            if (suites == null) suites = List.of();
        }

        /** A hand-written group: in scope everywhere, no generated metadata. */
        public RegistryGroup(String service, String name, List<RegistryTest> tests) {
            this(service, name, tests, List.of(), false, null, null);
        }

        /**
         * Whether {@code suite} may run this group.
         *
         * <p>A group that names its suites is out of scope for every other
         * suite — not in debt: it is not loaded at all there, so it emits no
         * tests, no skips and no results. On a generated group the list is
         * derived from backend availability by {@code cmd/compatgen}.
         */
        public boolean inScopeFor(String suite) {
            return suites.isEmpty() || suites.contains(suite);
        }
    }

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
        return buildGroups(suite, impls, setups, teardowns, capabilities, null);
    }

    /**
     * As {@link #buildGroups(String, Map, Map, Map, Set)}, with a
     * {@link ScenarioBackend} for the generated groups this suite can execute.
     *
     * @param backend May be {@code null} — no backend exists yet.
     */
    public static List<TestGroup> buildGroups(
            String suite,
            Map<String, TestFn> impls,
            Map<String, TestFn> setups,
            Map<String, TestFn> teardowns,
            Set<String> capabilities,
            ScenarioBackend backend) throws IOException {
        RegistryRoot root = load();
        validateImpls(root, impls, suite);
        return buildGroups(suite, root, impls, setups, teardowns, capabilities, backend);
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
        return buildGroups(suite, root, impls, setups, teardowns, capabilities, null);
    }

    /** As above, with an optional {@link ScenarioBackend}. */
    static List<TestGroup> buildGroups(
            String suite,
            RegistryRoot root,
            Map<String, TestFn> impls,
            Map<String, TestFn> setups,
            Map<String, TestFn> teardowns,
            Set<String> capabilities,
            ScenarioBackend backend) {

        Set<String> ambiguous = ambiguousTestNames(root);

        List<TestGroup> groups = new ArrayList<>();

        for (RegistryGroup rg : root.groups()) {
            // A generated group names the backends that can execute it, so a
            // suite absent from that list is out of scope and loads nothing.
            //
            // Hand-written groups are deliberately not filtered here. The only
            // one that declares "suites" is cdk-lifecycle, which this suite has
            // always loaded and reported as skips that compat/baseline/java-sdk.json
            // records — so scoping it out is a behaviour change of its own, not
            // part of reading the generated registry.
            if (rg.generated() && !rg.inScopeFor(suite)) continue;

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
                // A generated group is executed by an interpreter reading its
                // scenario IR rather than by a registered impl, so the backend
                // is the last resolution step before the sentinel.
                if (fn == null && backend != null) fn = backend.resolve(rg, rt);
                if (fn != null) {
                    tests.add(new TestCase(rt.name(), fn, op, null, rt.depends()));
                } else if (rg.generated()) {
                    tests.add(missingBackend(suite, rg, rt, op));
                } else {
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
     * The interim result for a generated test this suite cannot execute.
     *
     * <p>A generated group's {@code suites} list is derived from backend
     * availability by {@code cmd/compatgen}, so a suite named in it that has no
     * scenario backend is a generator or loader bug — and it has to be loud.
     * Reporting it as the not-implemented sentinel would file it as ordinary
     * parity debt, and reporting it as {@code na} would call it a permanent,
     * accepted divergence; both read as "nothing to see here". A candidate group
     * gates nothing ({@code cmd/compat}, #1367), so this cannot red a build
     * until the group is promoted to {@code gated} — at which point it is a real
     * regression and should.
     *
     * <p>Failing is how a test body reports, so the body throws. It carries no
     * {@code depends}: the runner cascade-skips a test whose dependency failed,
     * and a skip is exactly what this result must never be.
     */
    private static TestCase missingBackend(String suite, RegistryGroup rg, RegistryTest rt, String op) {
        String message = String.format(
                "generated group \"%s\" is scoped to %s but %s has no scenario backend",
                rg.name(), suite, suite);
        return new TestCase(rt.name(), ctx -> { throw new AssertionError(message); }, op, null, List.of());
    }

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
        return load(envPath != null && !envPath.isEmpty()
                ? new File(envPath)
                : new File("../registry.json"));
    }

    /**
     * Reads {@code file} and concatenates the generated groups from its
     * {@code registry.generated.json} sibling.
     *
     * <p>Hand-written groups come first, generated groups after, both in file
     * order — the loader sorts neither.
     */
    static RegistryRoot load(File file) throws IOException {
        if (!file.exists()) {
            throw new IOException("registry not found at: " + file.getAbsolutePath()
                    + " — set OVERCAST_REGISTRY_PATH to override");
        }
        RegistryRoot hand = MAPPER.readValue(file, RegistryRoot.class);

        File generated = new File(file.getAbsoluteFile().getParentFile(), GENERATED_REGISTRY_FILENAME);
        List<RegistryGroup> generatedGroups = loadGenerated(generated, hand);
        if (generatedGroups.isEmpty()) return hand;

        List<RegistryGroup> all = new ArrayList<>(hand.groups());
        all.addAll(generatedGroups);
        return new RegistryRoot(List.copyOf(all));
    }

    /**
     * Reads the generated registry, or returns nothing when it is absent.
     *
     * <p>Absence is not an error: suite images, CI artifacts and branches cut
     * before the file existed must all keep working, and "the file is not
     * there" has to produce the same groups as "the file is there and empty".
     * Everything else about a file that <em>is</em> there is an error, exactly
     * as a malformed {@code registry.json} is — a bad generated file must never
     * be silently dropped.
     */
    private static List<RegistryGroup> loadGenerated(File file, RegistryRoot hand) throws IOException {
        if (!file.exists()) return List.of();

        GeneratedRegistryRoot root;
        try {
            root = MAPPER.readValue(file, GeneratedRegistryRoot.class);
        } catch (IOException e) {
            throw new IOException("parse generated registry " + file.getAbsolutePath()
                    + ": " + e.getMessage(), e);
        }
        if (root.version() == null || root.version() != GENERATED_REGISTRY_VERSION) {
            throw new IOException("generated registry " + file.getAbsolutePath()
                    + " has version " + root.version()
                    + ", want " + GENERATED_REGISTRY_VERSION);
        }

        Set<String> handNames = new HashSet<>();
        for (RegistryGroup rg : hand.groups()) handNames.add(rg.name());

        for (RegistryGroup rg : root.groups()) {
            // The two registries are concatenated and every gate file
            // (baseline, flaky, parity-debt) keys on suite/group/test with no
            // notion of which file a group came from, so a reused name merges
            // two different groups rather than conflicting. cmd/compat lints
            // this; the loader is the second line of defence.
            if (handNames.contains(rg.name())) {
                throw new IOException("generated group \"" + rg.name()
                        + "\" in " + file.getAbsolutePath()
                        + " collides with a hand-written group of the same name");
            }
            if (!rg.generated()) {
                throw new IOException("generated group \"" + rg.name() + "\" in "
                        + file.getAbsolutePath() + " does not set \"generated\": true");
            }
            if (rg.state() == null || rg.state().isEmpty()) {
                throw new IOException("generated group \"" + rg.name() + "\" in "
                        + file.getAbsolutePath() + " has no \"state\"");
            }
            if (rg.suites().isEmpty()) {
                throw new IOException("generated group \"" + rg.name() + "\" in "
                        + file.getAbsolutePath() + " has no \"suites\"");
            }
        }
        return root.groups();
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
     * One service group's contribution to the suite's impl map, labelled with
     * the class it came from so a collision can name both sides.
     */
    public record ImplSource(String name, Map<String, TestFn> impls) {}

    /**
     * Flattens the per-service impl maps into the single map the loader
     * resolves against, refusing any key that two sources both register.
     *
     * <p>The merge used to be {@code impls.putAll(sg.impls())} — last writer
     * wins, and silently. Two service groups both registering
     * {@code "lambda-crud:CreateFunction"} left one implementation unreachable
     * with nothing said about it, and the run reported a result for whichever
     * one survived. {@link #validateImpls} cannot catch this: by the time it
     * sees the flattened map the discarded implementation is already gone, and
     * the surviving key resolves perfectly well.
     *
     * @throws IllegalStateException if any key is registered more than once.
     */
    public static Map<String, TestFn> mergeImpls(List<ImplSource> sources, String suite) {
        Map<String, TestFn> merged = new LinkedHashMap<>();
        Map<String, String> owner = new HashMap<>(); // key → first registrant

        List<String> problems = new ArrayList<>();
        for (ImplSource source : sources) {
            for (Map.Entry<String, TestFn> entry : source.impls().entrySet()) {
                String first = owner.get(entry.getKey());
                if (first != null) {
                    problems.add(duplicateProblem(entry.getKey(), first, source.name()));
                    continue;
                }
                owner.put(entry.getKey(), source.name());
                merged.put(entry.getKey(), entry.getValue());
            }
        }

        if (problems.isEmpty()) return merged;
        // Sorted so the message is the same however the source maps iterate.
        // Every problem starts with the key, which is what a reader scans for.
        java.util.Collections.sort(problems);
        throw new IllegalStateException(String.format(
                "[%s] %d duplicate impl registration(s):%n  - %s",
                suite, problems.size(), String.join(String.format("%n  - "), problems)));
    }

    /**
     * One collision. The two sources are the same when a single service group
     * registers the key twice.
     */
    private static String duplicateProblem(String key, String first, String second) {
        String where = first.equals(second)
                ? String.format("is registered twice by \"%s\"", first)
                : String.format("is registered by both \"%s\" and \"%s\"", first, second);
        return String.format(
                "impl \"%s\" %s — one of the two would be silently discarded;"
                        + " remove or re-key one", key, where);
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
