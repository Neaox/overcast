import Prism from "@/lib/prism"
import { formatBodyForDisplay, contentTypeToHint } from "@/lib/format-body"

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
  return /^(text|application)\/html$/i.test(normalizedContentType(contentType)) || /\.html?$/i.test(key)
}

export function formatPreviewText(
  text: string,
  contentType: string,
  key: string,
): { text: string; html?: string } {
  const htmlVoidTags = isHtmlMarkup(contentType, key)
  const sharedHint = contentTypeToHint(contentType)
  if (sharedHint === "json" || sharedHint === "xml") {
    return formatBodyForDisplay(text, sharedHint, contentType, { htmlVoidTags })
  }
  const language = previewLanguage(contentType, key)
  if (language === "json" || language === "markup") {
    return formatBodyForDisplay(text, language === "json" ? "json" : "xml", contentType, { htmlVoidTags })
  }
  if (language === "css" || language === "javascript") {
    return { text, html: Prism.highlight(text, Prism.languages[language], language) }
  }
  return { text }
}
