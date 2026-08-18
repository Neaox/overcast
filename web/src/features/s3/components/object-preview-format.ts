import { highlightCode } from "@/lib/highlight-code"
import { formatBodyForDisplay, contentTypeToHint, MAX_FORMAT_BYTES } from "@/lib/format-body"

type PreviewLanguage = "json" | "markup" | "css" | "javascript"

const genericMediaTypes = new Set(["", "application/octet-stream", "binary/octet-stream"])

export function isImagePreviewable(contentType: string): boolean {
  const mediaType = normalizedContentType(contentType)
  return /^image\/(png|jpe?g|gif|webp|svg\+xml|bmp|avif)$/i.test(mediaType)
}

export function isTextPreviewable(contentType: string, key: string): boolean {
  const mediaType = normalizedContentType(contentType)
  if (mediaType.startsWith("text/")) return true
  if (
    /^(application\/(json|xml|javascript|x-ndjson|xhtml\+xml)|.+\+(json|xml))$/i.test(mediaType)
  ) {
    return true
  }
  return /\.(json|jsonl|ndjson|txt|log|md|csv|xml|xhtml|html|htm|css|js|ts|tsx|jsx|yaml|yml|toml|ini|env)$/i.test(
    key,
  )
}

function normalizedContentType(contentType: string): string {
  return contentType.split(";", 1)[0].trim().toLowerCase()
}

function languageFromContentType(mediaType: string): PreviewLanguage | null {
  if (genericMediaTypes.has(mediaType)) return null
  if (/(^application\/(json|x-ndjson)$|\+json$)/i.test(mediaType)) {
    return "json"
  }
  if (/(^(application|text)\/(xml|xhtml\+xml|html)$|\+xml$)/i.test(mediaType)) {
    return "markup"
  }
  if (/^text\/css$/i.test(mediaType)) return "css"
  if (/^(application|text)\/javascript$/i.test(mediaType)) {
    return "javascript"
  }
  return null
}

function languageFromKey(key: string): PreviewLanguage | null {
  if (/\.(json|jsonl|ndjson)$/i.test(key)) return "json"
  if (/\.(xml|xhtml|html|htm)$/i.test(key)) return "markup"
  if (/\.css$/i.test(key)) return "css"
  if (/\.(mjs|cjs|js|jsx|ts|tsx)$/i.test(key)) return "javascript"
  return null
}

function previewLanguage(contentType: string, key: string): PreviewLanguage | null {
  return languageFromContentType(normalizedContentType(contentType)) ?? languageFromKey(key)
}

/** HTML markup (as opposed to XML) gets void-tag-aware indentation. */
function isHtmlMarkup(contentType: string, key: string): boolean {
  return (
    /^(text|application)\/html$/i.test(normalizedContentType(contentType)) || /\.html?$/i.test(key)
  )
}

export interface FormattedPreview {
  text: string
  html?: string
  /**
   * Set when the document would have been pretty-printed or highlighted but
   * was too large for it (see `MAX_FORMAT_BYTES`). The dialog says so rather
   * than letting a plain rendering imply the file was not recognised.
   */
  highlightSkipped?: boolean
}

export function formatPreviewText(
  text: string,
  contentType: string,
  key: string,
): FormattedPreview {
  const htmlVoidTags = isHtmlMarkup(contentType, key)
  const sharedHint = contentTypeToHint(contentType)
  const language = previewLanguage(contentType, key)
  const bodyHint =
    sharedHint === "json" || sharedHint === "xml"
      ? sharedHint
      : language === "json"
        ? "json"
        : language === "markup"
          ? "xml"
          : null
  if (bodyHint) {
    const formatted = formatBodyForDisplay(text, bodyHint, contentType, { htmlVoidTags })
    // `formatBodyForDisplay` also returns plain text for JSON that failed to
    // parse; only size gets the honesty flag — a parse failure is the
    // document's own doing and plain text is the truthful rendering of it.
    if (!formatted.html && text.length > MAX_FORMAT_BYTES) {
      return { ...formatted, highlightSkipped: true }
    }
    return formatted
  }
  if (language === "css" || language === "javascript") {
    // Prism's cost is linear in the text — the logs performance work measured
    // ~19 ms at 166 KiB and the 16 ms frame budget falling around ~70 KiB
    // (docs/plans/logs-view-performance.md §4) — and a preview can hold up to
    // 1 MiB. Past the shared cap, plain text instead of a dialog-open freeze;
    // under it, the cached highlighter so reopening the same object is a
    // lookup rather than a re-tokenise.
    if (text.length > MAX_FORMAT_BYTES) return { text, highlightSkipped: true }
    return { text, html: highlightCode(text, language) }
  }
  return { text }
}
