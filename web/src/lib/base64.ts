/**
 * Base64 → text, for the values AWS hands back encoded — a Lambda invocation's
 * `LogResult` above all.
 *
 * `atob` on its own is wrong twice over. It yields a *binary string*, one code
 * unit per byte, so anything outside ASCII arrives as mojibake: a Python
 * handler printing `café` renders `cafÃ©`, and an emoji from a Node
 * `console.log` becomes four stray characters. And it throws on input that is
 * not base64 — which is fatal where the call sits in a render path with no
 * error boundary above it, costing the whole panel rather than the one value it
 * could not read.
 *
 * So: base64 → bytes → `TextDecoder`, and failure reported as `null` for the
 * caller to render around.
 */

/**
 * Decodes base64 to UTF-8 text, or `null` if the input is not base64.
 *
 * The decoder is deliberately non-fatal: one malformed byte in a megabyte of
 * log output should cost that byte a `U+FFFD`, not the whole tail. Only input
 * `atob` itself rejects — characters outside the alphabet, a length that cannot
 * be a base64 string — comes back `null`.
 */
export function decodeBase64Text(value: string): string | null {
  try {
    const binary = atob(value)
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0))
    return new TextDecoder().decode(bytes)
  } catch {
    return null
  }
}
