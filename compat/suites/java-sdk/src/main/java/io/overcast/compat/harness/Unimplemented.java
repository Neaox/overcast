package io.overcast.compat.harness;

/**
 * Marks a failure the emulator answered with 501.
 *
 * <p>{@link Runner#isUnimplemented} honours it before anything else, so a
 * classification decided from the SDK's own status code survives being wrapped
 * in a composed message. Everything without it falls back to the substring
 * heuristic, which is all a hand-written group has to offer.
 */
public interface Unimplemented {
}
