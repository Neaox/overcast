import { Children, isValidElement, useEffect, useState, type ReactNode } from "react"
import { Tab, TabList, Tabs } from "@/components/ui/tabs"
import {
  getStoredLanguage,
  languageKey,
  setPreferredLanguage,
  subscribeLanguage,
} from "./code-tab-language"

/**
 * The docs viewer's rendering of an `overcast:code-tabs` region (see
 * lib/remark-code-tabs.ts for the Markdown convention that produces these
 * elements). One `<code-tabs-group>` holds one `<code-tabs-panel>` per
 * language; the group renders the tab strip and only the selected panel.
 *
 * The reader's language choice follows them across groups and pages: picking a
 * tab stores its language key (code-tab-language.ts) and every other mounted
 * group with a matching tab switches too.
 */

interface TabMeta {
  id: string
  label: string
}

function prop(el: unknown, name: string): string {
  if (!isValidElement(el)) return ""
  const props = el.props as Record<string, unknown>
  const value = props[name]
  return typeof value === "string" ? value : ""
}

export function CodeTabsGroup({ children }: { children?: ReactNode }) {
  const panels = Children.toArray(children).filter((child) => prop(child, "data-tab-id") !== "")
  const tabs: TabMeta[] = panels.map((panel) => ({
    id: prop(panel, "data-tab-id"),
    label: prop(panel, "data-label"),
  }))
  const tabIds = tabs.map((tab) => tab.id).join(" ")

  const [selected, setSelected] = useState<string>(() => {
    const stored = getStoredLanguage()
    const match = stored ? tabs.find((tab) => languageKey(tab.label) === stored) : undefined
    return (match ?? tabs.at(0))?.id ?? ""
  })

  function select(id: string) {
    setSelected(id)
    const tab = tabs.find((t) => t.id === id)
    if (tab) setPreferredLanguage(languageKey(tab.label))
  }

  // Follow language picks made in other groups on the page.
  useEffect(() => {
    return subscribeLanguage((key) => {
      const tab = tabs.find((t) => languageKey(t.label) === key)
      if (tab) setSelected(tab.id)
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- tabs is a per-render derivation of children; tabIds is its identity
  }, [tabIds])

  // Each tab keeps its heading's anchor id (the spans below), so deep links
  // and the "On this page" nav still land here — and should also reveal the
  // right tab, not just scroll to the group.
  useEffect(() => {
    const onHash = () => {
      const id = window.location.hash.slice(1)
      if (id && tabs.some((tab) => tab.id === id)) setSelected(id)
    }
    onHash()
    window.addEventListener("hashchange", onHash)
    return () => window.removeEventListener("hashchange", onHash)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- tabs is a per-render derivation of children; tabIds is its identity
  }, [tabIds])

  if (tabs.length === 0) return null

  return (
    <div className="my-4">
      {tabs.map((tab) => (
        <span key={tab.id} id={tab.id} />
      ))}
      <Tabs selectedKey={selected} onSelectionChange={select}>
        <TabList aria-label="Language" className="mb-3">
          {tabs.map((tab) => (
            <Tab key={tab.id} id={tab.id}>
              {tab.label}
            </Tab>
          ))}
        </TabList>
        {panels.filter((panel) => prop(panel, "data-tab-id") === selected)}
      </Tabs>
    </div>
  )
}

export function CodeTabsPanel({ children }: { children?: ReactNode }) {
  return <div role="tabpanel">{children}</div>
}
