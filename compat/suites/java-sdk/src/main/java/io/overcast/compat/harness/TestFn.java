package io.overcast.compat.harness;

/**
 * The functional interface for a single test body.
 *
 * <p>Throw any {@link Exception} (including {@link AssertionError}) to fail
 * the test. Return normally to pass.
 */
@FunctionalInterface
public interface TestFn {
    void run(TestContext ctx) throws Exception;
}
