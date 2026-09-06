package io.overcast.compat.scenario;

/**
 * One error, named two ways, because SDKs disagree about which they surface:
 * the modeled shape name and the wire code (the {@code awsQueryError} code where
 * the service declares one, else the shape name again). Either is accepted.
 *
 * @param shape the modeled error shape
 * @param code  the wire code
 */
public record ErrorSpec(String shape, String code) {

    /** Names an error by its modeled shape and its wire code. */
    public static ErrorSpec of(String shape, String code) {
        return new ErrorSpec(shape, code);
    }

    /** Renders both halves for a failure message. */
    String accepted() {
        if (shape.equals(code)) {
            return "error " + Json.quote(shape);
        }
        return "error " + Json.quote(shape) + " or " + Json.quote(code);
    }
}
