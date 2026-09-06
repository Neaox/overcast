package io.overcast.compat.scenario;

/**
 * A value expression that could not be evaluated, or a value that will not fit
 * the input member it was written into.
 *
 * <p>Unchecked, so an emitted {@code <Op>Request.builder()} chain stays a flat
 * list of setters with no error plumbing: the first bad member abandons the
 * whole call, which is the outcome the Go emitter reaches by recording the
 * failure on its binder instead. {@link Group} catches it and turns it into the
 * six-field failure message, with field 3 showing the params as the scenario
 * file writes them — nothing was sent.
 *
 * <p>{@link #contextPath()} is non-null exactly when the cause was an
 * unresolvable {@code $ref}: that is the one failure a teardown step is allowed
 * to be skipped for, and the one the message reports as {@code <unset>} rather
 * than as a rejected value. {@link #member()} is the modeled member name for
 * failure-message field 4.
 */
public final class ValueException extends RuntimeException {

    private static final long serialVersionUID = 1L;

    private final String contextPath;
    private final String member;

    private ValueException(String message, String contextPath, String member) {
        super(message);
        this.contextPath = contextPath;
        this.member = member;
    }

    /** A value that does not fit where it was written, with no member attached. */
    static ValueException of(String message) {
        return new ValueException(message, null, null);
    }

    /** A value that does not fit the named input member. */
    static ValueException forMember(String member, String message) {
        return new ValueException(message, null, member);
    }

    /** A {@code $ref} to a context path the group never exported. */
    static ValueException unresolvedRef(String path) {
        return new ValueException("context path \"" + path + "\" is not set", path, null);
    }

    /** The unresolved context path, or {@code null} for any other failure. */
    public String contextPath() {
        return contextPath;
    }

    /** The input member being bound, or {@code null} when none is known. */
    public String member() {
        return member;
    }
}
