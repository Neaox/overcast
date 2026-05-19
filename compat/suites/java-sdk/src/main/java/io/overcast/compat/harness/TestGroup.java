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
 */
public record TestGroup(
        String suite,
        String service,
        String name,
        List<TestCase> tests,
        TestFn setup,
        TestFn teardown) {

    /** Convenience constructor — no setup or teardown. */
    public TestGroup(String suite, String service, String name, List<TestCase> tests) {
        this(suite, service, name, tests, null, null);
    }
}
