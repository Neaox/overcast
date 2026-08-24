/**
 * Starting a multi-object download.
 *
 * The console submits a real form rather than calling `fetch`, and that choice
 * is what keeps the download streaming end to end. The BFF writes the archive
 * entry by entry as it reads each object, so it never holds more than one
 * buffer; a `fetch` whose response was read into a Blob would undo that on the
 * browser's side by collecting the whole archive in the tab's memory before
 * writing a single byte to disk. A form submission is a navigation, so the
 * browser streams the response straight to the downloads folder, and a
 * multi-gigabyte selection costs neither end multi-gigabyte memory.
 *
 * The form posts into a hidden iframe so the page the user is on stays put —
 * the response is an attachment, so nothing ever renders there.
 *
 * The keys travel in the body for the other half of the same reason: a
 * selection has no length limit and a URL does. Only the endpoint rides in the
 * action URL's query, because a form POST cannot set the header that normally
 * carries it (see resolveEndpointQP in internal/bff/bff.go).
 */

/** Name of the hidden iframe every archive download is submitted into. */
const SINK_NAME = "overcast-archive-download"

/**
 * Builds the form that requests an archive. Exported for tests: everything
 * worth asserting about the request is in the element this returns.
 */
export function buildArchiveForm(
  doc: Document,
  { action, prefix, keys }: { action: string; prefix: string; keys: readonly string[] },
): HTMLFormElement {
  const form = doc.createElement("form")
  form.method = "post"
  form.action = action
  form.target = SINK_NAME
  form.hidden = true

  const field = (name: string, value: string) => {
    const input = doc.createElement("input")
    input.type = "hidden"
    input.name = name
    input.value = value
    form.appendChild(input)
  }

  // The folder the selection was made in, so the archive unpacks relative to
  // it rather than rebuilding the whole key path.
  field("prefix", prefix)
  for (const key of keys) field("key", key)

  return form
}

/** The hidden iframe the download is submitted into, created on first use. */
function archiveSink(doc: Document): HTMLIFrameElement {
  const existing = doc.querySelector<HTMLIFrameElement>(`iframe[name="${SINK_NAME}"]`)
  if (existing) return existing

  const iframe = doc.createElement("iframe")
  iframe.name = SINK_NAME
  iframe.hidden = true
  iframe.setAttribute("aria-hidden", "true")
  doc.body.appendChild(iframe)
  return iframe
}

/**
 * Asks the browser to download the given keys as one archive.
 *
 * Returns once the request is on its way; the download itself proceeds in the
 * browser's own UI, which is where its progress and its completion belong.
 */
export function startArchiveDownload(
  doc: Document,
  args: { action: string; prefix: string; keys: readonly string[] },
): void {
  if (args.keys.length === 0) return
  archiveSink(doc)
  const form = buildArchiveForm(doc, args)
  doc.body.appendChild(form)
  form.submit()
  form.remove()
}
