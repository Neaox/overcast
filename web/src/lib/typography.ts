/**
 * The two uppercase-label specs, defined once.
 *
 * The design canvas draws small mono labels at four different size/tracking
 * pairs, but they serve exactly two roles, and **the tracking is what tells
 * them apart**: a label that names one field or one column sits at .14em, a
 * heading that names a group of things sits at .16em.
 *
 * Both now sit at the scale's floor (`text-2xs`, 11px) rather than at 9px and
 * 10px. The size was never what separated them — the canvas's own note says the
 * tracking is — and one pixel of difference does not survive a high-DPI panel,
 * where 9px mono uppercase in the muted colour goes thin and grey. The tracking
 * still does the whole job, and the floor is now a rem so it scales with the
 * root font on large displays instead of staying frozen at 9px while the body
 * text around it grows.
 *
 * Both are authored lowercase and uppercased by CSS — that is the mono-label
 * style throughout the canvas (`in use`, `log group`, `event tail`). Colour is
 * deliberately left off: field labels are always `text-fg-subtle`, but section
 * headings carry the tier colour, so the caller supplies it.
 */

/** Field and column labels — table headers, form labels, stat-tile labels. */
export const fieldLabel = "font-mono text-2xs tracking-[0.14em] uppercase"

/** Section headings — sidebar sections, dashboard tiers, grouped subheaders. */
export const sectionLabel = "font-mono text-2xs tracking-[0.16em] uppercase"
