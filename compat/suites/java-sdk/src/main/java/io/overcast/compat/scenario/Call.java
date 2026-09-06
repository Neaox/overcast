package io.overcast.compat.scenario;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.function.Function;

/**
 * One API call: the operation, the typed request the emitted code builds, the
 * client method that sends it, and the context paths it exports from its
 * response.
 */
public final class Call {

    private final String op;
    private final String params;
    private final Function<Binder, Object> build;
    private final Function<Object, Object> send;
    private final Map<String, String> export = new LinkedHashMap<>();

    /**
     * @param op     the AWS operation name — failure-message field 2
     * @param params the call's params exactly as the scenario file writes them,
     *               value expressions unevaluated. It is failure-message field 3
     *               for a failure raised <em>before</em> anything was sent (an
     *               unresolvable {@code $ref}, a value that will not fit the
     *               member): nothing went on the wire, so the message shows what
     *               the file asked for. Once the request is built, field 3 is the
     *               built request instead, which is what was actually sent
     * @param build  fills a typed SDK request. It throws {@link ValueException}
     *               rather than reporting, so an emitted builder chain is a flat
     *               list of setters
     * @param send   invokes the client method with whatever {@code build}
     *               returned
     */
    public Call(String op, String params, Function<Binder, Object> build, Function<Object, Object> send) {
        this.op = op;
        this.params = params;
        this.build = build;
        this.send = send;
    }

    /** Adds one export: a context path, and the path in this call's own response. */
    public Call export(String contextPath, String responsePath) {
        export.put(contextPath, responsePath);
        return this;
    }

    String op() {
        return op;
    }

    String params() {
        return params;
    }

    Object build(Binder b) {
        return build.apply(b);
    }

    Object send(Object request) {
        return send.apply(request);
    }

    Map<String, String> exports() {
        return export;
    }
}
