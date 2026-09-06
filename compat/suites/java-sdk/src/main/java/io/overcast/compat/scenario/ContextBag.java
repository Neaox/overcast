package io.overcast.compat.scenario;

import java.util.HashMap;
import java.util.Map;

/**
 * The map from context path ({@code "queue.url"}) to value that a group's
 * exports fill in and its refs read.
 *
 * <p>It lives on the harness {@code TestContext} for exactly one group run, so
 * it has the lifetime the IR gives a group's context. Access is synchronized:
 * the runner gives each group its own context and runs that group's steps in
 * order on one thread, but nothing in the harness's contract promises that, and
 * a bag that quietly lost a write would look like an unresolvable {@code $ref}
 * three tests later.
 */
final class ContextBag {

    private final Map<String, Object> values = new HashMap<>();

    synchronized boolean has(String path) {
        return values.containsKey(path);
    }

    synchronized Object get(String path) {
        return values.get(path);
    }

    synchronized void set(String path, Object value) {
        values.put(path, value);
    }
}
