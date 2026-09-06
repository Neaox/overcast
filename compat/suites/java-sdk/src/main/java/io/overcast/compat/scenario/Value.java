package io.overcast.compat.scenario;

/**
 * One deferred value expression ({@code compat/model/README.md} § Values).
 *
 * <p>It is deferred rather than evaluated where it is written because a clause
 * is built before the test's primary call runs, and a {@code $ref} that call
 * exports must not be read until it has been.
 */
@FunctionalInterface
public interface Value {

    /**
     * Evaluates this expression against the group's context bag.
     *
     * @throws ValueException when the expression cannot be evaluated — an
     *                        unresolvable {@code $ref}, or a part of the wrong
     *                        type. The step that carries it is abandoned.
     */
    Object eval(Binder b);
}
