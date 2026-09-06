package io.overcast.compat.scenario;

import io.overcast.compat.harness.Unimplemented;

/**
 * A failure the emulator answered with 501.
 *
 * <p>It reads as the failure it replaces — the message is byte for byte the
 * six-field one — and carries the {@link Unimplemented} marker, so the NDJSON
 * {@code error} field is unchanged and {@code Runner.isUnimplemented} still
 * classifies the test as {@code unimplemented} rather than {@code fail}. A 501
 * is not a failure ({@code compat/model/README.md} § Failure messages); it is
 * what the probe groups exist to record.
 */
public final class UnimplementedFailure extends Failure implements Unimplemented {

    private static final long serialVersionUID = 1L;

    UnimplementedFailure(String message) {
        super(message);
    }
}
