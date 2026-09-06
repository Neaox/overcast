package io.overcast.compat.harness;

/**
 * Marks a failure whose message this suite composed itself, so
 * {@link Runner#isUnimplemented} never runs its substring test over it.
 *
 * <p>The generated scenario runtime's failure message embeds the exact params
 * JSON sent, and a run id, a queue URL or a port number in there can contain
 * {@code "501"} while saying nothing at all about the response status. A composed
 * message is therefore not evidence: an unimplemented result is stated by
 * {@link Unimplemented} instead, from the one place that holds the raw SDK
 * exception.
 */
public interface ComposedFailure {
}
