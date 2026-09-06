package io.overcast.compat.registry;

import io.overcast.compat.harness.TestFn;

/**
 * Resolves a generated group's test to an implementation.
 *
 * <p>A generated group is not implemented by a registered impl the way a
 * hand-written one is: it is executed by an interpreter that reads the group's
 * {@link Registry.RegistryGroup#scenario()} IR. This is the extension point
 * that interpreter plugs into — the last step of the loader's resolution order,
 * after the group-qualified and bare impl keys and before the not-implemented
 * sentinel.
 *
 * <p>{@code Main} implements it over the generated group classes
 * {@code cmd/compatgen} writes into {@code io.overcast.compat.groups} — one
 * method per generated test, resolved by {@code "<group>:<test>"}. A generated
 * group this suite is scoped to but the backend cannot resolve still reports a
 * failure rather than a skip — see {@link Registry#buildGroups}.
 */
@FunctionalInterface
public interface ScenarioBackend {

    /**
     * Returns an implementation for {@code test} in {@code group}, or
     * {@code null} when this backend cannot execute it.
     */
    TestFn resolve(Registry.RegistryGroup group, Registry.RegistryTest test);
}
