import type { Ghost } from "@/hooks/use-ghost-tracker"
import { EventType } from "@/services/event-types"
import type { SQSMessage } from "@/types"

/** Minimum visual dwell per transient map state (ms). */
const VISUAL_STATE_TTL = 1_000

/** A live message or a ghost (recently deleted) message shown with strikethrough. */
export interface DisplayMessage {
  msg: SQSMessage
  isGhost: boolean
  deletedAt?: number
  visualPhase: SqsMessagePhase | "done"
}

export type SqsMessagePhase = "visible" | "inflight" | "delayed"

interface SQSMessageEventPayload {
  queueName: string
  messageId: string
}

interface SqsPhaseMemoryEntry {
  phase: SqsMessagePhase
  enteredAt: number
}

interface SqsGhostPhaseSeedEntry {
  deletedAt: number
  prevPhase: SqsMessagePhase
  prevEnteredAt: number
}

export interface SqsVisualMessagesState {
  phaseMemory: Map<string, SqsPhaseMemoryEntry>
  ghostPhaseSeed: Map<string, SqsGhostPhaseSeedEntry>
  eventCursor: number
}

interface SqsVisualMessagesInput {
  queueName: string
  liveMessages: SQSMessage[]
  ghosts: Map<string, Ghost<SQSMessage>>
  sqsEvents: Array<{
    type: string
    time: string
    payload: unknown
  }>
  nowMs: number
  state: SqsVisualMessagesState
}

interface SqsVisualMessagesResult {
  messages: DisplayMessage[]
  state: SqsVisualMessagesState
  needsClock: boolean
}

export function createSqsVisualMessagesState(): SqsVisualMessagesState {
  return {
    phaseMemory: new Map(),
    ghostPhaseSeed: new Map(),
    eventCursor: 0,
  }
}

function sentTimestamp(msg: SQSMessage): number {
  return Number(msg.attributes["SentTimestamp"] ?? 0)
}

function bySentTimestamp(a: SQSMessage, b: SQSMessage): number {
  return sentTimestamp(a) - sentTimestamp(b)
}

function byGhostSentTimestamp(a: Ghost<SQSMessage>, b: Ghost<SQSMessage>): number {
  return sentTimestamp(a.item) - sentTimestamp(b.item)
}

/**
 * Returns true if a message is currently invisible (in-flight or delayed).
 * Derived from visibleAfter so it self-updates each 1s tick.
 */
export function getSqsMessagePhase(msg: SQSMessage, nowMs = Date.now()): SqsMessagePhase {
  if (msg.delayed) return "delayed"
  if (msg.inflight) return "inflight"
  if (!msg.visibleAfter) return "visible"
  if (nowMs >= msg.visibleAfter) return "visible"
  return msg.approximateReceiveCount === 0 ? "delayed" : "inflight"
}

export function isInflight(msg: SQSMessage): boolean {
  return getSqsMessagePhase(msg) !== "visible"
}

function applySqsEvents({
  queueName,
  sqsEvents,
  nowMs,
  phaseMemory,
  eventCursor,
}: Pick<SqsVisualMessagesInput, "queueName" | "sqsEvents" | "nowMs"> & {
  phaseMemory: Map<string, SqsPhaseMemoryEntry>
  eventCursor: number
}): { phaseMemory: Map<string, SqsPhaseMemoryEntry>; eventCursor: number } {
  const nextPhaseMemory = new Map(phaseMemory)
  let nextEventCursor = eventCursor

  // Apply new SQS events since the last render. These are the authoritative
  // transition signals for fast actions that snapshots can miss.
  if (sqsEvents.length < nextEventCursor) {
    nextEventCursor = 0
  }

  for (let i = nextEventCursor; i < sqsEvents.length; i++) {
    const ev = sqsEvents[i]
    const payload = ev.payload as SQSMessageEventPayload | undefined
    if (!payload || payload.queueName !== queueName || !payload.messageId) continue

    const enteredAt = new Date(ev.time).getTime() || nowMs
    if (ev.type === EventType.sqs.MessageSent) {
      nextPhaseMemory.set(payload.messageId, { phase: "visible", enteredAt })
    } else if (ev.type === EventType.sqs.MessageInflight) {
      nextPhaseMemory.set(payload.messageId, { phase: "inflight", enteredAt })
    } else if (ev.type === EventType.sqs.MessageVisible) {
      nextPhaseMemory.set(payload.messageId, { phase: "visible", enteredAt })
    }
  }

  return { phaseMemory: nextPhaseMemory, eventCursor: sqsEvents.length }
}

function nextLivePhase(
  msg: SQSMessage,
  prev: SqsPhaseMemoryEntry | undefined,
  nowMs: number,
): SqsPhaseMemoryEntry {
  const realPhase = getSqsMessagePhase(msg, nowMs)

  // New message: always start at visible for VISUAL_STATE_TTL so ultra-fast
  // sends/receives are still observable on the map.
  if (!prev) {
    return { phase: "visible", enteredAt: nowMs }
  }

  if (prev.phase !== realPhase) {
    const elapsed = nowMs - prev.enteredAt
    // Promote to non-visible phases after dwell; avoid snapshot-only downgrade
    // back to visible unless SSE says so (MessageVisible).
    if (elapsed >= VISUAL_STATE_TTL && realPhase !== "visible") {
      return { phase: realPhase, enteredAt: nowMs }
    }
  }

  return prev
}

function buildLiveMessages(
  liveMessages: SQSMessage[],
  phaseMemory: Map<string, SqsPhaseMemoryEntry>,
  nowMs: number,
): { messages: DisplayMessage[]; phaseMemory: Map<string, SqsPhaseMemoryEntry> } {
  const nextPhaseMemory = new Map<string, SqsPhaseMemoryEntry>()
  const messages = [...liveMessages].sort(bySentTimestamp).map((msg) => {
    const phaseEntry = nextLivePhase(msg, phaseMemory.get(msg.messageId), nowMs)
    nextPhaseMemory.set(msg.messageId, phaseEntry)
    return { msg, isGhost: false, visualPhase: phaseEntry.phase }
  })

  return { messages, phaseMemory: nextPhaseMemory }
}

function pruneGhostPhaseSeed(
  ghostPhaseSeed: Map<string, SqsGhostPhaseSeedEntry>,
  ghosts: Map<string, Ghost<SQSMessage>>,
  currentIds: Set<string>,
): Map<string, SqsGhostPhaseSeedEntry> {
  const nextGhostPhaseSeed = new Map(ghostPhaseSeed)
  for (const id of nextGhostPhaseSeed.keys()) {
    if (currentIds.has(id) || !ghosts.has(id)) {
      nextGhostPhaseSeed.delete(id)
    }
  }
  return nextGhostPhaseSeed
}

function ghostVisualPhase(
  seed: SqsGhostPhaseSeedEntry,
  nowMs: number,
): SqsMessagePhase | "done" {
  const elapsedSinceDelete = nowMs - seed.deletedAt
  const visibleLeft =
    seed.prevPhase === "visible"
      ? Math.max(0, VISUAL_STATE_TTL - (seed.deletedAt - seed.prevEnteredAt))
      : 0

  if (elapsedSinceDelete < visibleLeft) return "visible"
  if (elapsedSinceDelete < visibleLeft + VISUAL_STATE_TTL) {
    return seed.prevPhase === "delayed" ? "delayed" : "inflight"
  }
  return "done"
}

function buildGhostMessages({
  ghosts,
  currentIds,
  phaseMemory,
  ghostPhaseSeed,
  nowMs,
}: Pick<SqsVisualMessagesInput, "ghosts" | "nowMs"> & {
  currentIds: Set<string>
  phaseMemory: Map<string, SqsPhaseMemoryEntry>
  ghostPhaseSeed: Map<string, SqsGhostPhaseSeedEntry>
}): { messages: DisplayMessage[]; ghostPhaseSeed: Map<string, SqsGhostPhaseSeedEntry> } {
  const nextGhostPhaseSeed = pruneGhostPhaseSeed(ghostPhaseSeed, ghosts, currentIds)
  const messages = [...ghosts.values()]
    .filter((g) => !currentIds.has(g.item.messageId))
    .sort(byGhostSentTimestamp)
    .map(({ item, deletedAt }) => {
      let seed = nextGhostPhaseSeed.get(item.messageId)
      if (!seed || seed.deletedAt !== deletedAt) {
        const prev = phaseMemory.get(item.messageId)
        seed = {
          deletedAt,
          prevPhase: prev?.phase ?? "visible",
          prevEnteredAt: prev?.enteredAt ?? nowMs,
        }
        nextGhostPhaseSeed.set(item.messageId, seed)
      }

      return {
        msg: item,
        isGhost: true,
        deletedAt,
        visualPhase: ghostVisualPhase(seed, nowMs),
      }
    })

  return { messages, ghostPhaseSeed: nextGhostPhaseSeed }
}

export function computeSqsVisualMessages({
  queueName,
  liveMessages,
  ghosts,
  sqsEvents,
  nowMs,
  state,
}: SqsVisualMessagesInput): SqsVisualMessagesResult {
  const currentIds = new Set(liveMessages.map((m) => m.messageId))
  const appliedEvents = applySqsEvents({
    queueName,
    sqsEvents,
    nowMs,
    phaseMemory: state.phaseMemory,
    eventCursor: state.eventCursor,
  })
  const live = buildLiveMessages(liveMessages, appliedEvents.phaseMemory, nowMs)
  const ghostList = buildGhostMessages({
    ghosts,
    currentIds,
    phaseMemory: appliedEvents.phaseMemory,
    ghostPhaseSeed: state.ghostPhaseSeed,
    nowMs,
  })

  const messages = [...live.messages, ...ghostList.messages]
  return {
    messages,
    state: {
      phaseMemory: live.phaseMemory,
      ghostPhaseSeed: ghostList.ghostPhaseSeed,
      eventCursor: appliedEvents.eventCursor,
    },
    needsClock: messages.some((m) => m.visualPhase !== "visible" || m.isGhost),
  }
}

function sqsPhaseMemoryEqual(
  a: Map<string, SqsPhaseMemoryEntry>,
  b: Map<string, SqsPhaseMemoryEntry>,
): boolean {
  if (a.size !== b.size) return false
  for (const [key, left] of a) {
    const right = b.get(key)
    if (!right || left.phase !== right.phase || left.enteredAt !== right.enteredAt) return false
  }
  return true
}

function sqsGhostPhaseSeedEqual(
  a: Map<string, SqsGhostPhaseSeedEntry>,
  b: Map<string, SqsGhostPhaseSeedEntry>,
): boolean {
  if (a.size !== b.size) return false
  for (const [key, left] of a) {
    const right = b.get(key)
    if (
      !right ||
      left.deletedAt !== right.deletedAt ||
      left.prevPhase !== right.prevPhase ||
      left.prevEnteredAt !== right.prevEnteredAt
    ) {
      return false
    }
  }
  return true
}

export function sqsVisualMessagesStateEqual(
  a: SqsVisualMessagesState,
  b: SqsVisualMessagesState,
): boolean {
  return (
    a.eventCursor === b.eventCursor &&
    sqsPhaseMemoryEqual(a.phaseMemory, b.phaseMemory) &&
    sqsGhostPhaseSeedEqual(a.ghostPhaseSeed, b.ghostPhaseSeed)
  )
}
