package io.overcast.compat.groups;

import io.overcast.compat.harness.TestFn;

import java.util.Map;

/**
 * Contract implemented by every service group class.
 *
 * <p>Each service group contributes three maps that are merged into the global
 * registry before group construction:
 * <ul>
 *   <li>{@link #impls()}     — test name → test body</li>
 *   <li>{@link #setups()}    — group name → setup function</li>
 *   <li>{@link #teardowns()} — group name → teardown function</li>
 * </ul>
 */
public interface ServiceGroup {

    /**
     * Label for this class's registrations. It is what a duplicate impl key
     * names, so a collision points at the two files to look in rather than just
     * the key they disagree about — the class name is the file name, which is
     * what a reader needs.
     */
    default String sourceName() {
        return getClass().getSimpleName();
    }

    /** Returns a map of all test implementations provided by this group class. */
    Map<String, TestFn> impls();

    /** Returns setup functions keyed by group name, e.g. {@code "s3-crud"}. */
    Map<String, TestFn> setups();

    /** Returns teardown functions keyed by group name, e.g. {@code "s3-crud"}. */
    Map<String, TestFn> teardowns();
}
