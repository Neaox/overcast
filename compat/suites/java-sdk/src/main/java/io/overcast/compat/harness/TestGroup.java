package io.overcast.compat.harness;

import java.util.List;

/**
 * A named collection of related tests with optional setup and teardown.
 *
 * @param suite     Suite identifier, e.g. {@code "java-sdk"}.
 * @param service   AWS service name, e.g. {@code "s3"}.
 * @param name      Group name in kebab-case, e.g. {@code "s3-crud"}.
 * @param tests     Ordered list of test cases to execute.
 * @param setup     Optional: runs once before all tests; may be {@code null}.
 * @param teardown  Optional: runs once after all tests, even on failure; may
 *                  be {@code null}. Should suppress all exceptions.
 * @param parallel  Whether the group's tests may run concurrently with one
 *                  another. Only a generated probe group sets it
 *                  ({@code registry.generated.json}'s {@code parallel}): a probe
 *                  is one call that creates nothing, exports nothing and reads
 *                  no other test's resource, so the group's steps are
 *                  independent of one another in a way an ordinary group's are
 *                  not. {@link Runner} still runs them in order when any test
 *                  declares a {@code depends}.
 */
public record TestGroup(
        String suite,
        String service,
        String name,
        List<TestCase> tests,
        TestFn setup,
        TestFn teardown,
        boolean parallel) {

    /** Convenience constructor — no setup or teardown. */
    public TestGroup(String suite, String service, String name, List<TestCase> tests) {
        this(suite, service, name, tests, null, null, false);
    }

    /** Convenience constructor — the ordinary, ordered group. */
    public TestGroup(String suite, String service, String name, List<TestCase> tests,
                     TestFn setup, TestFn teardown) {
        this(suite, service, name, tests, setup, teardown, false);
    }
}
