/**
 * The worker-client kernel, exercised against a stub Worker handed in
 * through the factory — no globals to stub, since lazy construction is the
 * caller's parameter. What is under test is exactly the machinery the three
 * call sites (highlight, docs search, map layout) delegate: correlation,
 * the retry ladder, stranded-work recovery through fallbacks, the guarded
 * post, pending-map hygiene on every error path, cancellation, and
 * transfer-list pass-through.
 */
import { createWorkerClient, type WorkerRequest } from "./worker-client"

interface StubReply {
  id: number
  value: string
}

class WorkerStub {
  posted: Array<{ message: unknown; transfer?: Transferable[] }> = []
  onmessage: ((event: { data: StubReply }) => void) | null = null
  onerror: ((event: unknown) => void) | null = null
  onmessageerror: ((event: unknown) => void) | null = null
  terminated = false
  postMessage(message: unknown, transfer?: Transferable[]) {
    this.posted.push({ message, transfer })
  }
  terminate() {
    this.terminated = true
  }
  /** Deliver a correlated reply, as the real worker would. */
  reply(id: number, value: string) {
    this.onmessage?.({ data: { id, value } })
  }
}

function makeClient(options?: { failureLimit?: number; createThrows?: boolean }) {
  const instances: WorkerStub[] = []
  const client = createWorkerClient<StubReply>({
    create: () => {
      if (options?.createThrows) throw new Error("CSP says no")
      const stub = new WorkerStub()
      instances.push(stub)
      return stub as unknown as Worker
    },
    failureLimit: options?.failureLimit,
  })
  return { client, instances }
}

/** A request whose message/decode/fallback are all observable strings. */
function echoRequest(tag: string, extras?: Partial<WorkerRequest<StubReply, string>>) {
  return {
    message: (id: number) => ({ id, tag }),
    decode: (reply: StubReply) => `decoded:${reply.value}`,
    fallback: () => `fallback:${tag}`,
    ...extras,
  } satisfies WorkerRequest<StubReply, string>
}

describe("correlation", () => {
  it("constructs the worker lazily, once, and resolves each request with its own decoded reply", async () => {
    const { client, instances } = makeClient()
    expect(instances).toHaveLength(0)

    const first = client.request(echoRequest("a"))
    const second = client.request(echoRequest("b"))
    expect(instances).toHaveLength(1)
    if (!first.async || !second.async) throw new Error("expected async outcomes")

    const worker = instances[0]
    const [msgA, msgB] = worker.posted.map((p) => p.message as { id: number; tag: string })
    expect(msgA.tag).toBe("a")
    expect(msgB.tag).toBe("b")
    expect(msgA.id).not.toBe(msgB.id)

    // Replies land out of order; each promise still gets its own.
    worker.reply(msgB.id, "B")
    worker.reply(msgA.id, "A")
    await expect(second.promise).resolves.toBe("decoded:B")
    await expect(first.promise).resolves.toBe("decoded:A")
  })

  it("drops replies for unknown ids without disturbing live requests", async () => {
    const { client, instances } = makeClient()
    const outcome = client.request(echoRequest("live"))
    if (!outcome.async) throw new Error("expected async outcome")
    const worker = instances[0]
    const posted = worker.posted[0].message as { id: number }

    worker.reply(posted.id + 999, "stranger")
    worker.reply(posted.id, "ok")
    await expect(outcome.promise).resolves.toBe("decoded:ok")
  })

  it("settles a request exactly once — a duplicate reply after resolution is ignored", async () => {
    const { client, instances } = makeClient()
    const decode = vi.fn((reply: StubReply) => reply.value)
    const outcome = client.request({
      message: (id: number) => ({ id }),
      decode,
      fallback: () => "fallback",
    })
    if (!outcome.async) throw new Error("expected async outcome")
    const worker = instances[0]
    const posted = worker.posted[0].message as { id: number }

    worker.reply(posted.id, "first")
    worker.reply(posted.id, "second")
    await expect(outcome.promise).resolves.toBe("first")
    expect(decode).toHaveBeenCalledTimes(1)
  })
})

describe("retry ladder and stranded-work recovery", () => {
  it("resolves stranded requests through their fallbacks when the worker errors, then respawns once", async () => {
    const { client, instances } = makeClient({ failureLimit: 2 })
    const strandedA = client.request(echoRequest("a"))
    const strandedB = client.request(echoRequest("b"))
    if (!strandedA.async || !strandedB.async) throw new Error("expected async outcomes")

    instances[0].onerror?.({ message: "worker exploded" })
    expect(instances[0].terminated).toBe(true)
    await expect(strandedA.promise).resolves.toBe("fallback:a")
    await expect(strandedB.promise).resolves.toBe("fallback:b")

    // One failure is not terminal: the next request gets a fresh worker.
    const retried = client.request(echoRequest("retry"))
    expect(retried.async).toBe(true)
    expect(instances).toHaveLength(2)

    // A second failure is: from here every request answers synchronously.
    instances[1].onerror?.({ message: "still exploding" })
    if (retried.async) await expect(retried.promise).resolves.toBe("fallback:retry")
    const after = client.request(echoRequest("after"))
    expect(after).toEqual({ async: false, value: "fallback:after" })
    expect(instances).toHaveLength(2)
  })

  it("treats onmessageerror as the same failure shape as onerror", async () => {
    const { client, instances } = makeClient({ failureLimit: 2 })
    const stranded = client.request(echoRequest("garbled"))
    if (!stranded.async) throw new Error("expected async outcome")
    instances[0].onmessageerror?.({})
    expect(instances[0].terminated).toBe(true)
    await expect(stranded.promise).resolves.toBe("fallback:garbled")
  })

  it("leaves no pending entry behind after a failure — a ghost reply into the respawned worker is inert", async () => {
    const { client, instances } = makeClient({ failureLimit: 2 })
    const decode = vi.fn((reply: StubReply) => reply.value)
    const stranded = client.request({
      message: (id: number) => ({ id }),
      decode,
      fallback: () => "recovered",
    })
    if (!stranded.async) throw new Error("expected async outcome")
    const oldId = (instances[0].posted[0].message as { id: number }).id

    instances[0].onerror?.({})
    await expect(stranded.promise).resolves.toBe("recovered")

    const next = client.request(echoRequest("next"))
    if (!next.async) throw new Error("expected async outcome")
    // The old id resolves nothing and decodes nothing on the new worker.
    instances[1].reply(oldId, "ghost")
    expect(decode).not.toHaveBeenCalled()
    const nextId = (instances[1].posted[0].message as { id: number }).id
    instances[1].reply(nextId, "real")
    await expect(next.promise).resolves.toBe("decoded:real")
  })

  it("answers synchronously through the fallback when the factory throws, without counting a failure to retry", () => {
    const { client, instances } = makeClient({ createThrows: true, failureLimit: 2 })
    expect(client.request(echoRequest("csp"))).toEqual({ async: false, value: "fallback:csp" })
    expect(client.request(echoRequest("again"))).toEqual({ async: false, value: "fallback:again" })
    expect(instances).toHaveLength(0)
  })
})

describe("guarded postMessage", () => {
  it("answers synchronously when postMessage throws, leaving no pending entry behind", async () => {
    const { client, instances } = makeClient()
    const decode = vi.fn((reply: StubReply) => reply.value)
    // First request wires the worker; then the worker turns unpostable.
    const ok = client.request(echoRequest("ok"))
    if (!ok.async) throw new Error("expected async outcome")
    const worker = instances[0]
    const okId = (worker.posted[0].message as { id: number }).id

    worker.postMessage = () => {
      throw new Error("worker already terminated")
    }
    const failed = client.request({
      message: (id: number) => ({ id }),
      decode,
      fallback: () => "sync-answer",
    })
    expect(failed).toEqual({ async: false, value: "sync-answer" })

    // The failed request left nothing pending: a reply for the id it would
    // have used (okId + 1) decodes nothing, and the live request still works.
    worker.reply(okId + 1, "ghost")
    expect(decode).not.toHaveBeenCalled()
    worker.reply(okId, "still-fine")
    await expect(ok.promise).resolves.toBe("decoded:still-fine")
  })
})

describe("transfer lists", () => {
  it("passes a request's transfer list through to postMessage, and omits it when absent", () => {
    const { client, instances } = makeClient()
    const buffer = new ArrayBuffer(8)
    client.request(echoRequest("moved", { transfer: [buffer] }))
    client.request(echoRequest("cloned"))
    const [withTransfer, withoutTransfer] = instances[0].posted
    expect(withTransfer.transfer).toEqual([buffer])
    expect(withoutTransfer.transfer).toBeUndefined()
  })
})

describe("cancellation", () => {
  it("aborting resolves with the fallback and posts the cancel message", async () => {
    const { client, instances } = makeClient()
    const controller = new AbortController()
    const outcome = client.request(
      echoRequest("cancelled", {
        signal: controller.signal,
        cancelMessage: (id: number) => ({ type: "cancel", id }),
      }),
    )
    if (!outcome.async) throw new Error("expected async outcome")
    const worker = instances[0]
    const requestId = (worker.posted[0].message as { id: number }).id

    controller.abort()
    await expect(outcome.promise).resolves.toBe("fallback:cancelled")
    expect(worker.posted[1].message).toEqual({ type: "cancel", id: requestId })

    // A late reply for the cancelled id is inert.
    worker.reply(requestId, "too-late")
    await expect(outcome.promise).resolves.toBe("fallback:cancelled")
  })

  it("a reply that beats the abort wins, and the abort then cancels nothing", async () => {
    const { client, instances } = makeClient()
    const controller = new AbortController()
    const outcome = client.request(
      echoRequest("raced", {
        signal: controller.signal,
        cancelMessage: (id: number) => ({ type: "cancel", id }),
      }),
    )
    if (!outcome.async) throw new Error("expected async outcome")
    const worker = instances[0]
    const requestId = (worker.posted[0].message as { id: number }).id

    worker.reply(requestId, "won")
    controller.abort()
    await expect(outcome.promise).resolves.toBe("decoded:won")
    // No cancel message was posted for an already-settled request.
    expect(worker.posted).toHaveLength(1)
  })

  it("an already-aborted signal answers synchronously without touching the worker", () => {
    const { client, instances } = makeClient()
    const controller = new AbortController()
    controller.abort()
    const outcome = client.request(
      echoRequest("pre-aborted", {
        signal: controller.signal,
        cancelMessage: (id: number) => ({ type: "cancel", id }),
      }),
    )
    expect(outcome).toEqual({ async: false, value: "fallback:pre-aborted" })
    expect(instances[0].posted).toHaveLength(0)
  })
})
