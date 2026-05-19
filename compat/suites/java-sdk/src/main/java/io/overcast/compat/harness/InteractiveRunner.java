package io.overcast.compat.harness;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Interactive mode runner for the Java compat suite.
 *
 * <p>When {@code OVERCAST_COMPAT_INTERACTIVE=1} is set, the suite enters a
 * long-lived stdin/stdout NDJSON protocol instead of running all tests and
 * exiting. See {@code compat/PROTOCOL.md} for the full specification.
 */
public final class InteractiveRunner {

    private static final ObjectMapper MAPPER = new ObjectMapper();

    private InteractiveRunner() {}

    /** Entry point for interactive mode. Blocks until shutdown or stdin closes. */
    public static void run(String suite, String endpoint, List<TestGroup> allGroups) {
        emit(new BuildingEvent(suite, "Loading registry and building test groups..."));

        int totalTests = allGroups.stream().mapToInt(g -> g.tests().size()).sum();
        emit(new ReadyEvent(suite, totalTests));

        int slots = 8;
        String slotsEnv = System.getenv("OVERCAST_COMPAT_PARALLEL_SLOTS");
        if (slotsEnv != null && !slotsEnv.isEmpty()) {
            try { slots = Math.max(1, Integer.parseInt(slotsEnv)); } catch (NumberFormatException ignored) {}
        }

        ExecutorService pool = Executors.newFixedThreadPool(slots);
        // Track active AbortControllers by "group:test" key for cancellation.
        ConcurrentHashMap<String, CancellationFlag> cancellationFlags = new ConcurrentHashMap<>();
        // Track currently running test ("suite" → "group:test") for pong responses.
        ConcurrentHashMap<String, String> runningTests = new ConcurrentHashMap<>();

        String region = System.getenv("OVERCAST_DEFAULT_REGION") != null
                ? System.getenv("OVERCAST_DEFAULT_REGION") : "us-east-1";
        String runId = System.getenv("OVERCAST_COMPAT_RUN_ID") != null
                ? System.getenv("OVERCAST_COMPAT_RUN_ID") : "local";

        // Build a lookup map: group name → TestGroup
        Map<String, TestGroup> groupMap = new LinkedHashMap<>();
        for (TestGroup g : allGroups) {
            groupMap.put(g.name(), g);
        }

        try (BufferedReader reader = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8))) {
            String line;
            while ((line = reader.readLine()) != null) {
                String trimmed = line.trim();
                if (trimmed.isEmpty()) continue;

                Map<?, ?> raw;
                try {
                    raw = MAPPER.readValue(trimmed, Map.class);
                } catch (Exception e) {
                    System.err.println("[java-sdk] invalid JSON on stdin: " + trimmed);
                    continue;
                }

                String command = (String) raw.get("command");
                if (command == null) {
                    System.err.println("[java-sdk] missing 'command' field: " + trimmed);
                    continue;
                }

                switch (command) {
                    case "run" -> handleRun(trimmed, suite, endpoint, region, runId,
                            groupMap, pool, cancellationFlags, runningTests);
                    case "cancel" -> handleCancel(raw, cancellationFlags);
                    case "ping" -> {
                        String pt = runningTests.get(suite);
                        emit(new PongEvent(suite, pt));
                    }
                    case "shutdown" -> {
                        // Cancel all in-flight work, then exit.
                        for (CancellationFlag flag : cancellationFlags.values()) {
                            flag.cancel();
                        }
                        pool.shutdownNow();
                        return;
                    }
                    default -> System.err.println("[java-sdk] unknown command: " + command);
                }
            }
        } catch (Exception e) {
            System.err.println("[java-sdk] stdin read error: " + e.getMessage());
        } finally {
            pool.shutdownNow();
        }
    }

    private static void handleRun(String json, String suite, String endpoint, String region,
                                   String runId, Map<String, TestGroup> groupMap,
                                   ExecutorService pool, ConcurrentHashMap<String, CancellationFlag> cancellationFlags,
                                   ConcurrentHashMap<String, String> runningTests) {
        RunCommand cmd;
        try {
            cmd = MAPPER.readValue(json, RunCommand.class);
        } catch (Exception e) {
            System.err.println("[java-sdk] failed to parse run command: " + e.getMessage());
            return;
        }

        // Resolve requested groups/tests.
        // A null or empty tests list means "run all groups" (the run-all command).
        List<TestGroup> groupsToRun = new ArrayList<>();
        if (cmd.tests == null || cmd.tests.isEmpty()) {
            groupsToRun.addAll(groupMap.values().stream()
                    .sorted(Comparator.comparing(TestGroup::name))
                    .toList());
        } else {
            for (RunCommand.TestRef ref : cmd.tests) {
                TestGroup group = groupMap.get(ref.group);
                if (group == null) {
                    System.err.println("[java-sdk] unknown group in run command: " + ref.group);
                    continue;
                }
                if (ref.tests != null && !ref.tests.isEmpty()) {
                    Set<String> requested = new HashSet<>(ref.tests);
                    List<TestCase> filtered = group.tests().stream()
                            .filter(tc -> requested.contains(tc.name()))
                            .toList();
                    groupsToRun.add(new TestGroup(group.suite(), group.service(), group.name(),
                            filtered, group.setup(), group.teardown()));
                } else {
                    groupsToRun.add(group);
                }
            }
        }

        // Fire off the batch asynchronously so stdin reading continues.
        pool.submit(() -> {
            long batchStart = System.currentTimeMillis();
            AtomicInteger passed = new AtomicInteger();
            AtomicInteger failed = new AtomicInteger();
            AtomicInteger skipped = new AtomicInteger();
            AtomicInteger unimplemented = new AtomicInteger();
            AtomicInteger cancelled = new AtomicInteger();

            List<Future<?>> futures = new ArrayList<>();
            for (TestGroup group : groupsToRun) {
                futures.add(pool.submit(() -> {
                    int[] counts = runGroupInteractive(suite, endpoint, region, runId, group,
                            cmd.batchId, cancellationFlags, runningTests);
                    passed.addAndGet(counts[0]);
                    failed.addAndGet(counts[1]);
                    skipped.addAndGet(counts[2]);
                    unimplemented.addAndGet(counts[3]);
                    cancelled.addAndGet(counts[4]);
                }));
            }

            for (Future<?> f : futures) {
                try {
                    f.get(5, TimeUnit.MINUTES);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                } catch (ExecutionException | TimeoutException ignored) {}
            }

            emit(new BatchCompleteEvent(suite, cmd.batchId,
                    passed.get(), failed.get(), skipped.get(), unimplemented.get(),
                    cancelled.get(), System.currentTimeMillis() - batchStart));
        });
    }

    private static void handleCancel(Map<?, ?> raw, ConcurrentHashMap<String, CancellationFlag> cancellationFlags) {
        String group = (String) raw.get("group");
        String test = (String) raw.get("test");

        if (group != null && test != null) {
            // Cancel a specific test.
            String key = group + ":" + test;
            CancellationFlag flag = cancellationFlags.get(key);
            if (flag != null) flag.cancel();
        } else {
            // Cancel all in-flight tests.
            for (CancellationFlag flag : cancellationFlags.values()) {
                flag.cancel();
            }
        }
    }

    /** Runs a single group, checking cancellation flags before each test. Returns [passed, failed, skipped, unimplemented, cancelled]. */
    private static int[] runGroupInteractive(String suite, String endpoint, String region,
                                             String runId, TestGroup group, String batchId,
                                             ConcurrentHashMap<String, CancellationFlag> cancellationFlags,
                                             ConcurrentHashMap<String, String> runningTests) {
        TestContext ctx = new TestContext(endpoint, region, runId);
        int passed = 0, failed = 0, skipped = 0, unimplemented = 0, cancelled = 0;

        // Register cancellation flags for each test.
        for (TestCase tc : group.tests()) {
            String key = group.name() + ":" + tc.name();
            cancellationFlags.put(key, new CancellationFlag());
        }

        try {
            // Setup phase
            boolean setupOk = true;
            if (group.setup() != null) {
                try {
                    group.setup().run(ctx);
                } catch (Throwable e) {
                    String reason = "setup failed: " + e.getMessage();
                    for (TestCase tc : group.tests()) {
                        emit(new TestResultEvent(suite, group.service(), group.name(), tc.name(), "skip", 0, reason));
                        skipped++;
                    }
                    setupOk = false;
                }
            }

            if (!setupOk) {
                return new int[]{passed, failed, skipped, unimplemented, cancelled};
            }

            // Test phase
            Set<String> passedTests = new HashSet<>();
            Set<String> failedOrSkipped = new HashSet<>();

            for (TestCase tc : group.tests()) {
                String key = group.name() + ":" + tc.name();
                CancellationFlag flag = cancellationFlags.get(key);

                // Check cancellation before running.
                if (flag != null && flag.isCancelled()) {
                    emit(new CancelledEvent(suite, batchId, group.name(), tc.name(), "user"));
                    cancelled++;
                    failedOrSkipped.add(tc.name());
                    continue;
                }

                if (tc.skip() != null && !tc.skip().isEmpty()) {
                    emit(new TestResultEvent(suite, group.service(), group.name(), tc.name(), "skip", 0, tc.skip()));
                    skipped++;
                    failedOrSkipped.add(tc.name());
                    continue;
                }

                // Cascade-skip: if any dependency failed or was skipped, skip this test.
                List<String> missingDeps = new ArrayList<>();
                for (String dep : tc.depends()) {
                    if (failedOrSkipped.contains(dep)) {
                        missingDeps.add(dep);
                    }
                }
                if (!missingDeps.isEmpty()) {
                    String reason = "dependency failed: " + String.join(", ", missingDeps);
                    emit(new TestResultEvent(suite, group.service(), group.name(), tc.name(), "skip", 0, reason));
                    skipped++;
                    failedOrSkipped.add(tc.name());
                    continue;
                }

                emit(new TestStartEvent(suite, group.service(), group.name(), tc.name()));

                runningTests.put(suite, group.name() + ":" + tc.name());

                long start = System.currentTimeMillis();
                try {
                    tc.fn().run(ctx);
                    long ms = System.currentTimeMillis() - start;

                    // Check cancellation after test completes (may have been cancelled mid-flight).
                    if (flag != null && flag.isCancelled()) {
                        emit(new CancelledEvent(suite, batchId, group.name(), tc.name(), "user"));
                        cancelled++;
                        failedOrSkipped.add(tc.name());
                    } else {
                        emit(new TestResultEvent(suite, group.service(), group.name(), tc.name(), "pass", ms, null));
                        passed++;
                        passedTests.add(tc.name());
                    }
                } catch (Throwable e) {
                    long ms = System.currentTimeMillis() - start;

                    if (flag != null && flag.isCancelled()) {
                        emit(new CancelledEvent(suite, batchId, group.name(), tc.name(), "user"));
                        cancelled++;
                    } else {
                        String msg = e.getMessage() != null ? e.getMessage() : e.getClass().getSimpleName();
                        String status = Runner.isUnimplemented(e) ? "unimplemented" : "fail";
                        emit(new TestResultEvent(suite, group.service(), group.name(), tc.name(), status, ms, msg));
                        if (status.equals("unimplemented")) {
                            unimplemented++;
                        } else {
                            failed++;
                        }
                    }
                    failedOrSkipped.add(tc.name());
                }
            }
            runningTests.remove(suite);
        } finally {
            // Always run teardown and clean up cancellation flags.
            if (group.teardown() != null) {
                try {
                    group.teardown().run(ctx);
                } catch (Throwable e) {
                    System.err.println("[java-sdk] teardown failed for " + group.name() + ": " + e.getMessage());
                }
            }
            for (TestCase tc : group.tests()) {
                cancellationFlags.remove(group.name() + ":" + tc.name());
            }
        }

        return new int[]{passed, failed, skipped, unimplemented, cancelled};
    }

    // ── Emit helper ───────────────────────────────────────────────────────────

    private static synchronized void emit(Object v) {
        try {
            System.out.println(MAPPER.writeValueAsString(v));
            System.out.flush();
        } catch (Exception e) {
            System.err.println("[java-sdk] failed to serialise event: " + e.getMessage());
        }
    }

    // ── Thread-safe cancellation flag ─────────────────────────────────────────

    static final class CancellationFlag {
        private volatile boolean cancelled;

        void cancel() { cancelled = true; }
        boolean isCancelled() { return cancelled; }
    }

    // ── Stdin command types ───────────────────────────────────────────────────

    @JsonIgnoreProperties(ignoreUnknown = true)
    record RunCommand(
            String command,
            @JsonProperty("batch_id") String batchId,
            List<TestRef> tests) {

        @JsonIgnoreProperties(ignoreUnknown = true)
        record TestRef(String group, List<String> tests) {}
    }

    // ── Event types ───────────────────────────────────────────────────────────

    record BuildingEvent(String event, String suite, String message) {
        BuildingEvent(String suite, String message) { this("building", suite, message); }
    }

    record PongEvent(String event, String suite, @JsonProperty("running_test") String runningTest) {
        PongEvent(String suite, String runningTest) { this("pong", suite, runningTest); }
    }

    record ReadyEvent(String event, String suite, int total_tests) {
        ReadyEvent(String suite, int totalTests) { this("ready", suite, totalTests); }
    }

    record TestStartEvent(String event, String suite, String service, String group, String test) {
        TestStartEvent(String suite, String service, String group, String test) {
            this("test_start", suite, service, group, test);
        }
    }

    record TestResultEvent(String event, String suite, String service, String group,
                           String test, String status, long duration_ms, String error) {
        TestResultEvent(String suite, String service, String group, String test,
                        String status, long durationMs, String error) {
            this("test_result", suite, service, group, test, status, durationMs, error);
        }
    }

    record CancelledEvent(String event, String suite, String batch_id, String group, String test, String reason) {
        CancelledEvent(String suite, String batchId, String group, String test, String reason) {
            this("cancelled", suite, batchId, group, test, reason);
        }
    }

    record BatchCompleteEvent(String event, String suite, String batch_id,
                              int passed, int failed, int skipped, int unimplemented,
                              int cancelled, long duration_ms) {
        BatchCompleteEvent(String suite, String batchId, int passed, int failed, int skipped,
                           int unimplemented, int cancelled, long durationMs) {
            this("batch_complete", suite, batchId, passed, failed, skipped, unimplemented, cancelled, durationMs);
        }
    }
}
