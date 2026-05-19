package io.overcast.compat.harness;

import java.util.Collection;
import java.util.Objects;

/**
 * Lightweight assertion helpers for compat test methods.
 *
 * <p>Every method throws {@link AssertionError} on failure, which the
 * {@link Runner} catches and records as a failing test result.
 *
 * <p>All error messages include the provided description <em>and</em> enough
 * contextual detail (expected vs. actual values) to diagnose the failure
 * without running the test again.
 */
public final class Assertions {

    private Assertions() {}

    // ── Boolean ───────────────────────────────────────────────────────────────

    public static void assertTrue(boolean condition, String message) {
        if (!condition) {
            throw new AssertionError(message);
        }
    }

    public static void assertFalse(boolean condition, String message) {
        if (condition) {
            throw new AssertionError(message);
        }
    }

    // ── Null / non-null ───────────────────────────────────────────────────────

    public static void assertNotNull(Object obj, String name) {
        if (obj == null) {
            throw new AssertionError("expected non-null: " + name);
        }
    }

    public static void assertNull(Object obj, String name) {
        if (obj != null) {
            throw new AssertionError("expected null for " + name + " but was: " + obj);
        }
    }

    // ── Equality ──────────────────────────────────────────────────────────────

    public static void assertEquals(Object expected, Object actual, String message) {
        if (!Objects.equals(expected, actual)) {
            throw new AssertionError(message
                    + ": expected <" + expected + "> but was <" + actual + ">");
        }
    }

    public static void assertNotEquals(Object unexpected, Object actual, String message) {
        if (Objects.equals(unexpected, actual)) {
            throw new AssertionError(message + ": did not expect <" + unexpected + ">");
        }
    }

    // ── String ────────────────────────────────────────────────────────────────

    public static void assertNotBlank(String value, String name) {
        if (value == null || value.isBlank()) {
            throw new AssertionError("expected non-blank string for: " + name);
        }
    }

    public static void assertContains(String haystack, String needle, String message) {
        if (haystack == null || !haystack.contains(needle)) {
            throw new AssertionError(message
                    + ": expected \"" + haystack + "\" to contain \"" + needle + "\"");
        }
    }

    // ── Collections ───────────────────────────────────────────────────────────

    public static void assertNotEmpty(Collection<?> collection, String name) {
        if (collection == null || collection.isEmpty()) {
            throw new AssertionError("expected non-empty collection for: " + name);
        }
    }

    public static <T> void assertContains(Collection<T> collection, T element, String message) {
        if (collection == null || !collection.contains(element)) {
            throw new AssertionError(message
                    + ": expected collection to contain <" + element + ">");
        }
    }

    public static <T> void assertNotContains(Collection<T> collection, T element, String message) {
        if (collection != null && collection.contains(element)) {
            throw new AssertionError(message
                    + ": expected collection not to contain <" + element + ">");
        }
    }

    // ── Numeric ───────────────────────────────────────────────────────────────

    public static void assertGreaterThan(long threshold, long actual, String message) {
        if (actual <= threshold) {
            throw new AssertionError(message
                    + ": expected > " + threshold + " but was " + actual);
        }
    }

    public static void assertGreaterThanOrEqual(long threshold, long actual, String message) {
        if (actual < threshold) {
            throw new AssertionError(message
                    + ": expected >= " + threshold + " but was " + actual);
        }
    }
}
