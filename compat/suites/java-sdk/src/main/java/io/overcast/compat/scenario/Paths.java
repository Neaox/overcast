package io.overcast.compat.scenario;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * Paths ({@code compat/model/README.md} § Paths): {@code $} is the response,
 * {@code .Name} selects a structure member or map key, {@code [n]} selects a
 * list element. Nothing else — no wildcards, filters, quoting or recursive
 * descent.
 *
 * <p>A path is walked over the <em>document</em> form of a response
 * ({@link Doc}), not over the SDK object, so member names are the modeled names
 * every backend writes and an unset member is absence rather than null.
 */
final class Paths {

    private Paths() {}

    /** One step of a path: a member name or a list index. */
    record Segment(String member, int index) {
        boolean isIndex() {
            return member == null;
        }
    }

    /** A resolved-or-not value. */
    record Resolved(Object value, boolean ok) {}

    /**
     * Splits a path into its segments. Anything the IR's path grammar does not
     * admit is rejected, so a malformed path fails the step rather than silently
     * resolving to nothing — the two are very different bugs.
     */
    static List<Segment> parse(String p) {
        if (p == null || p.isEmpty() || p.charAt(0) != '$') {
            throw ValueException.of("path " + Json.quote(String.valueOf(p)) + " does not start with $");
        }
        List<Segment> segs = new ArrayList<>();
        String rest = p.substring(1);
        while (!rest.isEmpty()) {
            char c = rest.charAt(0);
            if (c == '.') {
                rest = rest.substring(1);
                int end = indexOfAny(rest, ".[");
                if (end < 0) {
                    end = rest.length();
                }
                if (end == 0) {
                    throw ValueException.of("path " + Json.quote(p) + " has an empty member name");
                }
                segs.add(new Segment(rest.substring(0, end), -1));
                rest = rest.substring(end);
            } else if (c == '[') {
                int end = rest.indexOf(']');
                if (end < 0) {
                    throw ValueException.of("path " + Json.quote(p) + " has an unterminated index");
                }
                String digits = rest.substring(1, end);
                int n;
                try {
                    n = Integer.parseInt(digits);
                } catch (NumberFormatException e) {
                    n = -1;
                }
                if (n < 0) {
                    throw ValueException.of("path " + Json.quote(p) + " has a non-numeric index "
                            + Json.quote(digits));
                }
                segs.add(new Segment(null, n));
                rest = rest.substring(end + 1);
            } else {
                throw ValueException.of("path " + Json.quote(p) + " has an unexpected character "
                        + Json.quote(String.valueOf(c)));
            }
        }
        return segs;
    }

    /**
     * Walks a path over a document. {@code ok} is false when any segment is
     * absent — which is what {@code missing} tests for, and what makes an absent
     * list count as empty for {@code listContains} and {@code absent}.
     *
     * <p>A member the service sent as JSON null resolves, to {@link Json#NULL}.
     */
    static Resolved resolve(Object doc, String path) {
        Object cur = doc;
        for (Segment s : parse(path)) {
            if (s.isIndex()) {
                if (!(cur instanceof List<?> list) || s.index() >= list.size()) {
                    return new Resolved(null, false);
                }
                cur = list.get(s.index());
                continue;
            }
            if (!(cur instanceof Map<?, ?> obj)) {
                return new Resolved(null, false);
            }
            Object v = obj.get(s.member());
            if (v == null && !obj.containsKey(s.member())) {
                // The document's keys are the member names the SDK reports
                // on each SdkField, which is the modeled name — so a path
                // spelling a member the model writes lowercase (SQS models
                // ListDeadLetterSourceQueues' page as `queueUrls`) resolves
                // exactly. This retry is the tolerance behind that, for a
                // document whose key was capitalized on the way in, and it is
                // a fallback rather than a mapping: an exact key wins, and a
                // capitalized path never reaches for a lowercase one.
                String capitalized = capitalize(s.member());
                if (!obj.containsKey(capitalized)) {
                    return new Resolved(null, false);
                }
                v = obj.get(capitalized);
            }
            cur = v;
        }
        return new Resolved(cur, true);
    }

    static String capitalize(String member) {
        if (member.isEmpty() || !Character.isLowerCase(member.charAt(0))) {
            return member;
        }
        return Character.toUpperCase(member.charAt(0)) + member.substring(1);
    }

    private static int indexOfAny(String s, String chars) {
        for (int i = 0; i < s.length(); i++) {
            if (chars.indexOf(s.charAt(i)) >= 0) {
                return i;
            }
        }
        return -1;
    }
}
