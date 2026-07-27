/**
 * The two uppercase-label specs, defined once.
 *
 * The design canvas draws small mono labels at four different size/tracking
 * pairs, but they serve exactly two roles, and **the tracking is what tells
 * them apart**: a label that names one field or one column sits at 9px/.14em,
 * a heading that names a group of things sits at 10px/.16em. Letting the wider
 * tracking down to 9px makes a column header read as a section heading and
 * erases the distinction, so neither value is written out at a call site.
 *
 * Both are authored lowercase and uppercased by CSS — that is the mono-label
 * style throughout the canvas (`in use`, `log group`, `event tail`). Colour is
 * deliberately left off: field labels are always `text-fg-subtle`, but section
 * headings carry the tier colour, so the caller supplies it.
 */

/** Field and column labels — table headers, form labels, stat-tile labels. */
export const fieldLabel = "font-mono text-[9px] tracking-[0.14em] uppercase"

/** Section headings — sidebar sections, dashboard tiers, grouped subheaders. */
export const sectionLabel = "font-mono text-[10px] tracking-[0.16em] uppercase"
