package io.overcast.compat.harness;

import java.util.HashMap;
import java.util.Map;

/**
 * TestContext carries per-group state shared between setup, tests, and teardown.
 *
 * <p>The state bag is intentionally untyped — callers cast on retrieval. Use
 * {@link #getString(String)} for the common string case to avoid boilerplate.
 */
public final class TestContext {

    private final String endpoint;
    private final String region;
    private final String runId;

    private final Map<String, Object> state = new HashMap<>();

    public TestContext(String endpoint, String region, String runId) {
        this.endpoint = endpoint;
        this.region   = region;
        this.runId    = runId;
    }

    public String endpoint() { return endpoint; }
    public String region()   { return region; }
    public String runId()    { return runId; }

    /** Stores {@code value} under {@code key}. Overwrites any previous value. */
    public void set(String key, Object value) {
        state.put(key, value);
    }

    /**
     * Retrieves the value stored under {@code key}, or {@code null} when absent.
     * The caller is responsible for casting to the expected type.
     */
    @SuppressWarnings("unchecked")
    public <T> T get(String key) {
        return (T) state.get(key);
    }

    /**
     * Retrieves the string value stored under {@code key}, or {@code null} when
     * absent or the stored value is not a {@link String}.
     */
    public String getString(String key) {
        Object v = state.get(key);
        return v instanceof String s ? s : null;
    }

    /** Clears all state — called automatically between groups by the runner. */
    public void reset() {
        state.clear();
    }

    /** Writes a diagnostic message to stderr (never to stdout, which is NDJSON). */
    public void log(String msg) {
        System.err.println("[java-sdk] " + msg);
    }
}
