package io.overcast.compat.scenario;

import io.overcast.compat.harness.ComposedFailure;

import java.util.List;

/**
 * The six-field failure message.
 *
 * <p>Debuggability is the generated backend's whole cost, and it is paid here:
 * one builder produces every failure message and every clause uses it, so a
 * generated failure carries as much as a hand-written one would.
 *
 * <p>{@code compat/model/README.md} § Failure messages fixes the six fields and
 * their order:
 *
 * <ol>
 *   <li>{@code group/test}</li>
 *   <li>the operation — of the primary call, or of the clause's own call</li>
 *   <li>the exact params JSON sent, after evaluating every expression</li>
 *   <li>the assertion kind and, for checks/where, the path</li>
 *   <li>expected vs actual</li>
 *   <li>the scenario file and the step index</li>
 * </ol>
 *
 * <p>Rendered:
 *
 * <pre>
 * sqs-gen-queue/SetQueueAttributes: GetQueueAttributes params {"AttributeNames":["All"],"QueueUrl":"http://…"}: readback equals at $.Attributes.VisibilityTimeout: expected "60", actual "30" (compat/model/scenarios/sqs.json assert[0].assert)
 * </pre>
 *
 * <p>It is an {@link AssertionError} because that is how a test body fails in
 * this harness, and a {@link ComposedFailure} because the runner must never run
 * its "looks unimplemented" substring test over prose this package wrote — field
 * 3 is the params JSON, where a run id or a port puts a {@code 501} that means
 * nothing. A real 501 is stated by {@link UnimplementedFailure} instead.
 */
public class Failure extends AssertionError implements ComposedFailure {

    private static final long serialVersionUID = 1L;

    /** Caps one field of one failure message. */
    private static final int MAX_RENDERED = 4096;

    Failure(String message) {
        super(message);
    }

    /** Builds one failure message from its six fields. */
    static String message(String group, String test, String op, String params, String kind,
                          String path, String expected, String actual, String file, String step) {
        StringBuilder b = new StringBuilder();
        b.append(group).append('/').append(test).append(": ").append(op);
        if (params != null && !params.isEmpty()) {
            b.append(" params ").append(params);
        }
        b.append(": ").append(kind);
        if (path != null && !path.isEmpty()) {
            b.append(" at ").append(path);
        }
        b.append(": expected ").append(expected).append(", actual ").append(actual)
                .append(" (").append(file).append(' ').append(step).append(')');
        return b.toString();
    }

    /**
     * Renders a string as a failure message's expected or actual value. An SDK
     * error's text can be multi-line, so it is folded onto one line: the NDJSON
     * {@code error} field is read as a single line by the report tooling. It is
     * capped too — a transport failure can carry a long chain of causes.
     */
    static String quote(String s) {
        return Json.quote(clip(String.join(" ", s.trim().split("\\s+"))));
    }

    /**
     * Trims a rendered value and says how much it dropped, so the reader knows
     * the value is not all of what was there.
     */
    static String clip(String s) {
        if (s.length() <= MAX_RENDERED) {
            return s;
        }
        int cut = MAX_RENDERED;
        if (Character.isLowSurrogate(s.charAt(cut))) {
            cut--;
        }
        return s.substring(0, cut) + "… (" + (s.length() - cut) + " characters elided)";
    }

    /**
     * Prints the list a membership check searched. It is the actual value of the
     * failure, so it is printed rather than summarised — a generated failure that
     * says only "no match" cannot be diagnosed without re-running — but it is
     * capped, for the same reason every other field is.
     */
    static String renderList(List<Object> list) {
        if (list.isEmpty()) {
            return "an empty list";
        }
        return clip(Json.render(list));
    }

    /** Prints a where list for a failure message, in path order. */
    static String renderWhere(List<Where> where, List<Object> values) {
        StringBuilder b = new StringBuilder("{");
        for (int i = 0; i < where.size(); i++) {
            if (i > 0) {
                b.append(", ");
            }
            b.append(where.get(i).path()).append('=').append(Json.render(values.get(i)));
        }
        return b.append('}').toString();
    }
}
