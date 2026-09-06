package io.overcast.compat.scenario;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Value expressions ({@code compat/model/README.md} § Values), as Java.
 *
 * <p>The IR's five forms are five factories here. A value is ordinary Java data
 * — a {@link String}, a number, a {@link Boolean}, a {@link List}, a {@link Map}
 * — and a {@link Value} anywhere inside it is an expression to evaluate,
 * exactly as an object with one {@code $}-prefixed key is an expression in the
 * JSON the interpreters read. There are no conditionals, no arithmetic and no
 * scripting: eight implementations have to agree on every value.
 *
 * <pre>
 *   {"$lit": v}        → Values.lit(v)
 *   {"$ref": "q.url"}  → Values.ref("q.url")
 *   {"$name": "q"}     → Values.name("q")
 *   {"$concat": [...]} → Values.concat(...)
 *   {"$index": [v, n]} → Values.index(v, n)
 * </pre>
 */
public final class Values {

    private Values() {}

    /**
     * Wraps a literal that would otherwise be mistaken for something else. It
     * is rarely needed — a bare Java literal is already a literal — and exists
     * for the IR's {@code $lit}, whose whole job is to stop an object being read
     * as an expression.
     */
    public static Value lit(Object v) {
        return b -> v;
    }

    /** Reads a context path a previous call exported. */
    public static Value ref(String path) {
        return b -> b.lookup(path);
    }

    /**
     * The IR's only way to name a resource: {@code {runId}-{group}-{suffix}},
     * with the group token the whole group name and no shortening anywhere.
     * That is what makes the name-hygiene convention hold by construction, and
     * what lets the orphan sweep find anything a crashed run left behind.
     */
    public static Value name(String suffix) {
        return b -> b.runId() + "-" + b.group() + "-" + suffix;
    }

    /**
     * Joins its parts. A part that is a bare string is a literal; anything else
     * is an expression that must evaluate to a string.
     */
    public static Value concat(Object... parts) {
        return b -> {
            StringBuilder out = new StringBuilder();
            for (Object part : parts) {
                Object v = b.eval(part);
                if (!(v instanceof String s)) {
                    throw ValueException.of("concat part evaluated to " + Json.render(v)
                            + ", which is not a string");
                }
                out.append(s);
            }
            return out.toString();
        };
    }

    /** Takes element {@code n} of a list-valued expression. */
    public static Value index(Object list, int n) {
        return b -> {
            Object v = b.eval(list);
            if (!(v instanceof List<?> items)) {
                throw ValueException.of("index applies to a list, got " + Json.render(v));
            }
            if (n < 0 || n >= items.size()) {
                throw ValueException.of("index " + n + " is past the end of a list of " + items.size());
            }
            return items.get(n);
        };
    }

    /**
     * An untyped object, for the places a value is compared rather than sent:
     * an {@code equals} expectation and a {@code where} entry. Keys and values
     * alternate, and the iteration order is the emitted one so a failure message
     * reads the same on every run.
     */
    public static Map<String, Object> map(Object... keysAndValues) {
        if (keysAndValues.length % 2 != 0) {
            throw new IllegalArgumentException("Values.map wants alternating keys and values");
        }
        Map<String, Object> out = new LinkedHashMap<>();
        for (int i = 0; i < keysAndValues.length; i += 2) {
            out.put((String) keysAndValues[i], keysAndValues[i + 1]);
        }
        return out;
    }

    /** An untyped list, for the same two places {@link #map} serves. */
    public static List<Object> list(Object... items) {
        List<Object> out = new ArrayList<>(items.length);
        for (Object item : items) {
            out.add(item);
        }
        return out;
    }
}
