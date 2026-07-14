export type FormattedTriggerEvent =
  | { language: "json"; text: string }
  | { language: "plain"; text: string }

type ParsedJSON =
  | { ok: true; value: unknown }
  | { ok: false }

function tryParseJSON(value: string): ParsedJSON {
  try {
    return { ok: true, value: JSON.parse(value) }
  } catch {
    return { ok: false }
  }
}

function looksLikeBase64(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed.length < 8) return false
  if (!/^[A-Za-z0-9+/]+={0,2}$/.test(trimmed)) return false
  return trimmed.length % 4 !== 1
}

function decodeBase64(value: string): string | null {
  if (!looksLikeBase64(value)) return null

  try {
    const normalized = value.trim().padEnd(Math.ceil(value.trim().length / 4) * 4, "=")
    const binary = atob(normalized)
    const bytes = Uint8Array.from(binary, char => char.charCodeAt(0))
    return new TextDecoder().decode(bytes)
  } catch {
    return null
  }
}

export function formatTriggerEvent(event: unknown): FormattedTriggerEvent {
  if (event == null) return { language: "plain", text: "" }

  if (typeof event !== "string") {
    return { language: "json", text: JSON.stringify(event, null, 2) }
  }

  const parsed = tryParseJSON(event)
  if (parsed.ok) {
    return { language: "json", text: JSON.stringify(parsed.value, null, 2) }
  }

  const decoded = decodeBase64(event)
  if (decoded != null) {
    const decodedJSON = tryParseJSON(decoded)
    if (decodedJSON.ok) {
      return { language: "json", text: JSON.stringify(decodedJSON.value, null, 2) }
    }
    return { language: "plain", text: decoded }
  }

  return { language: "plain", text: event }
}
