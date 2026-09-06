/**
 * The hand-written half of this suite's generated compat coverage: the runtime
 * the emitted groups in {@code io.overcast.compat.groups.Scenarios*Gen} call
 * into.
 *
 * <p>The java-sdk suite is a <b>source-emitting</b> backend, not an
 * interpreter. The AWS SDK for Java v2 has no public dynamic-dispatch API, so
 * {@code cmd/compatgen} emits one method per scenario test, and each of those
 * builds a real typed {@code <Op>Request} and calls a real client method — the
 * SDK is exercised exactly as production code exercises it
 * ({@code docs/plans/compat-coverage-modelgen.md} §3.2). What is <em>not</em>
 * re-emitted per test is the semantics: the context bag, {@code $name}/{@code
 * $ref} evaluation, the closed check set, error matching, {@code eventually}
 * and the six-field failure message all live here, once, and the emitted code
 * is the data plus the typed calls.
 *
 * <p>The normative description of every rule implemented here is
 * {@code compat/model/README.md}. Where this package and that page disagree,
 * this package is wrong. In particular:
 *
 * <ul>
 *   <li>A group is setup → tests → teardown, with teardown running even after
 *       a failed setup, and every teardown step wrapped individually.</li>
 *   <li>Assertion kinds are a closed set, and so are the checks inside them.</li>
 *   <li>An error clause matches by equality against a code parsed out of one of
 *       the surfaces this SDK actually has, never by containment.</li>
 *   <li>{@code eventually} gives up with a fixed prefix in front of the last
 *       attempt's six-field message, byte for byte identical to the three
 *       interpreters.</li>
 *   <li>A 501 reaches the harness as its {@code unimplemented} classification,
 *       via the {@code Unimplemented} marker rather than a substring test over a
 *       message this package composed.</li>
 * </ul>
 */
package io.overcast.compat.scenario;
