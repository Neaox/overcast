package io.overcast.compat.scenario;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;

/**
 * The IR's document notation: canonical JSON, and equality "as JSON".
 *
 * <p>Every rule the IR states about a response is stated over JSON, so both
 * sides of a comparison are documents by the time they get here — a response
 * through {@link Doc}, an expected value through {@link Binder#eval}, which
 * normalises Java literals the same way. Comparing their canonical encodings is
 * then JSON equality with no coercion: {@code "30"} never equals {@code 30}, and
 * {@code true} never equals {@code 1}.
 *
 * <p>The encoder is written here rather than taken from Jackson because the
 * output is compared and printed, not parsed: object keys must be sorted, HTML
 * must not be escaped, an integral number must render as {@code 30} and not
 * {@code 30.0}, and there must be no trailing newline. Those are the choices
 * that make a java-sdk failure message read like a go-sdk one.
 */
public final class Json {

    private Json() {}

    /**
     * JSON {@code null}, as a value a path can resolve <em>to</em>.
     *
     * <p>Absence and null are different answers from the service and the IR
     * keeps them apart: an absent member is left out of the document entirely,
     * while this singleton is what a null the service really sent resolves to.
     * {@code missing} holds only for the first; {@code nonEmpty} fails for both.
     */
    public static final Object NULL = new Object() {
        @Override
        public String toString() {
            return "null";
        }
    };

    /** What a failure message prints where a path did not resolve. */
    public static final String MISSING = "<missing>";

    /**
     * Renders a document in a stable form: object keys sorted, no HTML
     * escaping, no trailing newline. It is both how values are compared and how
     * they are printed in a failure message, so "expected X, actual Y" reads in
     * the same notation the scenario file is written in.
     */
    public static String canonical(Object v) {
        StringBuilder out = new StringBuilder();
        write(out, v);
        return out.toString();
    }

    /** {@link #canonical}, for a failure message. */
    public static String render(Object v) {
        return canonical(v);
    }

    /** Prints a resolved-or-not value for a failure message. */
    public static String renderResolved(Object v, boolean resolved) {
        return resolved ? render(v) : MISSING;
    }

    /**
     * The IR's "equal, as JSON" ({@code compat/model/README.md} § Assertions).
     */
    public static boolean equal(Object a, Object b) {
        return canonical(a).equals(canonical(b));
    }

    /**
     * The IR's emptiness: null, {@code ""}, {@code []} or <code>{}</code>.
     * Numbers and booleans are never empty, which is what stops {@code nonEmpty}
     * failing on a legitimate 0 or false.
     */
    public static boolean isEmpty(Object v) {
        if (v == null || v == NULL) {
            return true;
        }
        if (v instanceof String s) {
            return s.isEmpty();
        }
        if (v instanceof List<?> l) {
            return l.isEmpty();
        }
        if (v instanceof Map<?, ?> m) {
            return m.isEmpty();
        }
        return false;
    }

    /** Renders one string as a JSON string literal. */
    public static String quote(String s) {
        StringBuilder out = new StringBuilder();
        writeString(out, s);
        return out.toString();
    }

    private static void write(StringBuilder out, Object v) {
        if (v == null || v == NULL) {
            out.append("null");
        } else if (v instanceof String s) {
            writeString(out, s);
        } else if (v instanceof Boolean b) {
            out.append(b ? "true" : "false");
        } else if (v instanceof Number n) {
            writeNumber(out, n);
        } else if (v instanceof Map<?, ?> m) {
            writeObject(out, m);
        } else if (v instanceof List<?> l) {
            writeList(out, l);
        } else {
            // Unreachable for a document Doc produced or a value the emitter
            // wrote: both are limited to the types above. Rendering rather than
            // throwing keeps a failure message readable if one ever is not.
            writeString(out, String.valueOf(v));
        }
    }

    private static void writeObject(StringBuilder out, Map<?, ?> m) {
        List<String> keys = new ArrayList<>(m.size());
        for (Object key : m.keySet()) {
            keys.add(String.valueOf(key));
        }
        Collections.sort(keys);
        out.append('{');
        boolean first = true;
        for (String key : keys) {
            if (!first) {
                out.append(',');
            }
            first = false;
            writeString(out, key);
            out.append(':');
            write(out, m.get(key));
        }
        out.append('}');
    }

    private static void writeList(StringBuilder out, List<?> l) {
        out.append('[');
        for (int i = 0; i < l.size(); i++) {
            if (i > 0) {
                out.append(',');
            }
            write(out, l.get(i));
        }
        out.append(']');
    }

    /**
     * Renders a number the way the interpreters' JSON does: an integral value
     * has no fractional part, whatever Java box it arrived in. Doc normalises
     * every number to a {@code Double}, so without this a response's {@code 30}
     * would print — and compare — as {@code 30.0} against the other backends'
     * {@code 30}.
     */
    private static void writeNumber(StringBuilder out, Number n) {
        double d = n.doubleValue();
        if (Double.isNaN(d) || Double.isInfinite(d)) {
            writeString(out, String.valueOf(n));
            return;
        }
        if (d == Math.rint(d) && Math.abs(d) < 1e15) {
            out.append(Long.toString((long) d));
            return;
        }
        out.append(BigDecimal.valueOf(d).stripTrailingZeros().toPlainString());
    }

    private static void writeString(StringBuilder out, String s) {
        out.append('"');
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"' -> out.append("\\\"");
                case '\\' -> out.append("\\\\");
                case '\n' -> out.append("\\n");
                case '\r' -> out.append("\\r");
                case '\t' -> out.append("\\t");
                case '\b' -> out.append("\\b");
                case '\f' -> out.append("\\f");
                default -> {
                    if (c < 0x20) {
                        out.append(String.format("\\u%04x", (int) c));
                    } else {
                        out.append(c);
                    }
                }
            }
        }
        out.append('"');
    }
}
