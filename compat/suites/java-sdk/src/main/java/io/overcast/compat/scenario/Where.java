package io.overcast.compat.scenario;

/**
 * One criterion an item of a list must satisfy.
 *
 * <p>{@code "$"} is the item itself, which is how a list of strings is matched:
 * {@code Where.of("$", Values.ref("queue.url"))}.
 *
 * @param path  an item-relative response path
 * @param value the value that path must equal, as JSON
 */
public record Where(String path, Object value) {

    /** One item criterion for {@code listContains} or the list form of {@code absent}. */
    public static Where of(String path, Object value) {
        return new Where(path, value);
    }
}
