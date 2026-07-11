import { useCallback, useEffect, useMemo, useState } from "react"
import { endpointStore } from "@/services/endpoint-store"
import type { CapturedMessage } from "@/types"

const CHANGE_EVENT = "overcast:inbox-read-state-changed"
const MAX_READ_IDS = 2000

function readStateKey() {
  const [baseUrl, region] = endpointStore.getKeys()
  return `overcast:inbox:read:${region}:${baseUrl}`
}

function readIds(key: string): string[] {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === "string") : []
  } catch {
    return []
  }
}

function writeIds(key: string, ids: string[]) {
  try {
    localStorage.setItem(key, JSON.stringify(ids.slice(-MAX_READ_IDS)))
  } catch {
    // Keep the in-memory state even when storage is unavailable.
  }
  window.dispatchEvent(new Event(CHANGE_EVENT))
}

export function useInboxReadState(messages: readonly CapturedMessage[]) {
  const key = readStateKey()
  const [readMessageIds, setReadMessageIds] = useState(() => readIds(key))

  useEffect(() => {
    const sync = () => setReadMessageIds(readIds(key))
    const syncStorage = (event: StorageEvent) => {
      if (event.key === key) sync()
    }

    sync()
    window.addEventListener(CHANGE_EVENT, sync)
    window.addEventListener("storage", syncStorage)
    return () => {
      window.removeEventListener(CHANGE_EVENT, sync)
      window.removeEventListener("storage", syncStorage)
    }
  }, [key])

  const messageIds = useMemo(() => messages.map((message) => message.id), [messages])
  const readIdSet = useMemo(() => new Set(readMessageIds), [readMessageIds])

  const markRead = useCallback(
    (id: string) => {
      setReadMessageIds((prev) => {
        if (prev.includes(id)) return prev
        const next = [...prev, id].slice(-MAX_READ_IDS)
        writeIds(key, next)
        return next
      })
    },
    [key],
  )

  const isRead = useCallback((id: string) => readIdSet.has(id), [readIdSet])
  const unreadCount = messageIds.reduce((count, id) => count + (readIdSet.has(id) ? 0 : 1), 0)

  return { unreadCount, isRead, markRead }
}
