package io.overcast.compat.scenario;

/**
 * One check on one response path. The set is closed
 * ({@code compat/model/README.md} § Assertions), and the factories below are
 * the only way the emitter builds one.
 *
 * @param path  the response path this check reads
 * @param kind  which check it is
 * @param value the expected value for {@code EQUALS} and the pattern for
 *              {@code MATCHES}; {@code null} for the rest
 */
public record Check(String path, Check.Kind kind, Object value) {

    /** The closed set of checks a clause may make on one path. */
    public enum Kind {
        NON_EMPTY("nonEmpty"),
        IS_LIST("isList"),
        EQUALS("equals"),
        MATCHES("matches"),
        MISSING("missing");

        private final String label;

        Kind(String label) {
            this.label = label;
        }

        /** The IR's own spelling, which is what a failure message names. */
        public String label() {
            return label;
        }
    }

    /**
     * Holds when the path resolves to a value that is not null, {@code ""},
     * {@code []} or <code>{}</code>. Numbers and booleans are never empty.
     */
    public static Check nonEmpty(String path) {
        return new Check(path, Kind.NON_EMPTY, null);
    }

    /**
     * Holds when the path resolves to a list, empty or not — or does not
     * resolve at all. A present value that is not a list fails it.
     */
    public static Check isList(String path) {
        return new Check(path, Kind.IS_LIST, null);
    }

    /**
     * Holds when the path resolves and the value is equal, as JSON, to the
     * evaluated expression.
     */
    public static Check equalTo(String path, Object want) {
        return new Check(path, Kind.EQUALS, want);
    }

    /** Holds when the path resolves to a string matching the pattern. */
    public static Check matches(String path, String pattern) {
        return new Check(path, Kind.MATCHES, pattern);
    }

    /**
     * Holds when the path does not resolve. A member the service sent as JSON
     * null resolves, so this fails on it.
     */
    public static Check missing(String path) {
        return new Check(path, Kind.MISSING, null);
    }
}
