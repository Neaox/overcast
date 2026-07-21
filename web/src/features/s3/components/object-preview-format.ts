import Prism from "@/lib/prism"

type PreviewLanguage = "json" | "markup" | "css" | "javascript"

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

function previewLanguage(contentType: string, key: string): PreviewLanguage | null {
  const mediaType = normalizedContentType(contentType)
  if (/(^application\/(json|x-ndjson)$|\+json$)/i.test(mediaType) || /\.json$/i.test(key)) {
    return "json"
  }
  if (/(^text\/(html|xml)$|xml$|\+xml$)/i.test(mediaType) || /\.(xml|xhtml|html|htm)$/i.test(key)) {
    return "markup"
  }
  if (/^text\/css$/i.test(mediaType) || /\.css$/i.test(key)) return "css"
  if (
    /^(application|text)\/javascript$/i.test(mediaType) ||
    /\.(mjs|cjs|js|jsx|ts|tsx)$/i.test(key)
  ) {
    return "javascript"
  }
  return null
}

function formatMarkup(text: string): string {
  const compact = text.trim()
  if (!compact) return text
  let depth = 0
  const lines = compact
    .replace(/>\s+</g, "><")
    .split(/(?=<)|(?<=>)/g)
    .map((part) => part.trim())
    .filter(Boolean)
  return lines
    .map((line) => {
      const isClosing = /^<\//.test(line)
      const isDeclaration = /^<\?/.test(line)
      const isComment = /^<!--/.test(line)
      const isDoctype = /^<!DOCTYPE\b/i.test(line)
      const isSelfClosing = /\/>$/.test(line)
      const isOpening = /^<[^!?/][^>]*>$/.test(line)

      if (isClosing) depth = Math.max(0, depth - 1)
      const rendered = `${"  ".repeat(depth)}${line}`
      if (
        isOpening &&
        !isSelfClosing &&
        !isDeclaration &&
        !isComment &&
        !isDoctype &&
        !/^<(area|base|br|col|embed|hr|img|input|link|meta|param|source|track|wbr)\b/i.test(line)
      ) {
        depth += 1
      }
      return rendered
    })
    .join("\n")
}

export function formatPreviewText(
  text: string,
  contentType: string,
  key: string,
): { text: string; html?: string } {
  const language = previewLanguage(contentType, key)
  if (language === "json") {
    try {
      const formatted = JSON.stringify(JSON.parse(text), null, 2)
      return { text: formatted, html: Prism.highlight(formatted, Prism.languages.json, "json") }
    } catch {
      return { text }
    }
  }
  if (language === "markup") {
    const formatted = formatMarkup(text)
    return { text: formatted, html: Prism.highlight(formatted, Prism.languages.markup, "markup") }
  }
  if (language) {
    return { text, html: Prism.highlight(text, Prism.languages[language], language) }
  }
  return { text }
}
