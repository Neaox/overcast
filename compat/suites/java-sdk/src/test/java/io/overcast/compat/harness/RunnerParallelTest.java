package io.overcast.compat.harness;

import org.junit.jupiter.api.Test;

import java.io.ByteArrayOutputStream;
import java.io.PrintStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * {@link TestGroup#parallel()}, which the generated registry sets on a probe
 * group and no other loader in this repository was ignoring.
 *
 * <p>Two halves are pinned, and both matter. The tests really do run
 * concurrently — a probe group is a dozen independent one-call tests, and
 * running them one at a time is a dozen sequential round trips for no ordering
 * anyone needs. And the NDJSON still comes out in declaration order, which is
 * what keeps a parallel group's stream identical to a serial one's for the
 * dashboard, the baseline and the flake detector.
 */
class RunnerParallelTest {

    /** Runs one group through the suite runner and returns its NDJSON lines. */
    private static List<String> run(TestGroup group) {
        PrintStream original = System.out;
        ByteArrayOutputStream captured = new ByteArrayOutputStream();
        try {
            System.setOut(new PrintStream(captured, true, StandardCharsets.UTF_8));
            Runner.runSuite("java-sdk", "http://127.0.0.1:1", List.of(group));
        } finally {
            System.setOut(original);
        }
        List<String> lines = new ArrayList<>();
        for (String line : captured.toString(StandardCharsets.UTF_8).split("\n")) {
            if (line.contains("\"event\":\"test_result\"")) {
                lines.add(line.trim());
            }
        }
        return lines;
    }

    private static String testNameOf(String line) {
        int at = line.indexOf("\"test\":\"");
        return line.substring(at + 8, line.indexOf('"', at + 8));
    }

    /**
     * The flag does what it says: three tests that each wait for the other two
     * to have started can only all finish if they are running at the same time.
     * Serially this deadlocks until the latch times out, and the assertion on
     * the latch is what would fail.
     */
    @Test
    void aParallelGroupRunsItsTestsConcurrently() throws Exception {
        CountDownLatch started = new CountDownLatch(3);
        AtomicInteger together = new AtomicInteger();
        List<TestCase> tests = new ArrayList<>();
        for (int i = 0; i < 3; i++) {
            tests.add(new TestCase("Probe" + i, ctx -> {
                started.countDown();
                if (started.await(10, TimeUnit.SECONDS)) {
                    together.incrementAndGet();
                }
            }));
        }

        List<String> results = run(new TestGroup("java-sdk", "widgets", "widgets-gen-probe",
                List.copyOf(tests), null, null, true));

        assertEquals(3, together.get(), "the group's tests did not overlap");
        assertEquals(3, results.size());
        for (String line : results) {
            assertTrue(line.contains("\"status\":\"pass\""), line);
        }
    }

    /**
     * The other half of the contract: results are emitted in declaration order,
     * not in the order the calls answered. The slowest test is declared first.
     */
    @Test
    void aParallelGroupEmitsItsResultsInDeclarationOrder() {
        List<TestCase> tests = List.of(
                new TestCase("Slow", ctx -> Thread.sleep(120)),
                new TestCase("Middling", ctx -> Thread.sleep(40)),
                new TestCase("Fast", ctx -> {}));

        List<String> results = run(new TestGroup("java-sdk", "widgets", "widgets-gen-probe",
                tests, null, null, true));

        assertEquals(List.of("Slow", "Middling", "Fast"),
                results.stream().map(RunnerParallelTest::testNameOf).toList());
    }

    /**
     * A registry marker still outranks the run: a test the registry skipped is
     * reported with its reason rather than executed, on the concurrent path as
     * on the serial one.
     */
    @Test
    void aParallelGroupStillHonoursASkipMarker() {
        AtomicInteger ran = new AtomicInteger();
        List<TestCase> tests = List.of(
                new TestCase("Skipped", ctx -> ran.incrementAndGet(), null, "not in this region", List.of()),
                new TestCase("Ran", ctx -> ran.incrementAndGet()));

        List<String> results = run(new TestGroup("java-sdk", "widgets", "widgets-gen-probe",
                tests, null, null, true));

        assertEquals(1, ran.get());
        assertTrue(results.get(0).contains("\"status\":\"skip\""), results.get(0));
        assertTrue(results.get(0).contains("not in this region"), results.get(0));
        assertTrue(results.get(1).contains("\"status\":\"pass\""), results.get(1));
    }

    /**
     * The concurrent path cannot express the dependency gate — it would have to
     * decide what to skip from outcomes that have not happened yet — so a group
     * declaring one runs serially even where the registry says parallel. The IR
     * never produces that combination (only a probe group is parallel, and a
     * probe has no exports for a depends to consume), which is why this is a
     * guard rather than a scheduler.
     */
    @Test
    void aParallelGroupWithDependenciesFallsBackToSerial() {
        List<String> order = java.util.Collections.synchronizedList(new ArrayList<>());
        List<TestCase> tests = List.of(
                new TestCase("First", ctx -> {
                    Thread.sleep(80);
                    order.add("First");
                    throw new AssertionError("no");
                }),
                new TestCase("Second", ctx -> order.add("Second"), null, null, List.of("First")));

        assertTrue(Runner.hasDependencies(tests));

        List<String> results = run(new TestGroup("java-sdk", "widgets", "widgets-gen-probe",
                tests, null, null, true));

        assertEquals(List.of("First"), order, "the dependent test should not have run");
        assertTrue(results.get(0).contains("\"status\":\"fail\""), results.get(0));
        assertTrue(results.get(1).contains("dependency failed: First"), results.get(1));
        assertFalse(results.get(1).contains("\"status\":\"pass\""), results.get(1));
    }
}
