package io.overcast.compat.harness;

import java.util.List;

/**
 * A single executable test within a {@link TestGroup}.
 *
 * @param name    PascalCase AWS operation name, e.g. {@code "CreateBucket"}.
 * @param fn      The test body; throw any {@link Exception} to fail the test.
 * @param op      AWS API operation name for doc links. {@code null} means use
 *                {@code name}; {@code ""} suppresses the doc link.
 * @param skip    Non-empty string causes the test to be emitted as {@code skip}.
 * @param depends Names of other tests in the same group that must pass before
 *                this test runs.  If any dependency failed or was skipped this
 *                test is automatically skipped.
 */
public record TestCase(String name, TestFn fn, String op, String skip, List<String> depends) {

    /** Canonical constructor — defaults depends to empty list. */
    public TestCase {
        if (depends == null) depends = List.of();
    }

    /** Convenience constructor — no op override, not skipped, no deps. */
    public TestCase(String name, TestFn fn) {
        this(name, fn, null, null, List.of());
    }

    /** Convenience constructor — backward compatible (no deps). */
    public TestCase(String name, TestFn fn, String op, String skip) {
        this(name, fn, op, skip, List.of());
    }
}
