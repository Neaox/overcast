let fallbackCounter = 0

export function createId(): string {
  const browserCrypto = (globalThis as { crypto?: Crypto }).crypto

  if (typeof browserCrypto?.randomUUID === "function") {
    return browserCrypto.randomUUID()
  }

  fallbackCounter += 1
  return `id-${Date.now()}-${fallbackCounter.toString(36)}-${Math.random().toString(36).slice(2)}`
}
