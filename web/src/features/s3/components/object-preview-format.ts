import Prism from "@/lib/prism"

type PreviewLanguage = "json" | "markup" | "css" | "javascript"

const genericMediaTypes = new Set(["", "application/octet-stream", "binary/octet-stream"])

const voidMarkupTags = new Set([
  "area",
  "base",
  "br",
  "col",
  "embed",
  "hr",
  "img",
  "input",
  "link",
  "meta",
  "param",
  "source",
  "track",
  "wbr",
])

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
  const mediaType = normalizedContentType(contentType)
  return languageFromContentType(mediaType) ?? languageFromKey(key)
}

function isHtmlMarkup(contentType: string, key: string): boolean {
  const mediaType = normalizedContentType(contentType)
  return /^(text|application)\/html$/i.test(mediaType) || /\.html?$/i.test(key)
}

function findMarkupTagEnd(text: string, start: number): number {
  let quote: string | null = null
  for (let i = start + 1; i < text.length; i += 1) {
    const char = text[i]
    if (quote) {
      if (char === quote) quote = null
      continue
    }
    if (char === '"' || char === "'") {
      quote = char
      continue
    }
    if (char === ">") return i
  }
  return -1
}

function markupTagName(tag: string): string {
  const match = /^<\/?\s*([A-Za-z][\w:.-]*)/.exec(tag)
  return match?.[1]?.toLowerCase() ?? ""
}

function isSelfClosingMarkupTag(tag: string, useHtmlVoidTags: boolean): boolean {
  return /\/\s*>$/.test(tag) || (useHtmlVoidTags && voidMarkupTags.has(markupTagName(tag)))
}

function formatMarkup(text: string, useHtmlVoidTags: boolean): string {
  const compact = text.trim()
  if (!compact) return text
  let depth = 0
  const lines: string[] = []

  const pushLine = (line: string, lineDepth = depth) => {
    const trimmed = line.trim()
    if (trimmed) lines.push(`${"  ".repeat(lineDepth)}${trimmed}`)
  }

  for (let i = 0; i < compact.length; ) {
    if (compact[i] !== "<") {
      const nextTag = compact.indexOf("<", i)
      const end = nextTag === -1 ? compact.length : nextTag
      for (const line of compact.slice(i, end).split(/\r?\n/)) pushLine(line)
      i = end
      continue
    }

    const tagEnd = findMarkupTagEnd(compact, i)
    if (tagEnd === -1) {
      pushLine(compact.slice(i))
      break
    }

    const tag = compact.slice(i, tagEnd + 1)
    const isClosing = /^<\//.test(tag)
    const isOpening = /^<[^!?/]/.test(tag)
    if (isClosing) depth = Math.max(0, depth - 1)
    pushLine(tag)
    if (isOpening && !isSelfClosingMarkupTag(tag, useHtmlVoidTags)) depth += 1
    i = tagEnd + 1
  }

  return lines.join("\n")
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
    const formatted = formatMarkup(text, isHtmlMarkup(contentType, key))
    return { text: formatted, html: Prism.highlight(formatted, Prism.languages.markup, "markup") }
  }
  if (language) {
    return { text, html: Prism.highlight(text, Prism.languages[language], language) }
  }
  return { text }
}
