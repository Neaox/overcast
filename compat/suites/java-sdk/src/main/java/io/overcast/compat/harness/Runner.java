package io.overcast.compat.harness;

import com.fasterxml.jackson.databind.ObjectMapper;

import java.time.Instant;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Runner executes a list of {@link TestGroup}s and emits NDJSON events to
 * {@code stdout}.
 *
 * <p>Events follow the same schema used by the node-js-sdk and go-sdk suites
 * so that the Overcast compatibility dashboard can aggregate results across
 * all language suites.
 *
 * <pre>
 * {"event":"run_start",   "suite":"java-sdk", "started_at":"...", "endpoint":"...", "version":"0"}
 * {"event":"test_result", "suite":"java-sdk", "service":"s3",  "group":"s3-crud",
 *                          "test":"CreateBucket", "status":"pass", "duration_ms":42}
 * {"event":"run_end",     "suite":"java-sdk", "passed":1, "failed":0, "skipped":0,
 *                          "unimplemented":0, "duration_ms":200}
 * </pre>
 */
public final class Runner {

    // Statuses
    private static final String PASS          = "pass";
    private static final String FAIL          = "fail";
    private static final String SKIP          = "skip";
    private static final String UNIMPLEMENTED = "unimplemented";

    private static final ObjectMapper MAPPER = new ObjectMapper();

    /** Emits run_start, executes every group in parallel (bounded by OVERCAST_COMPAT_PARALLEL_SLOTS), then emits run_end. */
    public static void runSuite(String suite, String endpoint, List<TestGroup> groups) {
        long suiteStart = System.currentTimeMillis();

        int totalTests = groups.stream().mapToInt(g -> g.tests().size()).sum();
        emit(new RunStartEvent(suite, Instant.now().toString(), endpoint, "1", totalTests));

        // Limit concurrent group execution to avoid overwhelming the emulator.
        // OVERCAST_COMPAT_PARALLEL_SLOTS is injected by the Go runner; default 8.
        int slots = 8;
        String slotsEnv = System.getenv("OVERCAST_COMPAT_PARALLEL_SLOTS");
        if (slotsEnv != null && !slotsEnv.isEmpty()) {
            try { slots = Math.max(1, Integer.parseInt(slotsEnv)); } catch (NumberFormatException ignored) {}
        }

        AtomicInteger passed = new AtomicInteger();
        AtomicInteger failed = new AtomicInteger();
        AtomicInteger skipped = new AtomicInteger();
        AtomicInteger unimplemented = new AtomicInteger();

        ExecutorService pool = Executors.newFixedThreadPool(slots);
        List<Future<?>> futures = new ArrayList<>(groups.size());
        String region = System.getenv("OVERCAST_DEFAULT_REGION") != null
                ? System.getenv("OVERCAST_DEFAULT_REGION") : "us-east-1";
        String runId = System.getenv("OVERCAST_COMPAT_RUN_ID") != null
                ? System.getenv("OVERCAST_COMPAT_RUN_ID") : "local";

        for (TestGroup group : groups) {
            futures.add(pool.submit(() -> {
                int[] counts = runGroup(suite, endpoint, region, runId, group);
                passed.addAndGet(counts[0]);
                failed.addAndGet(counts[1]);
                skipped.addAndGet(counts[2]);
                unimplemented.addAndGet(counts[3]);
            }));
        }

        for (Future<?> f : futures) {
            try {
                // 5-minute per-group timeout: prevents one hung AWS SDK call
                // from blocking the entire suite indefinitely.
                f.get(5, TimeUnit.MINUTES);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            } catch (ExecutionException | TimeoutException ignored) {}
        }
        pool.shutdown();

        long totalMs = System.currentTimeMillis() - suiteStart;
        emit(new RunEndEvent(suite, passed.get(), failed.get(), skipped.get(), unimplemented.get(), totalMs));
    }

    /** Runs a single group synchronously; returns [passed, failed, skipped, unimplemented]. */
    private static int[] runGroup(String suite, String endpoint, String region, String runId, TestGroup group) {
        TestContext ctx = new TestContext(endpoint, region, runId);
        int passed = 0, failed = 0, skipped = 0, unimplemented = 0;

            // Setup phase
            boolean setupOk = true;
            if (group.setup() != null) {
                try {
                    group.setup().run(ctx);
                } catch (Throwable e) {
                    String reason = "setup failed: " + e.getMessage();
                    for (TestCase tc : group.tests()) {
                        emit(new TestResultEvent(suite, group.service(), group.name(),
                                tc.name(), SKIP, 0, reason));
                        skipped++;
                    }
                    setupOk = false;
                }
            }

            if (!setupOk) {
                runTeardown(group, ctx);
                return new int[]{passed, failed, skipped, unimplemented};
            }

            // Test phase
            Set<String> passedTests = new HashSet<>();
            Set<String> failedOrSkipped = new HashSet<>();

            for (TestCase tc : group.tests()) {
                if (tc.skip() != null && !tc.skip().isEmpty()) {
                    emit(new TestResultEvent(suite, group.service(), group.name(),
                            tc.name(), SKIP, 0, tc.skip()));
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
                    emit(new TestResultEvent(suite, group.service(), group.name(),
                            tc.name(), SKIP, 0, reason));
                    skipped++;
                    failedOrSkipped.add(tc.name());
                    continue;
                }

                long start = System.currentTimeMillis();
                try {
                    tc.fn().run(ctx);
                    long ms = System.currentTimeMillis() - start;
                    emit(new TestResultEvent(suite, group.service(), group.name(),
                            tc.name(), PASS, ms, null));
                    passed++;
                    passedTests.add(tc.name());
                } catch (Throwable e) {
                    long ms = System.currentTimeMillis() - start;
                    String msg = e.getMessage() != null ? e.getMessage() : e.getClass().getSimpleName();
                    String status = isUnimplemented(e) ? UNIMPLEMENTED : FAIL;
                    emit(new TestResultEvent(suite, group.service(), group.name(),
                            tc.name(), status, ms, msg));
                    if (status.equals(UNIMPLEMENTED)) {
                        unimplemented++;
                    } else {
                        failed++;
                    }
                    failedOrSkipped.add(tc.name());
                }
            }

            runTeardown(group, ctx);
        return new int[]{passed, failed, skipped, unimplemented};
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private static void runTeardown(TestGroup group, TestContext ctx) {
        if (group.teardown() != null) {
            try {
                group.teardown().run(ctx);
            } catch (Throwable e) {
                System.err.println("[java-sdk] teardown failed for " + group.name() + ": " + e.getMessage());
            }
        }
    }

    /**
     * Returns {@code true} when {@code e} signals a 501 / not-implemented
     * response from the Overcast emulator.
     */
    public static boolean isUnimplemented(Throwable e) {
        if (e == null) return false;
        String msg = e.getMessage();
        if (msg == null) msg = e.getClass().getName();
        return msg.contains("501")
                || msg.contains("NotImplemented")
                || msg.contains("UnknownOperationException")
                || msg.contains("Unknown action")
                || msg.contains("not implemented");
    }

    // Serialises {@code v} as a single NDJSON line to stdout.  Must be
    // synchronised because multiple threads could call emit() concurrently;
    // this ensures lines are never interleaved.
    private static synchronized void emit(Object v) {
        try {
            System.out.println(MAPPER.writeValueAsString(v));
            System.out.flush();
        } catch (Exception e) {
            System.err.println("[java-sdk] failed to serialise event: " + e.getMessage());
        }
    }

    // ── Event record types ────────────────────────────────────────────────────

    record RunStartEvent(
            String event,
            String suite,
            String started_at,
            String endpoint,
            String version,
            int total_tests) {

        RunStartEvent(String suite, String startedAt, String endpoint, String version, int totalTests) {
            this("run_start", suite, startedAt, endpoint, version, totalTests);
        }
    }

    record TestResultEvent(
            String event,
            String suite,
            String service,
            String group,
            String test,
            String status,
            long duration_ms,
            String error) {

        TestResultEvent(String suite, String service, String group, String test,
                        String status, long durationMs, String error) {
            this("test_result", suite, service, group, test, status, durationMs, error);
        }

        // Jackson omits null fields automatically when the mapper is configured
        // with INCLUDE_NON_NULL — but the constructor sets the field, so we use a
        // custom serialiser or just accept null in JSON. The dashboard ignores null.
    }

    record RunEndEvent(
            String event,
            String suite,
            int passed,
            int failed,
            int skipped,
            int unimplemented,
            long duration_ms) {

        RunEndEvent(String suite, int passed, int failed, int skipped, int unimplemented, long ms) {
            this("run_end", suite, passed, failed, skipped, unimplemented, ms);
        }
    }
}
