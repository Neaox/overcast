import Prism from "@/lib/prism"

export type BodyLanguage = "json" | "xml" | "text"

export interface FormattedBody {
  text: string
  html?: string
}

export function formatBodyForDisplay(raw: string, hint: BodyLanguage): FormattedBody {
  if (hint === "json" || (hint === "text" && looksLikeJSON(raw))) {
    try {
      const formatted = JSON.stringify(JSON.parse(raw), null, 2)
      return { text: formatted, html: Prism.highlight(formatted, Prism.languages.json, "json") }
    } catch {
      return { text: raw }
    }
  }
  if (hint === "xml") {
    const formatted = formatXML(raw)
    return { text: formatted, html: Prism.highlight(formatted, Prism.languages.markup, "markup") }
  }
  return { text: raw }
}

function looksLikeJSON(s: string): boolean {
  const t = s.trim()
  return (t.startsWith("{") && t.endsWith("}")) || (t.startsWith("[") && t.endsWith("]"))
}

export function contentTypeToHint(contentType: string): BodyLanguage {
  const mediaType = contentType.split(";", 1)[0].trim().toLowerCase()
  if (/(^application\/(json|x-ndjson)$|\+json$)/i.test(mediaType)) return "json"
  if (/(^(application|text)\/(xml|xhtml\+xml|html)$|\+xml$)/i.test(mediaType)) return "xml"
  return "text"
}

export function bodyHintFromHeaders(headers: Record<string, string[]>): BodyLanguage {
  const ct = (headers["Content-Type"] ?? headers["content-type"] ?? headers["content_type"] ?? [""])[0]
  return contentTypeToHint(ct)
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

function formatXML(text: string): string {
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
    if (isOpening && !/\/\s*>$/.test(tag)) depth += 1
    i = tagEnd + 1
  }

  return lines.join("\n")
}
