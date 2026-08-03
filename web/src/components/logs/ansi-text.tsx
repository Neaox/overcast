import { Fragment, useMemo, type CSSProperties, type ReactNode } from "react"
import { isPlain, parseAnsi, type AnsiStyle } from "@/lib/ansi"

function cssOf(style: AnsiStyle): CSSProperties {
  const css: CSSProperties = {}
  if (style.fg) css.color = style.fg
  if (style.bg) css.backgroundColor = style.bg
  if (style.bold) css.fontWeight = 600
  if (style.italic) css.fontStyle = "italic"
  if (style.dim) css.opacity = 0.7
  const decoration = [style.underline ? "underline" : "", style.strike ? "line-through" : ""]
    .filter(Boolean)
    .join(" ")
  if (decoration) css.textDecoration = decoration
  return css
}

interface Props {
  /** The raw log message, escape sequences and all. */
  text: string
  /**
   * Wraps each run's text — used by the viewers that highlight filter matches,
   * so highlighting composes with colouring instead of competing for the same
   * string.
   */
  renderText?: (chunk: string) => ReactNode
}

/**
 * Renders a log message with its ANSI colours applied and every sequence the
 * viewer cannot act on removed.
 *
 * Emits bare text for unstyled runs, so a message with no escape sequences —
 * which is most of them — costs one text node, the same as rendering the string
 * directly.
 */
export function AnsiText({ text, renderText }: Props) {
  const segments = useMemo(() => parseAnsi(text), [text])
  return (
    <>
      {segments.map((segment, i) => {
        const content = renderText ? renderText(segment.text) : segment.text
        return isPlain(segment.style) ? (
          <Fragment key={i}>{content}</Fragment>
        ) : (
          <span key={i} style={cssOf(segment.style)}>
            {content}
          </span>
        )
      })}
    </>
  )
}
